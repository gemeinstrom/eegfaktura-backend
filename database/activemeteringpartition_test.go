package database

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// support ticket 6611200: base.activeMeteringPartition numbered the factors of all communities before the
// tenant filter of the caller applied (the window was partitioned without tenant). These tests compare the
// view with its former definition, which the down migration restores.

const (
	ampTenantA, ampTenantB = "TE900001", "TE900002"
	ampMeter, ampMeter2    = "AT0030000000000000000000000900001", "AT0030000000000000000000000900002"
	ampOldOwner            = "b6611200-0000-0000-0000-00000000000a"
	ampNewOwner            = "b6611200-0000-0000-0000-00000000000b"
	ampOtherTenant         = "b6611200-0000-0000-0000-00000000000c"
)

// ampTx opens a transaction (rolled back when the test ends) with the test rows and the former definition of
// the view as the temporary view amp_old.
func ampTx(t *testing.T) *sqlx.Tx {
	t.Helper()
	_, err := GetTestDB(t.Context(), testDB)
	require.NoError(t, err)
	tx, err := testDB.DbInstance.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	exec := func(q string, args ...interface{}) {
		t.Helper()
		_, err := tx.Exec(q, args...)
		require.NoError(t, err)
	}

	down, err := os.ReadFile("../migrations/20261105110000_activemeteringpartition_tenant.down.sql")
	require.NoError(t, err)
	const create = "CREATE OR REPLACE VIEW base.activeMeteringPartition AS"
	require.Equal(t, 1, strings.Count(string(down), create))
	exec(strings.Replace(string(down), create, "CREATE TEMP VIEW amp_old AS", 1))

	for p, tenant := range map[string]string{ampOldOwner: ampTenantA, ampNewOwner: ampTenantA, ampOtherTenant: ampTenantB} {
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, $2, 'Partition', 'Factor', 'ACTIVE', 'test', 'test')`, p, tenant)
	}
	meter := func(m, p, tenant string, flag int) {
		exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, flag) VALUES ($1, $2, $3, $4)`,
			m, p, tenant, flag)
	}
	meter(ampMeter, ampOldOwner, ampTenantA, 0) // owner change: the old member's moved entry
	meter(ampMeter, ampNewOwner, ampTenantA, 1)
	meter(ampMeter2, ampNewOwner, ampTenantA, 1)
	meter(ampMeter, ampOtherTenant, ampTenantB, 1) // the same number in a second community
	// several versions each; the last one per (metering point, participant) is the active factor
	for _, f := range []struct {
		m, p, tenant string
		fact         int
	}{
		{ampMeter, ampOldOwner, ampTenantA, 100}, {ampMeter, ampNewOwner, ampTenantA, 60}, {ampMeter, ampOldOwner, ampTenantA, 90},
		{ampMeter, ampOtherTenant, ampTenantB, 30}, {ampMeter2, ampNewOwner, ampTenantA, 10}, {ampMeter, ampNewOwner, ampTenantA, 50},
		{ampMeter, ampOtherTenant, ampTenantB, 40},
	} {
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
			VALUES ($1, $2, $3, $4, 'test')`, f.m, f.p, f.tenant, f.fact)
	}
	return tx
}

func TestActiveMeteringPartition_sameRowsAsFormerDefinition(t *testing.T) {
	tx := ampTx(t)

	var active []string
	require.NoError(t, tx.Select(&active, `SELECT tenant || ' ' || metering_point_id || ' ' || participant_id || ' ' || "partFact"
		FROM base.activeMeteringPartition WHERE tenant IN ($1, $2)`, ampTenantA, ampTenantB))
	sort.Strings(active)
	assert.Equal(t, []string{
		ampTenantA + " " + ampMeter + " " + ampOldOwner + " 90",
		ampTenantA + " " + ampMeter + " " + ampNewOwner + " 50",
		ampTenantA + " " + ampMeter2 + " " + ampNewOwner + " 10",
		ampTenantB + " " + ampMeter + " " + ampOtherTenant + " 40",
	}, active, "the last factor per member, each community on its own")

	// all rows, the seed data included, all columns
	for _, q := range []string{
		`SELECT count(*) FROM (SELECT * FROM amp_old EXCEPT ALL SELECT * FROM base.activeMeteringPartition) d`,
		`SELECT count(*) FROM (SELECT * FROM base.activeMeteringPartition EXCEPT ALL SELECT * FROM amp_old) d`,
	} {
		var n int
		require.NoError(t, tx.Get(&n, q))
		assert.Zero(t, n, q)
	}
}

func TestActiveMeteringPartition_tenantFilterBelowWindow(t *testing.T) {
	tx := ampTx(t)
	for view, pushed := range map[string]bool{"base.activeMeteringPartition": true, "amp_old": false} {
		plan := explainJSON(t, tx, `EXPLAIN (FORMAT JSON) SELECT "partFact" FROM `+view+` WHERE tenant = '`+ampTenantA+`'`)
		assert.Equal(t, pushed, tenantBelowWindow(t, plan, ampTenantA), "%s: tenant condition below the WindowAgg", view)
	}
}

// completeParticipants (members list) reads the view with the tenant filter: same metering points as without it.
func TestCompleteParticipantsQuery_tenantFilterOnView(t *testing.T) {
	tx := ampTx(t)
	stmt, _, err := completeParticipantsQuery(ampTenantA).ToSQL()
	require.NoError(t, err)
	former, _, err := buildGetParticipantQuery().
		Where(goqu.C("tenant").Table("meteringpoint").Schema("base").Eq(ampTenantA)).ToSQL()
	require.NoError(t, err)

	var got, want []model.MeteringPoint
	require.NoError(t, tx.Select(&got, stmt))
	require.NoError(t, tx.Select(&want, former))
	assert.Len(t, got, 3)
	assert.ElementsMatch(t, want, got)
	assert.True(t, tenantBelowWindow(t, explainJSON(t, tx, "EXPLAIN (FORMAT JSON) "+stmt), ampTenantA))
	assert.False(t, tenantBelowWindow(t, explainJSON(t, tx, "EXPLAIN (FORMAT JSON) "+former), ampTenantA))
}

// Migrations 20261105110000 (view) and 20261105110100 (index, CONCURRENTLY): up, down and up again.
func TestActiveMeteringPartitionMigrationUpDown(t *testing.T) {
	dbx, m := migrationDB(t, "activemeteringpartition_migration_test")
	state := func() (partitionedByTenant, index bool) {
		t.Helper()
		var def string
		require.NoError(t, dbx.Get(&def, `SELECT pg_get_viewdef('base.activemeteringpartition'::regclass)`))
		require.NoError(t, dbx.Get(&index, `SELECT to_regclass('base.metering_partition_factor_tenant_idx') IS NOT NULL`))
		return strings.Contains(def, "PARTITION BY metering_partition_factor.tenant,"), index
	}
	const indexMigration = 20261105110100
	require.NoError(t, m.Migrate(indexMigration))
	byTenant, index := state()
	assert.True(t, byTenant && index, "up")
	require.NoError(t, m.Steps(-2))
	byTenant, index = state()
	assert.False(t, byTenant || index, "down")
	require.NoError(t, m.Migrate(indexMigration))
	byTenant, index = state()
	assert.True(t, byTenant && index, "up again")
}

func explainJSON(t *testing.T, tx *sqlx.Tx, q string) map[string]interface{} {
	t.Helper()
	var raw string
	require.NoError(t, tx.Get(&raw, q))
	var out []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out[0]["Plan"].(map[string]interface{})
}

// tenantBelowWindow: the plan has one WindowAgg, and below it the factor table is filtered or searched on the
// tenant (a scan of metering_partition_factor, or its tenant index).
func tenantBelowWindow(t *testing.T, plan map[string]interface{}, tenant string) bool {
	t.Helper()
	windows := planNodes(plan, "WindowAgg")
	require.Len(t, windows, 1)
	cond := "(tenant = '" + tenant + "'::text)"
	for _, n := range planNodes(windows[0], "") {
		if n["Relation Name"] != "metering_partition_factor" && n["Index Name"] != "metering_partition_factor_tenant_idx" {
			continue
		}
		for _, k := range []string{"Filter", "Index Cond", "Recheck Cond"} {
			if c, _ := n[k].(string); strings.Contains(c, cond) {
				return true
			}
		}
	}
	return false
}

// planNodes are the nodes of the given type in the plan tree ("": all nodes).
func planNodes(n map[string]interface{}, nodeType string) []map[string]interface{} {
	var found []map[string]interface{}
	if nodeType == "" || n["Node Type"] == nodeType {
		found = append(found, n)
	}
	children, _ := n["Plans"].([]interface{})
	for _, c := range children {
		found = append(found, planNodes(c.(map[string]interface{}), nodeType)...)
	}
	return found
}
