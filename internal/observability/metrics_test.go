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
	metrics.ConfigurationRefreshSkipped()
	metrics.BeginRequest()
	metrics.EndRequest()
	metrics.SnapshotRefreshed(time.Unix(123, 0))
	metrics.SetGatewayReplicas(2, 1, 3)
	metrics.SetListenerPorts(70, 100)
	metrics.SetDatabaseConnections(2, 5)
	metrics.SetConfigurationVersion(42)
	metrics.SetUpstreams(2, 3)
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{"shinmon_http_requests_total", "status_class=\"2xx\"} 1", "shinmon_authentication_failures_total", "shinmon_configuration_version", "shinmon_rate_limited_requests_total", "shinmon_quota_exceeded_requests_total", "shinmon_circuit_open_rejections_total", "shinmon_coordination_failures_total", "shinmon_configuration_refresh_skipped_total", "shinmon_http_requests_in_flight", "shinmon_snapshot_last_refresh_timestamp_seconds", "shinmon_gateway_replicas_ready", "shinmon_gateway_replicas_current", "shinmon_listener_ports_used", "shinmon_database_connections_max", " 42", " 123"} {
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
