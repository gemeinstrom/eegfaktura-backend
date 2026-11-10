package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Check, the live entry and the lock read the same rows and attribute alike: a GEA (two tenants, one community id),
// a single request (no summary) before a bulk request of the sibling, an overlapping later bulk request, and a check
// as after a restart.
func TestEnergyRequestCheckLiveAndLockAgree(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)

	const ecId = "AT00999900000TC000177000000000001"
	const tenantA, tenantB = "TE000177", "TE000178"
	const zp1, zp2, zp3 = "AT0099990000000000000000000177001", "AT0099990000000000000000000177002", "AT0099990000000000000000000177003"
	for _, tenant := range []string{tenantA, tenantB} {
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'AGREE-TEST', 'Testgemeinschaft', 'TE000177', 'LOCAL', 'AT009999', 'Test Operator', $2,
			 'Solarweg', '1', 'Linz', '4020', 'agree-test@example.org')
			ON CONFLICT (tenant) DO NOTHING`, tenant, ecId)
		require.NoError(t, err)
	}
	month := func(m time.Month) (int64, int64) {
		return time.Date(2026, m, 1, 0, 0, 0, 0, time.Local).UnixMilli(),
			time.Date(2026, m+1, 0, 23, 45, 0, 0, time.Local).UnixMilli()
	}
	mayFrom, mayTo := month(time.May)
	aprFrom, aprTo := month(time.April)

	// R1: tenant A, ZP1 for May, a single request; R2: tenant B, ZP1 and ZP2 for April; R3: tenant A, ZP2 (April,
	// supersedes R2's) and ZP3
	r1 := energyrequest.NewRun(ecId, mayFrom, mayTo)
	r1.Add(zp1, mayFrom, mayTo)
	_, err = energyrequest.Save(ctx, db, tenantA, []string{zp1}, r1)
	require.NoError(t, err)
	r2 := energyrequest.NewRun(ecId, aprFrom, aprTo)
	r2.Add(zp1, aprFrom, aprTo)
	r2.Add(zp2, aprFrom, aprTo)
	_, err = energyrequest.Save(ctx, db, tenantB, []string{zp1, zp2}, r2)
	require.NoError(t, err)
	r3 := energyrequest.NewRun(ecId, aprFrom, aprTo)
	r3.Add(zp2, aprFrom, aprTo)
	r3.Add(zp3, aprFrom, aprTo)
	_, err = energyrequest.Save(ctx, db, tenantA, []string{zp2, zp3}, r3)
	require.NoError(t, err)

	// the answers are stored under one tenant of the community
	require.NoError(t, db.SaveNotification(tenantA, model.EBMS_ZP_RES, []string{zp1}, []string{}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification(tenantA, model.EBMS_ZP_REJ, []string{zp1}, []string{"86"}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification(tenantA, model.EBMS_ZP_RES, []string{zp2}, []string{}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification(tenantA, model.EBMS_ZP_RES, []string{zp3}, []string{}, model.CR_REQ_PT))

	now := time.Now()
	live := map[string]energyrequest.Summary{}
	for _, tenant := range []string{tenantA, tenantB} {
		rows, tenants, err := db.EnergyRequestCommunity(ctx, tenant)
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{tenantA: true, tenantB: true}, tenants)
		entries, _ := energyrequest.Live(rows, tenant, tenants, now)
		require.Len(t, entries, 1, tenant)
		live[entries[0].Summary.RunId] = entries[0].Summary
	}
	// R2: the first answer to ZP1 belongs to the older R1 (May), the rejection to R2; ZP2 is requested again by R3
	assert.Equal(t, []energyrequest.Rejected{{MeteringPoint: zp1, Codes: []string{"86"}}}, live[r2.RunId].Rejected)
	assert.Equal(t, []string{zp2}, live[r2.RunId].Superseded)
	assert.Len(t, live[r3.RunId].Accepted, 2)

	rowsA, tenants, err := db.EnergyRequestCommunity(ctx, tenantA)
	require.NoError(t, err)
	locksA := energyrequest.NewLocks(rowsA, tenantA, tenants, now)
	assert.NotZero(t, locksA.Until(zp1, mayFrom, mayTo), "R1 got an acceptance")
	assert.Zero(t, locksA.Until(zp1, aprFrom, aprTo), "R2 is tenant B's")
	locksB := energyrequest.NewLocks(rowsA, tenantB, tenants, now)
	assert.Zero(t, locksB.Until(zp1, aprFrom, aprTo), "R2 was rejected for ZP1")
	assert.NotZero(t, locksB.Until(zp2, aprFrom, aprTo), "superseded, not rejected")

	// a check (as after a restart: nothing kept in memory) writes the summaries the live entries showed
	require.NoError(t, energyrequest.Check(ctx, db, now))
	rows, _, err := db.EnergyRequestCommunity(ctx, tenantB)
	require.NoError(t, err)
	stored := map[string]energyrequest.Summary{}
	for _, r := range rows {
		var s energyrequest.Summary
		if r.Type == string(model.N_TYPE_MESSAGE) && json.Unmarshal(r.Message, &s) == nil && s.Type == energyrequest.SummaryType {
			stored[s.RunId] = s
		}
	}
	require.Len(t, stored, 2, "R2 and R3; R1 has no summary")
	for runId, s := range live {
		s.Live = false
		assert.Equal(t, s, stored[runId])
	}
}

// an answer to a run that is older than the deadline stays with that run: the run rows are read back further than
// the answers (R0 May 26 h ago, R1 April 23 h ago, the answer 22 h ago belongs to R0, not to the open R1)
func TestEnergyRequestOwnerOfAnAnswerStaysInTheSet(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	const tenant, ecId = "TE000181", "AT00999900000TC000181000000000001"
	const zp1, zp2 = "AT0099990000000000000000000181001", "AT0099990000000000000000000181002"
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'OWNER-TEST', 'Testgemeinschaft', 'TE000181', 'LOCAL', 'AT009999', 'Test Operator', $2,
		 'Solarweg', '1', 'Linz', '4020', 'owner-test@example.org')
		ON CONFLICT (tenant) DO NOTHING`, tenant, ecId)
	require.NoError(t, err)
	insert := func(ago time.Duration, process, rowType string, v interface{}) {
		msg, err := json.Marshal(v)
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.notification (tenant, notification, type, role, process, date)
			VALUES ($1, $2, $3, 'ADMIN', $4, (now() AT TIME ZONE 'utc') - make_interval(secs => $5))`,
			tenant, string(msg), rowType, process, ago.Seconds())
		require.NoError(t, err)
	}
	run := func(ago time.Duration, m time.Month, meters ...string) *energyrequest.Run {
		from := time.Date(2026, m, 1, 0, 0, 0, 0, time.Local).UnixMilli()
		to := time.Date(2026, m+1, 0, 23, 45, 0, 0, time.Local).UnixMilli()
		r := energyrequest.NewRun(ecId, from, to)
		for _, mp := range meters {
			r.Add(mp, from, to)
		}
		r.CreatedAt = time.Now().Add(-ago).UnixMilli()
		insert(ago, string(model.N_PROCESS_ENERGY_REQUEST), energyrequest.RowTypeRun, r)
		return r
	}
	run(26*time.Hour, time.May, zp1, zp2)
	r1 := run(23*time.Hour, time.April, zp1, zp2)
	insert(22*time.Hour, string(model.N_PROCESS_EDA_PROCESS), string(model.N_TYPE_MESSAGE),
		map[string]interface{}{"type": "ANTWORT_PT", "meteringPoints": []string{zp1}, "responseCodes": []string{}})

	rows, tenants, err := db.EnergyRequestCommunity(ctx, tenant)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	entries, hidden := energyrequest.Live(rows, tenant, tenants, time.Now())
	require.Len(t, entries, 1)
	assert.Equal(t, r1.RunId, entries[0].Summary.RunId)
	assert.Empty(t, entries[0].Summary.Accepted, "the answer belongs to the older May run")
	assert.ElementsMatch(t, []string{zp1, zp2}, entries[0].Summary.NoAnswer)
	assert.Empty(t, hidden)
}
