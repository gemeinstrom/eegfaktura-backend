package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collapseEeg inserts a community for the Messages tests.
func collapseEeg(t *testing.T, tenant, ecId string) {
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'COLLAPSE-TEST', 'Testgemeinschaft', $3, 'LOCAL', 'AT009999', 'Test Operator', $2,
		 'Solarweg', '1', 'Linz', '4020', 'collapse-test@example.org')
		ON CONFLICT (tenant) DO NOTHING`, tenant, ecId, tenant)
	require.NoError(t, err)
}

// collapseRun stores a run of tenant for n metering points (prefix + number) and returns the run, its row id and the
// metering points.
func collapseRun(t *testing.T, db *sqlDatabase, tenant, ecId, prefix string, n int) (*energyrequest.Run, int64, []string) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	to := time.Date(2026, 9, 30, 23, 45, 0, 0, time.Local).UnixMilli()
	run := energyrequest.NewRun(ecId, from, to)
	meters := []string{}
	for i := 0; i < n; i++ {
		zp := fmt.Sprintf("%s%03d", prefix, i)
		meters = append(meters, zp)
		run.Add(zp, from, to)
	}
	id, err := energyrequest.Save(context.Background(), db, tenant, meters, run)
	require.NoError(t, err)
	return run, id, meters
}

func summaryOf(t *testing.T, n model.EegNotification) energyrequest.Summary {
	var s energyrequest.Summary
	require.NoError(t, json.Unmarshal(n.Message, &s))
	return s
}

// A request for a whole community: one request echo per metering point and some answers, more than a page and more
// than one batch. The live entry stays on top and stands for all of them, then the stored summary does, also after
// the 24 h in which the list reads the live entries' rows.
func TestNotificationCollapse_wholeCommunity(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	sdb := db.(*sqlDatabase)
	defer func(first, next uint, maxBatches int) {
		collapseFirstBatch, collapseBatch, collapseMaxBatches = first, next, maxBatches
	}(collapseFirstBatch, collapseBatch, collapseMaxBatches)
	collapseFirstBatch, collapseBatch = 7, 11

	const tenant = "TE000183"
	const ecId = "AT00999900000TC000183000000000001"
	collapseEeg(t, tenant, ecId)
	const other = "AT0099990000000000000000000183999"
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{other}, []string{}, model.CR_REQ_PT))

	run, runRowId, meters := collapseRun(t, sdb, tenant, ecId, "AT0099990000000000000000000183", 40)
	for i, zp := range meters {
		require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_SYNC, []string{zp}, []string{}, model.CR_REQ_PT))
		if i == 20 { // a visible entry between the run and its last messages: the live entry is placed above it
			require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{other}, []string{}, model.CR_REQ_PT))
		}
	}
	for _, zp := range meters[:3] {
		require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, []string{zp}, []string{}, model.CR_REQ_PT))
	}

	list, err := db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "the live entry, the answer during the run, the answer before the run")
	assert.Equal(t, runRowId, list[0].Id, "the live entry keeps the run's row id, placed with its newest message")
	assert.Greater(t, list[1].Id, runRowId)
	s := summaryOf(t, list[0])
	assert.True(t, s.Live)
	assert.Equal(t, 40, s.Requested)
	assert.Len(t, s.Accepted, 3)

	// the batch limit ends the reading: the live entry is still there, older entries are missing
	collapseMaxBatches = 2
	list, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 1, "only the live entry: 7 + 11 rows hold 3 answers and 15 request echoes")
	collapseMaxBatches = 40

	// closed after the deadline: the stored summary stands for the requests and answers
	require.NoError(t, energyrequest.Check(ctx, db, time.Now().Add(energyrequest.Deadline+time.Minute)))
	list, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "the summary, the two other answers")
	s = summaryOf(t, list[0])
	assert.False(t, s.Live)
	assert.Equal(t, energyrequest.SummaryType, s.Type)
	assert.Len(t, s.NoAnswer, 37)

	// an answer after the summary is listed: the summary does not count it
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_REJ, []string{meters[5]}, []string{"86"}, model.CR_REQ_PT))
	list, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 4, "the late answer, the summary, the two other answers")
	assert.Equal(t, energyrequest.SummaryType, summaryOf(t, list[1]).Type)

	// 30 h later: no run younger than 24 h, the live entries' rows are not read; the run is still found
	_, err = testDB.DbInstance.Exec(`UPDATE base.notification SET date = date - interval '30 hours' WHERE tenant = $1`, tenant)
	require.NoError(t, err)
	run.CreatedAt = time.UnixMilli(run.CreatedAt).Add(-30 * time.Hour).UnixMilli()
	require.NoError(t, db.UpdateEnergyRequestRow(ctx, runRowId, run))
	clearCommunityCache()
	list, err = db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 4, "the late answer, the summary, the two other answers")
	assert.Equal(t, energyrequest.SummaryType, summaryOf(t, list[1]).Type)
}

// GEA: two tenants with one community id. The run of one tenant leaves out its messages in its own list; where they
// are stored under the other tenant (the backend stores EDA messages under one tenant of the community id), that list
// keeps them, as it has neither the live entry nor the summary.
func TestNotificationCollapse_otherTenantOfTheCommunity(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	sdb := db.(*sqlDatabase)

	const tenantA, tenantB = "TE000184", "TE000185"
	const ecId = "AT00999900000TC000184000000000001"
	collapseEeg(t, tenantA, ecId)
	collapseEeg(t, tenantB, ecId)

	_, runRowId, meters := collapseRun(t, sdb, tenantA, ecId, "AT0099990000000000000000000184", 3)
	for _, zp := range meters {
		require.NoError(t, db.SaveNotification(tenantA, model.EBMS_ZP_SYNC, []string{zp}, []string{}, model.CR_REQ_PT))
		require.NoError(t, db.SaveNotification(tenantB, model.EBMS_ZP_SYNC, []string{zp}, []string{}, model.CR_REQ_PT))
	}

	list, err := db.GetNotification(ctx, tenantA, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 1, "the live entry")
	assert.Equal(t, runRowId, list[0].Id)

	list, err = db.GetNotification(ctx, tenantB, 0, true)
	require.NoError(t, err)
	require.Len(t, list, 3, "the request echoes, no live entry")
	for _, n := range list {
		assert.NotEqual(t, runRowId, n.Id)
	}
}

// A request for a single metering point keeps its messages.
func TestNotificationCollapse_singleRequest(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	sdb := db.(*sqlDatabase)

	const tenant = "TE000186"
	const ecId = "AT00999900000TC000186000000000001"
	collapseEeg(t, tenant, ecId)
	_, _, meters := collapseRun(t, sdb, tenant, ecId, "AT0099990000000000000000000186", 1)
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_SYNC, meters, []string{}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification(tenant, model.EBMS_ZP_RES, meters, []string{}, model.CR_REQ_PT))

	list, err := db.GetNotification(ctx, tenant, 0, true)
	require.NoError(t, err)
	assert.Len(t, list, 2, "request echo and answer, no live entry")
}
