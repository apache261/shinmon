package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsUseBoundedLabelsAndPrometheusFormat(t *testing.T) {
	metrics := New("gateway\nunsafe")
	metrics.ObserveRequest(200, 8*time.Millisecond)
	metrics.ObserveRequest(503, 2*time.Second)
	metrics.AuthenticationFailure()
	metrics.UpstreamFailure()
	metrics.ConfigurationRefreshFailure()
	metrics.RateLimited()
	metrics.QuotaExceeded()
	metrics.CircuitOpen()
	metrics.CoordinationFailure()
	metrics.SetConfigurationVersion(42)
	metrics.SetUpstreams(2, 3)
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{"shinmon_http_requests_total", "status_class=\"2xx\"} 1", "shinmon_authentication_failures_total", "shinmon_configuration_version", "shinmon_rate_limited_requests_total", "shinmon_quota_exceeded_requests_total", "shinmon_circuit_open_rejections_total", "shinmon_coordination_failures_total", " 42"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{"unsafe\n", "path=", "correlation", "client="} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics contain unbounded label %q", forbidden)
		}
	}
}
