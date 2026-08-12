package coordination

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRedisDistributedPoliciesAndNotifications(t *testing.T) {
	rawURL := os.Getenv("SHINMON_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("SHINMON_TEST_REDIS_URL is not set")
	}
	first, err := New(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unique := time.Now().UTC().Format("150405.000000000")
	environment := "test-" + unique
	events := second.Events(ctx, environment)
	time.Sleep(50 * time.Millisecond)
	if err = first.Publish(ctx, environment, "credentials"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case event := <-events:
		if event.Type != "credentials" {
			t.Fatalf("event=%+v", event)
		}
	case <-ctx.Done():
		t.Fatal("notification not delivered")
	}
	if decision, err := first.AllowRequest(ctx, environment, "listener", "consumer", 1, 0, 0); err != nil || decision != DecisionAllow {
		t.Fatalf("first rate decision=%d err=%v", decision, err)
	}
	if decision, err := second.AllowRequest(ctx, environment, "listener", "consumer", 1, 0, 0); err != nil || decision != DecisionRateLimited {
		t.Fatalf("distributed rate decision=%d err=%v", decision, err)
	}
	if decision, err := first.AllowRequest(ctx, environment, "quota", "consumer", 0, 0, 1); err != nil || decision != DecisionAllow {
		t.Fatalf("first quota decision=%d err=%v", decision, err)
	}
	if decision, err := second.AllowRequest(ctx, environment, "quota", "consumer", 0, 0, 1); err != nil || decision != DecisionQuotaExceeded {
		t.Fatalf("distributed quota decision=%d err=%v", decision, err)
	}
	if err = first.RecordUpstream(ctx, environment, "upstream", false, 2, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err = second.RecordUpstream(ctx, environment, "upstream", false, 2, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if open, err := first.CircuitOpen(ctx, environment, "upstream"); err != nil || !open {
		t.Fatalf("open=%v err=%v", open, err)
	}
	time.Sleep(150 * time.Millisecond)
	if open, err := second.CircuitOpen(ctx, environment, "upstream"); err != nil || open {
		t.Fatalf("expired open=%v err=%v", open, err)
	}
}
