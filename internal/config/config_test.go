package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadDataPlaneDefaults(t *testing.T) {
	configured, err := load(DataPlane, mapLookup(map[string]string{
		"GATEWAY_ENVIRONMENT":            "development",
		"GATEWAY_TRUSTED_PROXY_CIDRS":    "127.0.0.1/32, 10.0.0.25/8",
		"GATEWAY_INSTANCE_ID":            "gateway-test-1",
		"GATEWAY_API_KEY_PEPPER":         strings.Repeat("p", 32),
		"GATEWAY_DATABASE_URL":           "postgres://localhost/shinmon",
		"GATEWAY_UPSTREAM_ALLOWED_CIDRS": "192.168.0.0/16",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if configured.ListenAddr != ":4040" {
		t.Fatalf("ListenAddr = %q", configured.ListenAddr)
	}
	if configured.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v", configured.LogLevel)
	}
	if configured.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %v", configured.ShutdownTimeout)
	}
	if got := configured.TrustedProxyCIDRs[1].String(); got != "10.0.0.0/8" {
		t.Fatalf("masked CIDR = %q", got)
	}
}

func TestLoadAdminDefaults(t *testing.T) {
	configured, err := load(Admin, mapLookup(map[string]string{
		"GATEWAY_ENVIRONMENT":            "staging",
		"GATEWAY_ADMIN_BEARER_TOKEN":     strings.Repeat("x", 32),
		"GATEWAY_API_KEY_PEPPER":         strings.Repeat("p", 32),
		"GATEWAY_DATABASE_URL":           "postgres://localhost/shinmon",
		"GATEWAY_UPSTREAM_ALLOWED_CIDRS": "192.168.0.0/16",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if configured.ListenAddr != "127.0.0.1:4041" {
		t.Fatalf("ListenAddr = %q", configured.ListenAddr)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	validData := map[string]string{
		"GATEWAY_ENVIRONMENT":            "development",
		"GATEWAY_TRUSTED_PROXY_CIDRS":    "127.0.0.1/32",
		"GATEWAY_INSTANCE_ID":            "gateway-test-1",
		"GATEWAY_API_KEY_PEPPER":         strings.Repeat("p", 32),
		"GATEWAY_DATABASE_URL":           "postgres://localhost/shinmon",
		"GATEWAY_UPSTREAM_ALLOWED_CIDRS": "192.168.0.0/16",
	}
	validAdmin := map[string]string{
		"GATEWAY_ENVIRONMENT":            "development",
		"GATEWAY_ADMIN_BEARER_TOKEN":     strings.Repeat("x", 32),
		"GATEWAY_API_KEY_PEPPER":         strings.Repeat("p", 32),
		"GATEWAY_DATABASE_URL":           "postgres://localhost/shinmon",
		"GATEWAY_UPSTREAM_ALLOWED_CIDRS": "192.168.0.0/16",
	}

	tests := []struct {
		name     string
		role     Role
		values   map[string]string
		contains string
	}{
		{name: "missing environment", role: DataPlane, values: map[string]string{}, contains: "GATEWAY_ENVIRONMENT is required"},
		{name: "invalid environment", role: DataPlane, values: merge(validData, "GATEWAY_ENVIRONMENT", "qa"), contains: "must be development"},
		{name: "malformed data address", role: DataPlane, values: merge(validData, "GATEWAY_HTTP_ADDR", "localhost"), contains: "GATEWAY_HTTP_ADDR is invalid"},
		{name: "invalid port", role: Admin, values: merge(validAdmin, "GATEWAY_ADMIN_HTTP_ADDR", "localhost:70000"), contains: "port must be"},
		{name: "invalid log level", role: DataPlane, values: merge(validData, "GATEWAY_LOG_LEVEL", "verbose"), contains: "GATEWAY_LOG_LEVEL"},
		{name: "malformed duration", role: DataPlane, values: merge(validData, "GATEWAY_SHUTDOWN_TIMEOUT", "soon"), contains: "valid duration"},
		{name: "zero duration", role: DataPlane, values: merge(validData, "GATEWAY_SHUTDOWN_TIMEOUT", "0s"), contains: "must be positive"},
		{name: "missing CIDRs", role: DataPlane, values: merge(validData, "GATEWAY_TRUSTED_PROXY_CIDRS", ""), contains: "is required"},
		{name: "malformed CIDR", role: DataPlane, values: merge(validData, "GATEWAY_TRUSTED_PROXY_CIDRS", "not-a-cidr"), contains: "invalid CIDR"},
		{name: "missing instance ID", role: DataPlane, values: merge(validData, "GATEWAY_INSTANCE_ID", ""), contains: "GATEWAY_INSTANCE_ID is required"},
		{name: "health timeout too long", role: DataPlane, values: merge(merge(validData, "GATEWAY_UPSTREAM_HEALTH_INTERVAL", "1s"), "GATEWAY_UPSTREAM_HEALTH_TIMEOUT", "1s"), contains: "must be shorter"},
		{name: "short admin token", role: Admin, values: merge(validAdmin, "GATEWAY_ADMIN_BEARER_TOKEN", "too-short"), contains: "at least 32"},
		{name: "short API key pepper", role: Admin, values: merge(validAdmin, "GATEWAY_API_KEY_PEPPER", "too-short"), contains: "GATEWAY_API_KEY_PEPPER"},
		{name: "missing database URL", role: Admin, values: merge(validAdmin, "GATEWAY_DATABASE_URL", ""), contains: "GATEWAY_DATABASE_URL is required"},
		{name: "invalid minimum connections", role: Admin, values: merge(validAdmin, "GATEWAY_DATABASE_MIN_CONNECTIONS", "many"), contains: "non-negative integer"},
		{name: "minimum exceeds maximum", role: Admin, values: merge(merge(validAdmin, "GATEWAY_DATABASE_MIN_CONNECTIONS", "11"), "GATEWAY_DATABASE_MAX_CONNECTIONS", "10"), contains: "must not exceed"},
		{name: "invalid database timeout", role: Admin, values: merge(validAdmin, "GATEWAY_DATABASE_CONNECT_TIMEOUT", "0s"), contains: "positive duration"},
		{name: "short metrics token", role: Admin, values: merge(validAdmin, "GATEWAY_METRICS_BEARER_TOKEN", "too-short"), contains: "GATEWAY_METRICS_BEARER_TOKEN"},
		{name: "invalid Redis URL", role: DataPlane, values: merge(validData, "GATEWAY_REDIS_URL", "http://redis:6379"), contains: "GATEWAY_REDIS_URL"},
		{name: "invalid approval count", role: Admin, values: merge(validAdmin, "GATEWAY_CONFIGURATION_APPROVALS_REQUIRED", "6"), contains: "APPROVALS_REQUIRED"},
		{name: "TLS certificate without key", role: Admin, values: merge(validAdmin, "GATEWAY_TLS_CERT_FILE", "/cert.pem"), contains: "must be configured together"},
		{name: "TLS key without certificate", role: DataPlane, values: merge(validData, "GATEWAY_TLS_KEY_FILE", "/key.pem"), contains: "must be configured together"},
		{name: "missing upstream allowlist", role: Admin, values: merge(validAdmin, "GATEWAY_UPSTREAM_ALLOWED_CIDRS", ""), contains: "GATEWAY_UPSTREAM_ALLOWED_CIDRS is required"},
		{name: "invalid upstream allowlist", role: Admin, values: merge(validAdmin, "GATEWAY_UPSTREAM_ALLOWED_CIDRS", "internal"), contains: "invalid CIDR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(test.role, mapLookup(test.values))
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestSecretIsRedacted(t *testing.T) {
	plaintext := "this-is-a-sensitive-token-value-123456"
	secret := newSecret(plaintext)
	configured := Config{AdminBearerToken: secret, APIKeyPepper: secret, DatabaseURL: secret, RedisURL: secret}

	formatted := fmt.Sprintf("%v %+v %#v %s", secret, secret, secret, configured.AdminBearerToken)
	encoded, err := json.Marshal(configured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(formatted, plaintext) || strings.Contains(string(encoded), plaintext) {
		t.Fatal("secret was exposed by formatting or JSON encoding")
	}
	if secret.Value() != plaintext {
		t.Fatal("secret value was not retained")
	}
}

func mapLookup(values map[string]string) lookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func merge(source map[string]string, key, value string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for sourceKey, sourceValue := range source {
		result[sourceKey] = sourceValue
	}
	result[key] = value
	return result
}
