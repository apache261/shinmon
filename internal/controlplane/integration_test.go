package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache261/Shinmon/internal/controlplane"
	"github.com/apache261/Shinmon/internal/database"
)

type recordingPublisher struct{ events []string }

func (p *recordingPublisher) Publish(_ context.Context, _ string, event string) error {
	p.events = append(p.events, event)
	return nil
}

func TestPostgreSQLControlPlaneWorkflow(t *testing.T) {
	databaseURL := os.Getenv("SHINMON_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SHINMON_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL, 1, 12, 10*time.Second)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}

	store := controlplane.NewStore(pool, "integration-test-pepper-at-least-32-characters", []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")})
	publisher := &recordingPublisher{}
	store.ConfigureDistributed(publisher, nil, 0)
	unique := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	permissionName := unique + ":stable:invoke"
	permission, err := store.CreatePermission(ctx, "integration-test", "staging", permissionName, "integration permission")
	if err != nil {
		t.Fatalf("create permission: %v", err)
	}
	service, err := store.CreateService(ctx, "integration-test", "staging", unique, "Integration Service", "tests")
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	service, err = store.UpdateService(ctx, "integration-test", "staging", service.Name, "Updated Integration Service", "platform-tests", true, service.RowVersion)
	if err != nil || service.DisplayName != "Updated Integration Service" {
		t.Fatalf("update service: %+v %v", service, err)
	}
	version, err := store.CreateServiceVersion(ctx, "integration-test", "staging", service.Name, "stable", "/health", 1000, 1024)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	version, err = store.UpdateServiceVersion(ctx, "integration-test", "staging", version.ID, "stable", "/ready", 1500, 2048, true, version.RowVersion)
	if err != nil || version.HealthCheckPath != "/ready" {
		t.Fatalf("update version: %+v %v", version, err)
	}
	versions, err := store.ListServiceVersions(ctx, "staging", service.Name)
	if err != nil || len(versions) != 1 || versions[0].ID != version.ID {
		t.Fatalf("list versions: count=%d err=%v", len(versions), err)
	}
	httpUpstream, err := store.CreateUpstream(ctx, "integration-test", "staging", version.ID, "192.168.2.10", 8080, 100, "/health")
	if err != nil {
		t.Fatalf("create upstream: %v", err)
	}
	httpUpstream, err = store.UpdateUpstream(ctx, "integration-test", "staging", httpUpstream.ID, "http", "192.168.2.11", 8081, 75, "/ready", true, httpUpstream.RowVersion)
	if err != nil || httpUpstream.Address != "192.168.2.11" || httpUpstream.Port != 8081 {
		t.Fatalf("update upstream: %+v %v", httpUpstream, err)
	}
	if _, err = store.CreateUpstreamWithScheme(ctx, "integration-test", "staging", version.ID, "https", "192.168.2.10", 8080, 100, "/health"); err != nil {
		t.Fatalf("create HTTPS upstream: %v", err)
	}
	upstreams, err := store.ListUpstreams(ctx, "staging", version.ID)
	if err != nil || len(upstreams) != 2 || upstreams[0].Scheme == upstreams[1].Scheme {
		t.Fatalf("list HTTP/HTTPS upstreams: %+v err=%v", upstreams, err)
	}

	const allocations = 12
	ports := make(chan int, allocations)
	errorsChannel := make(chan error, allocations)
	var wait sync.WaitGroup
	for range allocations {
		wait.Add(1)
		go func() {
			defer wait.Done()
			listener, allocationErr := store.AllocateListener(ctx, "integration-test", "staging", controlplane.AllocateListenerInput{Service: service.Name, ServiceVersion: "stable", RequiredPermission: permission.Name, AllowedMethods: []string{"GET", "POST"}})
			if allocationErr != nil {
				errorsChannel <- allocationErr
				return
			}
			ports <- listener.ListenPort
		}()
	}
	wait.Wait()
	close(ports)
	close(errorsChannel)
	for allocationErr := range errorsChannel {
		t.Errorf("concurrent allocation: %v", allocationErr)
	}
	seen := map[int]bool{}
	for port := range ports {
		if seen[port] {
			t.Fatalf("port %d allocated twice", port)
		}
		seen[port] = true
	}
	if len(seen) != allocations {
		t.Fatalf("allocated %d ports, want %d", len(seen), allocations)
	}
	listeners, err := store.ListListeners(ctx, "staging")
	if err != nil || len(listeners) < allocations {
		t.Fatalf("list listeners: count=%d err=%v", len(listeners), err)
	}
	policyListener, err := store.UpdateListenerPolicies(ctx, "integration-test", "staging", listeners[0].ID, controlplane.ListenerPolicyInput{RateLimitPerSecond: 25, RateLimitBurst: 5, QuotaRequestsPerMinute: 500, CircuitFailureThreshold: 3, CircuitOpenMS: 5000, UnprotectedRouteRegex: `^/(swagger|docs)(/.*)?$|\.(js|ya?ml)$`, ExpectedVersion: listeners[0].RowVersion})
	if err != nil || policyListener.RateLimitPerSecond != 25 || policyListener.CircuitFailureThreshold != 3 || policyListener.UnprotectedRouteRegex == "" {
		t.Fatalf("update policies: %+v %v", policyListener, err)
	}
	activeListener, err := store.UpdateListener(ctx, "integration-test", "staging", listeners[1].ID, "active", listeners[1].RowVersion)
	if err != nil {
		t.Fatalf("activate listener before block: %v", err)
	}
	blockedPort, err := store.UpdatePortStatus(ctx, "integration-test", "staging", activeListener.ListenPort, "blocked")
	if err != nil || blockedPort.Status != "blocked" {
		t.Fatalf("block active listener port: %+v %v", blockedPort, err)
	}
	var blockedListenerStatus string
	if err = pool.QueryRow(ctx, `SELECT status FROM listeners WHERE id=$1`, activeListener.ID).Scan(&blockedListenerStatus); err != nil || blockedListenerStatus != "disabled" {
		t.Fatalf("blocking port did not disable listener: status=%s err=%v", blockedListenerStatus, err)
	}
	releasedPort, err := store.UpdatePortStatus(ctx, "integration-test", "staging", activeListener.ListenPort, "available")
	if err != nil || releasedPort.Status != "available" || releasedPort.ListenerID != nil {
		t.Fatalf("release disabled listener port: %+v %v", releasedPort, err)
	}
	reusedPort := activeListener.ListenPort
	if _, err = store.AllocateListener(ctx, "integration-test", "staging", controlplane.AllocateListenerInput{Service: service.Name, ServiceVersion: "stable", PreferredPort: &reusedPort, RequiredPermission: permission.Name, AllowedMethods: []string{"GET"}}); err != nil {
		t.Fatalf("reallocate released port: %v", err)
	}
	temporaryPermission, err := store.CreatePermission(ctx, "integration-test", "staging", unique+":v1:read", "temporary")
	if err != nil {
		t.Fatalf("create temporary permission: %v", err)
	}
	if temporaryPermission, err = store.UpdatePermission(ctx, "integration-test", "staging", temporaryPermission.ID, "updated notes"); err != nil || temporaryPermission.Description != "updated notes" {
		t.Fatalf("update temporary permission: %+v %v", temporaryPermission, err)
	}
	if err = store.DeletePermission(ctx, "integration-test", "staging", temporaryPermission.ID); err != nil {
		t.Fatalf("delete unused permission: %v", err)
	}

	consumer, err := store.CreateConsumer(ctx, "integration-test", "staging", unique, "Integration Consumer", []string{permissionName})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	listedConsumers, err := store.ListConsumers(ctx, "staging")
	if err != nil {
		t.Fatalf("list consumers with permissions: %v", err)
	}
	var listedConsumer controlplane.Consumer
	for _, item := range listedConsumers {
		if item.ID == consumer.ID {
			listedConsumer = item
		}
	}
	if len(listedConsumer.Permissions) != 1 || listedConsumer.Permissions[0] != permissionName {
		t.Fatalf("consumer permissions missing: %+v", listedConsumer.Permissions)
	}
	consumer, err = store.UpdateConsumer(ctx, "integration-test", "staging", consumer.ID, "Updated Integration Consumer", true, []string{permissionName}, consumer.RowVersion)
	if err != nil || consumer.DisplayName != "Updated Integration Consumer" {
		t.Fatalf("update consumer: %+v %v", consumer, err)
	}
	temporaryConsumer, err := store.CreateConsumer(ctx, "integration-test", "staging", unique+"-temp", "Temporary Consumer", nil)
	if err != nil {
		t.Fatalf("create temporary consumer: %v", err)
	}
	if err = store.DeleteConsumer(ctx, "integration-test", "staging", temporaryConsumer.ID); err != nil {
		t.Fatalf("delete keyless consumer: %v", err)
	}
	expires := time.Now().Add(time.Hour)
	issued, err := store.IssueKey(ctx, "integration-test", "staging", consumer.ID, "integration key", []string{permissionName}, &expires, nil)
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}
	if !strings.HasPrefix(issued.Key, "shn_") {
		t.Fatalf("unexpected key format")
	}
	var verifier []byte
	if err = pool.QueryRow(ctx, `SELECT verifier FROM api_keys WHERE id=$1`, issued.ID).Scan(&verifier); err != nil {
		t.Fatalf("read verifier: %v", err)
	}
	if string(verifier) == issued.Key || len(verifier) != 32 {
		t.Fatal("raw API key was not protected")
	}
	listedKeys, err := store.ListKeys(ctx, "staging", consumer.ID)
	if err != nil || len(listedKeys) != 1 {
		t.Fatalf("list keys: count=%d err=%v", len(listedKeys), err)
	}
	encodedKeys, _ := json.Marshal(listedKeys)
	if strings.Contains(string(encodedKeys), issued.Key) || strings.Contains(string(encodedKeys), `"key"`) {
		t.Fatal("key listing revealed raw key material")
	}
	rotated, err := store.RotateKey(ctx, "integration-test", "staging", issued.ID)
	if err != nil || rotated.RotatedFromID == nil || *rotated.RotatedFromID != issued.ID {
		t.Fatalf("rotate key: %+v %v", rotated, err)
	}
	if len(publisher.events) == 0 || publisher.events[len(publisher.events)-1] != "credentials" {
		t.Fatal("rotation did not publish credential notification")
	}
	var revokedAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT revoked_at FROM api_keys WHERE id=$1`, issued.ID).Scan(&revokedAt); err != nil || revokedAt == nil {
		t.Fatalf("old key not revoked atomically: %v", err)
	}

	configuration, err := store.CreateConfiguration(ctx, "integration-test", "staging")
	if err != nil {
		t.Fatalf("create configuration: %v", err)
	}
	configuration, err = store.ValidateConfiguration(ctx, "integration-test", "staging", configuration.ID)
	if err != nil || configuration.Status != "validated" {
		t.Fatalf("validate configuration: %+v %v", configuration, err)
	}
	configuration, err = store.ActivateConfiguration(ctx, "integration-test", "staging", configuration.ID, nil)
	if err != nil || configuration.Status != "active" {
		t.Fatalf("activate configuration: %+v %v", configuration, err)
	}
	if publisher.events[len(publisher.events)-1] != "configuration" {
		t.Fatal("activation did not publish configuration notification")
	}
	rollback, err := store.RollbackConfiguration(ctx, "integration-test", "staging", configuration.ID)
	if err != nil || rollback.Status != "active" || rollback.SourceVersionID == nil {
		t.Fatalf("rollback: %+v %v", rollback, err)
	}
	store.ConfigureDistributed(publisher, nil, 2)
	approvalConfiguration, err := store.CreateConfiguration(ctx, "configuration-author", "staging")
	if err != nil {
		t.Fatalf("create approval configuration: %v", err)
	}
	approvalConfiguration, err = store.ValidateConfiguration(ctx, "configuration-author", "staging", approvalConfiguration.ID)
	if err != nil {
		t.Fatalf("validate approval configuration: %v", err)
	}
	if _, err = store.ActivateConfiguration(ctx, "configuration-author", "staging", approvalConfiguration.ID, nil); !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("activation without approvals err=%v", err)
	}
	if _, err = store.ApproveConfiguration(ctx, "configuration-author", "staging", approvalConfiguration.ID); err == nil {
		t.Fatal("configuration author approved own change")
	}
	if _, err = store.ApproveConfiguration(ctx, "operator-a", "staging", approvalConfiguration.ID); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if _, err = store.ApproveConfiguration(ctx, "operator-b", "staging", approvalConfiguration.ID); err != nil {
		t.Fatalf("second approval: %v", err)
	}
	approvalConfiguration, err = store.ActivateConfiguration(ctx, "operator-c", "staging", approvalConfiguration.ID, nil)
	if err != nil || approvalConfiguration.Status != "active" {
		t.Fatalf("approved activation: %+v %v", approvalConfiguration, err)
	}

	var auditID int64
	if err = pool.QueryRow(ctx, `SELECT MAX(id) FROM audit_events WHERE actor='integration-test'`).Scan(&auditID); err != nil {
		t.Fatalf("find audit: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE audit_events SET action='tampered' WHERE id=$1`, auditID); err == nil {
		t.Fatal("audit event update unexpectedly succeeded")
	}

	t.Logf("service=%s permission=%s configuration=%d rollback=%d allocated_ports=%d", service.ID, permission.ID, configuration.ID, rollback.ID, len(seen))
}

func ExampleStore() {
	fmt.Println("Set SHINMON_TEST_DATABASE_URL to run PostgreSQL integration tests")
	// Output: Set SHINMON_TEST_DATABASE_URL to run PostgreSQL integration tests
}
