package controlplane

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource conflict")
	ErrInvalid  = errors.New("invalid request")
)

type Service struct {
	ID          string    `json:"id"`
	Environment string    `json:"environment"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Owner       string    `json:"owner"`
	Enabled     bool      `json:"enabled"`
	RowVersion  int64     `json:"rowVersion"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ServiceVersion struct {
	ID               string    `json:"id"`
	ServiceID        string    `json:"serviceId"`
	Version          string    `json:"version"`
	HealthCheckPath  string    `json:"healthCheckPath"`
	RequestTimeoutMS int       `json:"requestTimeoutMs"`
	MaxRequestBytes  int64     `json:"maxRequestBytes"`
	Enabled          bool      `json:"enabled"`
	RowVersion       int64     `json:"rowVersion"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Upstream struct {
	ID               string    `json:"id"`
	ServiceVersionID string    `json:"serviceVersionId"`
	Scheme           string    `json:"scheme"`
	Address          string    `json:"address"`
	Port             int       `json:"port"`
	Weight           int       `json:"weight"`
	HealthCheckPath  string    `json:"healthCheckPath"`
	Enabled          bool      `json:"enabled"`
	RowVersion       int64     `json:"rowVersion"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Port struct {
	Environment string    `json:"environment"`
	ListenPort  int       `json:"listenPort"`
	Status      string    `json:"status"`
	ListenerID  *string   `json:"listenerId,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Listener struct {
	ID                      string    `json:"listenerId"`
	Environment             string    `json:"environment"`
	ServiceVersionID        string    `json:"serviceVersionId"`
	ListenPort              int       `json:"listenPort"`
	RequiredPermission      string    `json:"requiredPermission"`
	AllowedMethods          []string  `json:"allowedMethods"`
	UnprotectedRouteRegex   string    `json:"unprotectedRouteRegex"`
	AuthenticationPolicy    string    `json:"authenticationPolicy"`
	Status                  string    `json:"status"`
	ConfigurationVersion    *int64    `json:"configurationVersion"`
	RowVersion              int64     `json:"rowVersion"`
	CreatedAt               time.Time `json:"createdAt"`
	RateLimitPerSecond      int       `json:"rateLimitPerSecond"`
	RateLimitBurst          int       `json:"rateLimitBurst"`
	QuotaRequestsPerMinute  int       `json:"quotaRequestsPerMinute"`
	CircuitFailureThreshold int       `json:"circuitFailureThreshold"`
	CircuitOpenMS           int       `json:"circuitOpenMs"`
}

type Consumer struct {
	ID          string    `json:"id"`
	Environment string    `json:"environment"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Enabled     bool      `json:"enabled"`
	RowVersion  int64     `json:"rowVersion"`
	CreatedAt   time.Time `json:"createdAt"`
	Permissions []string  `json:"permissions"`
}

type Permission struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

type APIKey struct {
	ID            string     `json:"id"`
	ConsumerID    string     `json:"consumerId"`
	Name          string     `json:"name"`
	MaskedPrefix  string     `json:"maskedPrefix"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	RotatedFromID *string    `json:"rotatedFromId,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

type IssuedAPIKey struct {
	APIKey
	Key string `json:"key"`
}

type Configuration struct {
	ID                int64      `json:"configurationVersion"`
	Environment       string     `json:"environment"`
	Status            string     `json:"status"`
	SourceVersionID   *int64     `json:"sourceVersionId,omitempty"`
	CreatedBy         string     `json:"createdBy"`
	ValidatedAt       *time.Time `json:"validatedAt,omitempty"`
	ActivatedAt       *time.Time `json:"activatedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	ApprovalCount     int        `json:"approvalCount"`
	RequiredApprovals int        `json:"requiredApprovals"`
	ServiceCount      int        `json:"serviceCount"`
	VersionCount      int        `json:"versionCount"`
	UpstreamCount     int        `json:"upstreamCount"`
	ListenerCount     int        `json:"listenerCount"`
}

type ConfigurationApproval struct {
	ConfigurationVersion int64     `json:"configurationVersion"`
	Actor                string    `json:"actor"`
	CreatedAt            time.Time `json:"createdAt"`
}

type AuditEvent struct {
	ID           int64          `json:"id"`
	Environment  *string        `json:"environment,omitempty"`
	Actor        string         `json:"actor"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	Details      map[string]any `json:"details"`
	CreatedAt    time.Time      `json:"createdAt"`
}

type GatewayInstance struct {
	ID                         string    `json:"id"`
	Environment                string    `json:"environment"`
	Address                    string    `json:"address"`
	LoadedConfigurationVersion *int64    `json:"loadedConfigurationVersion,omitempty"`
	Ready                      bool      `json:"ready"`
	LastSeenAt                 time.Time `json:"lastSeenAt"`
}
