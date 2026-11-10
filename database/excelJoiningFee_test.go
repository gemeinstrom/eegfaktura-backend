package database

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"gopkg.in/guregu/null.v4"
)

// exportRows reads the sheet "Mitglieder" of an exported workbook (raw values).
func exportRows(t *testing.T, buf *bytes.Buffer) [][]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows("Mitglieder", excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	return rows
}

func column(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("column %q not in header %v", name, header)
	return -1
}

// Member export, konzept-beitrittsgebuehr.md (Erweiterung 27.10.2026): "Beitrittsgebühr (€)" and "Beitrittsgebühr
// Status" directly after "Mitgliedstarif", in every row of the member; the list from web only fills the status of the
// tenant's own members.
func TestExportMasterdataToExcel_joiningFee(t *testing.T) {
	const feeTariff, plainTariff = "7a1d0c2e-1111-4c55-9a51-7d3f2b6a0001", "7a1d0c2e-1111-4c55-9a51-7d3f2b6a0002"
	withFee := &model.EegParticipant{EegParticipantBase: model.EegParticipantBase{
		Id: uuid.Parse("7a1d0c2e-2222-4c55-9a51-7d3f2b6a0001"), FirstName: "Mit", LastName: "Gebühr",
		TariffId:      null.StringFrom(feeTariff),
		MeteringPoint: []*model.MeteringPoint{{MeteringPoint: "AT0030000000000000000000000000001", State: &model.MeterState{}}, {MeteringPoint: "AT0030000000000000000000000000002", State: &model.MeterState{}}},
	}}
	withoutFee := &model.EegParticipant{EegParticipantBase: model.EegParticipantBase{
		Id: uuid.Parse("7a1d0c2e-2222-4c55-9a51-7d3f2b6a0002"), FirstName: "Ohne", LastName: "Gebühr",
		TariffId:      null.StringFrom(plainTariff),
		MeteringPoint: []*model.MeteringPoint{{MeteringPoint: "AT0030000000000000000000000000003", State: &model.MeterState{}}},
	}}
	participants := []*model.EegParticipant{withFee, withoutFee}
	eeg := &model.Eeg{RcNumber: "RC100999"}
	tariffNames := map[string]string{feeTariff: "Beitrag mit Gebühr", plainTariff: "Beitrag"}
	joiningFees := map[string]float64{feeTariff: 49.9}

	states := []model.JoiningFeeState{
		// id in upper case: matched case-insensitively
		{ParticipantId: strings.ToUpper(withFee.Id.String()), EnergyBilled: true,
			JoiningFee: &model.JoiningFeeDocument{DocumentNumber: "RG202600012", DocumentDate: "2026-01-15"}},
		{ParticipantId: withoutFee.Id.String(), EnergyBilled: true},
		// a participant of another tenant: ignored
		{ParticipantId: "ea9942da-03da-11ee-b82b-5a985b4b033a", EnergyBilled: true,
			JoiningFee: &model.JoiningFeeDocument{DocumentNumber: "RG-FREMD", DocumentDate: "2026-02-01"}},
	}

	t.Run("the tax number of a member is not exported", func(t *testing.T) {
		withFee.TaxNumber = null.StringFrom("11 123/4567")
		defer func() { withFee.TaxNumber = null.String{} }()
		buf, err := ExportMasterdataToExcel(context.Background(), participants, eeg, tariffNames, joiningFees, states)
		require.NoError(t, err)
		rows := exportRows(t, buf)
		assert.NotContains(t, rows[0], "SteuerNr.")
		assert.NotContains(t, rows[1], "11 123/4567")
	})

	t.Run("with the list from billing", func(t *testing.T) {
		buf, err := ExportMasterdataToExcel(context.Background(), participants, eeg, tariffNames, joiningFees, states)
		require.NoError(t, err)
		rows := exportRows(t, buf)
		require.Len(t, rows, 4, "header + one row per metering point")
		tariffCol := column(t, rows[0], "Mitgliedstarif")
		assert.Equal(t, "Beitrittsgebühr (€)", rows[0][tariffCol+1])
		assert.Equal(t, "Beitrittsgebühr Status", rows[0][tariffCol+2])
		assert.Equal(t, "Zählpunkt", rows[0][tariffCol+3])

		for _, row := range rows[1:3] { // every row of the member
			assert.Equal(t, "Beitrag mit Gebühr", row[tariffCol])
			assert.Equal(t, "49.9", row[tariffCol+1])
			assert.Equal(t, "verrechnet mit RG202600012 vom 15.01.2026", row[tariffCol+2])
		}
		assert.Equal(t, "Beitrag", rows[3][tariffCol])
		assert.Equal(t, "", rows[3][tariffCol+1], "no fee: empty")
		assert.Equal(t, "", rows[3][tariffCol+2], "no fee and nothing recorded: empty")
		assert.Equal(t, "AT0030000000000000000000000000003", rows[3][tariffCol+3])
		for _, row := range rows {
			for _, cell := range row {
				assert.NotContains(t, cell, "RG-FREMD")
			}
		}
	})

	t.Run("empty list: no final document yet", func(t *testing.T) {
		buf, err := ExportMasterdataToExcel(context.Background(), participants, eeg, tariffNames, joiningFees, []model.JoiningFeeState{})
		require.NoError(t, err)
		rows := exportRows(t, buf)
		tariffCol := column(t, rows[0], "Mitgliedstarif")
		for _, row := range rows[1:3] {
			assert.Equal(t, "offen – wird mit der ersten Energieabrechnung verrechnet", row[tariffCol+2])
		}
		assert.Equal(t, "", rows[3][tariffCol+2])
	})

	t.Run("without a list", func(t *testing.T) {
		buf, err := ExportMasterdataToExcel(context.Background(), participants, eeg, tariffNames, joiningFees, nil)
		require.NoError(t, err)
		rows := exportRows(t, buf)
		require.Len(t, rows, 4)
		tariffCol := column(t, rows[0], "Mitgliedstarif")
		for _, row := range rows[1:3] {
			assert.Equal(t, "49.9", row[tariffCol+1])
			assert.Equal(t, "", row[tariffCol+2])
		}
	})
}

