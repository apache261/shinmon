package main

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond, 5 * time.Millisecond}
	if got := percentile(values, .50); got != 3*time.Millisecond {
		t.Fatalf("p50=%v", got)
	}
	if got := percentile(values, .99); got != 5*time.Millisecond {
		t.Fatalf("p99=%v", got)
	}
}
