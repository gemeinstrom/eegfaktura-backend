package database

import (
	"at.ourproject/vfeeg-backend/database/dbtest"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// runOperatorScriptIn runs a script of scripts/ended-meter-rejection/ the way the operator does with the
// pgz helper: psql reads it from stdin (database dbName), with ON_ERROR_STOP=1 and the given -v variables. The
// scripts commit for real.
func runOperatorScriptIn(t *testing.T, dbName, name string, vars ...string) (int, string) {
	t.Helper()
	ctx := context.Background()
	b, err := os.ReadFile("../scripts/ended-meter-rejection/" + name)
	require.NoError(t, err)
	require.NoError(t, testDB.Container.CopyToContainer(ctx, b, "/tmp/"+name, 0o644))
	cmd := "psql -U " + dbtest.DbUser + " -d " + dbName + " -X -v ON_ERROR_STOP=1"
	for _, v := range vars {
		cmd += " -v " + v
	}
	code, r, err := testDB.Container.Exec(ctx, []string{"sh", "-c", cmd + " < /tmp/" + name}, tcexec.Multiplexed())
	require.NoError(t, err)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	t.Logf("%s %v: exit %d: %s", name, vars, code, out)
	return code, string(out)
}

// checkTotals returns check.sql's total row (anzahl, beendet, aktiv, ohne_datum) and the row of tenant.
func checkTotals(t *testing.T, dbx *sqlx.DB, tenant string) (total, own map[string]int64) {
	t.Helper()
	b, err := os.ReadFile("../scripts/ended-meter-rejection/check.sql")
	require.NoError(t, err)
	rows, err := dbx.Queryx(string(b))
	require.NoError(t, err)
	defer rows.Close()
	total, own = map[string]int64{}, map[string]int64{}
	for rows.Next() {
		r := map[string]interface{}{}
		require.NoError(t, rows.MapScan(r))
		target := total
		if r["tenant"] == tenant {
			target = own
		} else if r["tenant"] != nil {
			continue
		}
		for _, k := range []string{"anzahl", "beendet", "aktiv", "ohne_datum", "aktiv_andere", "tenants"} {
			target[k] = r[k].(int64)
		}
	}
	return total, own
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// scriptsDB creates an empty, migrated database in the test container, so the scripts (which work on
// all tenants) see only this test's rows and do not change other tests' fixtures.
func scriptsDB(t *testing.T) (*sqlx.DB, string) {
	t.Helper()
	const name = "scripts_test"
	_, err := testDB.DbInstance.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
	require.NoError(t, err)
	_, err = testDB.DbInstance.Exec(`CREATE DATABASE ` + name)
	require.NoError(t, err)
	_, path, _, ok := runtime.Caller(0)
	require.True(t, ok)
	m, err := migrate.New("file:"+filepath.Dir(path)+"/../migrations",
		fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", dbtest.DbUser, dbtest.DbPass, testDB.DbAddress, name))
	require.NoError(t, err)
	require.NoError(t, m.Up())
	_, _ = m.Close()
	dbx, err := sqlx.Connect("postgres", strings.Replace(testDB.ConnectionString, "dbname="+dbtest.DbName, "dbname="+name, 1))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dbx.Close()
		_, _ = testDB.DbInstance.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
	})
	return dbx, name
}

// scripts/ended-meter-rejection/ (konzept-zaehlpunkt-ausblenden.md, part 2): check.sql counts the
// ended and the active variant, fix.sql changes exactly them only with the right expected count
// (self-guarding in one psql run), a re-run changes nothing, rollback.sql restores them.
func TestEndedMeterRejectionScripts(t *testing.T) {
	_, err := GetTestDB(context.Background(), testDB)
	require.NoError(t, err)
	dbx, dbName := scriptsDB(t)
	run := func(name string, vars ...string) (int, string) { return runOperatorScriptIn(t, dbName, name, vars...) }

	const tenant = "TE000019"
	const (
		endedInvalid   = "AT0030000000000000000000990019001"
		endedRejected  = "AT0030000000000000000000990019002"
		neverActive    = "AT0030000000000000000000990019003"
		endedNoDate    = "AT0030000000000000000000990019004"
		activeRejected = "AT0030000000000000000000990019005"
		activeOther    = "AT0030000000000000000000990019006"
		activeAbort    = "AT0030000000000000000000990019008"
		inactiveNever  = "AT0030000000000000000000990019007"
	)
	insertEndedRejectFixtureIn(t, dbx, tenant, map[string][3]string{
		endedInvalid:   {"INACTIVE", "INVALID", "2025-01-22"},
		endedRejected:  {"INACTIVE", "REJECTED", "2025-01-22"},
		neverActive:    {"INIT", "INVALID", ""},
		endedNoDate:    {"INACTIVE", "INVALID", ""},
		activeRejected: {"ACTIVE", "INVALID", "2999-12-31"},
		activeOther:    {"ACTIVE", "INVALID", "2999-12-31"},
		// an abort (REJECTED) after an old 188: not request-level, reported as aktiv_andere only
		activeAbort:   {"ACTIVE", "REJECTED", "2999-12-31"},
		inactiveNever: {"INACTIVE", "INVALID", "2025-01-22"},
	})
	_, err = dbx.Exec(`UPDATE base.meteringpoint SET activesince = NULL WHERE tenant = $1 AND metering_point_id = $2`,
		tenant, inactiveNever)
	require.NoError(t, err)
	// active variant: only request-level codes (188) are cleaned up; 57 concerns the metering point
	_, err = dbx.Exec(`UPDATE base.meteringpoint SET "statusCode" = CASE metering_point_id
		WHEN $2 THEN 188 WHEN $3 THEN 57 WHEN $4 THEN 188 ELSE 57 END WHERE tenant = $1`,
		tenant, activeRejected, activeOther, activeAbort)
	require.NoError(t, err)
	row := func(meter string) endedRejectRow { return readEndedRejectRow(t, dbx, tenant, meter) }

	total, own := checkTotals(t, dbx, tenant)
	assert.Equal(t, map[string]int64{"anzahl": 3, "beendet": 2, "aktiv": 1, "ohne_datum": 2, "aktiv_andere": 2, "tenants": 1}, own)
	assert.Equal(t, own["anzahl"], total["anzahl"], "isolated database: only this test's rows")
	expected := total["anzahl"]

	// without or with a wrong expected count nothing is committed
	code, out := run("fix.sql")
	assert.NotEqual(t, 0, code, out)
	code, out = run("fix.sql", "expected="+itoa(expected+1))
	assert.NotEqual(t, 0, code, out)
	assert.Equal(t, "INVALID", row(endedInvalid).ProcessState)
	assert.Equal(t, "INVALID", row(activeRejected).ProcessState)

	code, out = run("fix.sql", "expected="+itoa(expected))
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "COMMIT")

	r := row(endedInvalid)
	assert.Equal(t, "INACTIVE", r.ProcessState)
	assert.Equal(t, "INVALID", r.LastProcessState.String)
	assert.Equal(t, "cleanup-2026-10-24", r.ModifiedBy.String)
	r = row(endedRejected)
	assert.Equal(t, "INACTIVE", r.ProcessState)
	assert.Equal(t, "REJECTED", r.LastProcessState.String)
	r = row(activeRejected)
	assert.Equal(t, "ACTIVE", r.ProcessState)
	assert.Equal(t, "INVALID", r.LastProcessState.String)
	assert.Equal(t, "REJECTED", row(activeAbort).ProcessState, "active + REJECTED (abort): not touched")
	assert.Equal(t, "INVALID", row(neverActive).ProcessState, "never-active meter is not touched")
	assert.Equal(t, "test", row(neverActive).ModifiedBy.String)
	assert.Equal(t, "INVALID", row(endedNoDate).ProcessState, "no end date: not touched")
	assert.Equal(t, "INVALID", row(activeOther).ProcessState, "active + code 57: not touched")
	assert.Equal(t, "INVALID", row(inactiveNever).ProcessState, "never active (no activesince): not touched")

	total, _ = checkTotals(t, dbx, tenant)
	assert.Equal(t, int64(0), total["anzahl"])

	// a second run changes nothing (expected 0)
	code, out = run("fix.sql", "expected=0")
	require.Equal(t, 0, code, out)

	// an entry that became history meanwhile (flag 0) is not rolled back
	_, err = dbx.Exec(`UPDATE base.meteringpoint SET flag = 0 WHERE tenant = $1 AND metering_point_id = $2`, tenant, endedRejected)
	require.NoError(t, err)
	var toRestore int64
	require.NoError(t, dbx.Get(&toRestore, `SELECT count(*) FROM base.meteringpoint
		WHERE flag = 1 AND "modifiedBy" = 'cleanup-2026-10-24'`))

	code, out = run("rollback.sql", "expected="+itoa(toRestore+1))
	assert.NotEqual(t, 0, code, out)
	assert.Equal(t, "INACTIVE", row(endedInvalid).ProcessState)

	code, out = run("rollback.sql", "expected="+itoa(toRestore))
	require.Equal(t, 0, code, out)
	r = row(endedInvalid)
	assert.Equal(t, "INVALID", r.ProcessState)
	assert.False(t, r.LastProcessState.Valid)
	assert.Equal(t, "INVALID", row(activeRejected).ProcessState)
	var historyState string
	require.NoError(t, dbx.Get(&historyState, `SELECT process_state FROM base.meteringpoint
		WHERE tenant = $1 AND metering_point_id = $2 AND flag = 0`, tenant, endedRejected))
	assert.Equal(t, "INACTIVE", historyState)
	_, own = checkTotals(t, dbx, tenant)
	assert.Equal(t, int64(2), own["anzahl"])
}
