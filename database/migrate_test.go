package database

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/logging/logcapture"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emptyDB creates the database name with the empty schema base (as in the zones), dropped when the test ends.
func emptyDB(t *testing.T, name string) (string, *sqlx.DB) {
	t.Helper()
	dsn, dbx := testDB.CreateDatabase(t, name)
	_, err := dbx.Exec(`CREATE SCHEMA base`)
	require.NoError(t, err)
	return dsn, dbx
}

// migrationsURL is the file source of the migrations directory.
func migrationsURL(t *testing.T) string {
	t.Helper()
	_, path, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return "file:" + filepath.Dir(path) + "/../migrations"
}

// migrationVersions returns the versions of the up migrations, ascending, as numbers in a JSON log line.
func migrationVersions(t *testing.T) []float64 {
	t.Helper()
	_, path, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "..", "migrations", "*.up.sql"))
	require.NoError(t, err)
	versions := make([]float64, len(files))
	for i, f := range files {
		versions[i], err = strconv.ParseFloat(strings.SplitN(filepath.Base(f), "_", 2)[0], 64)
		require.NoError(t, err)
	}
	return versions
}

// lineOf returns the one captured line with event.action event.
func lineOf(t *testing.T, buf *bytes.Buffer, event string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range logtest.Lines(t, buf) {
		if l["event.action"] == event {
			found = append(found, l)
		}
	}
	require.Len(t, found, 1, buf.String())
	return found[0]
}

// eegfaktura-dev#54 #4: the result of a migration run is in the log, also when nothing changed.
func TestMigrateDBLogsTheResult(t *testing.T) {
	dsn, _ := emptyDB(t, "migrate_result_test")
	versions := migrationVersions(t)
	latest := versions[len(versions)-1]

	buf := logcapture.JSON(t)
	require.NoError(t, migrateDB(migrationsURL(t), dsn, time.Minute))
	done := lineOf(t, buf, "db.migrate.done")
	assert.Equal(t, "info", done["log.level"])
	assert.Equal(t, "database migrations applied", done["message"])
	assert.Equal(t, float64(0), done["migration.versionBefore"])
	assert.Equal(t, latest, done["migration.versionAfter"])
	assert.Equal(t, float64(len(versions)), done["migration.applied"])

	buf.Reset()
	require.NoError(t, migrateDB(migrationsURL(t), dsn, time.Minute))
	done = lineOf(t, buf, "db.migrate.done")
	assert.Equal(t, "database schema unchanged (no_change)", done["message"])
	assert.Equal(t, latest, done["migration.versionBefore"])
	assert.Equal(t, latest, done["migration.versionAfter"])
	assert.Equal(t, float64(0), done["migration.applied"])
}

// eegfaktura-dev#54 #1: a migration waiting for a lock fails after the lock timeout instead of keeping the start
// waiting, and says why.
// lock timeouts by SQLSTATE 55P03, also inside golang-migrate's database.Error, and by golang-migrate's own wait
func TestLockHint(t *testing.T) {
	timeout := &pq.Error{Code: "55P03", Message: "canceling statement due to lock timeout"}
	for _, err := range []error{migrate.ErrLockTimeout, timeout, database.Error{OrigErr: timeout, Err: "migration failed"},
		&database.Error{OrigErr: timeout, Err: "try lock failed"}} {
		assert.NotEmpty(t, lockHint(err), err.Error())
	}
	for _, err := range []error{errors.New("relation already exists"), &pq.Error{Code: "42P07"},
		database.Error{OrigErr: &pq.Error{Code: "42P07"}}} {
		assert.Empty(t, lockHint(err), err.Error())
	}
}

func TestMigrateDBBoundsTheLockWait(t *testing.T) {
	dsn, dbx := emptyDB(t, "migrate_lock_test")
	require.NoError(t, migrateDB(migrationsURL(t), dsn, time.Minute))

	// a long transaction holding a lock the migration needs
	tx, err := dbx.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.Exec(`LOCK TABLE base.schema_migrations IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	buf := logcapture.JSON(t)
	start := time.Now()
	err = migrateDB(migrationsURL(t), dsn, 300*time.Millisecond)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second)
	failed := lineOf(t, buf, "db.migrate.failed")
	assert.Equal(t, "error", failed["log.level"])
	assert.Contains(t, failed["message"], "a lock wait took too long")
	assert.Contains(t, failed["error.message"], "lock timeout")
}

// eegfaktura-dev#54 #3: a dirty version stops the start with the pointer to the recovery, as one ERROR line.
func TestMigrateDBDirtyPointsToTheRecovery(t *testing.T) {
	dsn, dbx := emptyDB(t, "migrate_dirty_test")
	require.NoError(t, migrateDB(migrationsURL(t), dsn, time.Minute))
	_, err := dbx.Exec(`UPDATE base.schema_migrations SET dirty = true`)
	require.NoError(t, err)

	buf := logcapture.JSON(t)
	require.Error(t, migrateDB(migrationsURL(t), dsn, time.Minute))
	versions := migrationVersions(t)
	dirty := lineOf(t, buf, "db.migrate.dirty")
	assert.Equal(t, "error", dirty["log.level"])
	assert.Equal(t, versions[len(versions)-1], dirty["migration.version"])
	assert.Contains(t, dirty["message"], `README of eegfaktura-backend, section "Failed migration (dirty)"`)
	assert.NotContains(t, buf.String(), "force version", "not golang-migrate's advice")
	errorLines := 0
	for _, l := range logtest.Lines(t, buf) {
		if l["log.level"] == "error" {
			errorLines++
		}
	}
	assert.Equal(t, 1, errorLines, buf.String())
}

// A file that fails leaves its version dirty: the line names it and the recovery.
func TestMigrateDBFailedFileIsDirty(t *testing.T) {
	dsn, _ := emptyDB(t, "migrate_failed_file_test")
	dir := t.TempDir()
	for name, content := range map[string]string{
		"1_ok.up.sql":     `CREATE TABLE base.t1 (id int);`,
		"2_broken.up.sql": "-- " + strings.Repeat("long comment ", 30) + "\nCREATE TABLE base.t1 (id int);",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	buf := logcapture.JSON(t)
	require.Error(t, migrateDB("file:"+filepath.ToSlash(dir), dsn, time.Minute))
	dirty := lineOf(t, buf, "db.migrate.dirty")
	assert.Equal(t, float64(2), dirty["migration.version"])
	text, _ := dirty["error.message"].(string)
	assert.Contains(t, text, "already exists")
	assert.NotContains(t, text, "\n", "first line only")
	assert.LessOrEqual(t, len([]rune(text)), maxErrorText+1, "cut")
}
