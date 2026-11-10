package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/database/dbtest"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

var testDB *dbtest.TestDatabase

// GetTestDB opens the container database once and makes it the database GetDB returns.
func GetTestDB(ctx context.Context, dbInstance *dbtest.TestDatabase) (Database, error) {
	if dbInstance.DbInstance == nil {
		if err := dbInstance.Open(ctx); err != nil {
			return nil, err
		}
	}
	d := NewDatabase(dbInstance.DbInstance)
	SetDB(d)
	return d, nil
}

// seedDB is the database as migrated and seeded, before any test wrote to it (dbtest.SeedDbName).
// Only for tests that read and assert the seed data; they would otherwise depend on the tests run
// before them. Do not write to it.
func seedDB(t *testing.T) Database {
	t.Helper()
	conn, err := sqlx.Connect("postgres", testDB.SeedConnectionString())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewDatabase(conn)
}

// newTxMock returns a connection on sqlmock and its mock; the connection is closed when the test ends.
func newTxMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return sqlx.NewDb(conn, "mock"), mock
}

// newMockDatabase is newTxMock as a Database. It does not change the database GetDB returns.
func newMockDatabase(t *testing.T) (Database, sqlmock.Sqlmock) {
	t.Helper()
	conn, mock := newTxMock(t)
	return NewDatabase(conn), mock
}
