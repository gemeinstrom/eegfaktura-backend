package database

import (
	"context"
	"sync"
	"testing"

	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#116: MQTT handlers run in parallel. Two revocations of the same active metering point
// at the same time must report WasActive only once, otherwise the member gets two mails.
func TestMeteringPointRevokeByConsentId_concurrentWasActiveOnce(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant, meter = "TE000016", "AT0030000000000000000000000116901"
	exec := func(q string, args ...interface{}) {
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ('b1160000-0000-0000-0000-000000000901', $1, 'Par', 'Allel', 'ACTIVE', 'test', 'test')`, tenant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, consent_id)
		VALUES ($1, 'b1160000-0000-0000-0000-000000000901', $2, 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test',
		'2025-01-01', '2025-01-01', '2999-12-31', 'C-PARALLEL')`, meter, tenant)
	t.Cleanup(func() {
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.participant WHERE id = 'b1160000-0000-0000-0000-000000000901'`)
	})

	consent := "C-PARALLEL"
	end := civil.DateFor(2026, 10, 1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			revoked, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, meter, end)
			if err != nil {
				return
			}
			for _, r := range revoked {
				if r.WasActive {
					mu.Lock()
					active++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, active)
}
