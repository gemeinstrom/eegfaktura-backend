// Package dbmock gives the tests outside package database a Database on sqlmock. Only tests import
// it, so go-sqlmock stays out of the service binary.
package dbmock

import (
	"at.ourproject/vfeeg-backend/database"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// New returns a Database on a fresh sqlmock connection and its mock. It does not change the
// database database.GetDB returns.
func New() (database.Database, sqlmock.Sqlmock, error) {
	conn, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}
	return database.NewDatabase(sqlx.NewDb(conn, "mock")), mock, nil
}
