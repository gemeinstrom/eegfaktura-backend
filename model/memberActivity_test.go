package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemberActivity(t *testing.T) {
	mp := func(s StatusType, p ProcessStatusType) *MeteringPoint {
		return &MeteringPoint{Status: s, ProcessState: p}
	}

	assert.Equal(t, MemberActive, MemberActivity([]*MeteringPoint{mp(S_INACTIVE, INACTIVE), mp(S_ACTIVE, ACTIVE)}))
	// a revocation is requested but not confirmed yet: still active
	assert.Equal(t, MemberActive, MemberActivity([]*MeteringPoint{mp(S_ACTIVE, REVOKED)}))
	// left: all metering points inactive
	assert.Equal(t, MemberInactive, MemberActivity([]*MeteringPoint{mp(S_INACTIVE, INACTIVE), mp(S_INACTIVE, REVOKED)}))
	// only rejected counts as inactive (decision 20.10.2026)
	assert.Equal(t, MemberInactive, MemberActivity([]*MeteringPoint{mp(S_INIT, REJECTED), mp(S_INIT, INVALID)}))
	// being registered, or no metering point: pending
	assert.Equal(t, MemberPending, MemberActivity([]*MeteringPoint{mp(S_INACTIVE, INACTIVE), mp(S_INIT, PENDING)}))
	assert.Equal(t, MemberPending, MemberActivity([]*MeteringPoint{mp(S_INIT, NEW)}))
	assert.Equal(t, MemberPending, MemberActivity(nil))
	// INACTIVE with a registration state (stale in prod, or a re-registration): inactive until active again
	assert.Equal(t, MemberInactive, MemberActivity([]*MeteringPoint{mp(S_INACTIVE, PENDING)}))
	// no status at all (occurs once in prod) with a registration state: pending
	assert.Equal(t, MemberPending, MemberActivity([]*MeteringPoint{mp("", PENDING)}))
}
