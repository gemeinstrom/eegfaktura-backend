package energyrequest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	aprFrom, aprTo = dayStart(2026, 4, 1), dayEnd(2026, 4, 30)
	mayFrom, mayTo = dayStart(2026, 5, 1), dayEnd(2026, 5, 31)
)

func periodRun(created time.Time, from, to int64, meters ...string) Run {
	r := NewRun("AT00000000000000000000RC100100", from, to)
	for _, m := range meters {
		r.Add(m, from, to)
	}
	r.CreatedAt = created.UnixMilli()
	return *r
}

// platform#173 attributed by metering point only: a later run for another period took every answer and marked the
// earlier run as requested again. Periods that share no day are independent requests.
func TestAttribute_runsForOtherPeriodsAreIndependent(t *testing.T) {
	for _, tt := range []struct {
		name          string
		first, second [2]int64
	}{
		{name: "May, then April", first: [2]int64{mayFrom, mayTo}, second: [2]int64{aprFrom, aprTo}},
		{name: "April, then May", first: [2]int64{aprFrom, aprTo}, second: [2]int64{mayFrom, mayTo}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			first := periodRun(created, tt.first[0], tt.first[1], "ZP1", "ZP2")
			second := periodRun(created.Add(time.Minute), tt.second[0], tt.second[1], "ZP1", "ZP2")
			rows := []Row{
				row(t, 10, "RC100100", RowTypeRun, first),
				row(t, 20, "RC100100", RowTypeRun, second),
				answerRow(t, 30, "RC100100", answerAccepted, "ZP1"),
				answerRow(t, 31, "RC100100", answerAccepted, "ZP2"),
				answerRow(t, 40, "RC100100", answerRejected, "ZP1", "86"),
			}
			s, done := Evaluate(first, 10, rows, tenants, created.Add(time.Hour))
			assert.True(t, done)
			assert.Empty(t, s.Superseded, "a run for another period does not request again")
			assert.Len(t, s.Accepted, 2, "the first answers go to the older run")

			s, done = Evaluate(second, 20, rows, tenants, created.Add(time.Hour))
			assert.False(t, done)
			assert.Empty(t, s.Superseded)
			assert.Equal(t, []Rejected{{MeteringPoint: "ZP1", Codes: []string{"86"}}}, s.Rejected)
			assert.Equal(t, []string{"ZP2"}, s.NoAnswer)
		})
	}
}

func TestAttribute_overlappingRunSupersedes(t *testing.T) {
	first := periodRun(created, mayFrom, mayTo, "ZP1")
	second := periodRun(created.Add(time.Minute), dayStart(2026, 5, 31), dayEnd(2026, 6, 30), "ZP1")
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, first),
		row(t, 20, "RC100100", RowTypeRun, second),
		answerRow(t, 30, "RC100100", answerAccepted, "ZP1"),
	}
	s, _ := Evaluate(first, 10, rows, tenants, created)
	assert.Equal(t, []string{"ZP1"}, s.Superseded, "31.05. is requested again")
	s, _ = Evaluate(second, 20, rows, tenants, created)
	assert.Len(t, s.Accepted, 1)
}

func TestAttribute_closedRunTakesNoLaterAnswer(t *testing.T) {
	first := periodRun(created, mayFrom, mayTo, "ZP1", "ZP2")
	second := periodRun(created.Add(time.Minute), aprFrom, aprTo, "ZP1", "ZP2")
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, first),
		row(t, 20, "RC100100", RowTypeRun, second),
		row(t, 25, "RC100100", messageRow, Summary{Type: SummaryType, RunId: first.RunId}),
		answerRow(t, 30, "RC100100", answerAccepted, "ZP1"),
	}
	s, _ := Evaluate(second, 20, rows, tenants, created)
	assert.Len(t, s.Accepted, 1)
}

// a rejection frees the period it belongs to: the rejected May request, not the April one
func TestLocks_rejectionFreesItsPeriod(t *testing.T) {
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, periodRun(created, mayFrom, mayTo, "ZP1", "ZP2")),
		row(t, 20, "RC100100", RowTypeRun, periodRun(created.Add(time.Minute), aprFrom, aprTo, "ZP1", "ZP2")),
		answerRow(t, 30, "RC100100", answerRejected, "ZP1", "86"),
	}
	locks := NewLocks(rows, "RC100100", tenants, created.Add(10*time.Minute))
	assert.Zero(t, locks.Until("ZP1", mayFrom, mayTo), "May was rejected")
	assert.Equal(t, created.Add(time.Minute+LockWindow).UnixMilli(), locks.Until("ZP1", aprFrom, aprTo))
	assert.NotZero(t, locks.Until("ZP2", mayFrom, mayTo))
}

// the lock reads the rows of LookBack, not only of LockWindow: a late answer to an older run of another period is
// attributed to that run and does not free the recent one
func TestLocks_lateRejectionOfAnOlderRun(t *testing.T) {
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, periodRun(created.Add(-3*time.Hour), mayFrom, mayTo, "ZP1")),
		row(t, 20, "RC100100", RowTypeRun, periodRun(created.Add(-30*time.Minute), aprFrom, aprTo, "ZP1")),
		answerRow(t, 30, "RC100100", answerRejected, "ZP1", "86"),
	}
	locks := NewLocks(rows, "RC100100", tenants, created)
	assert.Equal(t, created.Add(30*time.Minute).UnixMilli(), locks.Until("ZP1", aprFrom, aprTo))
	assert.Zero(t, locks.Until("ZP1", mayFrom, mayTo), "the May run is older than the lock window")
}
