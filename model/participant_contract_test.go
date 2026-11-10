package model

import (
	"encoding/json"
	"testing"

	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GET /participant JSON contract (caller eegfaktura-energystore, member tokens): energystore reads
// meters[].meteringPoint, meters[].status, meters[].registeredSince and meters[].participantState.activeSince/inactiveSince
// (yyyy-mm-dd or null) to limit a member to its own metering points and their participation window.
func TestParticipantMetersJSONForEnergystore(t *testing.T) {
	p := EegParticipant{EegParticipantBase: EegParticipantBase{MeteringPoint: []*MeteringPoint{
		{
			MeteringPoint:   "AT0030000000000000000000000000001",
			Status:          S_ACTIVE,
			RegisteredSince: civil.DateFor(2025, 1, 1),
			State: &MeterState{
				ActiveSince:   civil.NullDate{Date: civil.DateFor(2026, 2, 1), Valid: true},
				InactiveSince: civil.NullDate{},
			},
		},
	}}}
	b, err := json.Marshal([]*EegParticipant{&p})
	require.NoError(t, err)

	var got []struct {
		Meters []struct {
			MeteringPoint    string         `json:"meteringPoint"`
			Status           *string        `json:"status"`
			RegisteredSince  string         `json:"registeredSince"`
			ParticipantState map[string]any `json:"participantState"`
		} `json:"meters"`
	}
	require.NoError(t, json.Unmarshal(b, &got))
	require.Len(t, got, 1)
	require.Len(t, got[0].Meters, 1)
	m := got[0].Meters[0]
	assert.Equal(t, "AT0030000000000000000000000000001", m.MeteringPoint)
	require.NotNil(t, m.Status)
	assert.Equal(t, "ACTIVE", *m.Status)
	assert.Equal(t, "2025-01-01", m.RegisteredSince)
	assert.Equal(t, "2026-02-01", m.ParticipantState["activeSince"])
	assert.Contains(t, m.ParticipantState, "inactiveSince")
	assert.Nil(t, m.ParticipantState["inactiveSince"], "no end date: null")
}

// An empty status is left out (omitempty); energystore reads a missing status as empty.
func TestParticipantMeterEmptyStatusOmitted(t *testing.T) {
	b, err := json.Marshal(&MeteringPoint{MeteringPoint: "AT0030000000000000000000000000001"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	assert.NotContains(t, got, "status")
}
