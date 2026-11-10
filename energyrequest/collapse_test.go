package energyrequest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func msg(t *testing.T, id int64, at time.Time, typ string, meters ...string) Message {
	b, err := json.Marshal(map[string]interface{}{"type": typ, "meteringPoints": meters, "responseCodes": []string{}})
	require.NoError(t, err)
	return Message{Id: id, Date: at, Content: b}
}

func summaryRow(t *testing.T, id int64, run Run) Row {
	return row(t, id, "RC100100", messageRow, Summary{Type: SummaryType, RunId: run.RunId})
}

// collapse evaluates messages one hour after created (runs are open) unless now is given.
func collapse(t *testing.T, runs []Row, summaries []Row, messages ...Message) (map[int64]bool, map[int64]int64) {
	c := NewCollapser("RC100100", created.Add(time.Hour))
	c.AddRuns(runs)
	c.AddSummaries(summaries)
	return c.Hide(messages)
}

func singleRun(meter string) Run {
	r := testRun(meter)
	r.NoSummary = true
	return r
}

func TestCollapser_requestsAndAnswersOfAnOpenRun(t *testing.T) {
	runs := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
		row(t, 20, "RC100100", RowTypeRun, singleRun("ZP3")),
	}
	at := created.Add(time.Minute)
	hidden, latest := collapse(t, runs, nil,
		msg(t, 9, at, "ANFORDERUNG_PT", "ZP1"), // before the run
		msg(t, 11, at, "ANFORDERUNG_PT", "ZP1"),
		msg(t, 12, at, "ANFORDERUNG_PT", "zp2"), // metering points compare exactly, as in the summary's attribution
		msg(t, 13, at, answerAccepted, "ZP1"),
		msg(t, 14, at, answerAccepted, "ZP9"),   // not in the run
		msg(t, 15, at, "ABSCHLUSS_ECON", "ZP1"), // another process
		msg(t, 21, at, "ANFORDERUNG_PT", "ZP3"), // the single request keeps its messages
		msg(t, 22, at, answerRejected, "ZP3"),
		msg(t, 23, at, answerRejected, "ZP2"),
		msg(t, 24, at, answerAccepted, "ZP1", "ZP3"), // one of its metering points is the single request's
	)
	assert.Equal(t, map[int64]bool{11: true, 13: true, 23: true}, hidden)
	assert.Equal(t, map[int64]int64{10: 23}, latest)
}

