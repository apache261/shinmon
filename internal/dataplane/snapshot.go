package dataplane

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Version     int64
	Environment string
	Listeners   map[int]*Listener
	Credentials map[string]*Credential
	LoadedAt    time.Time
}

type Listener struct {
	ID                      string
	Port                    int
	Status                  string
	RequiredPermission      string
	AllowedMethods          map[string]struct{}
	AllowedContentTypes     map[string]struct{}
	UnprotectedRouteRegex   *regexp.Regexp
	RequestTimeout          time.Duration
	MaxRequestBytes         int64
	Upstreams               []*Upstream
	RateLimitPerSecond      int
	RateLimitBurst          int
	QuotaRequestsPerMinute  int
	CircuitFailureThreshold int
	CircuitOpen             time.Duration
	cursor                  atomic.Uint64
}

type Upstream struct {
	ID              string
	Scheme          string
	Address         netip.Addr
	Port            int
	Weight          int
	HealthCheckPath string
	healthy         atomic.Bool
}

func (u *Upstream) Healthy() bool           { return u.healthy.Load() }
func (u *Upstream) SetHealthy(healthy bool) { u.healthy.Store(healthy) }
func (u *Upstream) Authority() string       { return netip.AddrPortFrom(u.Address, uint16(u.Port)).String() }
func (u *Upstream) URLScheme() string {
	if u.Scheme == "https" {
		return "https"
	}
	return "http"
}

type Credential struct {
	ID          string
	ConsumerID  string
	Prefix      string
	Verifier    []byte
	ExpiresAt   *time.Time
	Permissions map[string]struct{}
}

func (l *Listener) SelectUpstream() *Upstream {
	return l.SelectUpstreamWhere(nil)
}

func (l *Listener) SelectUpstreamWhere(allowed func(*Upstream) bool) *Upstream {
	total := 0
	for _, upstream := range l.Upstreams {
		if upstream.Healthy() && (allowed == nil || allowed(upstream)) {
			total += upstream.Weight
		}
	}
	if total == 0 {
		return nil
	}
	selected := int((l.cursor.Add(1) - 1) % uint64(total))
	for _, upstream := range l.Upstreams {
		if !upstream.Healthy() || allowed != nil && !allowed(upstream) {
			continue
		}
		if selected < upstream.Weight {
			return upstream
		}
		selected -= upstream.Weight
	}
	return nil
}

type rawSnapshot struct {
	Environment     string              `json:"environment"`
	Services        []rawService        `json:"services"`
	ServiceVersions []rawServiceVersion `json:"serviceVersions"`
	Upstreams       []rawUpstream       `json:"upstreams"`
	Listeners       []rawListener       `json:"listeners"`
}

type rawService struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}
type rawServiceVersion struct {
	ID               string `json:"id"`
	ServiceID        string `json:"serviceId"`
	RequestTimeoutMS int    `json:"requestTimeoutMs"`
	MaxRequestBytes  int64  `json:"maxRequestBytes"`
	Enabled          bool   `json:"enabled"`
}
type rawUpstream struct {
	ID               string `json:"id"`
	ServiceVersionID string `json:"serviceVersionId"`
	Scheme           string `json:"scheme"`
	Address          string `json:"address"`
	Port             int    `json:"port"`
	Weight           int    `json:"weight"`
	HealthCheckPath  string `json:"healthCheckPath"`
	Enabled          bool   `json:"enabled"`
}
type rawListener struct {
	ID                      string   `json:"id"`
	ListenPort              int      `json:"listenPort"`
	ServiceVersionID        string   `json:"serviceVersionId"`
	RequiredPermission      string   `json:"requiredPermission"`
	AllowedMethods          []string `json:"allowedMethods"`
	AllowedContentTypes     []string `json:"allowedContentTypes"`
	UnprotectedRouteRegex   string   `json:"unprotectedRouteRegex"`
	Status                  string   `json:"status"`
	RateLimitPerSecond      int      `json:"rateLimitPerSecond"`
	RateLimitBurst          int      `json:"rateLimitBurst"`
	QuotaRequestsPerMinute  int      `json:"quotaRequestsPerMinute"`
	CircuitFailureThreshold int      `json:"circuitFailureThreshold"`
	CircuitOpenMS           int      `json:"circuitOpenMs"`
}

type credentialRecord struct {
	ID          string
	ConsumerID  string
	Prefix      string
	Verifier    []byte
	ExpiresAt   *time.Time
	Permissions []string
}

