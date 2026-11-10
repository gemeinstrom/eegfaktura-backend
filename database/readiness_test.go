package database

import (
	"at.ourproject/vfeeg-backend/database/dbtest"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestPing_answeringDatabase(t *testing.T) {
	require.NoError(t, ping(context.Background(), testDB.ConnectionString, "SELECT 1"))
}

func TestPing_unreachableDatabaseFailsWithinConnectTimeout(t *testing.T) {
	// TEST-NET-1 address: nothing answers, so only connect_timeout ends the attempt.
	start := time.Now()
	err := ping(context.Background(), "host=192.0.2.1 port=5432 user=x password=x dbname=x sslmode=disable", "SELECT 1")
	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestPing_slowStatementEndsByDriverStatementTimeout(t *testing.T) {
	// No context deadline: only statement_timeout=2000 can end the 10 s sleep.
	start := time.Now()
	err := ping(context.Background(), testDB.ConnectionString, "SELECT pg_sleep(10)")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "got %v", err)
	require.Equal(t, "57014", pgErr.Code) // query_canceled by statement_timeout
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestPing_tooManyConnectionsIsReported(t *testing.T) {
	// A role with CONNECTION LIMIT 0 makes Postgres refuse the login with SQLSTATE 53300.
	_, err := testDB.DbInstance.Exec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'ping_limited') THEN
			CREATE ROLE ping_limited LOGIN PASSWORD 'ping_limited' CONNECTION LIMIT 0;
		END IF; END $$`)
	require.NoError(t, err)

	host, port, err := net.SplitHostPort(testDB.DbAddress)
	require.NoError(t, err)
	info := "host=" + host + " port=" + port + " user=ping_limited password=ping_limited dbname=" + dbtest.DbName + " sslmode=disable"
	require.ErrorIs(t, ping(context.Background(), info, "SELECT 1"), ErrTooManyConnections)
}
