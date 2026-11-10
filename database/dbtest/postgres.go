// Package dbtest starts the PostgreSQL container of the database tests, with the migrations and the
// seed data. Only tests import it, so Testcontainers and the Docker client stay out of the service
// binary. It does not import package database, so the tests inside that package can use it too.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // used by migrator
	_ "github.com/golang-migrate/migrate/v4/source/file"       // used by migrator
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	DbName = "test_db"
	DbUser = "test_user"
	DbPass = "test_password"
	// SeedDbName is a copy of DbName right after migration and seed data, before any test writes:
	// for tests that assert the seed data, while other tests change test_db.
	SeedDbName = "test_seed"
)

const (
	nat5432 = "5432/tcp"
	// startupTimeout is the wait for the started container to take connections, after the pull.
	startupTimeout = 90 * time.Second
)

type TestDatabase struct {
	DbAddress        string
	DbInstance       *sqlx.DB
	ConnectionString string
	Container        testcontainers.Container
}

func SetupTestDatabase() *TestDatabase {

	// setup db container: the deadline covers an image pull, the readiness wait has its own (startupTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	container, connectionString, dbAddr, err := createContainer(ctx)
	if err != nil {
		log.Fatalf("setup test db: %v", err)
	}

	// migrate db schema
	err = migrateDb(dbAddr)
	if err != nil {
		log.Fatalf("migrate test db: %v", err)
	}
	if err = copySeed(connectionString); err != nil {
		log.Fatalf("copy the seed database: %v", err)
	}

	return &TestDatabase{
		Container:        container,
		ConnectionString: connectionString,
		DbAddress:        dbAddr,
	}
}

func (tdb *TestDatabase) Open(ctx context.Context) error {
	var err error
	tdb.DbInstance, err = sqlx.ConnectContext(ctx, "postgres", tdb.ConnectionString)
	return err
}

// SeedConnectionString is the connection string of the untouched copy SeedDbName.
func (tdb *TestDatabase) SeedConnectionString() string {
	return strings.Replace(tdb.ConnectionString, "dbname="+DbName, "dbname="+SeedDbName, 1)
}

// CreateDatabase creates the empty database name (dropped first if a run left it behind) and drops it when the test
// ends. It returns the connection string and a connection. DbInstance must be open.
func (tdb *TestDatabase) CreateDatabase(t testing.TB, name string) (string, *sqlx.DB) {
	t.Helper()
	drop := func() error {
		_, err := tdb.DbInstance.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		return err
	}
	if err := drop(); err != nil {
		t.Fatal(err)
	}
	if _, err := tdb.DbInstance.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	dsn := strings.Replace(tdb.ConnectionString, "dbname="+DbName, "dbname="+name, 1)
	dbx, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = dbx.Close()
		_ = drop()
	})
	return dsn, dbx
}

func (tdb *TestDatabase) TearDown() {
	tdb.DbInstance.Close()
	// remove test container
	_ = tdb.Container.Terminate(context.Background())
}

func createContainer(ctx context.Context) (testcontainers.Container, string, string, error) {

	var env = map[string]string{
		"POSTGRES_PASSWORD": DbPass,
		"POSTGRES_USER":     DbUser,
		"POSTGRES_DB":       DbName,
		//"TZ":                "Europe/Berlin",
		//"PGTZ":              "Europe/Berlin",
	}
	var port = nat5432

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:18.6-alpine", // the zones run PostgreSQL 18
			ExposedPorts: []string{port},
			Env:          env,
			// The message appears twice: first from the temporary server of initdb, then from
			// the real server; then the port must take connections (no fixed sleep).
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort(nat5432),
			).WithStartupTimeout(startupTimeout),
		},
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return container, "", "", fmt.Errorf("failed to start container: %v", err)
	}

	p, err := container.MappedPort(ctx, "5432")
	if err != nil {
		return container, "", "", fmt.Errorf("failed to get container external port: %v", err)
	}

	log.Println("postgres container ready and running at port: ", p.Port())

	dbAddr := fmt.Sprintf("127.0.0.1:%s", p.Port())

	//db, err := sqlx.Open("postgres", fmt.Sprintf("host=localhost port=%s user=%s password=%s dbname=%s sslmode=disable", p.Port(), DbUser, DbPass, DbName))
	//if err != nil {
	//	return container, "", dbAddr, fmt.Errorf("failed to establish database connection: %v", err)
	//}

	return container, fmt.Sprintf("host=127.0.0.1 port=%s user=%s password=%s dbname=%s sslmode=disable", p.Port(), DbUser, DbPass, DbName), dbAddr, nil
}

// copySeed creates SeedDbName from the migrated and seeded DbName (no connection to it is open yet).
func copySeed(connectionString string) error {
	dbm, err := sqlx.Open("postgres", strings.Replace(connectionString, "dbname="+DbName, "dbname=postgres", 1))
	if err != nil {
		return err
	}
	defer dbm.Close()
	_, err = dbm.Exec(`CREATE DATABASE ` + SeedDbName + ` TEMPLATE ` + DbName)
	return err
}

func migrateDb(dbAddr string) error {

	// get location of test
	_, path, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("failed to get path")
	}
	pathToMigrationFiles := filepath.Dir(path) + "/../../migrations"

	databaseURL := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", DbUser, DbPass, dbAddr, DbName)
	m, err := migrate.New(fmt.Sprintf("file:%s", pathToMigrationFiles), databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	log.Println("migration done")
	return loadSqlFile(databaseURL, path)
}

func loadSqlFile(connectionStr, path string) error {
	// Read file
	file, err := os.ReadFile(filepath.Dir(path) + "/../testdata/seed.sql")
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	dbm, err := sqlx.Open("postgres", connectionStr)
	if err != nil {
		return err
	}
	defer dbm.Close()

	// Execute all
	_, err = dbm.Exec(string(file))
	if err != nil {
		fmt.Println(err.Error())
	}
	return err
}
