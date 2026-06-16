package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"paperless-api/internal/config"
)

// New creates a single connection pool to the PaperLess database.
//
// PaperLess is single-database (it owns its own schema), so there is no
// per-tenant routing like sml-api-bybos. All SML access goes through the
// sml-api-bybos HTTP gateway, not through this pool.
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolCfg.MaxConns = cfg.DB.MaxConns
	poolCfg.MinConns = cfg.DB.MinConns

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
