package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

// 08.11.2026: the member sheet of the master data export is ordered by member number, numbers numerically.
func TestExportMasterdataToExcel_orderedByMemberNumber(t *testing.T) {
	member := func(nr, name, meteringPoint string) *model.EegParticipant {
		p := &model.EegParticipant{EegParticipantBase: model.EegParticipantBase{
			Id: uuid.NewRandom(), FirstName: name, LastName: "Test",
			MeteringPoint: []*model.MeteringPoint{{MeteringPoint: meteringPoint, State: &model.MeterState{}}},
		}}
		if nr != "" {
			p.ParticipantNumber = null.StringFrom(nr)
		}
		return p
	}
	participants := []*model.EegParticipant{
		member("10", "Zehn", "AT0030000000000000000000000000010"),
		member("", "Ohne", "AT0030000000000000000000000000099"),
		member("A7", "Buchstabe", "AT0030000000000000000000000000077"),
		member("2", "Zwei", "AT0030000000000000000000000000002"),
		member(" 003", "Drei", "AT0030000000000000000000000000003"),
	}

	buf, err := ExportMasterdataToExcel(context.Background(), participants, &model.Eeg{RcNumber: "RC100999"},
		map[string]string{}, map[string]float64{}, nil)
	require.NoError(t, err)
	rows := exportRows(t, buf)
	nameCol := column(t, rows[0], "Name 1")
	var names []string
	for _, r := range rows[1:] {
		names = append(names, r[nameCol])
	}
	assert.Equal(t, []string{"Zwei", "Drei", "Zehn", "Buchstabe", "Ohne"}, names)
	// the caller's slice is not reordered
	assert.Equal(t, "Zehn", participants[0].FirstName)
}

func Test_byMemberNumber_keepsOrderOfEqualNumbers(t *testing.T) {
	a := &model.EegParticipant{EegParticipantBase: model.EegParticipantBase{FirstName: "a", ParticipantNumber: null.StringFrom("5")}}
	b := &model.EegParticipant{EegParticipantBase: model.EegParticipantBase{FirstName: "b", ParticipantNumber: null.StringFrom("5")}}
	got := byMemberNumber([]*model.EegParticipant{b, a})
	assert.Equal(t, "b", got[0].FirstName)
	assert.Equal(t, "a", got[1].FirstName)
}
