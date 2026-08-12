package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
	permissionPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*:[a-zA-Z0-9.-]+:[a-z][a-z0-9-]*$`)
)

type Store struct {
	pool              *pgxpool.Pool
	pepper            []byte
	upstreamCIDRs     []netip.Prefix
	publisher         EventPublisher
	logger            *slog.Logger
	requiredApprovals int
}

type EventPublisher interface {
	Publish(context.Context, string, string) error
}

func NewStore(pool *pgxpool.Pool, pepper string, upstreamCIDRs []netip.Prefix) *Store {
	return &Store{pool: pool, pepper: []byte(pepper), upstreamCIDRs: append([]netip.Prefix(nil), upstreamCIDRs...), logger: slog.Default()}
}

func (s *Store) ConfigureDistributed(publisher EventPublisher, logger *slog.Logger, requiredApprovals int) {
	s.publisher = publisher
	if logger != nil {
		s.logger = logger
	}
	s.requiredApprovals = requiredApprovals
}

func (s *Store) CreateService(ctx context.Context, actor, environment, name, displayName, owner string) (Service, error) {
	name = strings.TrimSpace(name)
	displayName = strings.TrimSpace(displayName)
	if !serviceNamePattern.MatchString(name) || displayName == "" {
		return Service{}, ErrInvalid
	}
	id := newID("svc")
	var result Service
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO services(id, environment_id, name, display_name, owner)
			SELECT $1, id, $3, $4, $5 FROM environments WHERE name=$2
			RETURNING id, $2, name, display_name, owner, enabled, row_version, created_at`, id, environment, name, displayName, strings.TrimSpace(owner))
		if err := row.Scan(&result.ID, &result.Environment, &result.Name, &result.DisplayName, &result.Owner, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "service.created", "service", id, map[string]any{"name": name})
	})
	return result, err
}

func (s *Store) ListServices(ctx context.Context, environment string) ([]Service, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id,e.name,s.name,s.display_name,s.owner,s.enabled,s.row_version,s.created_at FROM services s JOIN environments e ON e.id=s.environment_id WHERE e.name=$1 ORDER BY s.name`, environment)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Service, 0)
	for rows.Next() {
		var item Service
		if err = rows.Scan(&item.ID, &item.Environment, &item.Name, &item.DisplayName, &item.Owner, &item.Enabled, &item.RowVersion, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateService(ctx context.Context, actor, environment, name, displayName, owner string, enabled bool, expectedVersion int64) (Service, error) {
	if strings.TrimSpace(displayName) == "" || expectedVersion < 1 {
		return Service{}, ErrInvalid
	}
	var result Service
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE services s SET display_name=$3,owner=$4,enabled=$5,row_version=row_version+1,updated_at=NOW() FROM environments e WHERE s.environment_id=e.id AND e.name=$1 AND s.name=$2 AND s.row_version=$6 RETURNING s.id,e.name,s.name,s.display_name,s.owner,s.enabled,s.row_version,s.created_at`, environment, name, strings.TrimSpace(displayName), strings.TrimSpace(owner), enabled, expectedVersion)
		if err := row.Scan(&result.ID, &result.Environment, &result.Name, &result.DisplayName, &result.Owner, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "service.updated", "service", result.ID, map[string]any{"enabled": enabled, "expectedVersion": expectedVersion})
	})
	return result, err
}

func (s *Store) CreateServiceVersion(ctx context.Context, actor, environment, serviceName, version, healthPath string, timeoutMS int, maxBytes int64) (ServiceVersion, error) {
	if !validVersion(version) || !validPath(healthPath) || timeoutMS < 1 || timeoutMS > 300000 || maxBytes < 1 || maxBytes > 1<<30 {
		return ServiceVersion{}, ErrInvalid
	}
	id := newID("ver")
	var result ServiceVersion
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO service_versions(id,service_id,version,health_check_path,request_timeout_ms,max_request_bytes)
			SELECT $1,s.id,$4,$5,$6,$7 FROM services s JOIN environments e ON e.id=s.environment_id WHERE e.name=$2 AND s.name=$3
			RETURNING id,service_id,version,health_check_path,request_timeout_ms,max_request_bytes,enabled,row_version,created_at`, id, environment, serviceName, version, healthPath, timeoutMS, maxBytes)
		if err := row.Scan(&result.ID, &result.ServiceID, &result.Version, &result.HealthCheckPath, &result.RequestTimeoutMS, &result.MaxRequestBytes, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "service-version.created", "service-version", id, map[string]any{"version": version})
	})
	return result, err
}

func validVersion(version string) bool {
	return utf8.ValidString(version) && utf8.RuneCountInString(version) >= 1 && utf8.RuneCountInString(version) <= 128 && !strings.ContainsFunc(version, unicode.IsSpace)
}

func (s *Store) ListServiceVersions(ctx context.Context, environment, serviceName string) ([]ServiceVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.id,v.service_id,v.version,v.health_check_path,v.request_timeout_ms,v.max_request_bytes,v.enabled,v.row_version,v.created_at FROM service_versions v JOIN services s ON s.id=v.service_id JOIN environments e ON e.id=s.environment_id WHERE e.name=$1 AND s.name=$2 ORDER BY v.version`, environment, serviceName)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]ServiceVersion, 0)
	for rows.Next() {
		var item ServiceVersion
		if err = rows.Scan(&item.ID, &item.ServiceID, &item.Version, &item.HealthCheckPath, &item.RequestTimeoutMS, &item.MaxRequestBytes, &item.Enabled, &item.RowVersion, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateServiceVersion(ctx context.Context, actor, environment, id, version, healthPath string, timeoutMS int, maxBytes int64, enabled bool, expectedVersion int64) (ServiceVersion, error) {
	if !validVersion(version) || !validPath(healthPath) || timeoutMS < 1 || timeoutMS > 300000 || maxBytes < 1 || maxBytes > 1<<30 || expectedVersion < 1 {
		return ServiceVersion{}, ErrInvalid
	}
	var result ServiceVersion
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE service_versions v SET version=$4,health_check_path=$5,request_timeout_ms=$6,max_request_bytes=$7,enabled=$8,row_version=v.row_version+1,updated_at=NOW() FROM services s JOIN environments e ON e.id=s.environment_id WHERE v.service_id=s.id AND e.name=$1 AND v.id=$2 AND v.row_version=$3 RETURNING v.id,v.service_id,v.version,v.health_check_path,v.request_timeout_ms,v.max_request_bytes,v.enabled,v.row_version,v.created_at`, environment, id, expectedVersion, version, healthPath, timeoutMS, maxBytes, enabled)
		if err := row.Scan(&result.ID, &result.ServiceID, &result.Version, &result.HealthCheckPath, &result.RequestTimeoutMS, &result.MaxRequestBytes, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "service-version.updated", "service-version", id, map[string]any{"enabled": enabled, "expectedVersion": expectedVersion})
	})
	return result, err
}

func (s *Store) CreateUpstream(ctx context.Context, actor, environment, versionID, address string, port, weight int, healthPath string) (Upstream, error) {
	return s.CreateUpstreamWithScheme(ctx, actor, environment, versionID, "http", address, port, weight, healthPath)
}

func (s *Store) CreateUpstreamWithScheme(ctx context.Context, actor, environment, versionID, scheme, address string, port, weight int, healthPath string) (Upstream, error) {
	parsed, err := netip.ParseAddr(strings.TrimSpace(address))
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if err != nil || parsed.IsUnspecified() || parsed.IsMulticast() || !s.addressAllowed(parsed) || (scheme != "http" && scheme != "https") || port < 1 || port > 65535 || weight < 1 || weight > 10000 || !validPath(healthPath) {
		return Upstream{}, ErrInvalid
	}
	id := newID("up")
	var result Upstream
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO upstreams(id,service_version_id,scheme,address,port,weight,health_check_path)
			SELECT $1,v.id,$3,$4,$5,$6,$7 FROM service_versions v JOIN services s ON s.id=v.service_id JOIN environments e ON e.id=s.environment_id WHERE v.id=$2 AND e.name=$8
			RETURNING id,service_version_id,scheme,host(address),port,weight,health_check_path,enabled,row_version,created_at`, id, versionID, scheme, parsed.String(), port, weight, healthPath, environment)
		if scanErr := row.Scan(&result.ID, &result.ServiceVersionID, &result.Scheme, &result.Address, &result.Port, &result.Weight, &result.HealthCheckPath, &result.Enabled, &result.RowVersion, &result.CreatedAt); scanErr != nil {
			return translate(scanErr)
		}
		return audit(ctx, tx, environment, actor, "upstream.created", "upstream", id, map[string]any{"scheme": scheme, "address": parsed.String(), "port": port})
	})
	return result, err
}

