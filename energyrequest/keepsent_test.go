package energyrequest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeStore records the changes KeepSent makes to a run row.
type fakeStore struct {
	updated *Run
	deleted bool
}

func (f *fakeStore) SaveEnergyRequestRow(context.Context, string, string, interface{}) (int64, error) {
	return 1, nil
}
func (f *fakeStore) UpdateEnergyRequestRow(_ context.Context, _ int64, v interface{}) error {
	f.updated = v.(*Run)
	return nil
}
func (f *fakeStore) DeleteEnergyRequestRow(context.Context, int64) error {
	f.deleted = true
	return nil
}
func (f *fakeStore) EnergyRequestRows(context.Context, []string) ([]Row, error) { return nil, nil }
func (f *fakeStore) TenantsByEcId(context.Context, string) ([]string, error)    { return nil, nil }
func (f *fakeStore) CloseEnergyRequest(context.Context, string, int64, Summary) (bool, error) {
	return false, nil
}

func TestKeepSent_failedParticipationIsNotClaimed(t *testing.T) {
	jan, mar := dayStart(2026, 1, 1), dayEnd(2026, 3, 31)
	apr, jun := dayStart(2026, 4, 1), dayEnd(2026, 6, 30)
	r := NewRun("EC", jan, jun)
	r.Add("ZP1", jan, mar) // two participations of ZP1: merged to Jan-Jun
	r.Add("ZP1", apr, jun)
	r.Add("ZP2", jan, jun)
	r.Add("ZP3", jan, jun)
	r.FitPeriod()
	store := &fakeStore{}

	// the April-June participation of ZP1 and ZP3 failed
	KeepSent(context.Background(), store, 1, r, []MeterRange{
		{MeteringPoint: "ZP1", From: jan, To: mar},
		{MeteringPoint: "ZP2", From: jan, To: jun},
	})
	assert.Equal(t, []MeterRange{{MeteringPoint: "ZP1", From: jan, To: mar}, {MeteringPoint: "ZP2", From: jan, To: jun}},
		store.updated.Meters)
	assert.False(t, store.deleted)
}

// a run with periods per metering point keeps the span of what was sent as its period
func TestKeepSent_refitsThePeriod(t *testing.T) {
	r := NewRun("EC", 0, 0)
	r.Add("ZP1", dayStart(2026, 1, 1), dayEnd(2026, 1, 31))
	r.Add("ZP2", dayStart(2026, 3, 1), dayEnd(2026, 3, 31))
	r.FitPeriod()
	store := &fakeStore{}
	KeepSent(context.Background(), store, 1, r, []MeterRange{{MeteringPoint: "ZP1", From: dayStart(2026, 1, 1), To: dayEnd(2026, 1, 31)}})
	assert.Equal(t, dayStart(2026, 1, 1), store.updated.From)
	assert.Equal(t, dayEnd(2026, 1, 31), store.updated.To)

	// a run with the request period keeps it
	r = NewRun("EC", sepFrom, sepTo)
	r.Add("ZP1", sepFrom, sepFrom+1)
	r.Add("ZP2", sepFrom, sepTo)
	KeepSent(context.Background(), store, 1, r, []MeterRange{{MeteringPoint: "ZP1", From: sepFrom, To: sepFrom + 1}})
	assert.Equal(t, sepTo, store.updated.To)
}

func TestKeepSent_nothingSentDeletesTheRun(t *testing.T) {
	r := NewRun("EC", sepFrom, sepTo)
	r.Add("ZP1", sepFrom, sepTo)
	store := &fakeStore{}
	KeepSent(context.Background(), store, 1, r, nil)
	assert.True(t, store.deleted)
	assert.Nil(t, store.updated)
}
