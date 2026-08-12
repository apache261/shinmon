package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apache261/Shinmon/internal/correlation"
	"github.com/apache261/Shinmon/internal/health"
	"github.com/apache261/Shinmon/internal/observability"
)

func TestHealthEndpoints(t *testing.T) {
	readiness := &health.Readiness{}
	fixedTime := time.Date(2026, 7, 29, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	handler := testHandler(readiness, fixedTime)

	assertHealth(t, handler, "/health/live", http.StatusOK, "live", fixedTime.UTC())
	assertHealth(t, handler, "/health/ready", http.StatusServiceUnavailable, "not_ready", fixedTime.UTC())
	readiness.Set(true)
	assertHealth(t, handler, "/health/ready", http.StatusOK, "ready", fixedTime.UTC())
	readiness.Set(false)
	assertHealth(t, handler, "/health/ready", http.StatusServiceUnavailable, "not_ready", fixedTime.UTC())
}

func TestMethodAndUnknownRouteResponses(t *testing.T) {
	handler := testHandler(&health.Readiness{}, time.Now())

	tests := []struct {
		method string
		path   string
		status int
		body   string
	}{
		{method: http.MethodPost, path: "/health/live", status: http.StatusMethodNotAllowed, body: "method not allowed"},
		{method: http.MethodGet, path: "/missing", status: http.StatusNotFound, body: "not found"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d", test.method, test.path, response.Code)
		}
		if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
			t.Fatalf("Content-Type = %q", contentType)
		}
		if !strings.Contains(response.Body.String(), test.body) {
			t.Fatalf("body = %q", response.Body.String())
		}
	}
}

func TestCorrelationIDPreservedOrGenerated(t *testing.T) {
	handler := testHandler(&health.Readiness{}, time.Now())

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set(correlation.Header, "client-request_123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get(correlation.Header); got != "client-request_123" {
		t.Fatalf("preserved correlation ID = %q", got)
	}

	request = httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set(correlation.Header, "unsafe value with spaces")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	generated := response.Header().Get(correlation.Header)
	if len(generated) != 32 || generated == "unsafe value with spaces" {
		t.Fatalf("generated correlation ID = %q", generated)
	}
}

func TestAdminBoundaryRequiresBearerToken(t *testing.T) {
	const token = "admin-development-token-value-123456"
	handler := New(Options{
		Service:          "gateway-admin",
		Environment:      "development",
		Logger:           slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Readiness:        &health.Readiness{},
		AdminBearerToken: token,
	})

	tests := []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "incorrect", header: "Bearer incorrect", status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer " + token, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/v1/future", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), token) {
				t.Fatal("response exposed bearer token")
			}
		})
	}
}

func TestAccessLogDoesNotExposeBearerToken(t *testing.T) {
	const token = "admin-development-token-value-123456"
	var output bytes.Buffer
	handler := New(Options{
		Service:          "gateway-admin",
		Environment:      "development",
		Logger:           slog.New(slog.NewJSONHandler(&output, nil)),
		Readiness:        &health.Readiness{},
		AdminBearerToken: token,
	})
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/future?token="+token, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if strings.Contains(output.String(), token) {
		t.Fatalf("access log exposed bearer token: %s", output.String())
	}
	if strings.Contains(output.String(), "?token=") || strings.Contains(output.String(), "/future") || !strings.Contains(output.String(), `"route":"/admin/v1/other"`) {
		t.Fatalf("access log did not use bounded route: %s", output.String())
	}
}

func TestMetricsRequireSeparateBearerAndContainNoRequestSecrets(t *testing.T) {
	const token = "metrics-development-token-value-123456"
	metrics := observability.New("gateway")
	handler := New(Options{Service: "gateway", Environment: "development", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Readiness: &health.Readiness{}, Metrics: metrics, MetricsBearerToken: token})
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "shinmon_http_requests_total") {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("metrics exposed bearer token")
	}
}

func assertHealth(t *testing.T, handler http.Handler, path string, statusCode int, status string, timestamp time.Time) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != statusCode {
		t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
	}
	var body healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if body.Status != status || body.Service != "gateway" || body.Environment != "development" || !body.Timestamp.Equal(timestamp) {
		t.Fatalf("unexpected health body: %+v", body)
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func testHandler(readiness *health.Readiness, now time.Time) http.Handler {
	return New(Options{
		Service:     "gateway",
		Environment: "development",
		Logger:      slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Readiness:   readiness,
		Now:         func() time.Time { return now },
	})
}
