package database

import (
	"context"
	"testing"

	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The same metering point is assigned in two communities (e.g. after a move). AUFHEBUNG_CCMI/CCMC
// carry no community id: before the fix both rows matched ("Meteringpoint … is not unique") and
// the revocation was dropped silently.
func TestMeteringPointRevokeByConsentId_sameMeterInTwoTenants(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const meterA = "AT0030000000000000000000000111001"
	const meterB = "AT0030000000000000000000000111002"
	const meterC = "AT0030000000000000000000000111003"
	setupRc := func(tenant, rcNumber, communityId string, meters ...string) {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'REVOKE-TEST', 'Revoke-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3,
			 'Weg', '1', 'Linz', '4020', 'revoke-test@example.org')
			ON CONFLICT (tenant) DO NOTHING`, tenant, rcNumber, communityId)
		require.NoError(t, err)
		var rows [][]interface{}
		for _, m := range meters {
			rows = append(rows, []interface{}{"AT003000", communityId, "4020", "Linz", "Weg", "1",
				m, "CONSUMPTION", "Rev", "Oke" + tenant, "privat", "", "", "", "ACTIVE", "", ""})
		}
		f := buildImportSheet(t, rows)
		buf, err := f.WriteToBuffer()
		require.NoError(t, err)
		require.NoError(t, db.ImportMasterdataFromExcel(ctx, buf, "test.xlsx", "EEG Stammdaten", tenant))
	}
	setup := func(tenant, communityId string, meters ...string) { setupRc(tenant, tenant, communityId, meters...) }
	// old community without consent id, new community with consent id
	setup("TE000011", "AT00300000000TC000011000000000001", meterA, meterB, meterC)
	setup("TE000012", "AT00300000000TC000012000000000001", meterA, meterB)
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET consent_id = 'CONSENT-A' WHERE tenant = 'TE000012' AND metering_point_id = $1`, meterA)
	require.NoError(t, err)

	end := civil.DateFor(2026, 10, 1)
	state := func(tenant, meter string) string {
		var s string
		require.NoError(t, testDB.DbInstance.Get(&s, `SELECT process_state FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter))
		return s
	}

	t.Run("exact consent id of another community: only without receiving RC number", func(t *testing.T) {
		// a receiving RC number without a row of the metering point: the consent id of another
		// community revokes nothing (decided 06.11.2026)
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area,
			gridoperator_code, gridoperator_name, "communityId", street, "streetNumber", city, zip, email)
			VALUES ('TE000014', 'REVOKE-TEST', 'Revoke-Test', 'TE000014', 'LOCAL', 'AT003000', 'Netz OÖ',
			'AT00300000000TC000014000000000001', 'Weg', '1', 'Linz', '4020', 'revoke-test@example.org')
			ON CONFLICT (tenant) DO NOTHING`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant = 'TE000014'`)
			require.NoError(t, err)
		})
		consent := "CONSENT-A"
		_, err = db.MeteringPointRevokeByConsentId(ctx, "TE000014", &consent, meterA, end)
		assert.ErrorContains(t, err, "not found")
		assert.Equal(t, "ACTIVE", state("TE000012", meterA))
		assert.Equal(t, "ACTIVE", state("TE000011", meterA))

		// without a receiving RC number the exact consent id wins, as before
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, meterA, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.Equal(t, []string{"TE000012"}, tenants)
		assert.Equal(t, "INACTIVE", state("TE000012", meterA))
		assert.Equal(t, "ACTIVE", state("TE000011", meterA))
	})

	t.Run("receiving tenant decides when no consent id is stored", func(t *testing.T) {
		consent := "CONSENT-UNKNOWN"
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "TE000011", &consent, meterB, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.Equal(t, []string{"TE000011"}, tenants)
		assert.Equal(t, "INACTIVE", state("TE000011", meterB))
		assert.Equal(t, "ACTIVE", state("TE000012", meterB))
	})

	t.Run("receiving tenant without row: another community is not revoked", func(t *testing.T) {
		consent := "CONSENT-UNKNOWN"
		_, err := db.MeteringPointRevokeByConsentId(ctx, "TE000012", &consent, meterC, end)
		assert.ErrorContains(t, err, "not found")
		assert.Equal(t, "ACTIVE", state("TE000011", meterC))

		// without a receiving tenant the only assigned row is still found
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, meterC, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.Equal(t, []string{"TE000011"}, tenants)
		assert.Equal(t, "INACTIVE", state("TE000011", meterC))
	})

	t.Run("still ambiguous without tenant and consent match: nothing changes", func(t *testing.T) {
		consent := "CONSENT-UNKNOWN"
		_, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, meterB, end)
		assert.ErrorContains(t, err, "not unique")
		assert.Equal(t, "ACTIVE", state("TE000012", meterB))
	})
	// GEA: one RC number owns several tenants; the MQTT topic carries the RC number
	const meterG = "AT0030000000000000000000000111004"
	const meterM = "AT0030000000000000000000000111005"
	setupRc("GC000020-001", "GC000020", "AT00300000000TC000020000000000001", meterG, meterM)
	setupRc("GC000020-002", "GC000020", "AT00300000000TC000020000000000002", meterG)
	setup("TE000013", "AT00300000000TC000013000000000001", meterG)

	t.Run("GEA: all tenants of the receiving RC number are revoked", func(t *testing.T) {
		consent := "CONSENT-UNKNOWN"
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "GC000020", &consent, meterG, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"GC000020-001", "GC000020-002"}, tenants)
		for _, r := range revoked {
			assert.True(t, r.WasActive, "%s was active before (platform#116)", r.Tenant)
		}
		assert.Equal(t, "INACTIVE", state("GC000020-001", meterG))
		assert.Equal(t, "INACTIVE", state("GC000020-002", meterG))
		assert.Equal(t, "ACTIVE", state("TE000013", meterG))
	})

	t.Run("GEA: exact consent id in several tenants of one RC number", func(t *testing.T) {
		_, err := testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET consent_id = 'CONSENT-G', process_state = 'ACTIVE', status = 'ACTIVE'
			WHERE tenant IN ('GC000020-001', 'GC000020-002') AND metering_point_id = $1`, meterG)
		require.NoError(t, err)
		consent := "CONSENT-G"
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, meterG, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"GC000020-001", "GC000020-002"}, tenants)
	})

	t.Run("migration placeholder counts as no consent id", func(t *testing.T) {
		_, err := testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET consent_id = 'Migration'
			WHERE tenant = 'GC000020-001' AND metering_point_id = $1`, meterM)
		require.NoError(t, err)
		consent := "CONSENT-UNKNOWN"
		revoked, err := db.MeteringPointRevokeByConsentId(ctx, "GC000020", &consent, meterM, end)
		tenants := TenantNames(revoked)
		require.NoError(t, err)
		assert.Equal(t, []string{"GC000020-001"}, tenants)
		assert.Equal(t, "INACTIVE", state("GC000020-001", meterM))
	})
}