func (s *Store) ListUpstreams(ctx context.Context, environment, versionID string) ([]Upstream, error) {
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.service_version_id,u.scheme,host(u.address),u.port,u.weight,u.health_check_path,u.enabled,u.row_version,u.created_at FROM upstreams u JOIN service_versions v ON v.id=u.service_version_id JOIN services s ON s.id=v.service_id JOIN environments e ON e.id=s.environment_id WHERE e.name=$1 AND v.id=$2 ORDER BY u.id`, environment, versionID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Upstream, 0)
	for rows.Next() {
		var item Upstream
		if err = rows.Scan(&item.ID, &item.ServiceVersionID, &item.Scheme, &item.Address, &item.Port, &item.Weight, &item.HealthCheckPath, &item.Enabled, &item.RowVersion, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateUpstream(ctx context.Context, actor, environment, id, scheme, address string, port, weight int, healthPath string, enabled bool, expectedVersion int64) (Upstream, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	parsed, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || scheme != "http" && scheme != "https" || port < 1 || port > 65535 || weight < 1 || weight > 10000 || !validPath(healthPath) || !s.addressAllowed(parsed) || expectedVersion < 1 {
		return Upstream{}, ErrInvalid
	}
	var result Upstream
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE upstreams u SET scheme=$4,address=$5,port=$6,weight=$7,health_check_path=$8,enabled=$9,row_version=u.row_version+1,updated_at=NOW() FROM service_versions v JOIN services s ON s.id=v.service_id JOIN environments e ON e.id=s.environment_id WHERE u.service_version_id=v.id AND e.name=$1 AND u.id=$2 AND u.row_version=$3 RETURNING u.id,u.service_version_id,u.scheme,host(u.address),u.port,u.weight,u.health_check_path,u.enabled,u.row_version,u.created_at`, environment, id, expectedVersion, scheme, parsed.String(), port, weight, healthPath, enabled)
		if err := row.Scan(&result.ID, &result.ServiceVersionID, &result.Scheme, &result.Address, &result.Port, &result.Weight, &result.HealthCheckPath, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "upstream.updated", "upstream", id, map[string]any{"scheme": scheme, "enabled": enabled, "expectedVersion": expectedVersion})
	})
	return result, err
}

