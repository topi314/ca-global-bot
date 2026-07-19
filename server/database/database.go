package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/topi314/gomigrate"
	"github.com/topi314/gomigrate/drivers/postgres"

	"github.com/topi314/ca-global-bot/server/database/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Database struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries
}

func New(cfg Config) (*Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := cfg.DataSourceName()

	stdlibDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = stdlibDB.Close()
	}()

	if err = gomigrate.Migrate(ctx, stdlibDB, postgres.New, migrations,
		gomigrate.WithDirectory("migrations"),
		gomigrate.WithLogger(slog.Default()),
	); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Database{
		Pool:    pool,
		Queries: sqlc.New(pool),
	}, nil
}

func (d *Database) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
}
