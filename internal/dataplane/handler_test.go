package dataplane

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apache261/Shinmon/internal/correlation"
)

const testPepper = "test-handler-pepper-at-least-32-characters"
const testKey = "shn_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef0123"

func TestGatewayProxyPreservesRequestAndSanitizesHeaders(t *testing.T) {
	received := make(chan *http.Request, 1)
	bodyReceived := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		bodyReceived <- string(payload)
		received <- r.Clone(context.Background())
		w.Header().Set("Server", "private-upstream")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"proxied":true}`))
	}))
	defer upstream.Close()
	handler, snapshot := proxyTestHandler(t, upstream.URL, 200*time.Millisecond)
	request := httptest.NewRequest(http.MethodPost, "http://gateway.example/payments?id=42", strings.NewReader(`{"amount":10}`))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Host = "attacker.example"
	request.Header.Set(ListenerPortHeader, "18081")
	request.Header.Set(APIKeyHeader, testKey)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("X-Forwarded-For", "203.0.113.5")
	request.Header.Set(correlation.Header, "client-correlation")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	proxied := <-received
	if proxied.Method != http.MethodPost || proxied.URL.Path != "/payments" || proxied.URL.RawQuery != "id=42" {
		t.Fatalf("request not preserved: %s %s", proxied.Method, proxied.URL.String())
	}
	if proxied.Host == "attacker.example" || proxied.Host != snapshot.Listeners[18081].Upstreams[0].Authority() {
		t.Fatalf("upstream Host=%q", proxied.Host)
	}
	if proxied.Header.Get(APIKeyHeader) != "" || proxied.Header.Get(ListenerPortHeader) != "" || proxied.Header.Get("X-Forwarded-For") != "" {
		t.Fatal("sensitive forwarding headers reached upstream")
	}
	if proxied.Header.Get(correlation.Header) != "client-correlation" {
		t.Fatalf("correlation=%q", proxied.Header.Get(correlation.Header))
	}
	if body := <-bodyReceived; body != `{"amount":10}` {
		t.Fatalf("body=%q", body)
	}
	if response.Header().Get("Server") != "" {
		t.Fatal("upstream Server header leaked")
	}
}

func TestGatewayProxiesToVerifiedHTTPSUpstream(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, _ := net.SplitHostPort(parsed.Host)
	port, _ := strconv.Atoi(portText)
	mac := hmac.New(sha256.New, []byte(testPepper))
	_, _ = mac.Write([]byte(testKey))
	target := &Upstream{ID: "tls-upstream", Scheme: "https", Address: netip.MustParseAddr(host), Port: port, Weight: 100, HealthCheckPath: "/health"}
	target.SetHealthy(true)
	snapshot := &Snapshot{Environment: "development", Listeners: map[int]*Listener{18081: {ID: "listener", Port: 18081, RequiredPermission: "rtp:v1:invoke", AllowedMethods: map[string]struct{}{http.MethodGet: {}}, MaxRequestBytes: 1024, RequestTimeout: time.Second, Upstreams: []*Upstream{target}}}, Credentials: map[string]*Credential{"abcdef123456": {Verifier: mac.Sum(nil), Permissions: map[string]struct{}{"rtp:v1:invoke": {}}}}}
	transport := upstream.Client().Transport.(*http.Transport)
	handler := NewHandler(HandlerOptions{Snapshot: func() *Snapshot { return snapshot }, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, APIKeyPepper: testPepper, Transport: transport})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, baseRequest())
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGatewayPolicyFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	handler, snapshot := proxyTestHandler(t, upstream.URL, 100*time.Millisecond)
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{name: "untrusted source", mutate: func(r *http.Request) { r.RemoteAddr = "203.0.113.1:20" }, status: 404},
		{name: "missing listener metadata", mutate: func(r *http.Request) { r.Header.Del(ListenerPortHeader) }, status: 404},
		{name: "unknown listener", mutate: func(r *http.Request) { r.Header.Set(ListenerPortHeader, "18082") }, status: 404},
		{name: "missing key", mutate: func(r *http.Request) { r.Header.Del(APIKeyHeader) }, status: 401},
		{name: "invalid key", mutate: func(r *http.Request) { r.Header.Set(APIKeyHeader, strings.Replace(testKey, "a", "b", 1)) }, status: 401},
		{name: "wrong permission", mutate: func(r *http.Request) { snapshot.Credentials["abcdef123456"].Permissions = map[string]struct{}{} }, status: 403},
		{name: "method denied", mutate: func(r *http.Request) { r.Method = http.MethodDelete }, status: 405},
		{name: "content type denied", mutate: func(r *http.Request) {
			r.Method = http.MethodPost
			r.Body = io.NopCloser(strings.NewReader("x"))
			r.ContentLength = 1
			r.Header.Set("Content-Type", "application/octet-stream")
		}, status: 415},
		{name: "body too large", mutate: func(r *http.Request) {
			r.Method = http.MethodPost
			r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 2048)))
			r.ContentLength = 2048
			r.Header.Set("Content-Type", "application/json")
		}, status: 413},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot.Credentials["abcdef123456"].Permissions = map[string]struct{}{"rtp:v1:invoke": {}}
			request := baseRequest()
			test.mutate(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if strings.Contains(response.Body.String(), upstream.URL) {
				t.Fatal("error exposed upstream")
			}
		})
	}
	snapshot.Listeners[18081].Upstreams[0].SetHealthy(false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, baseRequest())
	if response.Code != 503 {
		t.Fatalf("unhealthy status=%d", response.Code)
	}
}

func TestPublicDocumentationRoutesBypassOnlyGETAndHEADAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	handler, snapshot := proxyTestHandler(t, upstream.URL, time.Second)
	snapshot.Listeners[18081].UnprotectedRouteRegex = regexp.MustCompile(`^/docs(?:/.*)?$|^/openapi\.yml$|\.(?i:js|jpeg)$`)
	snapshot.Listeners[18081].AllowedMethods[http.MethodHead] = struct{}{}

	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/docs", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/docs/index.html", status: http.StatusNoContent},
		{method: http.MethodHead, path: "/openapi.yml", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/assets/swagger.js", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/images/logo.JPEG", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/private", status: http.StatusUnauthorized},
		{method: http.MethodGet, path: "/docs/../private", status: http.StatusUnauthorized},
		{method: http.MethodPost, path: "/docs/try-it", status: http.StatusUnauthorized},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://ignored.example"+test.path, nil)
			request.RemoteAddr = "127.0.0.1:50000"
			request.Header.Set(ListenerPortHeader, "18081")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestGatewayTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer upstream.Close()
	handler, _ := proxyTestHandler(t, upstream.URL, 20*time.Millisecond)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, baseRequest())
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTwoListenerPortsRouteIndependently(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("first")) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) }))
	defer second.Close()
	handler, snapshot := proxyTestHandler(t, first.URL, time.Second)
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(second.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	secondUpstream := &Upstream{ID: "second", Address: netip.MustParseAddr(host), Port: port, Weight: 100, HealthCheckPath: "/health"}
	secondUpstream.SetHealthy(true)
	snapshot.Listeners[18082] = &Listener{ID: "listener-two", Port: 18082, RequiredPermission: "rtp:v1:invoke", AllowedMethods: map[string]struct{}{http.MethodGet: {}}, AllowedContentTypes: map[string]struct{}{"application/json": {}}, RequestTimeout: time.Second, MaxRequestBytes: 1024, Upstreams: []*Upstream{secondUpstream}}
	for listenerPort, expected := range map[string]string{"18081": "first", "18082": "second"} {
		request := baseRequest()
		request.Header.Set(ListenerPortHeader, listenerPort)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || response.Body.String() != expected {
			t.Fatalf("port %s status=%d body=%q", listenerPort, response.Code, response.Body.String())
		}
	}
}

func TestLimitedBodyNeverReturnsBytesPastLimit(t *testing.T) {
	body := &limitedBody{ReadCloser: io.NopCloser(strings.NewReader("abcdef")), remaining: 5}
	payload, err := io.ReadAll(body)
	if !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("error=%v", err)
	}
	if string(payload) != "abcde" {
		t.Fatalf("payload=%q", payload)
	}
}

type fakeCoordinator struct {
	decision int
	err      error
	open     bool
	recorded bool
}

func (f *fakeCoordinator) AllowRequest(context.Context, string, string, string, int, int, int) (int, error) {
	return f.decision, f.err
}
func (f *fakeCoordinator) CircuitOpen(context.Context, string, string) (bool, error) {
	return f.open, f.err
}
func (f *fakeCoordinator) RecordUpstream(context.Context, string, string, bool, int, time.Duration) error {
	f.recorded = true
	return f.err
}

func TestDistributedPoliciesFailClosedAndRejectLimits(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	for _, test := range []struct {
		name        string
		coordinator *fakeCoordinator
		status      int
		code        string
	}{
		{name: "rate", coordinator: &fakeCoordinator{decision: 1}, status: http.StatusTooManyRequests, code: "GATEWAY_RATE_LIMITED"},
		{name: "quota", coordinator: &fakeCoordinator{decision: 2}, status: http.StatusTooManyRequests, code: "GATEWAY_QUOTA_EXCEEDED"},
		{name: "outage", coordinator: &fakeCoordinator{err: errors.New("redis unavailable")}, status: http.StatusServiceUnavailable, code: "GATEWAY_POLICY_UNAVAILABLE"},
		{name: "circuit", coordinator: &fakeCoordinator{open: true}, status: http.StatusServiceUnavailable, code: "GATEWAY_CIRCUIT_OPEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, snapshot := proxyTestHandlerWithCoordinator(t, upstream.URL, time.Second, test.coordinator)
			listener := snapshot.Listeners[18081]
			listener.RateLimitPerSecond = 1
			listener.QuotaRequestsPerMinute = 10
			listener.CircuitFailureThreshold = 2
			listener.CircuitOpen = time.Second
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, baseRequest())
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func proxyTestHandler(t *testing.T, upstreamURL string, timeout time.Duration) (http.Handler, *Snapshot) {
	return proxyTestHandlerWithCoordinator(t, upstreamURL, timeout, nil)
}

func proxyTestHandlerWithCoordinator(t *testing.T, upstreamURL string, timeout time.Duration, coordinator PolicyCoordinator) (http.Handler, *Snapshot) {
	t.Helper()
	addressText, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstreamURL, "http://"))
	port, _ := strconv.Atoi(portText)
	mac := hmac.New(sha256.New, []byte(testPepper))
	_, _ = mac.Write([]byte(testKey))
	upstream := &Upstream{ID: "upstream", Address: netip.MustParseAddr(addressText), Port: port, Weight: 100, HealthCheckPath: "/health"}
	upstream.SetHealthy(true)
	listener := &Listener{ID: "listener", Port: 18081, RequiredPermission: "rtp:v1:invoke", AllowedMethods: map[string]struct{}{http.MethodGet: {}, http.MethodPost: {}}, AllowedContentTypes: map[string]struct{}{"application/json": {}}, RequestTimeout: timeout, MaxRequestBytes: 1024, Upstreams: []*Upstream{upstream}}
	snapshot := &Snapshot{Version: 1, Environment: "development", Listeners: map[int]*Listener{18081: listener}, Credentials: map[string]*Credential{"abcdef123456": {ID: "key", ConsumerID: "consumer", Prefix: "abcdef123456", Verifier: mac.Sum(nil), Permissions: map[string]struct{}{"rtp:v1:invoke": {}}}}}
	gateway := NewHandler(HandlerOptions{Snapshot: func() *Snapshot { return snapshot }, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, APIKeyPepper: testPepper, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Coordinator: coordinator})
	return correlation.Middleware(gateway), snapshot
}
func baseRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://ignored.example/resource", bytes.NewReader(nil))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set(ListenerPortHeader, "18081")
	request.Header.Set(APIKeyHeader, testKey)
	return request
}
