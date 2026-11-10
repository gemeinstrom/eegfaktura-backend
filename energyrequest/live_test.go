package energyrequest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLive_openRunAndItsAnswers(t *testing.T) {
	single := testRun("ZP3")
	single.NoSummary = true
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
		answerRow(t, 11, "RC100100", answerAccepted, "ZP1"),
		answerRow(t, 12, "RC100100", answerAccepted, "ZP9"), // not in the run
		row(t, 13, "RC100100", RowTypeRun, single),
		answerRow(t, 14, "RC100100", answerRejected, "ZP3", "86"),
	}
	entries, hidden := Live(rows, "RC100100", tenants, created.Add(time.Hour))
	require.Len(t, entries, 1, "a single request has no live entry")
	e := entries[0]
	assert.Equal(t, int64(10), e.RowId)
	assert.Equal(t, created.UnixMilli(), e.CreatedAt)
	assert.True(t, e.Summary.Live)
	assert.Equal(t, SummaryType, e.Summary.Type)
	assert.Equal(t, []MeterRange{{MeteringPoint: "ZP1", From: sepFrom, To: sepTo}}, e.Summary.Accepted)
	assert.Equal(t, []string{"ZP2"}, e.Summary.NoAnswer)
	assert.Equal(t, map[int64]bool{11: true}, hidden, "only the answers that belong to the open run")
}

func TestLive_closedOrOldOrForeignRunsAreNotLive(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, run),
		answerRow(t, 11, "RC100100", answerAccepted, "ZP1"),
	}
	entries, _ := Live(rows, "RC200200", tenants, created)
	assert.Empty(t, entries, "a run of another tenant")

	entries, hidden := Live(rows, "RC100100", tenants, created.Add(Deadline))
	assert.Empty(t, entries, "older than the deadline")
	assert.Empty(t, hidden)

	closed := append(rows, row(t, 12, "RC100100", messageRow, Summary{Type: SummaryType, RunId: run.RunId}))
	entries, hidden = Live(closed, "RC100100", tenants, created.Add(time.Hour))
	assert.Empty(t, entries, "the stored summary replaces the live entry")
	assert.Empty(t, hidden, "answers of a closed run are listed as before")
}

func TestLive_answerOfALaterRunIsNotHidden(t *testing.T) {
	later := testRun("ZP1", "ZP3")
	later.CreatedAt = created.Add(time.Minute).UnixMilli()
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
		row(t, 11, "RC100100", RowTypeRun, later),
		answerRow(t, 12, "RC100100", answerAccepted, "ZP1"),
	}
	entries, hidden := Live(rows, "RC100100", tenants, created.Add(time.Hour))
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"ZP1"}, entries[0].Summary.Superseded)
	assert.Equal(t, map[int64]bool{12: true}, hidden, "hidden for the later run it belongs to")
}
