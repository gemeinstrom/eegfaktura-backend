package database

import (
	"context"
	"testing"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261005-ca4: the tenant check must refuse a participant id that does not exist
// instead of letting the call through.
func TestParticipantTenantCheckRefusesUnknownId(t *testing.T) {
	db, err := GetTestDB(context.Background(), testDB)
	require.NoError(t, err)
	ctx := context.Background()
	unknown := uuid.NewRandom().String()

	assert.Error(t, assertParticipantTenant(ctx, testDB.DbInstance, "TE000001", unknown))
	assert.Error(t, db.ConfirmParticipant(ctx, "TE000001", "test", unknown))
	assert.Error(t, db.DeleteParticipant(ctx, "TE000001", unknown))
	assert.Error(t, db.UpdateParticipantPartial(ctx, "TE000001", unknown, "contact.phone", "0000"))
}
