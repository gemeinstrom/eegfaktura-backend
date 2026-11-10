package energyrequest

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	sepFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	sepTo   = time.Date(2026, 9, 30, 23, 45, 0, 0, time.Local).UnixMilli()
	created = time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local)
	tenants = map[string]bool{"RC100100": true}
)

func row(t *testing.T, id int64, tenant, rowType string, v interface{}) Row {
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return Row{Id: id, Tenant: tenant, Type: rowType, Message: b}
}

func testRun(meters ...string) Run {
	r := NewRun("AT00000000000000000000RC100100", sepFrom, sepTo)
	for _, m := range meters {
		r.Add(m, sepFrom, sepTo)
	}
	r.CreatedAt = created.UnixMilli()
	return *r
}

func answerRow(t *testing.T, id int64, tenant, typ, meter string, codes ...string) Row {
	return row(t, id, tenant, messageRow, map[string]interface{}{"type": typ, "meteringPoints": []string{meter}, "responseCodes": codes})
}

func TestEvaluate_allStates(t *testing.T) {
	run := testRun("ZP1", "ZP2", "ZP3")
	run.Skipped = []Skipped{{MeteringPoint: "ZP4", Reason: ReasonNotActiveInPeriod}}
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, run),
		answerRow(t, 11, "RC100100", answerAccepted, "ZP1"),
		answerRow(t, 12, "RC100100", answerRejected, "ZP2", "Zählpunkt nicht gefunden"),
		// ZP3: no answer
	}

	s, done := Evaluate(run, 10, withRun(rows, run, 10), tenants, created.Add(time.Hour))
	assert.False(t, done, "a missing answer keeps the run open")
	assert.Equal(t, 3, s.Requested)
	assert.Equal(t, []MeterRange{{MeteringPoint: "ZP1", From: sepFrom, To: sepTo}}, s.Accepted)
	assert.Equal(t, []Rejected{{MeteringPoint: "ZP2", Codes: []string{"Zählpunkt nicht gefunden"}}}, s.Rejected)
	assert.Equal(t, []string{"ZP3"}, s.NoAnswer)
	assert.Equal(t, run.Skipped, s.Skipped)

	_, done = Evaluate(run, 10, withRun(rows, run, 10), tenants, created.Add(Deadline))
	assert.True(t, done, "closed at the deadline")

	rows = append(rows, answerRow(t, 13, "RC100100", answerAccepted, "ZP3"))
	_, done = Evaluate(run, 10, withRun(rows, run, 10), tenants, created.Add(time.Hour))
	assert.True(t, done, "closed as soon as every metering point has an answer")
}

func TestEvaluate_rowsBeforeTheRunAndOfOtherTenantsDoNotCount(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	rows := []Row{
		answerRow(t, 9, "RC100100", answerRejected, "ZP1", "56"),  // before the run
		answerRow(t, 11, "RC999999", answerRejected, "ZP2", "56"), // other tenant
	}
	s, _ := Evaluate(run, 10, withRun(rows, run, 10), tenants, created)
	assert.Equal(t, []string{"ZP1", "ZP2"}, s.NoAnswer)
}

func TestEvaluate_gemeinschaftWithSeveralTenants(t *testing.T) {
	// a GEA: the answer was stored under another tenant of the same community id
	run := testRun("ZP1", "ZP2")
	rows := []Row{row(t, 10, "GC100019-001", RowTypeRun, run), answerRow(t, 11, "GC100019-002", answerRejected, "ZP1", "56")}
	s, _ := Evaluate(run, 10, rows, map[string]bool{"GC100019-001": true, "GC100019-002": true}, created)
	assert.Len(t, s.Rejected, 1)
}

func TestEvaluate_laterRunSupersedes(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	later := testRun("ZP2", "ZP3")
	rows := []Row{
		row(t, 20, "RC100100", RowTypeRun, later),
		answerRow(t, 21, "RC100100", answerRejected, "ZP2", "56"), // belongs to the later run
	}
	s, _ := Evaluate(run, 10, withRun(rows, run, 10), tenants, created)
	assert.Equal(t, []string{"ZP2"}, s.Superseded)
	assert.Equal(t, []string{"ZP1"}, s.NoAnswer)
	assert.Empty(t, s.Rejected)

	s, _ = Evaluate(later, 20, withRun(rows, later, 20), tenants, created)
	assert.Len(t, s.Rejected, 1)
}

func TestEvaluate_answerBeforeALaterRunStays(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	later := testRun("ZP1", "ZP3")
	rows := []Row{
		answerRow(t, 11, "RC100100", answerAccepted, "ZP1"), // before the later run: belongs to this run
		row(t, 20, "RC100100", RowTypeRun, later),
		answerRow(t, 21, "RC100100", answerRejected, "ZP1", "86"), // belongs to the later run
	}
	s, _ := Evaluate(run, 10, withRun(rows, run, 10), tenants, created)
	assert.Equal(t, []MeterRange{{MeteringPoint: "ZP1", From: sepFrom, To: sepTo}}, s.Accepted)
	assert.Empty(t, s.Superseded)
	assert.Empty(t, s.Rejected)
}

func TestEvaluate_singleMeterRequestTakesItsAnswer(t *testing.T) {
	// a later request for one metering point has no summary, but its answer is not counted for this run
	run := testRun("ZP1", "ZP2")
	single := testRun("ZP2")
	single.NoSummary = true
	rows := []Row{
		row(t, 20, "RC100100", RowTypeRun, single),
		answerRow(t, 21, "RC100100", answerRejected, "ZP2", "86"),
	}
	s, _ := Evaluate(run, 10, withRun(rows, run, 10), tenants, created)
	assert.Equal(t, []string{"ZP2"}, s.Superseded)
	assert.Empty(t, s.Rejected)
}