func (s *Store) ListPorts(ctx context.Context, environment, status string) ([]Port, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.name,p.listen_port,p.status,p.listener_id,p.updated_at FROM port_inventory p JOIN environments e ON e.id=p.environment_id WHERE e.name=$1 AND ($2='' OR p.status=$2) ORDER BY p.listen_port`, environment, status)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Port, 0)
	for rows.Next() {
		var item Port
		if err = rows.Scan(&item.Environment, &item.ListenPort, &item.Status, &item.ListenerID, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdatePortStatus(ctx context.Context, actor, environment string, port int, status string) (Port, error) {
	if status != "available" && status != "blocked" && status != "cooldown" {
		return Port{}, ErrInvalid
	}
	var result Port
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if status == "blocked" {
			var listenerID *string
			if err := tx.QueryRow(ctx, `SELECT p.listener_id FROM port_inventory p JOIN environments e ON e.id=p.environment_id WHERE e.name=$1 AND p.listen_port=$2 FOR UPDATE OF p`, environment, port).Scan(&listenerID); err != nil {
				return translate(err)
			}
			if listenerID != nil {
				tag, err := tx.Exec(ctx, `UPDATE listeners SET status='disabled',row_version=row_version+1,updated_at=NOW() WHERE id=$1 AND status<>'disabled'`, *listenerID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() > 0 {
					if err := audit(ctx, tx, environment, actor, "listener.status-changed", "listener", *listenerID, map[string]any{"status": "disabled", "reason": "port-blocked"}); err != nil {
						return err
					}
				}
			}
		}
		if status == "available" {
			var releasable bool
			var listenerID *string
			if err := tx.QueryRow(ctx, `SELECT p.listener_id IS NULL OR l.status='disabled',p.listener_id FROM port_inventory p JOIN environments e ON e.id=p.environment_id LEFT JOIN listeners l ON l.id=p.listener_id WHERE e.name=$1 AND p.listen_port=$2 FOR UPDATE OF p`, environment, port).Scan(&releasable, &listenerID); err != nil {
				return translate(err)
			}
			if !releasable {
				return ErrConflict
			}
			if listenerID != nil {
				row := tx.QueryRow(ctx, `UPDATE port_inventory p SET status='available',listener_id=NULL,updated_at=NOW() FROM environments e WHERE p.environment_id=e.id AND e.name=$1 AND p.listen_port=$2 RETURNING e.name,p.listen_port,p.status,p.listener_id,p.updated_at`, environment, port)
				if err := row.Scan(&result.Environment, &result.ListenPort, &result.Status, &result.ListenerID, &result.UpdatedAt); err != nil {
					return translate(err)
				}
				if _, err := tx.Exec(ctx, `DELETE FROM listeners WHERE id=$1`, *listenerID); err != nil {
					return translate(err)
				}
				if err := audit(ctx, tx, environment, actor, "listener.deleted", "listener", *listenerID, map[string]any{"listenPort": port, "reason": "port-released"}); err != nil {
					return err
				}
				return audit(ctx, tx, environment, actor, "port.status-changed", "port", strconv.Itoa(port), map[string]any{"status": status})
			}
		}
		row := tx.QueryRow(ctx, `UPDATE port_inventory p SET status=$3,listener_id=CASE WHEN $3='available' THEN NULL ELSE listener_id END,updated_at=NOW() FROM environments e WHERE p.environment_id=e.id AND e.name=$1 AND p.listen_port=$2 AND ($3='blocked' OR p.status NOT IN ('reserved','active','draining')) RETURNING e.name,p.listen_port,p.status,p.listener_id,p.updated_at`, environment, port, status)
		if err := row.Scan(&result.Environment, &result.ListenPort, &result.Status, &result.ListenerID, &result.UpdatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "port.status-changed", "port", strconv.Itoa(port), map[string]any{"status": status})
	})
	return result, err
}

type AllocateListenerInput struct {
	Service                 string
	ServiceVersion          string
	PreferredPort           *int
	RequiredPermission      string
	AllowedMethods          []string
	AllowedContentTypes     []string
	UnprotectedRouteRegex   string
	RateLimitPerSecond      int
	RateLimitBurst          int
	QuotaRequestsPerMinute  int
	CircuitFailureThreshold int
	CircuitOpenMS           int
}

func (s *Store) AllocateListener(ctx context.Context, actor, environment string, input AllocateListenerInput) (Listener, error) {
	methods, err := normalizeMethods(input.AllowedMethods)
	contentTypes, contentTypeErr := normalizeContentTypes(input.AllowedContentTypes)
	unprotectedRouteRegex, regexErr := normalizeUnprotectedRouteRegex(input.UnprotectedRouteRegex)
	if input.CircuitFailureThreshold == 0 {
		input.CircuitFailureThreshold = 5
	}
	if input.CircuitOpenMS == 0 {
		input.CircuitOpenMS = 30000
	}
	if err != nil || contentTypeErr != nil || regexErr != nil || !permissionPattern.MatchString(input.RequiredPermission) || input.RateLimitPerSecond < 0 || input.RateLimitPerSecond > 1000000 || input.RateLimitBurst < 0 || input.RateLimitBurst > 1000000 || input.QuotaRequestsPerMinute < 0 || input.QuotaRequestsPerMinute > 100000000 || input.CircuitFailureThreshold < 1 || input.CircuitFailureThreshold > 1000 || input.CircuitOpenMS < 1000 || input.CircuitOpenMS > 3600000 {
		return Listener{}, ErrInvalid
	}
	id := newID("lis")
	var result Listener
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		var environmentID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM environments WHERE name=$1`, environment).Scan(&environmentID); err != nil {
			return translate(err)
		}
		var versionID string
		if err := tx.QueryRow(ctx, `SELECT v.id FROM service_versions v JOIN services s ON s.id=v.service_id WHERE s.environment_id=$1 AND s.name=$2 AND v.version=$3 AND s.enabled AND v.enabled`, environmentID, input.Service, input.ServiceVersion).Scan(&versionID); err != nil {
			return translate(err)
		}
		var permissionExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permissions WHERE name=$1)`, input.RequiredPermission).Scan(&permissionExists); err != nil {
			return err
		}
		if !permissionExists {
			return ErrInvalid
		}
		var port int
		if input.PreferredPort != nil {
			if err := tx.QueryRow(ctx, `SELECT listen_port FROM port_inventory WHERE environment_id=$1 AND listen_port=$2 AND status='available' FOR UPDATE`, environmentID, *input.PreferredPort).Scan(&port); err != nil {
				return translate(err)
			}
		} else {
			if err := tx.QueryRow(ctx, `SELECT listen_port FROM port_inventory WHERE environment_id=$1 AND status='available' ORDER BY listen_port LIMIT 1 FOR UPDATE SKIP LOCKED`, environmentID).Scan(&port); err != nil {
				return translate(err)
			}
		}
		row := tx.QueryRow(ctx, `INSERT INTO listeners(id,environment_id,service_version_id,listen_port,required_permission,allowed_methods,allowed_content_types,unprotected_route_regex,rate_limit_per_second,rate_limit_burst,quota_requests_per_minute,circuit_failure_threshold,circuit_open_ms) VALUES($1,$2,$3,$4,$5,$6,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id,$7,service_version_id,listen_port,required_permission,allowed_methods,unprotected_route_regex,auth_policy,status,configuration_version,row_version,created_at,rate_limit_per_second,rate_limit_burst,quota_requests_per_minute,circuit_failure_threshold,circuit_open_ms`, id, environmentID, versionID, port, input.RequiredPermission, methods, environment, contentTypes, unprotectedRouteRegex, input.RateLimitPerSecond, input.RateLimitBurst, input.QuotaRequestsPerMinute, input.CircuitFailureThreshold, input.CircuitOpenMS)
		if err := row.Scan(&result.ID, &result.Environment, &result.ServiceVersionID, &result.ListenPort, &result.RequiredPermission, &result.AllowedMethods, &result.UnprotectedRouteRegex, &result.AuthenticationPolicy, &result.Status, &result.ConfigurationVersion, &result.RowVersion, &result.CreatedAt, &result.RateLimitPerSecond, &result.RateLimitBurst, &result.QuotaRequestsPerMinute, &result.CircuitFailureThreshold, &result.CircuitOpenMS); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE port_inventory SET status='reserved',listener_id=$3,updated_at=NOW() WHERE environment_id=$1 AND listen_port=$2`, environmentID, port, id); err != nil {
			return err
		}
		return audit(ctx, tx, environment, actor, "listener.allocated", "listener", id, map[string]any{"listenPort": port})
	})
	return result, err
}

