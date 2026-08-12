package dataplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/apache261/Shinmon/internal/coordination"
	"github.com/apache261/Shinmon/internal/correlation"
	"github.com/apache261/Shinmon/internal/observability"
)

const (
	ListenerPortHeader = "X-Gateway-Listener-Port"
	APIKeyHeader       = "X-API-Key"
)

var errRequestTooLarge = errors.New("request body too large")
var errCoordinationUnavailable = errors.New("distributed coordination unavailable")

type PolicyCoordinator interface {
	AllowRequest(context.Context, string, string, string, int, int, int) (int, error)
	CircuitOpen(context.Context, string, string) (bool, error)
	RecordUpstream(context.Context, string, string, bool, int, time.Duration) error
}

type HandlerOptions struct {
	Snapshot          func() *Snapshot
	TrustedProxyCIDRs []netip.Prefix
	APIKeyPepper      string
	Logger            *slog.Logger
	Metrics           *observability.Metrics
	Coordinator       PolicyCoordinator
	Transport         *http.Transport
}
type GatewayHandler struct {
	snapshot    func() *Snapshot
	trusted     []netip.Prefix
	pepper      []byte
	transport   *http.Transport
	logger      *slog.Logger
	metrics     *observability.Metrics
	coordinator PolicyCoordinator
}

func NewHandler(options HandlerOptions) http.Handler {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	transport := options.Transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		transport = transport.Clone()
	}
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &GatewayHandler{snapshot: options.Snapshot, trusted: append([]netip.Prefix(nil), options.TrustedProxyCIDRs...), pepper: []byte(options.APIKeyPepper), transport: transport, logger: options.Logger, metrics: options.Metrics, coordinator: options.Coordinator}
}

