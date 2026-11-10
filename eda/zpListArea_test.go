package eda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"at.ourproject/vfeeg-backend/logging/logtest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#229 (decided 05.11.2026): ECType RC_L/RC_R of a metering point list sets LOCAL/REGIONAL at the tenants
// of the community id that have the other of the two, logged as eeg.area.updated_from_list; any other type stays
// as it is with a warning; another community is not touched.
func TestPodList_areaFromEcType(t *testing.T) {
	communities := map[string]struct{ rc, area string }{
		"TEAREA81": {"RC000081", "LOCAL"}, "TEAREA82": {"RC000082", "LOCAL"},
		"TEAREA83": {"GC000083", "GEA"}, "TEAREA84": {"PP000084", "P2P"},
	}
	tenants := []string{}
	for tenant, c := range communities {
		tenants = append(tenants, tenant)
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'Typ', 'Typ-Test', $2, $3, 'AT003000', 'Netz OÖ', $4,
			 'Weg', '1', 'Linz', '4020', 'typ@example.org')`, tenant, c.rc, c.area, ecIdOf(c.rc))
		require.NoError(t, err)
	}
	cleanupCommunities(t, tenants...)
	logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })

	podList := func(rc, ecType string, n int) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: rc}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s2026110500000000000000000%d",
			"sender":"AT003000","receiver":"%s","messageCode":"SENDEN_ECP","ecId":"%s","ecTypeEda":"%s","meterList":[]}`,
			rc, n, rc, ecIdOf(rc), ecType)), &msg.Payload))
		protocolEcPodListHandler(context.Background(), msg)
	}
	area := func(tenant string) string {
		var a string
		require.NoError(t, testDB.DbInstance.Get(&a, `SELECT area FROM base.eeg WHERE tenant = $1`, tenant))
		return a
	}

	podList("RC000081", "RC_R", 1)
	assert.Equal(t, "REGIONAL", area("TEAREA81"))
	assert.Equal(t, "LOCAL", area("TEAREA82"), "another community")
	assert.Contains(t, logged.String(), "eeg.area.updated_from_list")
	assert.Contains(t, logged.String(), "eeg.area.old=LOCAL")
	assert.Contains(t, logged.String(), "eeg.area.new=REGIONAL")

	podList("RC000081", "RC_L", 2)
	assert.Equal(t, "LOCAL", area("TEAREA81"), "and back")

	logged.Reset()
	podList("GC000083", "RC_R", 3)
	podList("PP000084", "RC_L", 4)
	assert.Equal(t, "GEA", area("TEAREA83"))
	assert.Equal(t, "P2P", area("TEAREA84"))
	assert.Contains(t, logged.String(), "ECType RC_R, the community type GEA means GC")
	assert.Contains(t, logged.String(), "ECType RC_L, the community type P2P means PP")
	assert.NotContains(t, logged.String(), "eeg.area.updated_from_list")

	// a LOCAL community with another mismatch (CC) only gets the warning
	podList("RC000082", "CC", 5)
	assert.Equal(t, "LOCAL", area("TEAREA82"))
	assert.Contains(t, logged.String(), "ECType CC, the community type LOCAL means RC_L")
}

// eegfaktura-dev#56 #10: a switch LOCAL<->REGIONAL from a list without ECZoneLevel leaves the Nahebereich empty
// instead of the old type's; with ECZoneLevel in the list the reported level is stored.
func TestPodList_areaSwitchClearsZoneLevel(t *testing.T) {
	const tenant, rc = "TEAREA85", "RC000085"
	cleanupCommunities(t, tenant)
	// rows left by an aborted run (cleanupCommunities only cleans up after the test)
	_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant = $1`, tenant)
	require.NoError(t, err)
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email, ec_zone_level)
		VALUES ($1, 'Typ', 'Typ-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3, 'Weg', '1', 'Linz', '4020',
		 'typ@example.org', 3)`, tenant, rc, ecIdOf(rc))
	require.NoError(t, err)

	podList := func(n int, extra string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: rc}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s2026110600000000000000000%d",
			"sender":"AT003000","receiver":"%s","messageCode":"SENDEN_ECP","ecId":"%s"%s,"meterList":[]}`,
			rc, n, rc, ecIdOf(rc), extra)), &msg.Payload))
		protocolEcPodListHandler(context.Background(), msg)
	}
	state := func() string {
		var s string
		require.NoError(t, testDB.DbInstance.Get(&s, `SELECT area || ' ' || COALESCE(ec_zone_level::text, 'NULL')
			FROM base.eeg WHERE tenant = $1`, tenant))
		return s
	}

	podList(1, `,"ecTypeEda":"RC_R"`)
	assert.Equal(t, "REGIONAL NULL", state(), "switch without ECZoneLevel")
	podList(2, `,"ecTypeEda":"RC_L","ecZoneLevel":3`)
	assert.Equal(t, "LOCAL 3", state(), "switch with ECZoneLevel")
	podList(3, `,"ecTypeEda":"RC_L"`)
	assert.Equal(t, "LOCAL 3", state(), "no switch: the stored level stays")
}

func ecIdOf(rc string) string { return "AT00300000000" + rc + "000000000001" }