func buildSnapshot(version int64, expectedEnvironment string, raw rawSnapshot, records []credentialRecord, allowlist []netip.Prefix) (*Snapshot, error) {
	if raw.Environment != expectedEnvironment {
		return nil, errors.New("active configuration environment mismatch")
	}
	enabledServices := map[string]bool{}
	for _, service := range raw.Services {
		enabledServices[service.ID] = service.Enabled
	}
	versions := map[string]rawServiceVersion{}
	for _, version := range raw.ServiceVersions {
		if version.Enabled && enabledServices[version.ServiceID] && version.RequestTimeoutMS > 0 && version.MaxRequestBytes > 0 {
			versions[version.ID] = version
		}
	}
	upstreams := map[string][]*Upstream{}
	for _, item := range raw.Upstreams {
		if item.Scheme == "" {
			item.Scheme = "http"
		}
		if item.Scheme != "http" && item.Scheme != "https" {
			return nil, fmt.Errorf("upstream %s has invalid scheme", item.ID)
		}
		if !item.Enabled || item.Port < 1 || item.Port > 65535 || item.Weight < 1 {
			continue
		}
		address, err := netip.ParseAddr(item.Address)
		if err != nil || !allowedAddress(address, allowlist) {
			return nil, fmt.Errorf("upstream %s is outside the configured allowlist", item.ID)
		}
		if _, ok := versions[item.ServiceVersionID]; !ok {
			continue
		}
		path := item.HealthCheckPath
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("upstream %s has invalid health path", item.ID)
		}
		upstream := &Upstream{ID: item.ID, Scheme: item.Scheme, Address: address, Port: item.Port, Weight: item.Weight, HealthCheckPath: path}
		upstream.healthy.Store(true)
		upstreams[item.ServiceVersionID] = append(upstreams[item.ServiceVersionID], upstream)
	}
	listeners := map[int]*Listener{}
	for _, item := range raw.Listeners {
		if item.Status == "disabled" || item.Status == "draining" {
			continue
		}
		version, ok := versions[item.ServiceVersionID]
		if !ok {
			return nil, fmt.Errorf("listener %s references an unavailable version", item.ID)
		}
		pool := upstreams[item.ServiceVersionID]
		if len(pool) == 0 {
			return nil, fmt.Errorf("listener %s has no upstreams", item.ID)
		}
		if item.ListenPort < 1 || item.ListenPort > 65535 {
			return nil, fmt.Errorf("listener %s has invalid port", item.ID)
		}
		if _, exists := listeners[item.ListenPort]; exists {
			return nil, fmt.Errorf("listener port %d is duplicated", item.ListenPort)
		}
		methods := map[string]struct{}{}
		for _, method := range item.AllowedMethods {
			methods[strings.ToUpper(method)] = struct{}{}
		}
		if len(methods) == 0 {
			return nil, fmt.Errorf("listener %s has no allowed methods", item.ID)
		}
		contentTypes := map[string]struct{}{}
		if len(item.AllowedContentTypes) == 0 {
			item.AllowedContentTypes = []string{"application/json", "application/xml", "text/xml", "application/x-www-form-urlencoded"}
		}
		for _, contentType := range item.AllowedContentTypes {
			contentTypes[strings.ToLower(contentType)] = struct{}{}
		}
		var unprotectedRouteRegex *regexp.Regexp
		if item.UnprotectedRouteRegex != "" {
			if len(item.UnprotectedRouteRegex) > 2048 || strings.ContainsAny(item.UnprotectedRouteRegex, "\x00\r\n") {
				return nil, fmt.Errorf("listener %s has an invalid unprotected route regex", item.ID)
			}
			var err error
			unprotectedRouteRegex, err = regexp.Compile(item.UnprotectedRouteRegex)
			if err != nil {
				return nil, fmt.Errorf("listener %s has an invalid unprotected route regex", item.ID)
			}
		}
		sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
		if item.RateLimitPerSecond < 0 || item.RateLimitBurst < 0 || item.QuotaRequestsPerMinute < 0 || item.CircuitFailureThreshold < 0 || item.CircuitOpenMS < 0 {
			return nil, fmt.Errorf("listener %s has invalid distributed policy", item.ID)
		}
		listeners[item.ListenPort] = &Listener{ID: item.ID, Port: item.ListenPort, Status: "active", RequiredPermission: item.RequiredPermission, AllowedMethods: methods, AllowedContentTypes: contentTypes, UnprotectedRouteRegex: unprotectedRouteRegex, RequestTimeout: time.Duration(version.RequestTimeoutMS) * time.Millisecond, MaxRequestBytes: version.MaxRequestBytes, Upstreams: pool, RateLimitPerSecond: item.RateLimitPerSecond, RateLimitBurst: item.RateLimitBurst, QuotaRequestsPerMinute: item.QuotaRequestsPerMinute, CircuitFailureThreshold: item.CircuitFailureThreshold, CircuitOpen: time.Duration(item.CircuitOpenMS) * time.Millisecond}
	}
	credentials := map[string]*Credential{}
	for _, record := range records {
		if len(record.Verifier) != 32 || record.Prefix == "" {
			return nil, fmt.Errorf("credential %s has invalid verifier metadata", record.ID)
		}
		permissions := map[string]struct{}{}
		for _, permission := range record.Permissions {
			permissions[permission] = struct{}{}
		}
		credentials[record.Prefix] = &Credential{ID: record.ID, ConsumerID: record.ConsumerID, Prefix: record.Prefix, Verifier: append([]byte(nil), record.Verifier...), ExpiresAt: record.ExpiresAt, Permissions: permissions}
	}
	return &Snapshot{Version: version, Environment: expectedEnvironment, Listeners: listeners, Credentials: credentials, LoadedAt: time.Now().UTC()}, nil
}

func allowedAddress(address netip.Addr, allowlist []netip.Prefix) bool {
	for _, prefix := range allowlist {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