func (h *GatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	port, ok := h.listenerPort(r)
	if !ok {
		writeGatewayError(w, r, http.StatusNotFound, "GATEWAY_NOT_FOUND", "The requested gateway listener is unavailable.")
		return
	}
	snapshot := h.snapshot()
	if snapshot == nil {
		writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_UNAVAILABLE", "The gateway is temporarily unavailable.")
		return
	}
	listener := snapshot.Listeners[port]
	if listener == nil {
		writeGatewayError(w, r, http.StatusNotFound, "GATEWAY_NOT_FOUND", "The requested gateway listener is unavailable.")
		return
	}
	publicRequest := listener.allowsPublicRequest(r.Method, r.URL.Path)
	credential := h.authenticate(snapshot, r.Header.Get(APIKeyHeader))
	if credential == nil && !publicRequest {
		if h.metrics != nil {
			h.metrics.AuthenticationFailure()
		}
		w.Header().Set("WWW-Authenticate", "ApiKey")
		writeGatewayError(w, r, http.StatusUnauthorized, "GATEWAY_UNAUTHORIZED", "A valid API key is required.")
		return
	}
	if credential != nil && !publicRequest {
		if _, allowed := credential.Permissions[listener.RequiredPermission]; !allowed {
			writeGatewayError(w, r, http.StatusForbidden, "GATEWAY_FORBIDDEN", "The client is not allowed to access this service.")
			return
		}
	}
	if _, allowed := listener.AllowedMethods[r.Method]; !allowed {
		w.Header().Set("Allow", joinKeys(listener.AllowedMethods))
		writeGatewayError(w, r, http.StatusMethodNotAllowed, "GATEWAY_METHOD_NOT_ALLOWED", "The request method is not allowed.")
		return
	}
	if !contentTypeAllowed(listener, r) {
		writeGatewayError(w, r, http.StatusUnsupportedMediaType, "GATEWAY_UNSUPPORTED_MEDIA_TYPE", "The request content type is not allowed.")
		return
	}
	if r.ContentLength > listener.MaxRequestBytes {
		writeGatewayError(w, r, http.StatusRequestEntityTooLarge, "GATEWAY_REQUEST_TOO_LARGE", "The request body exceeds the configured limit.")
		return
	}
	if r.Body != nil {
		r.Body = &limitedBody{ReadCloser: r.Body, remaining: listener.MaxRequestBytes}
	}
	if listener.RateLimitPerSecond > 0 || listener.QuotaRequestsPerMinute > 0 {
		if h.coordinator == nil {
			writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_POLICY_UNAVAILABLE", "Distributed request policy is temporarily unavailable.")
			return
		}
		consumerID := "public"
		if credential != nil {
			consumerID = credential.ConsumerID
		}
		decision, err := h.coordinator.AllowRequest(r.Context(), snapshot.Environment, listener.ID, consumerID, listener.RateLimitPerSecond, listener.RateLimitBurst, listener.QuotaRequestsPerMinute)
		if err != nil {
			if h.metrics != nil {
				h.metrics.CoordinationFailure()
			}
			writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_POLICY_UNAVAILABLE", "Distributed request policy is temporarily unavailable.")
			return
		}
		if decision == coordination.DecisionRateLimited {
			if h.metrics != nil {
				h.metrics.RateLimited()
			}
			w.Header().Set("Retry-After", "1")
			writeGatewayError(w, r, http.StatusTooManyRequests, "GATEWAY_RATE_LIMITED", "The request rate limit was exceeded.")
			return
		}
		if decision == coordination.DecisionQuotaExceeded {
			if h.metrics != nil {
				h.metrics.QuotaExceeded()
			}
			w.Header().Set("Retry-After", "60")
			writeGatewayError(w, r, http.StatusTooManyRequests, "GATEWAY_QUOTA_EXCEEDED", "The request quota was exceeded.")
			return
		}
	}
	blocked := map[string]bool{}
	if listener.CircuitFailureThreshold > 0 {
		if h.coordinator == nil {
			writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_POLICY_UNAVAILABLE", "Distributed circuit state is temporarily unavailable.")
			return
		}
		for _, candidate := range listener.Upstreams {
			open, err := h.coordinator.CircuitOpen(r.Context(), snapshot.Environment, candidate.ID)
			if err != nil {
				if h.metrics != nil {
					h.metrics.CoordinationFailure()
				}
				writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_POLICY_UNAVAILABLE", "Distributed circuit state is temporarily unavailable.")
				return
			}
			blocked[candidate.ID] = open
		}
	}
	upstream := listener.SelectUpstreamWhere(func(candidate *Upstream) bool { return !blocked[candidate.ID] })
	if upstream == nil {
		if len(blocked) > 0 && anyBlocked(blocked) {
			if h.metrics != nil {
				h.metrics.CircuitOpen()
			}
			writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_CIRCUIT_OPEN", "All upstream circuits are open.")
		} else {
			writeGatewayError(w, r, http.StatusServiceUnavailable, "GATEWAY_NO_HEALTHY_UPSTREAM", "No healthy upstream is available.")
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), listener.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	proxy := &httputil.ReverseProxy{Transport: h.transport, Rewrite: func(request *httputil.ProxyRequest) {
		request.Out.URL.Scheme = upstream.URLScheme()
		request.Out.URL.Host = upstream.Authority()
		request.Out.Host = upstream.Authority()
		stripForwardingHeaders(request.Out.Header)
		request.Out.Header.Del(ListenerPortHeader)
		request.Out.Header.Del(APIKeyHeader)
		request.Out.Header.Del("Authorization")
		request.Out.Header.Set(correlation.Header, correlation.FromContext(request.In.Context()))
	}, ModifyResponse: func(response *http.Response) error {
		response.Header.Del("Server")
		if listener.CircuitFailureThreshold > 0 && h.coordinator.RecordUpstream(response.Request.Context(), snapshot.Environment, upstream.ID, response.StatusCode < 500, listener.CircuitFailureThreshold, listener.CircuitOpen) != nil {
			if h.metrics != nil {
				h.metrics.CoordinationFailure()
			}
			return errCoordinationUnavailable
		}
		return nil
	}, ErrorHandler: func(response http.ResponseWriter, request *http.Request, err error) {
		if h.metrics != nil {
			h.metrics.UpstreamFailure()
		}
		if errors.Is(err, errRequestTooLarge) {
			writeGatewayError(response, request, http.StatusRequestEntityTooLarge, "GATEWAY_REQUEST_TOO_LARGE", "The request body exceeds the configured limit.")
			return
		}
		if errors.Is(err, errCoordinationUnavailable) {
			writeGatewayError(response, request, http.StatusServiceUnavailable, "GATEWAY_POLICY_UNAVAILABLE", "Distributed circuit state is temporarily unavailable.")
			return
		}
		if listener.CircuitFailureThreshold > 0 && h.coordinator != nil {
			recordContext, recordCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = h.coordinator.RecordUpstream(recordContext, snapshot.Environment, upstream.ID, false, listener.CircuitFailureThreshold, listener.CircuitOpen)
			recordCancel()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
			writeGatewayError(response, request, http.StatusGatewayTimeout, "GATEWAY_UPSTREAM_TIMEOUT", "The upstream service timed out.")
			return
		}
		h.logger.WarnContext(request.Context(), "upstream request failed", "upstream_id", upstream.ID, "listener_id", listener.ID, "error", "connection failure")
		writeGatewayError(response, request, http.StatusServiceUnavailable, "GATEWAY_UPSTREAM_UNAVAILABLE", "The upstream service is unavailable.")
	}}
	proxy.ServeHTTP(w, r)
}

func (l *Listener) allowsPublicRequest(method, requestPath string) bool {
	if method != http.MethodGet && method != http.MethodHead || !strings.HasPrefix(requestPath, "/") || strings.ContainsAny(requestPath, "\x00\r\n") {
		return false
	}
	for _, segment := range strings.Split(requestPath, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return l.UnprotectedRouteRegex != nil && l.UnprotectedRouteRegex.MatchString(requestPath)
}

func anyBlocked(values map[string]bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func (h *GatewayHandler) listenerPort(r *http.Request) (int, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return 0, false
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return 0, false
	}
	trusted := false
	for _, prefix := range h.trusted {
		if prefix.Contains(address) {
			trusted = true
			break
		}
	}
	if !trusted {
		return 0, false
	}
	values := r.Header.Values(ListenerPortHeader)
	if len(values) != 1 {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(values[0]))
	return port, err == nil && port >= 1 && port <= 65535
}

func (h *GatewayHandler) authenticate(snapshot *Snapshot, raw string) *Credential {
	prefix, ok := keyPrefix(raw)
	if !ok {
		return nil
	}
	credential := snapshot.Credentials[prefix]
	if credential == nil {
		return nil
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(time.Now()) {
		return nil
	}
	mac := hmac.New(sha256.New, h.pepper)
	_, _ = mac.Write([]byte(raw))
	candidate := mac.Sum(nil)
	if subtle.ConstantTimeCompare(candidate, credential.Verifier) != 1 {
		return nil
	}
	return credential
}

func keyPrefix(raw string) (string, bool) {
	if len(raw) != 69 || !strings.HasPrefix(raw, "shn_") || raw[16] != '.' {
		return "", false
	}
	prefix := raw[4:16]
	for _, character := range raw[4:] {
		if character == '.' {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return "", false
		}
	}
	return prefix, true
}
func contentTypeAllowed(listener *Listener, r *http.Request) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	_, ok := listener.AllowedContentTypes[strings.ToLower(mediaType)]
	return ok
}
func stripForwardingHeaders(header http.Header) {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP"} {
		header.Del(name)
	}
}
func joinKeys(values map[string]struct{}) string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	slicesSort(items)
	return strings.Join(items, ", ")
}
func slicesSort(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (body *limitedBody) Read(buffer []byte) (int, error) {
	if body.remaining < 0 {
		return 0, errRequestTooLarge
	}
	if body.remaining == 0 {
		var probe [1]byte
		count, err := body.ReadCloser.Read(probe[:])
		if count > 0 {
			body.remaining = -1
			return 0, errRequestTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > body.remaining {
		buffer = buffer[:body.remaining]
	}
	count, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(count)
	return count, err
}

type gatewayErrorEnvelope struct {
	Error gatewayError `json:"error"`
}
type gatewayError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlationId"`
}

func writeGatewayError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(gatewayErrorEnvelope{Error: gatewayError{Code: code, Message: message, CorrelationID: correlation.FromContext(r.Context())}})
}
