package eda

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261028-mm1: in a GEA several tenants share one community id. An inbound message is applied to the
// tenant that holds its metering point, not to whichever tenant the community id lookup returns first.
func TestInboundMessage_geaTenantByMeteringPoint(t *testing.T) {
	const ecId = "AT00300000000GC000041000000000001"
	const first, second = "GC000041", "GC000041-002"
	const meter = "AT0030000000000000000000000041002"
	cleanupCommunities(t, first, second)
	for _, tenant := range []string{first, second} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'GEA-41', 'GEA-Test', 'GC000041', 'GEA', 'AT003000', 'Netz OÖ', $2,
			 'Weg', '1', 'Linz', '4020', 'gea-41@example.org')`, tenant, ecId)
		require.NoError(t, err)
	}
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
		status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
		VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'INIT', 'PENDING', now(), 'test',
		 '2026-10-01', 1)`, meter, second)
	require.NoError(t, err)

	count := func(query string, args ...interface{}) int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, query, args...))
		return n
	}
	message := func(protocol model.EdaProtocol, code model.EbMsMessageType, conversationId, body string) model.SubscribeMessage {
		msg := model.SubscribeMessage{MessageCode: code, Protocol: protocol, Tenant: first}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000","receiver":"%s",
			"messageCode":"%s","ecId":"%s",%s}`, conversationId, first, code, ecId, body)), &msg.Payload))
		return msg
	}
	historyTenant := func(conversationId string) []string {
		var tenants []string
		require.NoError(t, testDB.DbInstance.Select(&tenants, `SELECT DISTINCT tenant FROM base.processhistory
			WHERE "conversationId" = $1`, conversationId))
		return tenants
	}

	// ZUSTIMMUNG_ECON for the metering point of the second tenant
	protocolEcReqOnlHandler(context.Background(), message(model.EC_REQ_ONL, model.EBMS_ONLINE_REG_APPROVAL,
		"GC000041202610280000000000000000001",
		`"responseData":[{"meteringPoint":"`+meter+`","responseCode":[175],"consentId":"CONSENT-41"}]`))
	assert.Equal(t, 1, count(`SELECT count(*) FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2
		AND process_state = 'APPROVED' AND consent_id = 'CONSENT-41'`, second, meter))
	assert.Equal(t, 1, count(`SELECT count(*) FROM base.notification WHERE tenant = $1
		AND notification->>'type' = 'ZUSTIMMUNG_ECON'`, second))
	assert.Equal(t, 0, count(`SELECT count(*) FROM base.notification WHERE tenant = $1`, first))
	assert.Equal(t, []string{second}, historyTenant("GC000041202610280000000000000000001"))

	// ANTWORT_PT (energy data request) for the metering point of the second tenant
	protocolCrReqPtHandler(context.Background(), message(model.CR_REQ_PT, model.EBMS_ZP_RES,
		"GC000041202610280000000000000000002",
		`"meter":{"meteringPoint":"`+meter+`"},"responseData":[{"meteringPoint":"`+meter+`","responseCode":[99]}]`))
	assert.Equal(t, []string{second}, historyTenant("GC000041202610280000000000000000002"))

	// a metering point that none of the tenants holds: recorded under the base tenant named like the RC number
	protocolCrReqPtHandler(context.Background(), message(model.CR_REQ_PT, model.EBMS_ZP_RES,
		"GC000041202610280000000000000000003",
		`"meter":{"meteringPoint":"AT0030000000000000000000000041999"},"responseData":[{"meteringPoint":"AT0030000000000000000000000041999","responseCode":[99]}]`))
	assert.Equal(t, []string{first}, historyTenant("GC000041202610280000000000000000003"))
}

// 261028-mm1: the metering point list (SENDEN_ECP) covers the whole community; in a GEA every tenant updates the
// metering points it holds, not only the tenant named like the receiving RC number.
func TestPodList_geaSyncsEveryTenant(t *testing.T) {
	const ecId = "AT00300000000GC000042000000000001"
	const first, second = "GC000042", "GC000042-002"
	meters := map[string]string{first: "AT0030000000000000000000000042001", second: "AT0030000000000000000000000042002"}
	cleanupCommunities(t, first, second)
	for _, tenant := range []string{first, second} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'GEA-42', 'GEA-Test', 'GC000042', 'GEA', 'AT003000', 'Netz OÖ', $2,
			 'Weg', '1', 'Linz', '4020', 'gea-42@example.org')`, tenant, ecId)
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
			status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
			VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'INIT', 'PENDING', now(), 'test',
			 '2026-10-01', 1)`, meters[tenant], tenant)
		require.NoError(t, err)
	}

	msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: first}
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"GC000042202610280000000000000000001",
		"sender":"AT003000","receiver":"%s","messageCode":"SENDEN_ECP","ecId":"%s","meterList":[
		{"meteringPoint":"%s","direction":"CONSUMPTION","from":1759269600000,"partFact":100},
		{"meteringPoint":"%s","direction":"CONSUMPTION","from":1759269600000,"partFact":100}]}`,
		first, ecId, meters[first], meters[second])), &msg.Payload))
	// the second tenant asked for the list: the answer (mail, notification, history) goes to it, not to the base tenant
	mqttclient.RememberZpListRequester(ecId, second)
	protocolEcPodListHandler(context.Background(), msg)
	assert.Equal(t, 1, historyCount(t, second, "GC000042202610280000000000000000001"))
	assert.Equal(t, 0, historyCount(t, first, "GC000042202610280000000000000000001"))

	// nobody asked (e.g. after a restart): the base tenant named like the RC number
	msg.Payload.ConversationId = "GC000042202610280000000000000000002"
	protocolEcPodListHandler(context.Background(), msg)
	assert.Equal(t, 1, historyCount(t, first, "GC000042202610280000000000000000002"))

	for tenant, meter := range meters {
		var status string
		require.NoError(t, testDB.DbInstance.Get(&status, `SELECT status FROM base.meteringpoint
			WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter))
		assert.Equal(t, "ACTIVE", status, tenant)
	}
}

func historyCount(t *testing.T, tenant, conversationId string) int {
	var n int
	require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.processhistory
		WHERE tenant = $1 AND "conversationId" = $2`, tenant, conversationId))
	return n
}
