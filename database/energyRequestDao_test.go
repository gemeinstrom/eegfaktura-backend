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

// platform#173: run, answers and summary live in base.notification; the hidden run row is not listed.
func TestEnergyRequestSummary(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)

	// a GEA: two tenants share one community id; the answers are stored under the second one
	const ecId = "AT00999900000TC000173000000000001"
	for _, tenant := range []string{"TE000173", "TE000174"} {
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'SUMMARY-TEST', 'Testgemeinschaft', 'TE000173', 'LOCAL', 'AT009999', 'Test Operator', $2,
			 'Solarweg', '1', 'Linz', '4020', 'summary-test@example.org')
			ON CONFLICT (tenant) DO NOTHING`, tenant, ecId)
		require.NoError(t, err)
	}
	tenants, err := db.TenantsByEcId(ctx, ecId)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"TE000173", "TE000174"}, tenants)

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	to := time.Date(2026, 9, 30, 23, 45, 0, 0, time.Local).UnixMilli()
	run := energyrequest.NewRun(ecId, from, to)
	run.Add("AT0099990000000000000000000173001", from, to)
	run.Add("AT0099990000000000000000000173002", from, to)
	run.Skip("AT0099990000000000000000000173003", energyrequest.ReasonNotActiveInPeriod)
	_, err = energyrequest.Save(ctx, db, "TE000173", []string{"AT0099990000000000000000000173001",
		"AT0099990000000000000000000173002", "AT0099990000000000000000000173003"}, run)
	require.NoError(t, err)

	// answers as the CR_REQ_PT handler stores them, under the tenant GetEegByEcId returns
	require.NoError(t, db.SaveNotification("TE000174", model.EBMS_ZP_RES, []string{"AT0099990000000000000000000173001"}, []string{}, model.CR_REQ_PT))
	require.NoError(t, db.SaveNotification("TE000174", model.EBMS_ZP_REJ, []string{"AT0099990000000000000000000173002"}, []string{"Zählpunkt nicht gefunden"}, model.CR_REQ_PT))

	rows, err := db.EnergyRequestRows(ctx, nil)
	require.NoError(t, err)
	// only this test's rows: other tests leave rows in the shared database
	var runs, answers int
	for _, r := range rows {
		if r.Tenant != "TE000173" && r.Tenant != "TE000174" {
			continue
		}
		if r.Type == energyrequest.RowTypeRun {
			runs++
		} else {
			answers++
		}
	}
	assert.Equal(t, 1, runs)
	assert.GreaterOrEqual(t, answers, 2)

	require.NoError(t, energyrequest.Check(ctx, db, time.Now()))

	// the hidden rows are not listed in Messages, the summary is
	list, err := db.GetNotification(ctx, "TE000173", 0, true)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, model.N_PROCESS_EDA_PROCESS, list[0].Process)
	var s energyrequest.Summary
	require.NoError(t, json.Unmarshal(list[0].Message, &s))
	assert.Equal(t, energyrequest.SummaryType, s.Type)
	assert.Equal(t, 2, s.Requested)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: "AT0099990000000000000000000173001", From: from, To: to}}, s.Accepted)
	assert.Equal(t, []energyrequest.Rejected{{MeteringPoint: "AT0099990000000000000000000173002", Codes: []string{"Zählpunkt nicht gefunden"}}}, s.Rejected)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: "AT0099990000000000000000000173003", Reason: energyrequest.ReasonNotActiveInPeriod}}, s.Skipped)

	// a second check and a second close do not write a second summary
	require.NoError(t, energyrequest.Check(ctx, db, time.Now()))
	var runRowId int64
	for _, r := range rows {
		if r.Type == energyrequest.RowTypeRun {
			runRowId = r.Id
		}
	}
	written, err := db.CloseEnergyRequest(ctx, "TE000173", runRowId, s)
	require.NoError(t, err)
	assert.False(t, written)
	list, err = db.GetNotification(ctx, "TE000173", 0, true)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

// a request with a single metering point gets no summary
func TestEnergyRequestSummary_singleMeter(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	run := energyrequest.NewRun("AT00999900000TC000175000000000001", 1, 2)
	run.Add("AT0099990000000000000000000175001", 1, 2)
	_, err = energyrequest.Save(ctx, db, "TE000175", []string{"AT0099990000000000000000000175001"}, run)
	require.NoError(t, err)

	// stored as a boundary for older runs, but never summarised
	rows, err := db.EnergyRequestRows(ctx, []string{"TE000175"})
	require.NoError(t, err)
	var stored int
	for _, r := range rows {
		if r.Tenant == "TE000175" {
			stored++
			var run energyrequest.Run
			require.NoError(t, json.Unmarshal(r.Message, &run))
			assert.True(t, run.NoSummary)
		}
	}
	assert.Equal(t, 1, stored)

	require.NoError(t, energyrequest.Check(ctx, db, time.Now().Add(energyrequest.Deadline)))
	list, err := db.GetNotification(ctx, "TE000175", 0, true)
	require.NoError(t, err)
	assert.Empty(t, list)
}
