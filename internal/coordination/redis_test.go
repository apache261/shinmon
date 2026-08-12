package coordination

import "testing"

func TestNewRejectsMalformedRedisURL(t *testing.T) {
	if _, err := New("not a redis url"); err == nil {
		t.Fatal("malformed Redis URL accepted")
	}
}

func TestCoordinationKeysAreEnvironmentScoped(t *testing.T) {
	if eventChannel("staging") != "shinmon:staging:events" || circuitOpenKey("production", "up_1") != "shinmon:production:circuit:up_1:open" {
		t.Fatal("coordination keys are not scoped")
	}
}
