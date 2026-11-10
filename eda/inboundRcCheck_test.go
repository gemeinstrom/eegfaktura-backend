package eda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"at.ourproject/vfeeg-backend/logging/logtest"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/services"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261105-mm5: the inbound EDA handlers process a message for a community id of the receiving RC number (the MQTT
// topic): for the receiving community, and in a GEA (two tenants, one RC number and community id) for the tenant that
// holds the metering point. Any other message is dropped: no status, factor, notification, history or mail.
func TestInbound_onlyForTheReceivingRcNumber(t *testing.T) {
	cases := []struct {
		name    string
		process string // event.action eda.<process>.*
		handle  func(context.Context, model.SubscribeMessage)
		proto   model.EdaProtocol
		code    model.EbMsMessageType
		body    string                                        // payload after the ecId; %[1]s is the metering point
		applied func(t *testing.T, tenant, meter string) bool // the handler's effect beside the history; nil: history only
	}{
		{"registration", "registration", protocolEcReqOnlHandler, model.EC_REQ_ONL, model.EBMS_ONLINE_REG_ANSWER,
			`"responseData":[{"meteringPoint":"%[1]s","responseCode":[99]}]`, meterColumnIs("process_state", "PENDING")},
		{"energy data", "crmsg", protocolCrMsgHandler, model.CR_MSG, model.EBMS_ENERGY_FILE_RESPONSE,
			`"meter":{"meteringPoint":"%[1]s"},"energy":[{"start":1759269600000,"end":1759356000000}]`, nil},
		{"energy data request", "energyrequest", protocolCrReqPtHandler, model.CR_REQ_PT, model.EBMS_ZP_RES,
			`"meter":{"meteringPoint":"%[1]s"},"responseData":[{"meteringPoint":"%[1]s","responseCode":[99]}]`,
			notified(model.EBMS_ZP_RES)},
		{"revocation", "revoke", protocolCmRevImpHandler, model.CM_REV_SP, model.EBMS_ANTWORT_CCMS,
			`"consentEnd":1759269600000,"meter":{"meteringPoint":"%[1]s","consentId":"C-S4"},
			"responseData":[{"meteringPoint":"%[1]s","responseCode":[176],"consentId":"C-S4"}]`,
			meterColumnIs("status", "INACTIVE")},
		{"revocation request echo", "revoke", protocolCmRevImpHandler, model.CM_REV_SP, model.EBMS_AUFHEBUNG_CCMS,
			`"consentEnd":1759269600000,"meter":{"meteringPoint":"%[1]s","consentId":"C-S4"},
			"responseData":[{"meteringPoint":"%[1]s","responseCode":[99],"consentId":"C-S4"}]`,
			// processed: notified, and the status stays ACTIVE (platform#238)
			func(t *testing.T, tenant, meter string) bool {
				return notified(model.EBMS_AUFHEBUNG_CCMS)(t, tenant, meter) && meterColumnIs("status", "ACTIVE")(t, tenant, meter)
			}},
		{"revocation rejected", "revoke", protocolCmRevImpHandler, model.CM_REV_SP, model.EBMS_ABLEHNUNG_CCMS,
			`"meter":{"meteringPoint":"%[1]s","consentId":"C-S4"},
			"responseData":[{"meteringPoint":"%[1]s","responseCode":[37],"consentId":"C-S4"}]`,
			notified(model.EBMS_ABLEHNUNG_CCMS)},
		{"participation factor", "partfact", protocolEcPrtChangeHandler, model.EC_PRTFACT_CHANGE, model.EBMS_ANS_CHANGE_PARTFACT,
			`"meterList":[{"meteringPoint":"%[1]s","direction":"CONSUMPTION","partFact":50}]`, partFactStored(50)},
		{"participation factor rejected", "partfact", protocolEcPrtChangeHandler, model.EC_PRTFACT_CHANGE,
			model.EBMS_REJ_CHANGE_PARTFACT, `"responseData":[{"meteringPoint":"%[1]s","responseCode":[56]}]`,
			notified(model.EBMS_REJ_CHANGE_PARTFACT)},
	}

	var mails atomic.Int32
	countMail := func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		mails.Add(1)
		return nil
	}
	origActivation, origInactive := sendActivationMail, sendInactiveMail
	sendActivationMail, sendInactiveMail = countMail, countMail
	t.Cleanup(func() { sendActivationMail, sendInactiveMail = origActivation, origInactive })

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mails.Store(0)
			n := 310 + i
			sole, gea, geaBase := fmt.Sprintf("TS%06d", n), fmt.Sprintf("GS%06d-002", n), fmt.Sprintf("GS%06d", n)
			noEcId := fmt.Sprintf("TN%06d", n)
			soleEc, geaEc := "AT00300000000"+sole+"000000000001", "AT00300000000"+geaBase+"000000000001"
			meter := func(k int) string { return fmt.Sprintf("AT00300000000000000000000030%02d%03d", i, k) }
			inboundFixture(t, sole, sole, soleEc, meter(1), n, 1)
			inboundFixture(t, geaBase, geaBase, geaEc, "", n, 2)
			inboundFixture(t, gea, geaBase, geaEc, meter(2), n, 3)
			// a community without community id
			inboundFixture(t, noEcId, noEcId, "", meter(3), n, 4)

			send := func(topic, ecId, meter, conversation string) {
				msg := model.SubscribeMessage{MessageCode: c.code, Protocol: c.proto, Tenant: topic}
				require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000",
					"receiver":"%s","messageCode":"%s","ecId":"%s",`, conversation, topic, c.code, ecId)+
					fmt.Sprintf(c.body, meter)+`}`), &msg.Payload))
				c.handle(context.Background(), msg)
				flushMails(t)
			}
			untouched := func(tenant, meter, conversation string) {
				t.Helper()
				assert.Equal(t, 0, historyCount(t, tenant, conversation), tenant)
				assert.Equal(t, 0, notificationCount(t, tenant), tenant)
				if c.applied != nil {
					assert.False(t, c.applied(t, tenant, meter), tenant)
				}
			}

			// the GEA's RC number gets a message for the community id of the sole community: dropped
			logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
			send(strings.ToLower(geaBase), soleEc, meter(1), "S4FOREIGN"+sole)
			assert.Contains(t, logged.String(), "event.action=eda."+c.process+".foreign_community")
			assert.Contains(t, logged.String(), "ecId="+soleEc)
			untouched(sole, meter(1), "S4FOREIGN"+sole)
			untouched(gea, meter(2), "S4FOREIGN"+sole)

			// a message without a community id: dropped, also where a community has none
			logged.Reset()
			send(strings.ToLower(noEcId), "", meter(3), "S4NOEC"+sole)
			assert.Contains(t, logged.String(), "event.action=eda."+c.process+".no_community_id")
			untouched(noEcId, meter(3), "S4NOEC"+sole)
			assert.Equal(t, int32(0), mails.Load(), "no mail for a dropped message")

			// its own RC number (topic in lower case): processed
			send(strings.ToLower(sole), soleEc, meter(1), "S4OWN"+sole)
			assert.Equal(t, 1, historyCount(t, sole, "S4OWN"+sole))
			if c.applied != nil {
				assert.True(t, c.applied(t, sole, meter(1)))
			}

			// GEA: the tenant that holds the metering point, not the base tenant named like the RC number
			send(strings.ToLower(geaBase), geaEc, meter(2), "S4GEA"+sole)
			assert.Equal(t, 1, historyCount(t, gea, "S4GEA"+sole))
			assert.Equal(t, 0, historyCount(t, geaBase, "S4GEA"+sole))
			if c.applied != nil {
				assert.True(t, c.applied(t, gea, meter(2)))
			}
		})
	}
}

// 261105-mm5: a rejected metering point list request (ABLEHNUNG_ECP) clears the remembered requester only for a
// community id of the receiving RC number; the history entry under the receiving RC number stays as before.
func TestPodListRejection_onlyForTheReceivingRcNumber(t *testing.T) {
	const sole, other = "TS000320", "TS000321"
	const soleEc, otherEc = "AT00300000000TS000320000000000001", "AT00300000000TS000321000000000001"
	inboundFixture(t, sole, sole, soleEc, "", 320, 1)
	inboundFixture(t, other, other, otherEc, "", 321, 1)
	t.Cleanup(func() { mqttclient.TakeZpListRequester(soleEc) })

	reject := func(topic, ecId, conversation string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ZP_LIST_REJECTION, Protocol: model.EC_PODLIST, Tenant: topic}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000","receiver":"%s",
			"messageCode":"ABLEHNUNG_ECP","ecId":"%s","responseData":[{"responseCode":[56]}]}`, conversation, topic, ecId)), &msg.Payload))
		protocolEcPodListHandler(context.Background(), msg)
	}

	mqttclient.RememberZpListRequester(soleEc, sole)
	logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
	reject(other, soleEc, "S4REJFOREIGN")
	assert.Contains(t, logged.String(), "event.action=eda.podlist.foreign_community")
	assert.Equal(t, 1, historyCount(t, other, "S4REJFOREIGN"))
	requester, asked := mqttclient.TakeZpListRequester(soleEc)
	assert.True(t, asked, "a foreign rejection keeps the requester")
	assert.Equal(t, sole, requester)

	mqttclient.RememberZpListRequester(soleEc, sole)
	reject(sole, soleEc, "S4REJOWN")
	assert.Equal(t, 1, historyCount(t, sole, "S4REJOWN"))
	_, asked = mqttclient.TakeZpListRequester(soleEc)
	assert.False(t, asked, "the own rejection clears the requester")
}