// The status texts of the member details and their order (a final document wins, then the external payment, then
// the request; without any of them the tariff fee decides).
func Test_joiningFeeStatus(t *testing.T) {
	doc := &model.JoiningFeeDocument{DocumentNumber: "RG202600012", DocumentDate: "2026-01-15"}
	paid := &model.JoiningFeePaidExternal{PaidOn: "2025-03-01", Note: "bar beim Beitritt"}
	tests := []struct {
		name  string
		state *model.JoiningFeeState
		fee   float64
		want  string
	}{
		{"billed", &model.JoiningFeeState{EnergyBilled: true, JoiningFee: doc}, 50, "verrechnet mit RG202600012 vom 15.01.2026"},
		{"billed wins over the rest, also after the tariff lost its fee", &model.JoiningFeeState{EnergyBilled: true, JoiningFee: doc, Requested: true, PaidExternally: paid}, 0, "verrechnet mit RG202600012 vom 15.01.2026"},
		{"paid outside with note", &model.JoiningFeeState{EnergyBilled: true, PaidExternally: paid}, 50, "außerhalb bezahlt am 01.03.2025 (bar beim Beitritt)"},
		{"paid outside without note, no fee", &model.JoiningFeeState{PaidExternally: &model.JoiningFeePaidExternal{PaidOn: "2025-03-01"}}, 0, "außerhalb bezahlt am 01.03.2025"},
		{"requested", &model.JoiningFeeState{EnergyBilled: true, Requested: true}, 50, "zur Verrechnung im nächsten Abrechnungslauf vorgemerkt"},
		{"requested, but the tariff has no fee (any more): nothing will be charged, as in web", &model.JoiningFeeState{EnergyBilled: true, Requested: true}, 0, ""},
		{"open: never billed with energy", &model.JoiningFeeState{}, 50, "offen – wird mit der ersten Energieabrechnung verrechnet"},
		{"open: no entry in the list", nil, 50, "offen – wird mit der ersten Energieabrechnung verrechnet"},
		{"not billed: already billed with energy", &model.JoiningFeeState{EnergyBilled: true}, 50, "nicht verrechnet – Mitglied wurde schon abgerechnet"},
		{"no fee, already billed", &model.JoiningFeeState{EnergyBilled: true}, 0, ""},
		{"no fee, no entry", nil, 0, ""},
		{"date in another format is shown as sent", &model.JoiningFeeState{JoiningFee: &model.JoiningFeeDocument{DocumentNumber: "RG1", DocumentDate: "15.1.2026"}}, 50, "verrechnet mit RG1 vom 15.1.2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, joiningFeeStatus(tt.state, tt.fee))
		})
	}
}
