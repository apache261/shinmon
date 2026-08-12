package dataplane

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildSnapshotAndWeightedSelection(t *testing.T) {
	raw := rawSnapshot{Environment: "development", Services: []rawService{{ID: "svc", Enabled: true}}, ServiceVersions: []rawServiceVersion{{ID: "v1", ServiceID: "svc", RequestTimeoutMS: 1000, MaxRequestBytes: 1024, Enabled: true}}, Upstreams: []rawUpstream{{ID: "a", ServiceVersionID: "v1", Address: "192.168.1.10", Port: 8080, Weight: 1, HealthCheckPath: "/health", Enabled: true}, {ID: "b", ServiceVersionID: "v1", Address: "192.168.1.11", Port: 8080, Weight: 2, HealthCheckPath: "/health", Enabled: true}}, Listeners: []rawListener{{ID: "listener", ListenPort: 18081, ServiceVersionID: "v1", RequiredPermission: "rtp:v1:invoke", AllowedMethods: []string{"GET"}, UnprotectedRouteRegex: `^/swagger(?:/.*)?$|\.js$`, Status: "reserved"}}}
	snapshot, err := buildSnapshot(7, "development", raw, nil, []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")})
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}
	listener := snapshot.Listeners[18081]
	if listener.UnprotectedRouteRegex == nil || !listener.UnprotectedRouteRegex.MatchString("/swagger/index.html") {
		t.Fatal("unprotected route regex was not compiled")
	}
	counts := map[string]int{}
	for range 6 {
		counts[listener.SelectUpstream().ID]++
	}
	if counts["a"] != 2 || counts["b"] != 4 {
		t.Fatalf("weighted counts = %#v", counts)
	}
	listener.Upstreams[0].SetHealthy(false)
	for range 5 {
		if selected := listener.SelectUpstream(); selected == nil || selected.ID != "b" {
			t.Fatalf("selected unhealthy upstream: %#v", selected)
		}
	}
	listener.Upstreams[1].SetHealthy(false)
	if listener.SelectUpstream() != nil {
		t.Fatal("expected no healthy upstream")
	}
}

func TestBuildSnapshotRejectsUnsafeConfiguration(t *testing.T) {
	base := rawSnapshot{Environment: "development", Services: []rawService{{ID: "svc", Enabled: true}}, ServiceVersions: []rawServiceVersion{{ID: "v1", ServiceID: "svc", RequestTimeoutMS: 1000, MaxRequestBytes: 1024, Enabled: true}}, Upstreams: []rawUpstream{{ID: "a", ServiceVersionID: "v1", Address: "203.0.113.10", Port: 8080, Weight: 1, HealthCheckPath: "/health", Enabled: true}}, Listeners: []rawListener{{ID: "listener", ListenPort: 18081, ServiceVersionID: "v1", RequiredPermission: "rtp:v1:invoke", AllowedMethods: []string{"GET"}, Status: "active"}}}
	if _, err := buildSnapshot(1, "development", base, nil, []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}); err == nil {
		t.Fatal("outside-allowlist upstream accepted")
	}
	base.Environment = "staging"
	if _, err := buildSnapshot(1, "development", base, nil, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); err == nil {
		t.Fatal("environment mismatch accepted")
	}
	base.Environment = "development"
	base.Upstreams[0].Address = "192.168.1.10"
	base.Upstreams[0].Scheme = "ftp"
	if _, err := buildSnapshot(1, "development", base, nil, []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}); err == nil {
		t.Fatal("unsupported upstream scheme accepted")
	}
	base.Upstreams[0].Scheme = "http"
	base.Listeners[0].UnprotectedRouteRegex = "["
	if _, err := buildSnapshot(1, "development", base, nil, []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}); err == nil {
		t.Fatal("invalid unprotected route regex accepted")
	}
}

func TestCopyHealthAcrossSnapshotSwap(t *testing.T) {
	old := &Snapshot{Listeners: map[int]*Listener{1: {Upstreams: []*Upstream{{ID: "same"}}}}}
	old.Listeners[1].Upstreams[0].SetHealthy(false)
	next := &Snapshot{Listeners: map[int]*Listener{1: {Upstreams: []*Upstream{{ID: "same"}}}}}
	next.Listeners[1].Upstreams[0].SetHealthy(true)
	copyHealth(old, next)
	if next.Listeners[1].Upstreams[0].Healthy() {
		t.Fatal("health state was not retained")
	}
}

func TestExpiredCredentialMetadata(t *testing.T) {
	expired := time.Now().Add(-time.Second)
	snapshot, err := buildSnapshot(1, "development", rawSnapshot{Environment: "development"}, []credentialRecord{{ID: "key", ConsumerID: "consumer", Prefix: "abcdef123456", Verifier: make([]byte, 32), ExpiresAt: &expired}}, []netip.Prefix{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if snapshot.Credentials["abcdef123456"].ExpiresAt == nil {
		t.Fatal("expiry not retained")
	}
}

func TestHealthProbeTransitions(t *testing.T) {
	var successful atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if successful.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	upstream := &Upstream{ID: "health", Address: netip.MustParseAddr(host), Port: port, Weight: 1, HealthCheckPath: "/health"}
	upstream.SetHealthy(true)
	runtime := NewRuntime(RuntimeOptions{HealthTimeout: time.Second})
	runtime.current.Store(&Snapshot{Listeners: map[int]*Listener{1: {Upstreams: []*Upstream{upstream}}}})
	runtime.probe(t.Context())
	if upstream.Healthy() {
		t.Fatal("failed health probe remained healthy")
	}
	successful.Store(true)
	runtime.probe(t.Context())
	if !upstream.Healthy() {
		t.Fatal("successful health probe remained unhealthy")
	}
}
