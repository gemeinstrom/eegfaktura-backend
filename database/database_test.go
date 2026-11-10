package database

import (
	"at.ourproject/vfeeg-backend/database/dbtest"
	"context"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	testDB = dbtest.SetupTestDatabase()
	db, err := GetTestDB(context.Background(), testDB)
	if err != nil {
		panic(err)
	}
	// os.Exit skips deferred calls: close and remove the container before it
	code := m.Run()
	_ = db.CloseDB()
	testDB.TearDown()
	os.Exit(code)
}