func (s *Store) UpdateListener(ctx context.Context, actor, environment, id, status string, expectedVersion int64) (Listener, error) {
	portStatus := map[string]string{"reserved": "reserved", "active": "active", "draining": "draining", "disabled": "cooldown"}[status]
	if portStatus == "" || expectedVersion < 1 {
		return Listener{}, ErrInvalid
	}
	var result Listener
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE listeners l SET status=$3,row_version=row_version+1,updated_at=NOW() FROM environments e WHERE l.environment_id=e.id AND l.id=$1 AND e.name=$2 AND l.row_version=$4
			RETURNING l.id,e.name,l.service_version_id,l.listen_port,l.required_permission,l.allowed_methods,l.unprotected_route_regex,l.auth_policy,l.status,l.configuration_version,l.row_version,l.created_at,l.rate_limit_per_second,l.rate_limit_burst,l.quota_requests_per_minute,l.circuit_failure_threshold,l.circuit_open_ms`, id, environment, status, expectedVersion)
		if err := row.Scan(&result.ID, &result.Environment, &result.ServiceVersionID, &result.ListenPort, &result.RequiredPermission, &result.AllowedMethods, &result.UnprotectedRouteRegex, &result.AuthenticationPolicy, &result.Status, &result.ConfigurationVersion, &result.RowVersion, &result.CreatedAt, &result.RateLimitPerSecond, &result.RateLimitBurst, &result.QuotaRequestsPerMinute, &result.CircuitFailureThreshold, &result.CircuitOpenMS); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE port_inventory SET status=$2,updated_at=NOW() WHERE listener_id=$1`, id, portStatus); err != nil {
			return err
		}
		return audit(ctx, tx, environment, actor, "listener.status-changed", "listener", id, map[string]any{"status": status, "expectedVersion": expectedVersion})
	})
	return result, err
}

type ListenerPolicyInput struct {
	RateLimitPerSecond      int
	RateLimitBurst          int
	QuotaRequestsPerMinute  int
	CircuitFailureThreshold int
	CircuitOpenMS           int
	UnprotectedRouteRegex   string
	ExpectedVersion         int64
}

func (s *Store) UpdateListenerPolicies(ctx context.Context, actor, environment, id string, input ListenerPolicyInput) (Listener, error) {
	unprotectedRouteRegex, regexErr := normalizeUnprotectedRouteRegex(input.UnprotectedRouteRegex)
	if regexErr != nil || input.ExpectedVersion < 1 || input.RateLimitPerSecond < 0 || input.RateLimitPerSecond > 1000000 || input.RateLimitBurst < 0 || input.RateLimitBurst > 1000000 || input.QuotaRequestsPerMinute < 0 || input.QuotaRequestsPerMinute > 100000000 || input.CircuitFailureThreshold < 0 || input.CircuitFailureThreshold > 1000 || input.CircuitOpenMS < 1000 || input.CircuitOpenMS > 3600000 {
		return Listener{}, ErrInvalid
	}
	var result Listener
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE listeners l SET rate_limit_per_second=$4,rate_limit_burst=$5,quota_requests_per_minute=$6,circuit_failure_threshold=$7,circuit_open_ms=$8,unprotected_route_regex=$9,row_version=row_version+1,updated_at=NOW() FROM environments e WHERE l.environment_id=e.id AND l.id=$1 AND e.name=$2 AND l.row_version=$3 RETURNING l.id,e.name,l.service_version_id,l.listen_port,l.required_permission,l.allowed_methods,l.unprotected_route_regex,l.auth_policy,l.status,l.configuration_version,l.row_version,l.created_at,l.rate_limit_per_second,l.rate_limit_burst,l.quota_requests_per_minute,l.circuit_failure_threshold,l.circuit_open_ms`, id, environment, input.ExpectedVersion, input.RateLimitPerSecond, input.RateLimitBurst, input.QuotaRequestsPerMinute, input.CircuitFailureThreshold, input.CircuitOpenMS, unprotectedRouteRegex)
		if err := row.Scan(&result.ID, &result.Environment, &result.ServiceVersionID, &result.ListenPort, &result.RequiredPermission, &result.AllowedMethods, &result.UnprotectedRouteRegex, &result.AuthenticationPolicy, &result.Status, &result.ConfigurationVersion, &result.RowVersion, &result.CreatedAt, &result.RateLimitPerSecond, &result.RateLimitBurst, &result.QuotaRequestsPerMinute, &result.CircuitFailureThreshold, &result.CircuitOpenMS); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "listener.policies-updated", "listener", id, map[string]any{"rateLimitPerSecond": input.RateLimitPerSecond, "rateLimitBurst": input.RateLimitBurst, "quotaRequestsPerMinute": input.QuotaRequestsPerMinute, "circuitFailureThreshold": input.CircuitFailureThreshold, "circuitOpenMs": input.CircuitOpenMS, "hasUnprotectedRouteRegex": unprotectedRouteRegex != "", "expectedVersion": input.ExpectedVersion})
	})
	return result, err
}

func (s *Store) ListListeners(ctx context.Context, environment string) ([]Listener, error) {
	rows, err := s.pool.Query(ctx, `SELECT l.id,e.name,l.service_version_id,l.listen_port,l.required_permission,l.allowed_methods,l.unprotected_route_regex,l.auth_policy,l.status,l.configuration_version,l.row_version,l.created_at,l.rate_limit_per_second,l.rate_limit_burst,l.quota_requests_per_minute,l.circuit_failure_threshold,l.circuit_open_ms FROM listeners l JOIN environments e ON e.id=l.environment_id WHERE e.name=$1 ORDER BY l.listen_port,l.created_at DESC`, environment)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Listener, 0)
	for rows.Next() {
		var item Listener
		if err = rows.Scan(&item.ID, &item.Environment, &item.ServiceVersionID, &item.ListenPort, &item.RequiredPermission, &item.AllowedMethods, &item.UnprotectedRouteRegex, &item.AuthenticationPolicy, &item.Status, &item.ConfigurationVersion, &item.RowVersion, &item.CreatedAt, &item.RateLimitPerSecond, &item.RateLimitBurst, &item.QuotaRequestsPerMinute, &item.CircuitFailureThreshold, &item.CircuitOpenMS); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreatePermission(ctx context.Context, actor, environment, name, description string) (Permission, error) {
	if !permissionPattern.MatchString(name) {
		return Permission{}, ErrInvalid
	}
	id := newID("per")
	var result Permission
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO permissions(id,name,description) VALUES($1,$2,$3) RETURNING id,name,description,created_at`, id, name, strings.TrimSpace(description)).Scan(&result.ID, &result.Name, &result.Description, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "permission.created", "permission", id, map[string]any{"name": name})
	})
	return result, err
}

