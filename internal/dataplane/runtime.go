package dataplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apache261/Shinmon/internal/coordination"
	"github.com/apache261/Shinmon/internal/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runtime struct {
	pool                 *pgxpool.Pool
	environment          string
	allowlist            []netip.Prefix
	instanceID           string
	advertiseAddr        string
	pollInterval         time.Duration
	pollJitter           time.Duration
	healthInterval       time.Duration
	healthTimeout        time.Duration
	logger               *slog.Logger
	current              atomic.Pointer[Snapshot]
	metrics              *observability.Metrics
	events               <-chan coordination.Event
	transport            http.RoundTripper
	configGeneration     atomic.Int64
	credentialGeneration atomic.Int64
}

type RuntimeOptions struct {
	Pool           *pgxpool.Pool
	Environment    string
	Allowlist      []netip.Prefix
	InstanceID     string
	AdvertiseAddr  string
	PollInterval   time.Duration
	PollJitter     time.Duration
	HealthInterval time.Duration
	HealthTimeout  time.Duration
	Logger         *slog.Logger
	Metrics        *observability.Metrics
	Events         <-chan coordination.Event
	Transport      http.RoundTripper
}

func NewRuntime(options RuntimeOptions) *Runtime {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	transport := options.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Runtime{pool: options.Pool, environment: options.Environment, allowlist: append([]netip.Prefix(nil), options.Allowlist...), instanceID: options.InstanceID, advertiseAddr: options.AdvertiseAddr, pollInterval: options.PollInterval, pollJitter: options.PollJitter, healthInterval: options.HealthInterval, healthTimeout: options.HealthTimeout, logger: options.Logger, metrics: options.Metrics, events: options.Events, transport: transport}
}
func (r *Runtime) Snapshot() *Snapshot { return r.current.Load() }

func (r *Runtime) MarkNotReady(ctx context.Context) { r.report(ctx, false) }

func (r *Runtime) Load(ctx context.Context) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("begin runtime snapshot")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var version int64
	var credentialGeneration int64
	var encoded []byte
	if err = tx.QueryRow(ctx, `SELECT c.id,c.snapshot FROM configuration_versions c JOIN environments e ON e.id=c.environment_id WHERE e.name=$1 AND c.status='active'`, r.environment).Scan(&version, &encoded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("no active configuration")
		}
		return errors.New("load active configuration")
	}
	if err = tx.QueryRow(ctx, credentialGenerationQuery, r.environment).Scan(&credentialGeneration); err != nil {
		return errors.New("load credential generation")
	}
	var raw rawSnapshot
	if err = json.Unmarshal(encoded, &raw); err != nil {
		return errors.New("decode active configuration")
	}
	rows, err := tx.Query(ctx, `SELECT k.id,c.id,k.key_prefix,k.verifier,k.expires_at,COALESCE(array_agg(p.name) FILTER (WHERE p.name IS NOT NULL),'{}') FROM api_keys k JOIN consumers c ON c.id=k.consumer_id JOIN environments e ON e.id=c.environment_id LEFT JOIN consumer_permissions cp ON cp.consumer_id=c.id LEFT JOIN permissions p ON p.id=cp.permission_id WHERE e.name=$1 AND c.enabled AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>NOW()) GROUP BY k.id,c.id`, r.environment)
	if err != nil {
		return errors.New("load active credentials")
	}
	records := make([]credentialRecord, 0)
	for rows.Next() {
		var record credentialRecord
		if err = rows.Scan(&record.ID, &record.ConsumerID, &record.Prefix, &record.Verifier, &record.ExpiresAt, &record.Permissions); err != nil {
			rows.Close()
			return errors.New("scan active credential")
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return errors.New("read active credentials")
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return errors.New("commit runtime snapshot")
	}
	snapshot, err := buildSnapshot(version, r.environment, raw, records, r.allowlist)
	if err != nil {
		return err
	}
	if previous := r.current.Load(); previous != nil {
		copyHealth(previous, snapshot)
	}
	r.current.Store(snapshot)
	r.configGeneration.Store(version)
	r.credentialGeneration.Store(credentialGeneration)
	if r.metrics != nil {
		r.metrics.SetConfigurationVersion(version)
		r.metrics.SnapshotRefreshed(time.Now())
		r.updateUpstreamMetrics(snapshot)
	}
	return nil
}

func (r *Runtime) Run(ctx context.Context) {
	poll := time.NewTimer(jitteredDelay(r.pollInterval, r.pollJitter))
	healthJitter := min(r.healthInterval/10, time.Second)
	health := time.NewTimer(jitteredDelay(r.healthInterval, healthJitter))
	heartbeat := time.NewTicker(10 * time.Second)
	defer poll.Stop()
	defer health.Stop()
	defer heartbeat.Stop()
	r.report(ctx, true)
	events := r.events
	for {
		select {
		case <-ctx.Done():
			r.report(context.Background(), false)
			return
		case <-poll.C:
			changed, refreshErr := r.refreshIfChanged(ctx)
			if refreshErr != nil {
				if r.metrics != nil {
					r.metrics.ConfigurationRefreshFailure()
				}
				r.logger.Warn("runtime snapshot refresh failed; retaining last valid snapshot", "error", refreshErr)
			} else if changed {
				r.report(ctx, true)
			}
			poll.Reset(jitteredDelay(r.pollInterval, r.pollJitter))
		case <-health.C:
			r.probe(ctx)
			health.Reset(jitteredDelay(r.healthInterval, healthJitter))
		case <-heartbeat.C:
			r.report(ctx, true)
		case event, ok := <-events:
			if !ok {
				events = nil
				r.logger.Warn("Redis notification stream unavailable; PostgreSQL polling remains active")
				continue
			}
			if err := r.Load(ctx); err != nil {
				if r.metrics != nil {
					r.metrics.ConfigurationRefreshFailure()
				}
				r.logger.Warn("notified runtime refresh failed; retaining last valid snapshot", "event_type", event.Type, "error", err)
			} else {
				r.report(ctx, true)
			}
		}
	}
}

