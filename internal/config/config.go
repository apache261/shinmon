package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Role identifies which binary is loading its bootstrap configuration.
type Role string

const (
	DataPlane Role = "gateway"
	Admin     Role = "gateway-admin"
)

// Secret deliberately redacts itself when formatted. Value should only be used
// at the point where authentication is performed.
type Secret struct {
	value string
}

func newSecret(value string) Secret { return Secret{value: value} }
func (s Secret) Value() string      { return s.value }
func (s Secret) String() string     { return "[REDACTED]" }
func (s Secret) GoString() string   { return "[REDACTED]" }

// Config contains process bootstrap settings. Runtime gateway configuration
// will be introduced separately with the PostgreSQL-backed control plane.
type Config struct {
	Role                           Role
	Environment                    string
	ListenAddr                     string
	LogLevel                       slog.Level
	ShutdownTimeout                time.Duration
	TrustedProxyCIDRs              []netip.Prefix
	AdminBearerToken               Secret
	APIKeyPepper                   Secret
	DatabaseURL                    Secret
	DatabaseMinConns               int32
	DatabaseMaxConns               int32
	DatabaseTimeout                time.Duration
	UpstreamCIDRs                  []netip.Prefix
	ConfigPollInterval             time.Duration
	ConfigPollJitter               time.Duration
	HealthInterval                 time.Duration
	HealthTimeout                  time.Duration
	GatewayInstanceID              string
	GatewayAdvertiseAddr           string
	MetricsBearerToken             Secret
	RedisURL                       Secret
	ConfigurationApprovalsRequired int
	TLSCertFile                    string
	TLSKeyFile                     string
	UpstreamTLSCAFile              string
}

type lookupFunc func(string) (string, bool)

// Load reads configuration from the process environment.
func Load(role Role) (Config, error) {
	return load(role, os.LookupEnv)
}