// A single request and a run compete for a metering point: answers carry no period, so its messages stay (review
// finding: the summary may count the answer for the single request).
func TestCollapser_aSingleRequestCompetingForAMeteringPoint(t *testing.T) {
	runs := []Row{
		row(t, 10, "RC100100", RowTypeRun, singleRun("ZP1")),
		row(t, 20, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
		row(t, 30, "RC100100", RowTypeRun, singleRun("ZP2")),
	}
	at := created.Add(time.Minute)
	hidden, _ := collapse(t, runs, nil,
		msg(t, 11, at, answerAccepted, "ZP1"), // only the single request before it
		msg(t, 21, at, answerAccepted, "ZP1"), // single request and run
		msg(t, 22, at, answerAccepted, "ZP2"), // only the run
		msg(t, 31, at, answerAccepted, "ZP2"), // run and later single request
	)
	assert.Equal(t, map[int64]bool{22: true}, hidden)
}

// A run closed before the message cannot own it: a later run of the same metering points (Smart re-request) does.
func TestCollapser_aRunClosedBeforeTheMessage(t *testing.T) {
	first, second := testRun("ZP1", "ZP2"), testRun("ZP1", "ZP2")
	runs := []Row{
		row(t, 10, "RC100100", RowTypeRun, first),
		row(t, 30, "RC100100", RowTypeRun, second),
	}
	at := created.Add(time.Minute)
	hidden, latest := collapse(t, runs, []Row{summaryRow(t, 20, first)},
		msg(t, 11, at, answerAccepted, "ZP1"), // before the first summary
		msg(t, 25, at, answerAccepted, "ZP2"), // after it, before the second run: the summary does not count it
		msg(t, 31, at, "ANFORDERUNG_PT", "ZP1"),
	)
	assert.Equal(t, map[int64]bool{11: true, 31: true}, hidden)
	assert.Equal(t, map[int64]int64{10: 11, 30: 31}, latest)
}

// Without a summary after Deadline (Check could not close the run), its messages come back.
func TestCollapser_noSummaryAfterTheDeadline(t *testing.T) {
	runs := []Row{row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2"))}
	c := NewCollapser("RC100100", created.Add(Deadline))
	c.AddRuns(runs)
	hidden, _ := c.Hide([]Message{msg(t, 11, created.Add(time.Minute), answerAccepted, "ZP1")})
	assert.Empty(t, hidden)
}

func TestCollapser_onlyWithinTheWindowAfterTheRun(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	runs := []Row{row(t, 10, "RC100100", RowTypeRun, run)}
	c := NewCollapser("RC100100", created.Add(2*CollapseWindow))
	c.AddRuns(runs)
	c.AddSummaries([]Row{summaryRow(t, 20, run)})
	hidden, _ := c.Hide([]Message{
		msg(t, 11, created.Add(CollapseWindow), answerAccepted, "ZP1"),
		msg(t, 12, created.Add(CollapseWindow+time.Minute), answerAccepted, "ZP2"),
	})
	assert.Equal(t, map[int64]bool{11: true}, hidden)
}

// GEA: the messages of another tenant's run stay in this tenant's list (that tenant has the live entry and summary).
func TestCollapser_runOfAnotherTenantOfTheCommunity(t *testing.T) {
	runs := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
		row(t, 20, "RC200200", RowTypeRun, testRun("ZP1", "ZP2")),
	}
	at := created.Add(time.Minute)
	hidden, _ := collapse(t, runs, nil,
		msg(t, 11, at, "ANFORDERUNG_PT", "ZP1"),
		msg(t, 21, at, "ANFORDERUNG_PT", "ZP1"),
		msg(t, 22, at, answerAccepted, "ZP2"),
	)
	assert.Equal(t, map[int64]bool{11: true}, hidden)
}

// A run of another tenant closed before the message no longer competes (GEA, review finding).
func TestCollapser_foreignRunClosedBeforeTheMessage(t *testing.T) {
	foreign := testRun("ZP1", "ZP2")
	runs := []Row{
		row(t, 10, "RC200200", RowTypeRun, foreign),
		row(t, 30, "RC100100", RowTypeRun, testRun("ZP1", "ZP2")),
	}
	at := created.Add(time.Minute)
	hidden, _ := collapse(t, runs, []Row{summaryRow(t, 20, foreign)},
		msg(t, 11, at, "ANFORDERUNG_PT", "ZP1"), // the foreign run's: kept
		msg(t, 31, at, "ANFORDERUNG_PT", "ZP1"), // the own run's, the foreign one is closed
	)
	assert.Equal(t, map[int64]bool{31: true}, hidden)
}

func TestCollapser_addRunsAndSummariesTwice(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	c := NewCollapser("RC100100", created.Add(time.Hour))
	r := row(t, 10, "RC100100", RowTypeRun, run)
	c.AddRuns([]Row{r})
	c.AddRuns([]Row{r, answerRow(t, 11, "RC100100", answerAccepted, "ZP1")}) // not a run: ignored
	c.AddSummaries([]Row{r, summaryRow(t, 20, run)})                         // the run row is not a summary
	hidden, _ := c.Hide([]Message{
		msg(t, 12, created.Add(time.Minute), answerAccepted, "ZP1"),
		msg(t, 21, created.Add(time.Minute), answerAccepted, "ZP2"),
	})
	assert.Equal(t, map[int64]bool{12: true}, hidden)
}

func TestCollapsible(t *testing.T) {
	assert.True(t, Collapsible(msg(t, 1, created, "ANFORDERUNG_PT", "ZP1").Content))
	assert.True(t, Collapsible(msg(t, 1, created, answerRejected, "ZP1").Content))
	assert.False(t, Collapsible(msg(t, 1, created, SummaryType).Content))
	assert.False(t, Collapsible(json.RawMessage(`not json`)))
}