func TestEvaluate_latestAnswerWins(t *testing.T) {
	run := testRun("ZP1", "ZP2")
	rows := []Row{
		answerRow(t, 11, "RC100100", answerAccepted, "ZP1"),
		answerRow(t, 12, "RC100100", answerRejected, "ZP1", "86"),
	}
	s, _ := Evaluate(run, 10, withRun(rows, run, 10), tenants, created)
	assert.Len(t, s.Rejected, 1)
	assert.Empty(t, s.Accepted)
}

func TestRun_AddAndSkip(t *testing.T) {
	r := NewRun("EC", sepFrom, sepTo)
	r.Skip("ZP1", ReasonNotAssigned) // an old participation of ZP1
	r.Add("ZP1", sepFrom, sepFrom+1000)
	r.Add("ZP1", sepFrom+500, sepTo) // a second participation: widest period
	r.Skip("ZP1", ReasonNotAssigned)
	r.Skip("ZP2", ReasonNotRegistered)
	r.Skip("ZP2", ReasonNotAssigned)
	// an ended participation first, then the assigned one without an inactive date: its reason counts
	r.Skip("ZP3", ReasonNotAssigned)
	r.Skip("ZP3", ReasonNoActivePeriod)

	assert.Equal(t, []MeterRange{{MeteringPoint: "ZP1", From: sepFrom, To: sepTo}}, r.Meters)
	assert.Equal(t, []Skipped{{MeteringPoint: "ZP2", Reason: ReasonNotRegistered},
		{MeteringPoint: "ZP3", Reason: ReasonNoActivePeriod}}, r.Skipped)
}

// owner change: the old member's entry outside its period gives way to the new member's reason, an archived entry
// to both, in either order; NOT_FOUND never replaces a reason
func TestRun_SkipReasonOfSeveralParticipations(t *testing.T) {
	r := NewRun("EC", sepFrom, sepTo)
	r.Skip("ZP1", ReasonNotActiveInPeriod)
	r.SkipUntil("ZP1", ReasonAlreadyRequested, 42)
	r.SkipUntil("ZP2", ReasonAlreadyRequested, 42)
	r.Skip("ZP2", ReasonNotActiveInPeriod)
	r.Skip("ZP3", ReasonNotActiveInPeriod)
	r.Skip("ZP3", ReasonNotAssigned)
	r.Skip("ZP4", ReasonNotAssigned)
	r.Skip("ZP4", ReasonNotActiveInPeriod)
	r.Skip("ZP4", ReasonNotFound)
	assert.Equal(t, []Skipped{{MeteringPoint: "ZP1", Reason: ReasonAlreadyRequested, Until: 42},
		{MeteringPoint: "ZP2", Reason: ReasonAlreadyRequested, Until: 42},
		{MeteringPoint: "ZP3", Reason: ReasonNotActiveInPeriod},
		{MeteringPoint: "ZP4", Reason: ReasonNotActiveInPeriod}}, r.Skipped)
}

// the reason of several participations does not depend on their order; locked twice: the later until counts
func TestRun_SkipReasonIndependentOfOrder(t *testing.T) {
	reasons := []struct {
		reason string
		until  int64
	}{{ReasonAlreadyRequested, 42}, {ReasonNotRegistered, 0}, {ReasonNoActivePeriod, 0},
		{ReasonAlreadyRequested, 99}, {ReasonNotActiveInPeriod, 0}, {ReasonNotAssigned, 0}}
	for _, order := range [][]int{{0, 1, 2, 3, 4, 5}, {5, 4, 3, 2, 1, 0}, {2, 3, 1, 0, 5, 4}} {
		r := NewRun("EC", sepFrom, sepTo)
		for _, i := range order {
			r.SkipUntil("ZP1", reasons[i].reason, reasons[i].until)
		}
		assert.Equal(t, []Skipped{{MeteringPoint: "ZP1", Reason: ReasonAlreadyRequested, Until: 99}}, r.Skipped, "order %v", order)
	}
	r := NewRun("EC", sepFrom, sepTo)
	r.Skip("ZP1", ReasonNoActivePeriod)
	r.Skip("ZP1", ReasonNotRegistered)
	assert.Equal(t, ReasonNotRegistered, r.Skipped[0].Reason)
}

// an unknown reason never replaces a known one, and a known one replaces it
func TestRun_SkipUnknownReasonIsWeakest(t *testing.T) {
	r := NewRun("EC", sepFrom, sepTo)
	r.Skip("ZP1", ReasonNotAssigned)
	r.Skip("ZP1", "NOT_A_REASON")
	r.Skip("ZP2", "NOT_A_REASON")
	r.Skip("ZP2", ReasonNotAssigned)
	assert.Equal(t, []Skipped{{MeteringPoint: "ZP1", Reason: ReasonNotAssigned},
		{MeteringPoint: "ZP2", Reason: ReasonNotAssigned}}, r.Skipped)
}

// withRun returns rows with the run in row runRowId, added in id order when rows lack it (Evaluate needs the run row).
func withRun(rows []Row, run Run, runRowId int64) []Row {
	i := sort.Search(len(rows), func(i int) bool { return rows[i].Id >= runRowId })
	if i < len(rows) && rows[i].Id == runRowId {
		return rows
	}
	msg, err := json.Marshal(run)
	if err != nil {
		return rows
	}
	out := make([]Row, 0, len(rows)+1)
	out = append(append(append(out, rows[:i]...), Row{Id: runRowId, Tenant: "RC100100", Type: RowTypeRun, Message: msg}), rows[i:]...)
	return out
}
