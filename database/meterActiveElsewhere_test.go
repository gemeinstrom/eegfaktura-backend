package database

import (
	"errors"
	"fmt"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The metering point is read from the detail of the violated index; without it, the first one of the request.
func TestActiveAtOtherParticipantErr(t *testing.T) {
	violation := func(constraint, detail string) error {
		return &pq.Error{Code: "23505", Constraint: constraint, Detail: detail}
	}
	text := func(meter string) string { return fmt.Sprintf(model.MeterActiveAtOtherParticipantText, meter) }

	err := activeAtOtherParticipantErr(violation("idx_unique_meteringpoint_active",
		"Key (metering_point_id, tenant, flag)=(AT002, TE100100, 1) already exists."), []string{"AT001", "AT002"})
	var ve *model.VfeegError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, 1207, ve.Code)
	assert.Equal(t, text("AT002"), model.UserText(ve))

	err = activeAtOtherParticipantErr(violation("idx_unique_meteringpoint_active", ""), []string{"AT001"})
	assert.Equal(t, text("AT001"), model.UserText(err))

	assert.Nil(t, activeAtOtherParticipantErr(violation("meteringpointpk", ""), []string{"AT001"}))
	assert.Nil(t, activeAtOtherParticipantErr(errors.New("other"), []string{"AT001"}))
}