const credentialGenerationQuery = `SELECT GREATEST(
	COALESCE((SELECT MAX((EXTRACT(EPOCH FROM c.updated_at)*1000000)::BIGINT) FROM consumers c JOIN environments e ON e.id=c.environment_id WHERE e.name=$1),0),
	COALESCE((SELECT MAX((EXTRACT(EPOCH FROM k.created_at)*1000000)::BIGINT) FROM api_keys k JOIN consumers c ON c.id=k.consumer_id JOIN environments e ON e.id=c.environment_id WHERE e.name=$1),0),
	COALESCE((SELECT MAX((EXTRACT(EPOCH FROM k.revoked_at)*1000000)::BIGINT) FROM api_keys k JOIN consumers c ON c.id=k.consumer_id JOIN environments e ON e.id=c.environment_id WHERE e.name=$1),0)
)`

func (r *Runtime) refreshIfChanged(ctx context.Context) (bool, error) {
	var configurationGeneration int64
	var credentialGeneration int64
	if err := r.pool.QueryRow(ctx, `SELECT c.id FROM configuration_versions c JOIN environments e ON e.id=c.environment_id WHERE e.name=$1 AND c.status='active'`, r.environment).Scan(&configurationGeneration); err != nil {
		return false, errors.New("check configuration generation")
	}
	if err := r.pool.QueryRow(ctx, credentialGenerationQuery, r.environment).Scan(&credentialGeneration); err != nil {
		return false, errors.New("check credential generation")
	}
	if configurationGeneration == r.configGeneration.Load() && credentialGeneration == r.credentialGeneration.Load() {
		if r.metrics != nil {
			r.metrics.ConfigurationRefreshSkipped()
		}
		return false, nil
	}
	if err := r.Load(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func jitteredDelay(interval, jitter time.Duration) time.Duration {
	if interval <= 0 || jitter <= 0 {
		return interval
	}
	spread := int64(jitter)*2 + 1
	return interval - jitter + time.Duration(rand.Int64N(spread))
}

func (r *Runtime) probe(ctx context.Context) {
	snapshot := r.current.Load()
	if snapshot == nil {
		return
	}
	client := &http.Client{Timeout: r.healthTimeout, Transport: r.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seen := map[string]*Upstream{}
	for _, listener := range snapshot.Listeners {
		for _, upstream := range listener.Upstreams {
			seen[upstream.ID] = upstream
		}
	}
	var wait sync.WaitGroup
	slots := make(chan struct{}, 16)
	for _, upstream := range seen {
		wait.Add(1)
		go func(upstream *Upstream) {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			probeContext, cancel := context.WithTimeout(ctx, r.healthTimeout)
			defer cancel()
			request, err := http.NewRequestWithContext(probeContext, http.MethodGet, upstream.URLScheme()+"://"+upstream.Authority()+upstream.HealthCheckPath, nil)
			healthy := false
			if err == nil {
				response, requestErr := client.Do(request)
				if requestErr == nil {
					healthy = response.StatusCode >= 200 && response.StatusCode < 400
					_ = response.Body.Close()
				}
			}
			upstream.SetHealthy(healthy)
		}(upstream)
	}
	wait.Wait()
	if r.metrics != nil {
		r.updateUpstreamMetrics(snapshot)
	}
}

func (r *Runtime) updateUpstreamMetrics(snapshot *Snapshot) {
	seen := map[string]bool{}
	for _, listener := range snapshot.Listeners {
		for _, upstream := range listener.Upstreams {
			seen[upstream.ID] = upstream.Healthy()
		}
	}
	healthy := 0
	for _, state := range seen {
		if state {
			healthy++
		}
	}
	r.metrics.SetUpstreams(healthy, len(seen))
}

func (r *Runtime) report(ctx context.Context, ready bool) {
	reportContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snapshot := r.current.Load()
	var version *int64
	if snapshot != nil {
		value := snapshot.Version
		version = &value
	}
	_, err := r.pool.Exec(reportContext, `INSERT INTO gateway_instances(id,environment_id,address,loaded_configuration_version,ready,last_seen_at) SELECT $1,id,$3,$4,$5,NOW() FROM environments WHERE name=$2 ON CONFLICT(id) DO UPDATE SET address=EXCLUDED.address,loaded_configuration_version=EXCLUDED.loaded_configuration_version,ready=EXCLUDED.ready,last_seen_at=NOW()`, r.instanceID, r.environment, r.advertiseAddr, version, ready)
	if err != nil && ctx.Err() == nil {
		r.logger.Warn("gateway instance heartbeat failed", "error", errors.New("database unavailable"))
	}
}

func copyHealth(previous, next *Snapshot) {
	states := map[string]bool{}
	for _, listener := range previous.Listeners {
		for _, upstream := range listener.Upstreams {
			states[upstream.ID] = upstream.Healthy()
		}
	}
	for _, listener := range next.Listeners {
		for _, upstream := range listener.Upstreams {
			if healthy, ok := states[upstream.ID]; ok {
				upstream.SetHealthy(healthy)
			}
		}
	}
}
