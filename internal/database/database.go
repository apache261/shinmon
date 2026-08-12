package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, databaseURL string, minConnections, maxConnections int32, timeout time.Duration) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse database configuration")
	}
	poolConfig.MinConns = minConnections
	poolConfig.MaxConns = maxConnections
	connectContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectContext, poolConfig)
	if err != nil {
		return nil, errors.New("create database pool")
	}
	if err = pool.Ping(connectContext); err != nil {
		pool.Close()
		return nil, errors.New("connect to database")
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, "SELECT pg_advisory_lock(261, 1)"); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), "SELECT pg_advisory_unlock(261, 1)") }()

	if _, err = connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	for _, name := range entries {
		base := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		versionText, _, _ := strings.Cut(base, "_")
		version, parseErr := strconv.ParseInt(versionText, 10, 64)
		if parseErr != nil {
			return fmt.Errorf("invalid migration filename %q", name)
		}
		var applied bool
		queryErr := connection.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied)
		if queryErr != nil {
			return fmt.Errorf("read migration %d state: %w", version, queryErr)
		}
		if applied {
			continue
		}
		contents, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			return fmt.Errorf("read migration %d: %w", version, readErr)
		}
		tx, beginErr := connection.BeginTx(ctx, pgx.TxOptions{})
		if beginErr != nil {
			return fmt.Errorf("begin migration %d: %w", version, beginErr)
		}
		if _, execErr := tx.Exec(ctx, string(contents)); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %d: %w", version, execErr)
		}
		if _, execErr := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); execErr != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %d: %w", version, execErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit migration %d: %w", version, commitErr)
		}
	}
	return nil
}
