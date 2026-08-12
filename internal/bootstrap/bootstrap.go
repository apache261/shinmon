package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apache261/Shinmon/internal/config"
	"github.com/apache261/Shinmon/internal/controlplane"
	"github.com/apache261/Shinmon/internal/coordination"
	"github.com/apache261/Shinmon/internal/database"
	"github.com/apache261/Shinmon/internal/dataplane"
	"github.com/apache261/Shinmon/internal/health"
	"github.com/apache261/Shinmon/internal/httpapi"
	"github.com/apache261/Shinmon/internal/observability"
	"github.com/apache261/Shinmon/internal/server"
)

func Run(role config.Role) int {
	configured, err := config.Load(role)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid bootstrap configuration", "error", err)
		return 1
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: configured.LogLevel})).With(
		"service", string(role),
		"environment", configured.Environment,
	)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseContext, cancel := context.WithTimeout(ctx, configured.DatabaseTimeout)
	pool, databaseErr := database.Open(databaseContext, configured.DatabaseURL.Value(), configured.DatabaseMinConns, configured.DatabaseMaxConns, configured.DatabaseTimeout)
	cancel()
	if databaseErr != nil {
		logger.Error("database initialization failed", "error", databaseErr)
		return 1
	}
	defer pool.Close()
	coordinator, coordinationErr := coordination.New(configured.RedisURL.Value())
	if coordinationErr != nil {
		logger.Error("Redis coordination configuration failed", "error", coordinationErr)
		return 1
	}
	defer coordinator.Close()
	readiness := &health.Readiness{}
	metrics := observability.New(string(role))
	handlerOptions := httpapi.Options{
		Service:            string(role),
		Environment:        configured.Environment,
		Logger:             logger,
		Readiness:          readiness,
		Metrics:            metrics,
		MetricsBearerToken: configured.MetricsBearerToken.Value(),
	}
	var dataRuntime *dataplane.Runtime
	var upstreamTransport *http.Transport
	if role == config.Admin {
		migrationContext, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
		databaseErr = database.Migrate(migrationContext, pool)
		migrationCancel()
		if databaseErr != nil {
			logger.Error("database migration failed", "error", databaseErr)
			return 1
		}
		handlerOptions.AdminBearerToken = configured.AdminBearerToken.Value()
		store := controlplane.NewStore(pool, configured.APIKeyPepper.Value(), configured.UpstreamCIDRs)
		store.ConfigureDistributed(coordinator, logger, configured.ConfigurationApprovalsRequired)
		handlerOptions.AdminAPI = controlplane.NewHandler(store, configured.Environment, logger)
	} else {
		upstreamTransport, err = dataplane.NewTransport(configured.UpstreamTLSCAFile)
		if err != nil {
			logger.Error("upstream TLS configuration failed", "error", err)
			return 1
		}
		dataRuntime = dataplane.NewRuntime(dataplane.RuntimeOptions{Pool: pool, Environment: configured.Environment, Allowlist: configured.UpstreamCIDRs, InstanceID: configured.GatewayInstanceID, AdvertiseAddr: configured.GatewayAdvertiseAddr, PollInterval: configured.ConfigPollInterval, HealthInterval: configured.HealthInterval, HealthTimeout: configured.HealthTimeout, Logger: logger, Metrics: metrics, Events: coordinator.Events(ctx, configured.Environment), Transport: upstreamTransport})
		if databaseErr = dataRuntime.Load(ctx); databaseErr != nil {
			logger.Error("initial runtime snapshot failed", "error", databaseErr)
			return 1
		}
		handlerOptions.GatewayAPI = dataplane.NewHandler(dataplane.HandlerOptions{Snapshot: dataRuntime.Snapshot, TrustedProxyCIDRs: configured.TrustedProxyCIDRs, APIKeyPepper: configured.APIKeyPepper.Value(), Logger: logger, Metrics: metrics, Coordinator: coordinator, Transport: upstreamTransport})
		go dataRuntime.Run(ctx)
	}
	err = server.Run(ctx, server.Options{
		Addr:            configured.ListenAddr,
		Handler:         httpapi.New(handlerOptions),
		Logger:          logger,
		Readiness:       readiness,
		ShutdownTimeout: configured.ShutdownTimeout,
		TLSCertFile:     configured.TLSCertFile,
		TLSKeyFile:      configured.TLSKeyFile,
	})
	if dataRuntime != nil {
		reportContext, reportCancel := context.WithTimeout(context.Background(), 3*time.Second)
		dataRuntime.MarkNotReady(reportContext)
		reportCancel()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped with error", "error", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}
