package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pingParams bound the readiness ping by the driver itself: 2 s to connect,
// 2 s for the statement.
const pingParams = " connect_timeout=2 statement_timeout=2000"

// ErrTooManyConnections reports that Postgres refused the ping with SQLSTATE
// 53300: the server is saturated, but the application pool may still serve.
var ErrTooManyConnections = errors.New("database refused the ping: too many connections")

// Ping checks that the configured database answers. It opens one dedicated,
// short-lived connection outside the application pool, so a busy pool does not
// make the service look unready and callers of /readyz cannot exhaust the pool.
// The ping uses pgx, the pool lib/pq; both read the same connection string.
func Ping(ctx context.Context) error {
	return ping(ctx, connInfo(), "SELECT 1")
}

func ping(ctx context.Context, info, query string) error {
	conn, err := pgx.Connect(ctx, info+pingParams)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "53300" {
			return ErrTooManyConnections
		}
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, query)
	return err
}
