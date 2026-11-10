package eda

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Re-review backend#107 (2): an active metering point stays ACTIVE only if ALL codes of the rejection
// concern the request (model.REJECTED_KEEP_ACTIVE_CODES). The stored code decides it in the DAO, so a
// code about the metering point itself wins, in either order. Only codes the old logic considered
// are candidates (INVALID: the REJECTED_INVALID_CODES intersection; REJECTED: all codes).
func TestRejectionCode(t *testing.T) {
	invalid := func(codes ...int16) int16 { return rejectionCode(intersectCodes(REJECTED_INVALID_CODES, codes)) }

	assert.Equal(t, int16(57), invalid(188, 57))
	assert.Equal(t, int16(57), invalid(57, 188))
	assert.Equal(t, int16(188), invalid(188))
	assert.Equal(t, int16(188), invalid(188, 173))
	assert.Equal(t, int16(56), invalid(56, 57), "first code as before")
	// 82 is no INVALID code: the known reason 172 stays stored (web shows it)
	assert.Equal(t, int16(172), invalid(172, 82))

	// REJECTED (no INVALID code): candidates are all codes
	assert.Equal(t, int16(99), rejectionCode([]int16{183, 99}))
	assert.Equal(t, int16(183), rejectionCode([]int16{183}))
}