func load(role Role, lookup lookupFunc) (Config, error) {
	if role != DataPlane && role != Admin {
		return Config{}, fmt.Errorf("unsupported process role %q", role)
	}

	environment := strings.TrimSpace(value(lookup, "GATEWAY_ENVIRONMENT", ""))
	if environment == "" {
		return Config{}, errors.New("GATEWAY_ENVIRONMENT is required")
	}
	switch environment {
	case "development", "staging", "production":
	default:
		return Config{}, errors.New("GATEWAY_ENVIRONMENT must be development, staging, or production")
	}

	listenVariable := "GATEWAY_HTTP_ADDR"
	listenDefault := ":4040"
	if role == Admin {
		listenVariable = "GATEWAY_ADMIN_HTTP_ADDR"
		listenDefault = "127.0.0.1:4041"
	}
	listenAddr := strings.TrimSpace(value(lookup, listenVariable, listenDefault))
	if err := validateListenAddr(listenAddr); err != nil {
		return Config{}, fmt.Errorf("%s is invalid: %w", listenVariable, err)
	}

	level, err := parseLogLevel(value(lookup, "GATEWAY_LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := time.ParseDuration(strings.TrimSpace(value(lookup, "GATEWAY_SHUTDOWN_TIMEOUT", "10s")))
	if err != nil {
		return Config{}, errors.New("GATEWAY_SHUTDOWN_TIMEOUT must be a valid duration")
	}
	if shutdownTimeout <= 0 {
		return Config{}, errors.New("GATEWAY_SHUTDOWN_TIMEOUT must be positive")
	}

	config := Config{
		Role:            role,
		Environment:     environment,
		ListenAddr:      listenAddr,
		LogLevel:        level,
		ShutdownTimeout: shutdownTimeout,
	}
	config.TLSCertFile = strings.TrimSpace(value(lookup, "GATEWAY_TLS_CERT_FILE", ""))
	config.TLSKeyFile = strings.TrimSpace(value(lookup, "GATEWAY_TLS_KEY_FILE", ""))
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		return Config{}, errors.New("GATEWAY_TLS_CERT_FILE and GATEWAY_TLS_KEY_FILE must be configured together")
	}

	if role == DataPlane {
		config.UpstreamTLSCAFile = strings.TrimSpace(value(lookup, "GATEWAY_UPSTREAM_TLS_CA_FILE", ""))
		cidrValue := strings.TrimSpace(value(lookup, "GATEWAY_TRUSTED_PROXY_CIDRS", ""))
		if cidrValue == "" {
			return Config{}, errors.New("GATEWAY_TRUSTED_PROXY_CIDRS is required")
		}
		for _, raw := range strings.Split(cidrValue, ",") {
			prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(raw))
			if parseErr != nil {
				return Config{}, errors.New("GATEWAY_TRUSTED_PROXY_CIDRS contains an invalid CIDR")
			}
			config.TrustedProxyCIDRs = append(config.TrustedProxyCIDRs, prefix.Masked())
		}
		config.GatewayInstanceID = strings.TrimSpace(value(lookup, "GATEWAY_INSTANCE_ID", ""))
		if config.GatewayInstanceID == "" || len(config.GatewayInstanceID) > 128 {
			return Config{}, errors.New("GATEWAY_INSTANCE_ID is required and must not exceed 128 characters")
		}
		config.GatewayAdvertiseAddr = strings.TrimSpace(value(lookup, "GATEWAY_ADVERTISE_ADDR", listenAddr))
		if err := validateListenAddr(config.GatewayAdvertiseAddr); err != nil {
			return Config{}, fmt.Errorf("GATEWAY_ADVERTISE_ADDR is invalid: %w", err)
		}
		var durationErr error
		config.ConfigPollInterval, durationErr = positiveDuration(lookup, "GATEWAY_CONFIG_POLL_INTERVAL", "2s")
		if durationErr != nil {
			return Config{}, durationErr
		}
		config.ConfigPollJitter, durationErr = nonNegativeDuration(lookup, "GATEWAY_CONFIG_POLL_JITTER", "500ms")
		if durationErr != nil {
			return Config{}, durationErr
		}
		if config.ConfigPollJitter >= config.ConfigPollInterval {
			return Config{}, errors.New("GATEWAY_CONFIG_POLL_JITTER must be shorter than GATEWAY_CONFIG_POLL_INTERVAL")
		}
		config.HealthInterval, durationErr = positiveDuration(lookup, "GATEWAY_UPSTREAM_HEALTH_INTERVAL", "10s")
		if durationErr != nil {
			return Config{}, durationErr
		}
		config.HealthTimeout, durationErr = positiveDuration(lookup, "GATEWAY_UPSTREAM_HEALTH_TIMEOUT", "2s")
		if durationErr != nil {
			return Config{}, durationErr
		}
		if config.HealthTimeout >= config.HealthInterval {
			return Config{}, errors.New("GATEWAY_UPSTREAM_HEALTH_TIMEOUT must be shorter than GATEWAY_UPSTREAM_HEALTH_INTERVAL")
		}
	} else {
		token := value(lookup, "GATEWAY_ADMIN_BEARER_TOKEN", "")
		if len(token) < 32 {
			return Config{}, errors.New("GATEWAY_ADMIN_BEARER_TOKEN must contain at least 32 characters")
		}
		config.AdminBearerToken = newSecret(token)
		approvals, approvalsErr := strconv.Atoi(strings.TrimSpace(value(lookup, "GATEWAY_CONFIGURATION_APPROVALS_REQUIRED", "0")))
		if approvalsErr != nil || approvals < 0 || approvals > 5 {
			return Config{}, errors.New("GATEWAY_CONFIGURATION_APPROVALS_REQUIRED must be an integer from 0 to 5")
		}
		config.ConfigurationApprovalsRequired = approvals
	}

	redisURL := strings.TrimSpace(value(lookup, "GATEWAY_REDIS_URL", "redis://127.0.0.1:4044/0"))
	parsedRedisURL, redisErr := url.Parse(redisURL)
	if redisErr != nil || (parsedRedisURL.Scheme != "redis" && parsedRedisURL.Scheme != "rediss") || parsedRedisURL.Host == "" {
		return Config{}, errors.New("GATEWAY_REDIS_URL must be a valid redis or rediss URL")
	}
	config.RedisURL = newSecret(redisURL)

	pepper := value(lookup, "GATEWAY_API_KEY_PEPPER", "")
	if len(pepper) < 32 {
		return Config{}, errors.New("GATEWAY_API_KEY_PEPPER must contain at least 32 characters")
	}
	config.APIKeyPepper = newSecret(pepper)

	databaseURL := strings.TrimSpace(value(lookup, "GATEWAY_DATABASE_URL", ""))
	if databaseURL == "" {
		return Config{}, errors.New("GATEWAY_DATABASE_URL is required")
	}
	config.DatabaseURL = newSecret(databaseURL)

	minConnections, parseErr := parseConnectionCount(value(lookup, "GATEWAY_DATABASE_MIN_CONNECTIONS", "1"), "GATEWAY_DATABASE_MIN_CONNECTIONS")
	if parseErr != nil {
		return Config{}, parseErr
	}
	maxConnections, parseErr := parseConnectionCount(value(lookup, "GATEWAY_DATABASE_MAX_CONNECTIONS", "10"), "GATEWAY_DATABASE_MAX_CONNECTIONS")
	if parseErr != nil {
		return Config{}, parseErr
	}
	if minConnections > maxConnections {
		return Config{}, errors.New("GATEWAY_DATABASE_MIN_CONNECTIONS must not exceed GATEWAY_DATABASE_MAX_CONNECTIONS")
	}
	config.DatabaseMinConns = minConnections
	config.DatabaseMaxConns = maxConnections

	databaseTimeout, parseErr := positiveDuration(lookup, "GATEWAY_DATABASE_CONNECT_TIMEOUT", "10s")
	if parseErr != nil {
		return Config{}, parseErr
	}
	config.DatabaseTimeout = databaseTimeout

	metricsToken := value(lookup, "GATEWAY_METRICS_BEARER_TOKEN", "")
	if metricsToken != "" && len(metricsToken) < 32 {
		return Config{}, errors.New("GATEWAY_METRICS_BEARER_TOKEN must be empty or contain at least 32 characters")
	}
	config.MetricsBearerToken = newSecret(metricsToken)

	upstreamCIDRs := strings.TrimSpace(value(lookup, "GATEWAY_UPSTREAM_ALLOWED_CIDRS", ""))
	if upstreamCIDRs == "" {
		return Config{}, errors.New("GATEWAY_UPSTREAM_ALLOWED_CIDRS is required")
	}
	for _, raw := range strings.Split(upstreamCIDRs, ",") {
		prefix, prefixErr := netip.ParsePrefix(strings.TrimSpace(raw))
		if prefixErr != nil {
			return Config{}, errors.New("GATEWAY_UPSTREAM_ALLOWED_CIDRS contains an invalid CIDR")
		}
		config.UpstreamCIDRs = append(config.UpstreamCIDRs, prefix.Masked())
	}

	return config, nil
}

func positiveDuration(lookup lookupFunc, name, fallback string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value(lookup, name, fallback)))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func nonNegativeDuration(lookup lookupFunc, name, fallback string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value(lookup, name, fallback)))
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration", name)
	}
	return parsed, nil
}

func parseConnectionCount(raw, name string) (int32, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	if parsed == 0 && name == "GATEWAY_DATABASE_MAX_CONNECTIONS" {
		return 0, errors.New("GATEWAY_DATABASE_MAX_CONNECTIONS must be positive")
	}
	return int32(parsed), nil
}

func value(lookup lookupFunc, key, fallback string) string {
	if configured, ok := lookup(key); ok {
		return configured
	}
	return fallback
}

func validateListenAddr(address string) error {
	if address == "" {
		return errors.New("value is empty")
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("expected host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("GATEWAY_LOG_LEVEL must be debug, info, warn, or error")
	}
}
