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

// platform#229 (konzept-buergerenergie-eigenversorgung-p2p.md, J): a metering point list (SENDEN_ECP) sets the
// Nahebereich at every tenant of the community id and TechCode/FuelCode at their metering points; an absent or
// invalid value keeps what is stored, an empty code never overwrites; a foreign tenant stays as it is; an ECType that
// does not fit the community type is logged.
func TestPodList_zoneLevelAndCodes(t *testing.T) {
	const ecId = "AT00300000000GC000043000000000001"
	const first, second, foreign = "GC000043", "GC000043-002", "GC000044"
	const meter = "AT0030000000000000000000000043001"
	cleanupCommunities(t, first, second, foreign)
	for tenant, communityId := range map[string]string{first: ecId, second: ecId, foreign: "AT00300000000GC000044000000000001"} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email, ec_zone_level)
			VALUES ($1, 'GEA-43', 'GEA-Test', $3, 'GEA', 'AT003000', 'Netz OÖ', $2,
			 'Weg', '1', 'Linz', '4020', 'gea-43@example.org', 5)`, tenant, communityId, tenant[:8])
		require.NoError(t, err)
		// every tenant holds the same metering point number: only those of the community id get the codes
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
			status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
			VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'GENERATION', 'INIT', 'PENDING', now(), 'test',
			 '2026-10-01', 1)`, meter, tenant)
		require.NoError(t, err)
	}

	podList := func(conversation int, extra, meterExtra string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: first}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"GC00004320261105000000000000000000%d",
			"sender":"AT003000","receiver":"%s","messageCode":"SENDEN_ECP","ecId":"%s"%s,"meterList":[
			{"meteringPoint":"%s","direction":"GENERATION","from":1759269600000,"partFact":100%s}]}`,
			conversation, first, ecId, extra, meter, meterExtra)), &msg.Payload))
		protocolEcPodListHandler(context.Background(), msg)
	}
	zone := func(tenant string) null.Int {
		var z null.Int
		require.NoError(t, testDB.DbInstance.Get(&z, `SELECT ec_zone_level FROM base.eeg WHERE tenant = $1`, tenant))
		return z
	}
	codes := func(tenant string) string {
		var c string
		require.NoError(t, testDB.DbInstance.Get(&c, `SELECT concat(tech_code, '/', fuel_code) FROM base.meteringpoint
			WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter))
		return c
	}

	podList(1, `,"ecZoneLevel":2,"ecTypeEda":"GC"`, `,"techCode":"PV","fuelCode":"E000"`)
	for _, tenant := range []string{first, second} {
		assert.Equal(t, null.IntFrom(2), zone(tenant), tenant)
		assert.Equal(t, "PV/E000", codes(tenant), tenant)
	}
	assert.Equal(t, null.IntFrom(5), zone(foreign), "foreign tenant")
	assert.Equal(t, "/", codes(foreign), "foreign tenant")

	// without ECZoneLevel and codes: the stored values stay
	podList(2, ``, ``)
	assert.Equal(t, null.IntFrom(2), zone(first))
	assert.Equal(t, "PV/E000", codes(first))

	// an invalid ECZoneLevel is dropped with a warning; an ECType that does not fit GEA is logged
	logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
	podList(3, `,"ecZoneLevel":8,"ecTypeEda":"PP"`, `,"techCode":"","fuelCode":"E001"`)
	assert.Equal(t, null.IntFrom(2), zone(first))
	assert.Equal(t, "PV/E001", codes(first), "an empty code does not overwrite")
	assert.Contains(t, logged.String(), "invalid ECZoneLevel 8")
	assert.Contains(t, logged.String(), "ECType PP, the community type GEA means GC")

	// a code longer than 10 characters or with other characters is dropped with a warning, never stored
	// truncated; 10 characters are kept
	podList(4, ``, `,"techCode":"ABCDEFGHIJK","fuelCode":"0123456789"`)
	assert.Equal(t, "PV/0123456789", codes(first))
	assert.Contains(t, logged.String(), "invalid TechCode (11 characters)")
	assert.Contains(t, logged.String(), "event.action=eda.podlist.code_dropped")
	assert.NotContains(t, logged.String(), meter, "metering point number masked (eegfaktura-dev#56 #12)")
	podList(5, ``, `,"techCode":"P V","fuelCode":"E;1"`)
	assert.Equal(t, "PV/0123456789", codes(first))
	assert.Contains(t, logged.String(), "invalid FuelCode (3 characters)")
	podList(6, ``, `,"techCode":"W.IND_1-A","fuelCode":"E002"`)
	assert.Equal(t, "W.IND_1-A/E002", codes(first))

	// without a community id the list is dropped: nothing changes
	msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_RESPONSE, Protocol: model.EC_PODLIST, Tenant: first}
	require.NoError(t, json.Unmarshal([]byte(`{"conversationId":"GC000043202611050000000000000000009","sender":"AT003000",
		"receiver":"GC000043","messageCode":"SENDEN_ECP","ecZoneLevel":4,"meterList":[{"meteringPoint":"`+meter+`",
		"direction":"GENERATION","from":1759269600000,"partFact":100,"techCode":"WIND"}]}`), &msg.Payload))
	protocolEcPodListHandler(context.Background(), msg)
	assert.Equal(t, null.IntFrom(2), zone(first))
	assert.Equal(t, "W.IND_1-A/E002", codes(first))
	assert.Equal(t, 0, historyCount(t, first, "GC000043202611050000000000000000009"))
}
