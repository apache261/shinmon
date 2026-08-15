package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var latencyBounds = [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 5 * time.Second}

// Metrics contains only fixed-cardinality process metrics. It deliberately
// excludes paths, client IDs, correlation IDs, upstream addresses, and keys.
type Metrics struct {
	service                string
	requests               [5]atomic.Uint64
	latency                [len(latencyBounds) + 1]atomic.Uint64
	latencyNanoseconds     atomic.Uint64
	authFailures           atomic.Uint64
	upstreamFailures       atomic.Uint64
	configRefreshFailures  atomic.Uint64
	configVersion          atomic.Int64
	healthyUpstreams       atomic.Int64
	totalUpstreams         atomic.Int64
	rateLimited            atomic.Uint64
	quotaExceeded          atomic.Uint64
	circuitOpen            atomic.Uint64
	coordinationFailures   atomic.Uint64
	configRefreshSkipped   atomic.Uint64
	inFlight               atomic.Int64
	snapshotRefreshedUnix  atomic.Int64
	gatewayReplicasReady   atomic.Int64
	gatewayReplicasTotal   atomic.Int64
	gatewayReplicasCurrent atomic.Int64
	listenerPortsUsed      atomic.Int64
	listenerPortsTotal     atomic.Int64
	databaseConnections    atomic.Int64
	databaseConnectionsMax atomic.Int64
}

func New(service string) *Metrics { return &Metrics{service: sanitizeLabel(service)} }

func (m *Metrics) ObserveRequest(status int, duration time.Duration) {
	class := status/100 - 1
	if class < 0 || class >= len(m.requests) {
		class = len(m.requests) - 1
	}
	m.requests[class].Add(1)
	m.latencyNanoseconds.Add(uint64(max(duration, 0)))
	index := len(latencyBounds)
	for candidate, bound := range latencyBounds {
		if duration <= bound {
			index = candidate
			break
		}
	}
	m.latency[index].Add(1)
}