func (s *Store) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,description,created_at FROM permissions ORDER BY name`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Permission, 0)
	for rows.Next() {
		var item Permission
		if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdatePermission(ctx context.Context, actor, environment, id, description string) (Permission, error) {
	var result Permission
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE permissions SET description=$2 WHERE id=$1 RETURNING id,name,description,created_at`, id, strings.TrimSpace(description)).Scan(&result.ID, &result.Name, &result.Description, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "permission.updated", "permission", id, map[string]any{"name": result.Name})
	})
	return result, err
}

func (s *Store) DeletePermission(ctx context.Context, actor, environment, id string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM permissions WHERE id=$1 FOR UPDATE`, id).Scan(&name); err != nil {
			return translate(err)
		}
		var inUse bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM consumer_permissions WHERE permission_id=$1) OR EXISTS(SELECT 1 FROM listeners WHERE required_permission=$2)`, id, name).Scan(&inUse); err != nil {
			return err
		}
		if inUse {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `DELETE FROM permissions WHERE id=$1`, id); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "permission.deleted", "permission", id, map[string]any{"name": name})
	})
}

func (s *Store) CreateConsumer(ctx context.Context, actor, environment, name, displayName string, permissions []string) (Consumer, error) {
	if !serviceNamePattern.MatchString(name) || strings.TrimSpace(displayName) == "" {
		return Consumer{}, ErrInvalid
	}
	id := newID("con")
	var result Consumer
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO consumers(id,environment_id,name,display_name) SELECT $1,id,$3,$4 FROM environments WHERE name=$2 RETURNING id,$2,name,display_name,enabled,row_version,created_at`, id, environment, name, strings.TrimSpace(displayName))
		if err := row.Scan(&result.ID, &result.Environment, &result.Name, &result.DisplayName, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		if err := assignPermissions(ctx, tx, id, permissions); err != nil {
			return err
		}
		return audit(ctx, tx, environment, actor, "consumer.created", "consumer", id, map[string]any{"name": name, "permissions": permissions})
	})
	result.Permissions = append([]string(nil), permissions...)
	return result, err
}

func (s *Store) ListConsumers(ctx context.Context, environment string) ([]Consumer, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id,e.name,c.name,c.display_name,c.enabled,c.row_version,c.created_at,COALESCE(array_agg(p.name ORDER BY p.name) FILTER (WHERE p.name IS NOT NULL),'{}') FROM consumers c JOIN environments e ON e.id=c.environment_id LEFT JOIN consumer_permissions cp ON cp.consumer_id=c.id LEFT JOIN permissions p ON p.id=cp.permission_id WHERE e.name=$1 GROUP BY c.id,e.name ORDER BY c.name`, environment)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Consumer, 0)
	for rows.Next() {
		var item Consumer
		if err = rows.Scan(&item.ID, &item.Environment, &item.Name, &item.DisplayName, &item.Enabled, &item.RowVersion, &item.CreatedAt, &item.Permissions); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateConsumer(ctx context.Context, actor, environment, id, displayName string, enabled bool, permissions []string, expectedVersion int64) (Consumer, error) {
	if strings.TrimSpace(displayName) == "" || expectedVersion < 1 {
		return Consumer{}, ErrInvalid
	}
	var result Consumer
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE consumers c SET display_name=$3,enabled=$4,row_version=row_version+1,updated_at=NOW() FROM environments e WHERE c.environment_id=e.id AND c.id=$1 AND e.name=$2 AND c.row_version=$5 RETURNING c.id,e.name,c.name,c.display_name,c.enabled,c.row_version,c.created_at`, id, environment, strings.TrimSpace(displayName), enabled, expectedVersion)
		if err := row.Scan(&result.ID, &result.Environment, &result.Name, &result.DisplayName, &result.Enabled, &result.RowVersion, &result.CreatedAt); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM consumer_permissions WHERE consumer_id=$1`, id); err != nil {
			return err
		}
		if err := assignPermissions(ctx, tx, id, permissions); err != nil {
			return err
		}
		result.Permissions = append([]string(nil), permissions...)
		return audit(ctx, tx, environment, actor, "consumer.updated", "consumer", id, map[string]any{"enabled": enabled, "permissions": permissions, "expectedVersion": expectedVersion})
	})
	if err == nil {
		s.notify(ctx, environment, "credentials")
	}
	return result, err
}

func (s *Store) DeleteConsumer(ctx context.Context, actor, environment, id string) error {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		var hasKeys bool
		if err := tx.QueryRow(ctx, `SELECT c.name,EXISTS(SELECT 1 FROM api_keys k WHERE k.consumer_id=c.id) FROM consumers c JOIN environments e ON e.id=c.environment_id WHERE c.id=$1 AND e.name=$2 FOR UPDATE OF c`, id, environment).Scan(&name, &hasKeys); err != nil {
			return translate(err)
		}
		if hasKeys {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `DELETE FROM consumers WHERE id=$1`, id); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "consumer.deleted", "consumer", id, map[string]any{"name": name})
	})
	if err == nil {
		s.notify(ctx, environment, "credentials")
	}
	return err
}

