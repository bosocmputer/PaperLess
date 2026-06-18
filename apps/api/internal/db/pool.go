package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/config"
)

// New creates a single connection pool to the PaperLess database.
//
// PaperLess is single-database (it owns its own schema), so there is no
// per-tenant routing like sml-api-bybos. All SML access goes through the
// sml-api-bybos HTTP gateway, not through this pool.
//
// DB_STATEMENT_TIMEOUT_MS applies a server-side statement_timeout on every
// connection via AfterConnect. This cuts off stuck OLTP queries before they pin
// a connection indefinitely. FinalizeDocument (PDF generation) runs outside the
// short timeout by using its own context without the pool timeout mechanism
// — the timeout only applies to SQL statements, not to application-level work.
// DB_MAX_CONNS (default 10): at 20–100 concurrent signers (pilot scale),
// 10 connections is comfortable — each sign is a short transaction (< 100ms),
// so the effective throughput is well above 100 RPS before queuing occurs.
// Raise DB_MAX_CONNS at scale-out; document alongside infrastructure changes.
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolCfg.MaxConns = cfg.DB.MaxConns
	poolCfg.MinConns = cfg.DB.MinConns

	if cfg.DB.StatementTimeout > 0 {
		timeoutMS := cfg.DB.StatementTimeout
		poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", timeoutMS))
			return err
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}
