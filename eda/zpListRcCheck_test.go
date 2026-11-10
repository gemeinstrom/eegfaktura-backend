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
	"gopkg.in/guregu/null.v4"
)

// A metering point list is processed only for a community of the RC number it came in for. A list for the community
// id of another RC number changes nothing (no metering point, no zone, no history); a sole tenant named unlike its
// RC number still gets its list (the GEA case: TestPodList_geaSyncsEveryTenant).
func TestPodList_onlyForTheReceivingRcNumber(t *testing.T) {
	const ecId = "AT00300000000RC000071000000000001"
	const tenant, other = "TESOLE71", "TESOLE72"
	const meter = "AT0030000000000000000000000071001"
	cleanupCommunities(t, tenant, other)
	for name, rc := range map[string]string{tenant: "RC000071", other: "RC000072"} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email, ec_zone_level)
			VALUES ($1, 'Solo', 'RC-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3,
			 'Weg', '1', 'Linz', '4020', 'solo@example.org', 3)`, name, rc, "AT00300000000"+rc+"000000000001")
		require.NoError(t, err)
	}
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
		status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
		VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'INIT', 'PENDING', now(), 'test',
		 '2026-10-01', 1)`, meter, tenant)
	require.NoError(t, err)

	podList := func(topicRc, conversation string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: topicRc}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000",
			"receiver":"%s","messageCode":"SENDEN_ECP","ecId":"%s","ecZoneLevel":1,"meterList":[
			{"meteringPoint":"%s","direction":"CONSUMPTION","from":1759269600000,"partFact":100,"techCode":"PV"}]}`,
			conversation, topicRc, ecId, meter)), &msg.Payload))
		protocolEcPodListHandler(context.Background(), msg)
	}
	state := func() (string, null.String, null.Int) {
		var status string
		var code null.String
		var zone null.Int
		require.NoError(t, testDB.DbInstance.QueryRow(`SELECT m.status, m.tech_code, e.ec_zone_level
			FROM base.meteringpoint m JOIN base.eeg e ON e.tenant = m.tenant
			WHERE m.tenant = $1 AND m.metering_point_id = $2`, tenant, meter).Scan(&status, &code, &zone))
		return status, code, zone
	}

	// the other RC number sends a list for this community id: dropped, logged with event.action and the community id
	// (eegfaktura-dev#56 #12)
	logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
	podList("RC000072", "RC000072202611050000000000000000001")
	assert.Contains(t, logged.String(), "event.action=eda.podlist.foreign_community")
	assert.Contains(t, logged.String(), "ecId="+ecId)
	status, code, zone := state()
	assert.Equal(t, "INIT", status)
	assert.Equal(t, null.String{}, code)
	assert.Equal(t, null.IntFrom(3), zone)
	assert.Equal(t, 0, historyCount(t, tenant, "RC000072202611050000000000000000001"))
	assert.Equal(t, 0, historyCount(t, other, "RC000072202611050000000000000000001"))

	// its own RC number (topic in lower case, tenant named unlike the RC number): processed
	podList("rc000071", "RC000071202611050000000000000000001")
	status, code, zone = state()
	assert.Equal(t, "ACTIVE", status)
	assert.Equal(t, null.StringFrom("PV"), code)
	assert.Equal(t, null.IntFrom(1), zone)
	assert.Equal(t, 1, historyCount(t, tenant, "RC000071202611050000000000000000001"))
}