func (s *Store) IssueKey(ctx context.Context, actor, environment, consumerID, name string, permissions []string, expiresAt *time.Time, rotatedFrom *string) (IssuedAPIKey, error) {
	if strings.TrimSpace(name) == "" || (expiresAt != nil && !expiresAt.After(time.Now())) {
		return IssuedAPIKey{}, ErrInvalid
	}
	raw, prefix, verifier := s.generateKey()
	id := newID("key")
	var result IssuedAPIKey
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := assignPermissions(ctx, tx, consumerID, permissions); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `INSERT INTO api_keys(id,consumer_id,name,key_prefix,verifier,expires_at,rotated_from_id) SELECT $1,c.id,$3,$4,$5,$6,$7 FROM consumers c JOIN environments e ON e.id=c.environment_id WHERE c.id=$2 AND e.name=$8 AND c.enabled RETURNING id,consumer_id,name,key_prefix,expires_at,revoked_at,rotated_from_id,created_at`, id, consumerID, strings.TrimSpace(name), prefix, verifier, expiresAt, rotatedFrom, environment)
		var storedPrefix string
		if err := row.Scan(&result.ID, &result.ConsumerID, &result.Name, &storedPrefix, &result.ExpiresAt, &result.RevokedAt, &result.RotatedFromID, &result.CreatedAt); err != nil {
			return translate(err)
		}
		result.MaskedPrefix = maskPrefix(storedPrefix)
		result.Key = raw
		return audit(ctx, tx, environment, actor, "api-key.issued", "api-key", id, map[string]any{"consumerId": consumerID, "keyPrefix": maskPrefix(prefix)})
	})
	if err != nil {
		result.Key = ""
	}
	return result, err
}

func (s *Store) ListKeys(ctx context.Context, environment, consumerID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT k.id,k.consumer_id,k.name,k.key_prefix,k.expires_at,k.revoked_at,k.rotated_from_id,k.created_at FROM api_keys k JOIN consumers c ON c.id=k.consumer_id JOIN environments e ON e.id=c.environment_id WHERE e.name=$1 AND c.id=$2 ORDER BY k.created_at DESC`, environment, consumerID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]APIKey, 0)
	for rows.Next() {
		var item APIKey
		var prefix string
		if err = rows.Scan(&item.ID, &item.ConsumerID, &item.Name, &prefix, &item.ExpiresAt, &item.RevokedAt, &item.RotatedFromID, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.MaskedPrefix = maskPrefix(prefix)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) RotateKey(ctx context.Context, actor, environment, keyID string) (IssuedAPIKey, error) {
	raw, prefix, verifier := s.generateKey()
	newKeyID := newID("key")
	var result IssuedAPIKey
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var consumerID, name string
		var expires *time.Time
		var revoked *time.Time
		if err := tx.QueryRow(ctx, `SELECT k.consumer_id,k.name,k.expires_at,k.revoked_at FROM api_keys k JOIN consumers c ON c.id=k.consumer_id JOIN environments e ON e.id=c.environment_id WHERE k.id=$1 AND e.name=$2 FOR UPDATE OF k`, keyID, environment).Scan(&consumerID, &name, &expires, &revoked); err != nil {
			return translate(err)
		}
		if revoked != nil {
			return ErrConflict
		}
		row := tx.QueryRow(ctx, `INSERT INTO api_keys(id,consumer_id,name,key_prefix,verifier,expires_at,rotated_from_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,consumer_id,name,key_prefix,expires_at,revoked_at,rotated_from_id,created_at`, newKeyID, consumerID, name+" rotation", prefix, verifier, expires, keyID)
		var storedPrefix string
		if err := row.Scan(&result.ID, &result.ConsumerID, &result.Name, &storedPrefix, &result.ExpiresAt, &result.RevokedAt, &result.RotatedFromID, &result.CreatedAt); err != nil {
			return translate(err)
		}
		tag, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at=NOW() WHERE id=$1 AND revoked_at IS NULL`, keyID)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		result.MaskedPrefix = maskPrefix(storedPrefix)
		result.Key = raw
		return audit(ctx, tx, environment, actor, "api-key.rotated", "api-key", newKeyID, map[string]any{"rotatedFrom": keyID, "keyPrefix": result.MaskedPrefix})
	})
	if err != nil {
		result.Key = ""
	} else {
		s.notify(ctx, environment, "credentials")
	}
	return result, err
}

