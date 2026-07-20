package database

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/duynhlab/notification-service/config"
	"github.com/duynhlab/pkg/dbx"
)

// Connect builds the service's Postgres pool via the shared dbx helper. dbx
// wires otelpgx query tracing (bounded span names, no bind-parameter or
// connection PII) and pgxpool.* pool-stat metrics, and applies the
// transaction-mode-pooler-safe settings (simple protocol, statement/description
// caches off) required by the PgDog/PgBouncer pooler.
//
// The DSN is cfg.Database.BuildDSN() — the single source shared with the
// `migrate` subcommand, so the app and migrations connect identically.
//
// When DB_PASSWORD_FILE is set (RFC-0008 / ADR-025 pattern A), WithPasswordFile
// makes dbx read the password from that mounted file on every new connection, so
// a rotated password is used without a pod restart; the DSN password is then a
// fallback. An empty path is a no-op, so the env-password path is unchanged.
func Connect(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	return dbx.NewPool(ctx, cfg.Database.BuildDSN(),
		dbx.WithMaxConns(cfg.Database.MaxConnections),
		dbx.WithPasswordFile(cfg.Database.PasswordFile),
	)
}
