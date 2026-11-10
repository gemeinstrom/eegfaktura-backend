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

// An open run with a summary is listed as a live entry (with its row id), its answers are left out of the list; once
// closed, the stored summary takes its place and the answers stay out (decision 28.10.2026).
func TestEnergyRequestLiveEntry(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)

	const tenant = "TE000176"
	const ecId = "AT00999900000TC000176000000000001"
	const zp1, zp2, zp9 = "AT0099990000000000000000000176001", "AT0099990000000000000000000176002", "AT0099990000000000000000000176009"
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'LIVE-TEST', 'Testgemeinschaft', $3, 'LOCAL', 'AT009999', 'Test Operator', $2,
		 'Solarweg', '1', 'Linz', '4020', 'live-test@example.org')
		ON CONFLICT (tenant) DO NOTHING`, tenant, ecId, tenant)
	require.NoError(t, err)

	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{zp9}, []string{}, model.CR_REQ_PT))
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	to := time.Date(2026, 9, 30, 23, 45, 0, 0, time.Local).UnixMilli()
	run := energyrequest.NewRun(ecId, from, to)
	run.Add(zp1, from, to)
	run.Add(zp2, from, to)
	_, err = energyrequest.Save(ctx, db, tenant, []string{zp1, zp2}, run)
	require.NoError(t, err)
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{zp1}, []string{}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{zp9}, []string{}, model.CR_REQ_PT))

	rows, tenants, err := db.EnergyRequestCommunity(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{tenant: true}, tenants)
	require.Len(t, rows, 3, "the run and the answers from it on")
	runRowId := rows[0].Id
	assert.Equal(t, energyrequest.RowTypeRun, rows[0].Type)

	list, err := db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "answer to zp9 after the run, the live entry, answer to zp9 before the run")
	assert.Greater(t, list[0].Id, runRowId)
	assert.Equal(t, runRowId, list[1].Id)
	assert.Less(t, list[2].Id, runRowId)
	live := list[1]
	assert.Equal(t, string(model.N_TYPE_MESSAGE), live.MsgType)
	assert.Equal(t, model.N_PROCESS_EDA_PROCESS, live.Process)
	assert.Equal(t, run.CreatedAt, live.Date.UnixMilli())
	var s energyrequest.Summary
	require.NoError(t, json.Unmarshal(live.Message, &s))
	assert.True(t, s.Live)
	assert.Equal(t, energyrequest.SummaryType, s.Type)
	assert.Equal(t, run.RunId, s.RunId)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: zp1, From: from, To: to}}, s.Accepted)
	assert.Equal(t, []string{zp2}, s.NoAnswer)

	// paging: only entries after start
	list, err = db.GetNotification(ctx, tenant, runRowId, true)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// no live entry for users (only ADMIN rows) and for other tenants
	list, err = db.GetNotification(ctx, tenant, 0, false)
	require.NoError(t, err)
	assert.Empty(t, list)
	list, err = db.GetNotification(ctx, "TE000004", 0, true)
	require.NoError(t, err)
	for _, n := range list {
		assert.NotEqual(t, runRowId, n.Id)
	}

	// the energy request rows cannot be read (here: cancelled context, not cached): the plain list, nothing left out
	clearCommunityCache()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	list, err = db.GetNotification(cancelled, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "the three answers, no live entry")
	for _, n := range list {
		assert.NotEqual(t, runRowId, n.Id)
	}

	// closed: the stored summary instead of the live entry; it still stands for the run's answers
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_REJ, []string{zp2}, []string{"86"}, model.CR_REQ_PT))
	require.NoError(t, energyrequest.Check(ctx, db, time.Now()))
	list, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "summary, answer zp9, answer zp9")
	s = energyrequest.Summary{}
	require.NoError(t, json.Unmarshal(list[0].Message, &s))
	assert.Equal(t, energyrequest.SummaryType, s.Type)
	assert.False(t, s.Live)
	for _, n := range list {
		assert.NotEqual(t, runRowId, n.Id)
	}
}

// the list keeps the community rows for communityCacheTTL; writing a run clears them, so the lock and a new live
// entry are not delayed by this instance's cache
func TestEnergyRequestCommunityCache(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	clearCommunityCache()

	const tenant = "TE000179" // no base.eeg row needed: an own tenant without community
	_, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	communityCache.Lock()
	_, cached := communityCache.entries[tenant]
	communityCache.Unlock()
	assert.True(t, cached)

	single := energyrequest.NewRun("AT00999900000TC000179000000000001", 1, 2)
	single.NoSummary = true
	_, err = db.SaveEnergyRequestRow(ctx, tenant, energyrequest.RowTypeRun, single)
	require.NoError(t, err)
	communityCache.Lock()
	assert.Empty(t, communityCache.entries)
	communityCache.Unlock()
}

// rows read before a clear (a run written meanwhile) are not stored after it
func TestEnergyRequestCommunityCache_readBeforeAClear(t *testing.T) {
	clearCommunityCache()
	communityCache.Lock()
	generation := communityCache.generation
	communityCache.Unlock()

	clearCommunityCache() // a write while the read was running
	storeCommunityRows("TE000179", generation, communityRows{at: time.Now()})
	communityCache.Lock()
	assert.Empty(t, communityCache.entries)
	communityCache.Unlock()

	communityCache.Lock()
	generation = communityCache.generation
	communityCache.Unlock()
	storeCommunityRows("TE000179", generation, communityRows{at: time.Now()})
	communityCache.Lock()
	assert.Len(t, communityCache.entries, 1)
	communityCache.Unlock()
	clearCommunityCache()
}

// the list reads the community rows only for a tenant with a run younger than Deadline: no such run, no live entry
func TestEnergyRequestRunProbe(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	clearCommunityCache()

	const tenant = "TE000182" // no base.eeg row needed: an own tenant without community
	found, err := db.(*sqlDatabase).hasEnergyRequestRun(ctx, tenant)
	require.NoError(t, err)
	assert.False(t, found)
	_, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	communityCache.Lock()
	e, cached := communityCache.entries[tenant]
	communityCache.Unlock()
	assert.True(t, cached)
	assert.Empty(t, e.rows, "no run: no rows read")

	single := energyrequest.NewRun("AT00999900000TC000182000000000001", 1, 2)
	single.NoSummary = true
	id, err := db.SaveEnergyRequestRow(ctx, tenant, energyrequest.RowTypeRun, single)
	require.NoError(t, err)
	t.Cleanup(func() { // a young run left behind would fail the first assertion of every later run for 24 h
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.notification WHERE id = $1`, id)
		assert.NoError(t, err)
	})
	found, err = db.(*sqlDatabase).hasEnergyRequestRun(ctx, tenant)
	require.NoError(t, err)
	assert.True(t, found)
	_, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	communityCache.Lock()
	e = communityCache.entries[tenant]
	communityCache.Unlock()
	assert.Len(t, e.rows, 1, "a run: the rows are read")

	// older than Deadline: never live, the id bound is the run row itself
	_, err = testDB.DbInstance.Exec(`UPDATE base.notification SET date = date - make_interval(secs => $1) WHERE id = $2`,
		(energyrequest.Deadline + time.Minute).Seconds(), id)
	require.NoError(t, err)
	found, err = db.(*sqlDatabase).hasEnergyRequestRun(ctx, tenant)
	require.NoError(t, err)
	assert.False(t, found)
}
