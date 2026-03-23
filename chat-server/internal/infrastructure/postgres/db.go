package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a connection pool and runs the schema migration.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	id         TEXT        PRIMARY KEY,
	room_id    TEXT        NOT NULL,
	sender_id  TEXT        NOT NULL,
	type       TEXT        NOT NULL,
	body       TEXT        NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_room_created
	ON messages (room_id, created_at DESC);

CREATE TABLE IF NOT EXISTS message_reads (
	message_id  TEXT        NOT NULL,
	user_id     TEXT        NOT NULL,
	read_at     TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_message_reads_message
	ON message_reads (message_id);
`

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}
