package energyrequest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func dayStart(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local).UnixMilli()
}

func dayEnd(y int, m time.Month, d int) int64 {
	// the handler cuts the end of a period to 23:45 (util.TruncateToEndOfDay)
	return time.Date(y, m, d, 23, 45, 0, 0, time.Local).UnixMilli()
}

func TestLocks_overlapIsASharedDay(t *testing.T) {
	rows := []Row{row(t, 10, "RC100100", RowTypeRun, testRun("ZP1"))}
	locks := NewLocks(rows, "RC100100", tenants, created.Add(10*time.Minute))
	until := created.Add(LockWindow).UnixMilli()

	tests := []struct {
		name     string
		from, to int64
		want     int64
	}{
		{"same period", sepFrom, sepTo, until},
		{"last day shared", dayStart(2026, 9, 30), dayEnd(2026, 10, 31), until},
		{"first day shared", dayStart(2026, 8, 1), dayStart(2026, 9, 1), until},
		{"inside", dayStart(2026, 9, 10), dayEnd(2026, 9, 10), until},
		{"next month", dayStart(2026, 10, 1), dayEnd(2026, 10, 31), 0},
		{"month before", dayStart(2026, 8, 1), dayEnd(2026, 8, 31), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, locks.Until("ZP1", tt.from, tt.to))
		})
	}
	assert.Zero(t, locks.Until("ZP2", sepFrom, sepTo), "a metering point not in the run is free")
}

func TestLocks_rejectedIsFree(t *testing.T) {
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1", "ZP2", "ZP3")),
		answerRow(t, 11, "RC100100", answerRejected, "ZP1", "86"),
		answerRow(t, 12, "RC100100", answerAccepted, "ZP2"),
		answerRow(t, 13, "RC100100", answerAccepted, "ZP3"),
		answerRow(t, 14, "RC100100", answerRejected, "ZP3", "86"), // the latest answer counts
	}
	locks := NewLocks(rows, "RC100100", tenants, created.Add(time.Minute))
	assert.Zero(t, locks.Until("ZP1", sepFrom, sepTo))
	assert.NotZero(t, locks.Until("ZP2", sepFrom, sepTo), "accepted stays locked")
	assert.Zero(t, locks.Until("ZP3", sepFrom, sepTo))
}

func TestLocks_rejectionUnderAnotherTenantOfTheCommunity(t *testing.T) {
	gea := map[string]bool{"RC100100": true, "RC100101": true}
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1")),
		answerRow(t, 11, "RC100101", answerRejected, "ZP1", "86"),
	}
	assert.Zero(t, NewLocks(rows, "RC100100", gea, created).Until("ZP1", sepFrom, sepTo))
	assert.NotZero(t, NewLocks(rows, "RC100100", tenants, created).Until("ZP1", sepFrom, sepTo),
		"an answer of a tenant outside the community does not count")
}

func TestLocks_freeAfterTheWindow(t *testing.T) {
	rows := []Row{row(t, 10, "RC100100", RowTypeRun, testRun("ZP1"))}
	assert.Equal(t, created.Add(LockWindow).UnixMilli(),
		NewLocks(rows, "RC100100", tenants, created.Add(LockWindow-time.Millisecond)).Until("ZP1", sepFrom, sepTo))
	assert.Zero(t, NewLocks(rows, "RC100100", tenants, created.Add(LockWindow)).Until("ZP1", sepFrom, sepTo))
}

func TestLocks_otherTenantIsNotLocked(t *testing.T) {
	rows := []Row{row(t, 10, "RC100100", RowTypeRun, testRun("ZP1"))}
	assert.Zero(t, NewLocks(rows, "RC200200", tenants, created).Until("ZP1", sepFrom, sepTo))
	assert.NotZero(t, NewLocks(rows, "rc100100", tenants, created).Until("ZP1", sepFrom, sepTo),
		"the tenant is compared case-insensitively")
}

func TestLocks_singleRequestLocksToo(t *testing.T) {
	single := testRun("ZP1")
	single.NoSummary = true
	rows := []Row{row(t, 10, "RC100100", RowTypeRun, single)}
	assert.NotZero(t, NewLocks(rows, "RC100100", tenants, created).Until("ZP1", sepFrom, sepTo))
}

func TestLocks_latestRunGivesUntil(t *testing.T) {
	later := testRun("ZP1")
	later.CreatedAt = created.Add(20 * time.Minute).UnixMilli()
	rows := []Row{
		row(t, 10, "RC100100", RowTypeRun, testRun("ZP1")),
		row(t, 20, "RC100100", RowTypeRun, later),
	}
	assert.Equal(t, created.Add(20*time.Minute+LockWindow).UnixMilli(),
		NewLocks(rows, "RC100100", tenants, created.Add(30*time.Minute)).Until("ZP1", sepFrom, sepTo))
}

func TestRun_SkipUntil(t *testing.T) {
	r := NewRun("EC", sepFrom, sepTo)
	r.Skip("ZP1", ReasonNotAssigned) // an ended participation gives way
	r.SkipUntil("ZP1", ReasonAlreadyRequested, 42)
	r.Skip("ZP2", ReasonComplete)
	assert.Equal(t, []Skipped{{MeteringPoint: "ZP1", Reason: ReasonAlreadyRequested, Until: 42},
		{MeteringPoint: "ZP2", Reason: ReasonComplete}}, r.Skipped)
}

// Touch: the periods overlap or the second starts on the day after the first ends (summer time included)
func TestTouch(t *testing.T) {
	at := func(m time.Month, d, h, min int) int64 {
		return time.Date(2025, m, d, h, min, 0, 0, time.Local).UnixMilli()
	}
	assert.True(t, Touch(at(6, 15, 23, 45), at(6, 16, 0, 0)), "next day")
	assert.True(t, Touch(at(6, 15, 23, 45), at(6, 10, 0, 0)), "overlap")
	assert.False(t, Touch(at(6, 15, 23, 45), at(6, 17, 0, 0)), "a free day between")
	assert.True(t, Touch(at(3, 29, 23, 45), at(3, 30, 0, 0)), "summer time starts on the next day")
	assert.True(t, Touch(at(3, 30, 23, 45), at(3, 31, 0, 0)), "after the summer time day")
	assert.False(t, Touch(at(3, 29, 23, 45), at(3, 31, 0, 0)))
}