func (s *Store) RevokeKey(ctx context.Context, actor, environment, keyID string) error {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE api_keys k SET revoked_at=NOW() FROM consumers c,environments e WHERE k.id=$1 AND k.consumer_id=c.id AND c.environment_id=e.id AND e.name=$2 AND k.revoked_at IS NULL`, keyID, environment)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit(ctx, tx, environment, actor, "api-key.revoked", "api-key", keyID, nil)
	})
	if err == nil {
		s.notify(ctx, environment, "credentials")
	}
	return err
}

func (s *Store) CreateConfiguration(ctx context.Context, actor, environment string) (Configuration, error) {
	var result Configuration
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var snapshot []byte
		if err := tx.QueryRow(ctx, `SELECT jsonb_build_object(
			'environment',$1::text,
			'services',(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',s.id,'name',s.name,'displayName',s.display_name,'enabled',s.enabled) ORDER BY s.name),'[]'::jsonb) FROM services s WHERE s.environment_id=e.id),
			'serviceVersions',(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',v.id,'serviceId',v.service_id,'version',v.version,'requestTimeoutMs',v.request_timeout_ms,'maxRequestBytes',v.max_request_bytes,'enabled',v.enabled) ORDER BY v.id),'[]'::jsonb) FROM service_versions v JOIN services s ON s.id=v.service_id WHERE s.environment_id=e.id),
			'upstreams',(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',u.id,'serviceVersionId',u.service_version_id,'scheme',u.scheme,'address',host(u.address),'port',u.port,'weight',u.weight,'healthCheckPath',u.health_check_path,'enabled',u.enabled) ORDER BY u.id),'[]'::jsonb) FROM upstreams u JOIN service_versions v ON v.id=u.service_version_id JOIN services s ON s.id=v.service_id WHERE s.environment_id=e.id),
			'listeners',(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',l.id,'listenPort',l.listen_port,'serviceVersionId',l.service_version_id,'requiredPermission',l.required_permission,'allowedMethods',l.allowed_methods,'allowedContentTypes',l.allowed_content_types,'unprotectedRouteRegex',l.unprotected_route_regex,'status',l.status,'rateLimitPerSecond',l.rate_limit_per_second,'rateLimitBurst',l.rate_limit_burst,'quotaRequestsPerMinute',l.quota_requests_per_minute,'circuitFailureThreshold',l.circuit_failure_threshold,'circuitOpenMs',l.circuit_open_ms) ORDER BY l.listen_port),'[]'::jsonb) FROM listeners l WHERE l.environment_id=e.id AND l.status<>'disabled')
		) FROM environments e WHERE e.name=$1`, environment).Scan(&snapshot); err != nil {
			return translate(err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO configuration_versions(environment_id,snapshot,created_by) SELECT id,$2,$3 FROM environments WHERE name=$1 RETURNING id,$1,status,source_version_id,created_by,validated_at,activated_at,created_at`, environment, snapshot, actor).Scan(&result.ID, &result.Environment, &result.Status, &result.SourceVersionID, &result.CreatedBy, &result.ValidatedAt, &result.ActivatedAt, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "configuration.created", "configuration", fmt.Sprint(result.ID), nil)
	})
	return result, err
}

func (s *Store) ValidateConfiguration(ctx context.Context, actor, environment string, id int64) (Configuration, error) {
	var result Configuration
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var invalid int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM listeners l LEFT JOIN permissions p ON p.name=l.required_permission WHERE l.environment_id=(SELECT id FROM environments WHERE name=$1) AND (p.id IS NULL OR NOT EXISTS(SELECT 1 FROM upstreams u WHERE u.service_version_id=l.service_version_id AND u.enabled))`, environment).Scan(&invalid); err != nil {
			return err
		}
		if invalid > 0 {
			return ErrInvalid
		}
		row := tx.QueryRow(ctx, `UPDATE configuration_versions c SET status='validated',validated_at=NOW() FROM environments e WHERE c.id=$1 AND c.environment_id=e.id AND e.name=$2 AND c.status='draft' RETURNING c.id,e.name,c.status,c.source_version_id,c.created_by,c.validated_at,c.activated_at,c.created_at`, id, environment)
		if err := scanConfiguration(row, &result); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "configuration.validated", "configuration", fmt.Sprint(id), nil)
	})
	return result, err
}

func (s *Store) ActivateConfiguration(ctx context.Context, actor, environment string, id int64, expectedActive *int64) (Configuration, error) {
	var result Configuration
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var environmentID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM environments WHERE name=$1 FOR UPDATE`, environment).Scan(&environmentID); err != nil {
			return translate(err)
		}
		var activeID *int64
		if err := tx.QueryRow(ctx, `SELECT MAX(id) FILTER (WHERE status='active') FROM configuration_versions WHERE environment_id=$1`, environmentID).Scan(&activeID); err != nil {
			return err
		}
		if expectedActive != nil && (activeID == nil || *activeID != *expectedActive) {
			return ErrConflict
		}
		if s.requiredApprovals > 0 {
			var approvals int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM configuration_approvals a JOIN configuration_versions c ON c.id=a.configuration_version WHERE c.id=$1 AND c.environment_id=$2 AND c.status='validated'`, id, environmentID).Scan(&approvals); err != nil {
				return err
			}
			if approvals < s.requiredApprovals {
				return ErrConflict
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE configuration_versions SET status='superseded' WHERE environment_id=$1 AND status='active'`, environmentID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `UPDATE configuration_versions SET status='active',activated_at=NOW() WHERE id=$1 AND environment_id=$2 AND status='validated' RETURNING id,$3,status,source_version_id,created_by,validated_at,activated_at,created_at`, id, environmentID, environment)
		if err := scanConfiguration(row, &result); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE listeners SET configuration_version=$2,status=CASE WHEN status='reserved' THEN 'active' ELSE status END,updated_at=NOW() WHERE environment_id=$1`, environmentID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE port_inventory SET status='active',updated_at=NOW() WHERE environment_id=$1 AND status='reserved'`, environmentID); err != nil {
			return err
		}
		return audit(ctx, tx, environment, actor, "configuration.activated", "configuration", fmt.Sprint(id), map[string]any{"previousVersion": activeID})
	})
	if err == nil {
		s.notify(ctx, environment, "configuration")
	}
	return result, err
}

