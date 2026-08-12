package dataplane_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/apache261/Shinmon/internal/controlplane"
	"github.com/apache261/Shinmon/internal/coordination"
	"github.com/apache261/Shinmon/internal/database"
	"github.com/apache261/Shinmon/internal/dataplane"
)

func TestRuntimeSnapshotCredentialRefreshAndLastKnownGood(t *testing.T) {
	databaseURL := os.Getenv("SHINMON_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SHINMON_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL, 1, 8, 10*time.Second)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err = database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	pepper := "dataplane-integration-pepper-at-least-32-characters"
	allowlist := []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}
	store := controlplane.NewStore(pool, pepper, allowlist)
	unique := "d" + strconv.FormatInt(time.Now().UnixNano(), 36)
	permissionName := unique + ":v1:invoke"
	permission, err := store.CreatePermission(ctx, "dataplane-test", "production", permissionName, "data plane test")
	if err != nil {
		pool.Close()
		t.Fatalf("permission: %v", err)
	}
	service, err := store.CreateService(ctx, "dataplane-test", "production", unique, "Data Plane Test", "tests")
	if err != nil {
		pool.Close()
		t.Fatalf("service: %v", err)
	}
	version, err := store.CreateServiceVersion(ctx, "dataplane-test", "production", service.Name, "v1", "/health", 250, 2048)
	if err != nil {
		pool.Close()
		t.Fatalf("version: %v", err)
	}
	if _, err = store.CreateUpstream(ctx, "dataplane-test", "production", version.ID, "192.168.50.10", 8080, 100, "/health"); err != nil {
		pool.Close()
		t.Fatalf("upstream: %v", err)
	}
	listener, err := store.AllocateListener(ctx, "dataplane-test", "production", controlplane.AllocateListenerInput{Service: service.Name, ServiceVersion: "v1", RequiredPermission: permission.Name, AllowedMethods: []string{"GET", "HEAD", "POST"}, AllowedContentTypes: []string{"application/json"}, UnprotectedRouteRegex: `^/(swagger|docs)(/.*)?$|\.(js|ya?ml)$`})
	if err != nil {
		pool.Close()
		t.Fatalf("listener: %v", err)
	}
	consumer, err := store.CreateConsumer(ctx, "dataplane-test", "production", unique, "Data Plane Consumer", []string{permissionName})
	if err != nil {
		pool.Close()
		t.Fatalf("consumer: %v", err)
	}
	issued, err := store.IssueKey(ctx, "dataplane-test", "production", consumer.ID, "data plane key", []string{permissionName}, nil, nil)
	if err != nil {
		pool.Close()
		t.Fatalf("key: %v", err)
	}
	configuration, err := store.CreateConfiguration(ctx, "dataplane-test", "production")
	if err != nil {
		pool.Close()
		t.Fatalf("configuration: %v", err)
	}
	configuration, err = store.ValidateConfiguration(ctx, "dataplane-test", "production", configuration.ID)
	if err != nil {
		pool.Close()
		t.Fatalf("validate: %v", err)
	}
	configuration, err = store.ActivateConfiguration(ctx, "dataplane-test", "production", configuration.ID, nil)
	if err != nil {
		pool.Close()
		t.Fatalf("activate: %v", err)
	}
	events := make(chan coordination.Event, 2)
	runtime := dataplane.NewRuntime(dataplane.RuntimeOptions{Pool: pool, Environment: "production", Allowlist: allowlist, InstanceID: "integration-gateway", AdvertiseAddr: "127.0.0.1:4040", PollInterval: time.Hour, HealthInterval: time.Hour, HealthTimeout: 100 * time.Millisecond, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Events: events})
	if err = runtime.Load(ctx); err != nil {
		pool.Close()
		t.Fatalf("runtime load: %v", err)
	}
	runContext, stopRuntime := context.WithCancel(ctx)
	runtimeDone := make(chan struct{})
	go func() { runtime.Run(runContext); close(runtimeDone) }()
	snapshot := runtime.Snapshot()
	if snapshot.Version != configuration.ID || snapshot.Listeners[listener.ListenPort] == nil {
		pool.Close()
		t.Fatalf("unexpected snapshot version=%d listeners=%d", snapshot.Version, len(snapshot.Listeners))
	}
	if snapshot.Listeners[listener.ListenPort].UnprotectedRouteRegex == nil || !snapshot.Listeners[listener.ListenPort].UnprotectedRouteRegex.MatchString("/swagger/index.html") {
		pool.Close()
		t.Fatal("unprotected route regex missing from active snapshot")
	}
	prefix := issued.Key[4:16]
	if snapshot.Credentials[prefix] == nil {
		pool.Close()
		t.Fatal("issued credential missing from snapshot")
	}
	nextConfiguration, err := store.CreateConfiguration(ctx, "dataplane-test", "production")
	if err != nil {
		pool.Close()
		t.Fatalf("next configuration: %v", err)
	}
	nextConfiguration, err = store.ValidateConfiguration(ctx, "dataplane-test", "production", nextConfiguration.ID)
	if err != nil {
		pool.Close()
		t.Fatalf("validate next configuration: %v", err)
	}
	expectedActive := configuration.ID
	nextConfiguration, err = store.ActivateConfiguration(ctx, "dataplane-test", "production", nextConfiguration.ID, &expectedActive)
	if err != nil {
		pool.Close()
		t.Fatalf("activate next configuration: %v", err)
	}
	previousSnapshot := snapshot
	events <- coordination.Event{Type: "configuration"}
	deadline := time.Now().Add(time.Second)
	for runtime.Snapshot().Version != nextConfiguration.ID && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runtime.Snapshot() == previousSnapshot || runtime.Snapshot().Version != nextConfiguration.ID || previousSnapshot.Version != configuration.ID {
		pool.Close()
		t.Fatal("configuration snapshot was not atomically replaced")
	}
	if err = store.RevokeKey(ctx, "dataplane-test", "production", issued.ID); err != nil {
		pool.Close()
		t.Fatalf("revoke: %v", err)
	}
	events <- coordination.Event{Type: "credentials"}
	deadline = time.Now().Add(time.Second)
	for runtime.Snapshot().Credentials[prefix] != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	lastGood := runtime.Snapshot()
	if lastGood.Credentials[prefix] != nil {
		pool.Close()
		t.Fatal("revoked credential remained in snapshot")
	}
	stopRuntime()
	<-runtimeDone
	pool.Close()
	if err = runtime.Load(ctx); err == nil {
		t.Fatal("load unexpectedly succeeded after database close")
	}
	if runtime.Snapshot() != lastGood {
		t.Fatal("failed refresh replaced last-known-good snapshot")
	}
}