// 261105-mm5: AUFHEBUNG_CCMI/CCMC carry no community id; they revoke only within the receiving RC number. A consent id
// of another community revokes nothing there: no mail, the message is recorded in the receiving community's history.
func TestRevocationCcmi_onlyWithinTheReceivingRcNumber(t *testing.T) {
	const sole, other, geaBase, gea = "TS000330", "TS000331", "GS000332", "GS000332-002"
	meter := func(k int) string { return fmt.Sprintf("AT00300000000000000000000003%05d", 33000+k) }
	inboundFixture(t, sole, sole, "AT00300000000TS000330000000000001", meter(1), 330, 1)
	inboundFixture(t, other, other, "AT00300000000TS000331000000000001", "", 331, 1)
	inboundFixture(t, geaBase, geaBase, "AT00300000000GS000332000000000001", "", 332, 1)
	inboundFixture(t, gea, geaBase, "AT00300000000GS000332000000000001", meter(2), 332, 2)

	var mails atomic.Int32
	orig := sendInactiveMail
	sendInactiveMail = func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		mails.Add(1)
		return nil
	}
	t.Cleanup(func() { sendInactiveMail = orig })
	revoke := func(topic, meter, conversation string) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_AUFHEBUNG_CCMI, Protocol: model.CM_REV_IMP, Tenant: topic}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000","receiver":"%s",
			"messageCode":"AUFHEBUNG_CCMI","consentEnd":1759269600000,"meter":{"meteringPoint":"%s","consentId":"C-S4"},
			"responseData":[{"meteringPoint":"%s","responseCode":[1099],"consentEnd":1759269600000,"consentId":"C-S4"}]}`,
			conversation, topic, meter, meter)), &msg.Payload))
		protocolCmRevImpHandler(context.Background(), msg)
		flushMails(t)
	}
	status := meterColumnIs("status", "INACTIVE")

	// another RC number with the consent id of this community's metering point
	revoke(strings.ToLower(other), meter(1), "S4CCMIFOREIGN")
	assert.False(t, status(t, sole, meter(1)))
	assert.Equal(t, 0, historyCount(t, sole, "S4CCMIFOREIGN"))
	assert.Equal(t, 1, historyCount(t, other, "S4CCMIFOREIGN"))
	assert.Equal(t, 0, notificationCount(t, sole))
	assert.Equal(t, int32(0), mails.Load())

	// its own RC number, and a GEA tenant by the GEA's RC number
	revoke(strings.ToLower(sole), meter(1), "S4CCMIOWN")
	assert.True(t, status(t, sole, meter(1)))
	assert.Equal(t, 1, historyCount(t, sole, "S4CCMIOWN"))
	revoke(strings.ToLower(geaBase), meter(2), "S4CCMIGEA")
	assert.True(t, status(t, gea, meter(2)))
	assert.Equal(t, int32(2), mails.Load())
}

// inboundFixture: a community (tenant, RC number, community id) and, with a meter, a member holding it (status ACTIVE,
// process INIT, participation 100, consent C-S4, a mail address). seq/k make the participant id unique.
func inboundFixture(t *testing.T, tenant, rc, ecId, meter string, seq, k int) {
	t.Helper()
	exec := func(q string, args ...interface{}) {
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	cleanupCommunities(t, tenant)
	t.Cleanup(func() { exec(`DELETE FROM base.participant WHERE tenant = $1`, tenant) })
	exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name,
		"communityId", street, "streetNumber", city, zip, email)
		VALUES ($1, 'S4', 'S4-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3, 'Weg', '1', 'Linz', '4020', 's4@example.org')`,
		tenant, rc, ecId)
	if meter == "" {
		return
	}
	participant := fmt.Sprintf("c5%06d-0000-0000-0000-%012d", seq, k)
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ($1, $2, 'Anna', 'Muster', 'ACTIVE', 'test', 'test')`, participant, tenant)
	for _, typ := range []string{"BILLING", "RESIDENCE"} {
		exec(`INSERT INTO base.address (participant_id, type, street, "streetNumber", zip, city)
			VALUES ($1, $2, 'Weg', '1', '4020', 'Linz')`, participant, typ)
	}
	exec(`INSERT INTO base.contactdetail (participant_id, email) VALUES ($1, 'member@example.org')`, participant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, consent_id, flag)
		VALUES ($1, $2, $3, 'CONSUMPTION', 'ACTIVE', 'INIT', now(), 'test', '2025-01-01', '2025-01-01', '2999-12-31',
		'C-S4', 1)`, meter, participant, tenant)
	exec(`INSERT INTO base.metering_partition_factor (metering_point_id, tenant, participant_id, "partFact", "createdBy")
		VALUES ($1, $2, $3, 100, 'test')`, meter, tenant, participant)
}

func meterColumnIs(column, value string) func(*testing.T, string, string) bool {
	return func(t *testing.T, tenant, meter string) bool {
		var v string
		require.NoError(t, testDB.DbInstance.Get(&v, `SELECT `+column+` FROM base.meteringpoint
			WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter))
		return v == value
	}
}

func notified(code model.EbMsMessageType) func(*testing.T, string, string) bool {
	return func(t *testing.T, tenant, _ string) bool {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1
			AND notification->>'type' = $2`, tenant, string(code)))
		return n > 0
	}
}

func partFactStored(partFact int) func(*testing.T, string, string) bool {
	return func(t *testing.T, tenant, meter string) bool {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.metering_partition_factor
			WHERE tenant = $1 AND metering_point_id = $2 AND "partFact" = $3`, tenant, meter, partFact))
		return n > 0
	}
}

func notificationCount(t *testing.T, tenant string) int {
	var n int
	require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1`, tenant))
	return n
}