func (s *Store) ApproveConfiguration(ctx context.Context, actor, environment string, id int64) (ConfigurationApproval, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" || len(actor) > 128 {
		return ConfigurationApproval{}, ErrInvalid
	}
	var result ConfigurationApproval
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO configuration_approvals(configuration_version,actor) SELECT c.id,$3 FROM configuration_versions c JOIN environments e ON e.id=c.environment_id WHERE c.id=$1 AND e.name=$2 AND c.status='validated' AND c.created_by<>$3 RETURNING configuration_version,actor,created_at`, id, environment, actor)
		if err := row.Scan(&result.ConfigurationVersion, &result.Actor, &result.CreatedAt); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "configuration.approved", "configuration", fmt.Sprint(id), nil)
	})
	return result, err
}

func (s *Store) RollbackConfiguration(ctx context.Context, actor, environment string, sourceID int64) (Configuration, error) {
	var result Configuration
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var environmentID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM environments WHERE name=$1 FOR UPDATE`, environment).Scan(&environmentID); err != nil {
			return translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE configuration_versions SET status='superseded' WHERE environment_id=$1 AND status='active'`, environmentID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `INSERT INTO configuration_versions(environment_id,status,snapshot,source_version_id,created_by,validated_at,activated_at) SELECT environment_id,'active',snapshot,id,$3,NOW(),NOW() FROM configuration_versions WHERE id=$1 AND environment_id=$2 AND status IN ('active','superseded') RETURNING id,$4,status,source_version_id,created_by,validated_at,activated_at,created_at`, sourceID, environmentID, actor, environment)
		if err := scanConfiguration(row, &result); err != nil {
			return translate(err)
		}
		return audit(ctx, tx, environment, actor, "configuration.rolled-back", "configuration", fmt.Sprint(result.ID), map[string]any{"sourceVersion": sourceID})
	})
	if err == nil {
		s.notify(ctx, environment, "configuration")
	}
	return result, err
}

func (s *Store) ListConfigurations(ctx context.Context, environment string) ([]Configuration, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id,e.name,c.status,c.source_version_id,c.created_by,c.validated_at,c.activated_at,c.created_at,COUNT(a.configuration_version),COALESCE(jsonb_array_length(c.snapshot->'services'),0),COALESCE(jsonb_array_length(c.snapshot->'serviceVersions'),0),COALESCE(jsonb_array_length(c.snapshot->'upstreams'),0),COALESCE(jsonb_array_length(c.snapshot->'listeners'),0) FROM configuration_versions c JOIN environments e ON e.id=c.environment_id LEFT JOIN configuration_approvals a ON a.configuration_version=c.id WHERE e.name=$1 GROUP BY c.id,e.name ORDER BY c.id DESC`, environment)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]Configuration, 0)
	for rows.Next() {
		var item Configuration
		if err = rows.Scan(&item.ID, &item.Environment, &item.Status, &item.SourceVersionID, &item.CreatedBy, &item.ValidatedAt, &item.ActivatedAt, &item.CreatedAt, &item.ApprovalCount, &item.ServiceCount, &item.VersionCount, &item.UpstreamCount, &item.ListenerCount); err != nil {
			return nil, err
		}
		item.RequiredApprovals = s.requiredApprovals
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListAudit(ctx context.Context, environment string, limit int) ([]AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id,e.name,a.actor,a.action,a.resource_type,a.resource_id,a.details,a.created_at FROM audit_events a LEFT JOIN environments e ON e.id=a.environment_id WHERE e.name=$1 ORDER BY a.id DESC LIMIT $2`, environment, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]AuditEvent, 0)
	for rows.Next() {
		var item AuditEvent
		var details []byte
		if err = rows.Scan(&item.ID, &item.Environment, &item.Actor, &item.Action, &item.ResourceType, &item.ResourceID, &details, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(details, &item.Details); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListGatewayInstances(ctx context.Context, environment string) ([]GatewayInstance, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.id,e.name,g.address,g.loaded_configuration_version,g.ready,g.last_seen_at FROM gateway_instances g JOIN environments e ON e.id=g.environment_id WHERE e.name=$1 ORDER BY g.id`, environment)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := make([]GatewayInstance, 0)
	for rows.Next() {
		var item GatewayInstance
		if err = rows.Scan(&item.ID, &item.Environment, &item.Address, &item.LoadedConfigurationVersion, &item.Ready, &item.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = fn(tx); err != nil {
		return err
	}
	return translate(tx.Commit(ctx))
}
func (s *Store) notify(ctx context.Context, environment, eventType string) {
	if s.publisher == nil {
		return
	}
	notifyContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.publisher.Publish(notifyContext, environment, eventType); err != nil {
		s.logger.Warn("Redis notification failed; PostgreSQL polling remains active", "event_type", eventType, "error", errors.New("coordination unavailable"))
	}
}
func audit(ctx context.Context, tx pgx.Tx, environment, actor, action, resourceType, resourceID string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO audit_events(environment_id,actor,action,resource_type,resource_id,details) SELECT id,$2,$3,$4,$5,$6 FROM environments WHERE name=$1`, environment, actor, action, resourceType, resourceID, encoded)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func assignPermissions(ctx context.Context, tx pgx.Tx, consumerID string, names []string) error {
	for _, name := range names {
		if !permissionPattern.MatchString(name) {
			return ErrInvalid
		}
		tag, err := tx.Exec(ctx, `INSERT INTO consumer_permissions(consumer_id,permission_id) SELECT $1,id FROM permissions WHERE name=$2 ON CONFLICT DO NOTHING`, consumerID, name)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM consumer_permissions cp JOIN permissions p ON p.id=cp.permission_id WHERE cp.consumer_id=$1 AND p.name=$2)`, consumerID, name).Scan(&exists); err != nil || !exists {
				return ErrInvalid
			}
		}
	}
	return nil
}
func (s *Store) generateKey() (string, string, []byte) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	secret := hex.EncodeToString(random[:])
	prefix := secret[:12]
	raw := "shn_" + prefix + "." + secret[12:]
	mac := hmac.New(sha256.New, s.pepper)
	_, _ = mac.Write([]byte(raw))
	return raw, prefix, mac.Sum(nil)
}
func (s *Store) addressAllowed(address netip.Addr) bool {
	for _, prefix := range s.upstreamCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
func newID(prefix string) string {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	return prefix + "_" + hex.EncodeToString(random[:])
}
func maskPrefix(prefix string) string {
	if len(prefix) <= 4 {
		return prefix + "****"
	}
	return prefix[:4] + strings.Repeat("*", len(prefix)-4)
}
func validPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\r\n") && len(path) <= 256
}
func normalizeMethods(input []string) ([]string, error) {
	if len(input) == 0 {
		input = []string{"GET"}
	}
	set := map[string]struct{}{}
	for _, method := range input {
		method = strings.ToUpper(strings.TrimSpace(method))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			set[method] = struct{}{}
		default:
			return nil, ErrInvalid
		}
	}
	result := make([]string, 0, len(set))
	for method := range set {
		result = append(result, method)
	}
	sort.Strings(result)
	return result, nil
}
func normalizeContentTypes(input []string) ([]string, error) {
	if len(input) == 0 {
		input = []string{"application/json", "application/xml", "text/xml", "application/x-www-form-urlencoded"}
	}
	set := map[string]struct{}{}
	for _, raw := range input {
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
		if err != nil || mediaType == "" || strings.Contains(mediaType, "*") {
			return nil, ErrInvalid
		}
		set[strings.ToLower(mediaType)] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for item := range set {
		result = append(result, item)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeUnprotectedRouteRegex(input string) (string, error) {
	if input == "" {
		return "", nil
	}
	if len(input) > 2048 || strings.ContainsAny(input, "\x00\r\n") {
		return "", ErrInvalid
	}
	if _, err := regexp.Compile(input); err != nil {
		return "", ErrInvalid
	}
	return input, nil
}
func scanConfiguration(row interface{ Scan(...any) error }, result *Configuration) error {
	return row.Scan(&result.ID, &result.Environment, &result.Status, &result.SourceVersionID, &result.CreatedBy, &result.ValidatedAt, &result.ActivatedAt, &result.CreatedAt)
}
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return ErrConflict
		case "23503", "23514", "22P02":
			return ErrInvalid
		}
	}
	return err
}
