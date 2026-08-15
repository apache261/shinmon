package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/apache261/Shinmon/internal/correlation"
	"github.com/apache261/Shinmon/internal/health"
	"github.com/apache261/Shinmon/internal/observability"
)

type Options struct {
	Service            string
	Environment        string
	Logger             *slog.Logger
	Readiness          *health.Readiness
	AdminBearerToken   string
	AdminAPI           http.Handler
	GatewayAPI         http.Handler
	Metrics            *observability.Metrics
	MetricsBearerToken string
	Now                func() time.Time
}

type handler struct {
	service            string
	environment        string
	readiness          *health.Readiness
	adminTokenDigest   [sha256.Size]byte
	hasAdminToken      bool
	adminAPI           http.Handler
	gatewayAPI         http.Handler
	metrics            *observability.Metrics
	metricsTokenDigest [sha256.Size]byte
	hasMetricsToken    bool
	now                func() time.Time
}

type healthResponse struct {
	Status      string    `json:"status"`
	Service     string    `json:"service"`
	Environment string    `json:"environment"`
	Timestamp   time.Time `json:"timestamp"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(options Options) http.Handler {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &handler{
		service:     options.Service,
		environment: options.Environment,
		readiness:   options.Readiness,
		adminAPI:    options.AdminAPI,
		gatewayAPI:  options.GatewayAPI,
		metrics:     options.Metrics,
		now:         now,
	}
	if options.AdminBearerToken != "" {
		h.adminTokenDigest = sha256.Sum256([]byte(options.AdminBearerToken))
		h.hasAdminToken = true
	}
	if options.MetricsBearerToken != "" {
		h.metricsTokenDigest = sha256.Sum256([]byte(options.MetricsBearerToken))
		h.hasMetricsToken = true
	}

	return correlation.Middleware(accessLog(logger, h, options.Metrics))
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health/live":
		if r.Method != http.MethodGet {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.writeHealth(w, http.StatusOK, "live")
	case "/health/ready":
		if r.Method != http.MethodGet {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if h.readiness != nil && h.readiness.IsReady() {
			h.writeHealth(w, http.StatusOK, "ready")
			return
		}
		h.writeHealth(w, http.StatusServiceUnavailable, "not_ready")
	case "/metrics":
		if r.Method != http.MethodGet {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if h.metrics == nil || !authorized(r.Header.Get("Authorization"), h.metricsTokenDigest, h.hasMetricsToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			h.writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.metrics.ServeHTTP(w, r)
	default:
		if r.URL.Path == "/admin/v1" || strings.HasPrefix(r.URL.Path, "/admin/v1/") {
			if !h.authorized(r.Header.Get("Authorization")) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				h.writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if h.adminAPI != nil {
				h.adminAPI.ServeHTTP(w, r)
				return
			}
		}
		if h.gatewayAPI != nil {
			h.gatewayAPI.ServeHTTP(w, r)
			return
		}
		h.writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *handler) authorized(header string) bool {
	return authorized(header, h.adminTokenDigest, h.hasAdminToken)
}

func authorized(header string, digest [sha256.Size]byte, configured bool) bool {
	if !configured {
		return false
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	candidate := sha256.Sum256([]byte(parts[1]))
	return subtle.ConstantTimeCompare(candidate[:], digest[:]) == 1
}

func (h *handler) writeHealth(w http.ResponseWriter, statusCode int, status string) {
	writeJSON(w, statusCode, healthResponse{
		Status:      status,
		Service:     h.service,
		Environment: h.environment,
		Timestamp:   h.now().UTC(),
	})
}

func (h *handler) writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, statusCode int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func accessLog(logger *slog.Logger, next http.Handler, metrics *observability.Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		if metrics != nil {
			metrics.BeginRequest()
			defer metrics.EndRequest()
		}
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		duration := time.Since(started)
		if metrics != nil {
			metrics.ObserveRequest(recorder.status, duration)
		}
		logger.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"route", boundedRoute(r.URL.Path),
			"status", recorder.status,
			"duration_ms", duration.Milliseconds(),
			"correlation_id", correlation.FromContext(r.Context()),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func boundedRoute(path string) string {
	switch path {
	case "/health/live", "/health/ready", "/metrics":
		return path
	}
	if path == "/admin/v1" || strings.HasPrefix(path, "/admin/v1/") {
		parts := strings.Split(strings.TrimPrefix(path, "/admin/v1/"), "/")
		if len(parts) > 0 {
			switch parts[0] {
			case "services", "service-versions", "ports", "listeners", "permissions", "consumers", "keys", "configurations", "audit-events", "audit-logs", "gateway-instances":
				return "/admin/v1/" + parts[0]
			}
		}
		return "/admin/v1/other"
	}
	return "gateway"
}
