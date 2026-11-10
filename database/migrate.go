package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	log "github.com/sirupsen/logrus"
)

// migrationLockTimeout bounds every lock wait of the migrations (eegfaktura-dev#54 #1): the backend runs one replica
// (Recreate) and migrates before it serves, so a migration waiting behind a long transaction or a pg_dump keeps the
// API down. With the bound it fails, the pod exits and restarts; a failed migration file leaves the version dirty
// (README, "Failed migration (dirty)").
const migrationLockTimeout = 60 * time.Second

// migrateDB applies the migrations of sourceURL to the database of dsn, on a connection of its own that is closed
// afterwards: lock_timeout lockTimeout ends every lock wait of it, also the wait for the migration lock while the
// driver opens (golang-migrate's own wait for it in Up ends after 15 s). It logs the result once: db.migrate.done
// with the version before and after and the number of migrations applied (0: no change), or the error.
func migrateDB(sourceURL, dsn string, lockTimeout time.Duration) error {
	log.Info("Start migration ...")
	src, err := source.Open(sourceURL)
	if err != nil {
		return migrateFailed(err)
	}
	// lib/pq passes a parameter it does not know to the server as a run-time setting of the session
	conn, err := sql.Open("postgres", fmt.Sprintf("%s lock_timeout=%d", dsn, lockTimeout.Milliseconds()))
	if err != nil {
		_ = src.Close()
		return migrateFailed(err)
	}
	driver, err := postgres.WithInstance(conn, &postgres.Config{SchemaName: "base"})
	if err != nil {
		_ = src.Close()
		_ = conn.Close()
		return migrateFailed(err)
	}
	m, err := migrate.NewWithInstance("migrations", src, "postgres", driver)
	if err != nil {
		_ = src.Close()
		_ = driver.Close()
		return migrateFailed(err)
	}
	defer m.Close()

	before, err := schemaVersion(m)
	if err != nil {
		return migrateFailed(err)
	}
	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		var dirty migrate.ErrDirty
		if !errors.As(err, &dirty) {
			// a file that failed leaves its version dirty
			if v, d, verr := m.Version(); verr == nil && d {
				dirty.Version = int(v)
			}
		}
		if dirty.Version != 0 {
			return migrateDirty(err, dirty.Version)
		}
		return migrateFailed(err)
	}
	after, err := schemaVersion(m)
	if err != nil {
		return migrateFailed(err)
	}
	applied := appliedMigrations(src, before, after)
	message := "database migrations applied"
	if applied == 0 {
		message = "database schema unchanged (no_change)"
	}
	log.WithFields(log.Fields{
		"event.action":            "db.migrate.done",
		"event.outcome":           "success",
		"migration.versionBefore": before,
		"migration.versionAfter":  after,
		"migration.applied":       applied,
	}).Info(message)
	return nil
}

// schemaVersion is the version in base.schema_migrations, 0 for a database without one.
func schemaVersion(m *migrate.Migrate) (uint, error) {
	v, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, nil
	}
	return v, err
}

// appliedMigrations counts the migrations of src above before up to after.
func appliedMigrations(src source.Driver, before, after uint) int {
	v, err := src.First()
	if before > 0 {
		v, err = src.Next(before)
	}
	n := 0
	for ; err == nil && v <= after; v, err = src.Next(v) {
		n++
	}
	return n
}

// migrateDirty logs a failure that leaves the version dirty with the pointer to the recovery. golang-migrate's own
// text for a dirty start ("Fix and force version") is left out: it names a CLI that is not in the image and is wrong
// for an invalid index; the error of a file that failed is kept.
func migrateDirty(err error, version int) error {
	entry := log.WithFields(log.Fields{"event.action": "db.migrate.dirty", "event.outcome": "failure",
		"migration.version": version})
	if !errors.As(err, &migrate.ErrDirty{}) {
		entry = entry.WithError(shortError(err))
	}
	entry.Error(fmt.Sprintf("database migration %d is dirty (started, not finished)%s: recover by hand as in the "+
		"README of eegfaktura-backend, section \"Failed migration (dirty)\"; never only reset the version",
		version, lockHint(err)))
	return err
}

func migrateFailed(err error) error {
	log.WithField("event.action", "db.migrate.failed").WithField("event.outcome", "failure").
		WithError(shortError(err)).Error("database migration failed" + lockHint(err))
	return err
}

// lockHint names the cause of a lock timeout: PostgreSQL's lock_timeout (SQLSTATE 55P03) or golang-migrate's own
// wait for its migration lock in Up (ErrLockTimeout, 15 s).
func lockHint(err error) string {
	if errors.Is(err, migrate.ErrLockTimeout) || sqlState(err) == "55P03" {
		return ": a lock wait took too long, check pg_stat_activity for long transactions or a running pg_dump"
	}
	return ""
}

// sqlState is the SQLSTATE of the driver's error. golang-migrate wraps it in database.Error (OrigErr, no Unwrap), as
// a value or a pointer; the text of lib/pq is the fallback when the code is not reachable.
func sqlState(err error) string {
	var value database.Error
	var pointer *database.Error
	switch {
	case errors.As(err, &value):
		err = value.OrigErr
	case errors.As(err, &pointer):
		err = pointer.OrigErr
	}
	var coded interface{ SQLState() string }
	if err != nil && errors.As(err, &coded) {
		return coded.SQLState()
	}
	if err != nil && strings.Contains(err.Error(), "canceling statement due to lock timeout") {
		return "55P03"
	}
	return ""
}

// maxErrorText bounds error.message of a failed migration: the text of database.Error holds the whole file.
const maxErrorText = 200

// shortError is the first line of err's text, at most maxErrorText characters.
func shortError(err error) error {
	text, _, _ := strings.Cut(err.Error(), "\n")
	if r := []rune(text); len(r) > maxErrorText {
		text = string(r[:maxErrorText]) + "…"
	}
	return errors.New(text)
}