func (m *Metrics) AuthenticationFailure()         { m.authFailures.Add(1) }
func (m *Metrics) UpstreamFailure()               { m.upstreamFailures.Add(1) }
func (m *Metrics) ConfigurationRefreshFailure()   { m.configRefreshFailures.Add(1) }
func (m *Metrics) RateLimited()                   { m.rateLimited.Add(1) }
func (m *Metrics) QuotaExceeded()                 { m.quotaExceeded.Add(1) }
func (m *Metrics) CircuitOpen()                   { m.circuitOpen.Add(1) }
func (m *Metrics) CoordinationFailure()           { m.coordinationFailures.Add(1) }
func (m *Metrics) ConfigurationRefreshSkipped()   { m.configRefreshSkipped.Add(1) }
func (m *Metrics) BeginRequest()                  { m.inFlight.Add(1) }
func (m *Metrics) EndRequest()                    { m.inFlight.Add(-1) }
func (m *Metrics) SnapshotRefreshed(at time.Time) { m.snapshotRefreshedUnix.Store(at.Unix()) }
func (m *Metrics) SetGatewayReplicas(ready, current, total int64) {
	m.gatewayReplicasReady.Store(ready)
	m.gatewayReplicasCurrent.Store(current)
	m.gatewayReplicasTotal.Store(total)
}
func (m *Metrics) SetListenerPorts(used, total int64) {
	m.listenerPortsUsed.Store(used)
	m.listenerPortsTotal.Store(total)
}
func (m *Metrics) SetDatabaseConnections(acquired, maximum int64) {
	m.databaseConnections.Store(acquired)
	m.databaseConnectionsMax.Store(maximum)
}
func (m *Metrics) SetConfigurationVersion(version int64) { m.configVersion.Store(version) }
func (m *Metrics) SetUpstreams(healthy, total int) {
	m.healthyUpstreams.Store(int64(healthy))
	m.totalUpstreams.Store(int64(total))
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	service := strconv.Quote(m.service)
	writeHelp(w, "shinmon_http_requests_total", "HTTP requests by bounded status class.", "counter")
	for index := range m.requests {
		fmt.Fprintf(w, "shinmon_http_requests_total{service=%s,status_class=\"%dxx\"} %d\n", service, index+1, m.requests[index].Load())
	}
	writeHelp(w, "shinmon_http_request_duration_seconds", "HTTP request duration.", "histogram")
	var cumulative uint64
	for index, bound := range latencyBounds {
		cumulative += m.latency[index].Load()
		fmt.Fprintf(w, "shinmon_http_request_duration_seconds_bucket{service=%s,le=\"%g\"} %d\n", service, bound.Seconds(), cumulative)
	}
	cumulative += m.latency[len(latencyBounds)].Load()
	fmt.Fprintf(w, "shinmon_http_request_duration_seconds_bucket{service=%s,le=\"+Inf\"} %d\n", service, cumulative)
	fmt.Fprintf(w, "shinmon_http_request_duration_seconds_sum{service=%s} %g\n", service, float64(m.latencyNanoseconds.Load())/float64(time.Second))
	fmt.Fprintf(w, "shinmon_http_request_duration_seconds_count{service=%s} %d\n", service, cumulative)
	writeCounter(w, "shinmon_authentication_failures_total", "Rejected authentication attempts.", service, m.authFailures.Load())
	writeCounter(w, "shinmon_upstream_failures_total", "Controlled upstream failures.", service, m.upstreamFailures.Load())
	writeCounter(w, "shinmon_configuration_refresh_failures_total", "Failed configuration refreshes.", service, m.configRefreshFailures.Load())
	writeCounter(w, "shinmon_rate_limited_requests_total", "Requests rejected by distributed rate limits.", service, m.rateLimited.Load())
	writeCounter(w, "shinmon_quota_exceeded_requests_total", "Requests rejected by distributed quotas.", service, m.quotaExceeded.Load())
	writeCounter(w, "shinmon_circuit_open_rejections_total", "Requests rejected by open upstream circuits.", service, m.circuitOpen.Load())
	writeCounter(w, "shinmon_coordination_failures_total", "Conservative failures caused by unavailable coordination.", service, m.coordinationFailures.Load())
	writeCounter(w, "shinmon_configuration_refresh_skipped_total", "Polling refreshes skipped because generations were unchanged.", service, m.configRefreshSkipped.Load())
	writeGauge(w, "shinmon_configuration_version", "Loaded configuration version.", service, m.configVersion.Load())
	writeGauge(w, "shinmon_http_requests_in_flight", "Requests currently being processed.", service, m.inFlight.Load())
	writeGauge(w, "shinmon_snapshot_last_refresh_timestamp_seconds", "Unix timestamp of the last valid snapshot refresh.", service, m.snapshotRefreshedUnix.Load())
	writeGauge(w, "shinmon_gateway_replicas_ready", "Recently seen gateway replicas reporting ready.", service, m.gatewayReplicasReady.Load())
	writeGauge(w, "shinmon_gateway_replicas_current", "Ready gateway replicas on the active configuration.", service, m.gatewayReplicasCurrent.Load())
	writeGauge(w, "shinmon_gateway_replicas_total", "Recently seen gateway replicas.", service, m.gatewayReplicasTotal.Load())
	writeGauge(w, "shinmon_listener_ports_used", "Listener ports not currently available.", service, m.listenerPortsUsed.Load())
	writeGauge(w, "shinmon_listener_ports_total", "Listener ports in the configured environment inventory.", service, m.listenerPortsTotal.Load())
	writeGauge(w, "shinmon_database_connections", "Database connections currently acquired by this process.", service, m.databaseConnections.Load())
	writeGauge(w, "shinmon_database_connections_max", "Maximum database connections configured for this process.", service, m.databaseConnectionsMax.Load())
	writeGauge(w, "shinmon_upstreams_healthy", "Healthy upstream count.", service, m.healthyUpstreams.Load())
	writeGauge(w, "shinmon_upstreams_total", "Configured upstream count.", service, m.totalUpstreams.Load())
}

func writeHelp(w http.ResponseWriter, name, help, kind string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}
func writeCounter(w http.ResponseWriter, name, help, service string, value uint64) {
	writeHelp(w, name, help, "counter")
	fmt.Fprintf(w, "%s{service=%s} %d\n", name, service, value)
}
func writeGauge(w http.ResponseWriter, name, help, service string, value int64) {
	writeHelp(w, name, help, "gauge")
	fmt.Fprintf(w, "%s{service=%s} %d\n", name, service, value)
}
func sanitizeLabel(value string) string {
	return strings.NewReplacer("\\", "_", "\"", "_", "\n", "_", "\r", "_").Replace(value)
}
