package eda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/services"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mailWait = 10 * time.Second

// flushMails returns when the worker has handled everything queued so far.
func flushMails(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	mailWorker.Enqueue(services.MailJob{Ctx: context.Background(), Attempts: []services.MailAttempt{{
		Send: func() error { close(done); return nil },
	}}})
	select {
	case <-done:
	case <-time.After(mailWait):
		t.Fatal("the mail worker did not get through its queue")
	}
}

// routeClient is an MQTT client that only collects the broker's routes, so a test can deliver a
// message the way paho does.
type routeClient struct {
	mqtt.Client
	routes map[string]mqtt.MessageHandler
}

func (c *routeClient) AddRoute(topic string, h mqtt.MessageHandler) { c.routes[topic] = h }

type inboundMessage struct {
	mqtt.Message
	topic   string
	payload []byte
}

func (m inboundMessage) Topic() string   { return m.topic }
func (m inboundMessage) Payload() []byte { return m.payload }
func (m inboundMessage) Qos() byte       { return 1 }

// mailFixture creates (or resets, for -count=2) a community with one member and one metering point.
func mailFixture(t *testing.T, tenant, ecId, meter, email, process string) {
	t.Helper()
	exec := func(q string, args ...interface{}) {
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	participant := fmt.Sprintf("b1%s-0000-0000-0000-000000000001", tenant[2:])
	exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name,
		"communityId", street, "streetNumber", city, zip, email)
		VALUES ($1, 'Mail-EEG', 'Mail-EEG Verein', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3, 'Weg', '1', 'Linz', '4020',
		'office@mail-eeg.at') ON CONFLICT (tenant) DO NOTHING`, tenant, tenant, ecId)
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ($1, $2, 'Anna', 'Muster', 'ACTIVE', 'test', 'test') ON CONFLICT DO NOTHING`, participant, tenant)
	exec(`DELETE FROM base.address WHERE participant_id = $1`, participant)
	for _, typ := range []string{"BILLING", "RESIDENCE"} {
		exec(`INSERT INTO base.address (participant_id, type, street, "streetNumber", zip, city)
			VALUES ($1, $2, 'Weg', '1', '4020', 'Linz')`, participant, typ)
	}
	exec(`DELETE FROM base.contactdetail WHERE participant_id = $1`, participant)
	exec(`INSERT INTO base.contactdetail (participant_id, email) VALUES ($1, $2)`, participant, email)
	exec(`DELETE FROM base.metering_partition_factor WHERE metering_point_id = $1`, meter)
	exec(`DELETE FROM base.meteringpoint WHERE metering_point_id = $1`, meter)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, consent_id)
		VALUES ($1, $2, $3, 'CONSUMPTION', $4, $4, now(), 'test', '2025-01-01', '2025-01-01', '2999-12-31', 'C-MAIL')`,
		meter, participant, tenant, process)
	exec(`INSERT INTO base.metering_partition_factor (metering_point_id, tenant, participant_id, "partFact", "createdBy")
		VALUES ($1, $2, $3, 100, 'test')`, meter, tenant, participant)
}

func revokeMessage(t *testing.T, ecId, meter string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"conversationId": "C" + meter, "sender": "AT003000", "messageCode": model.EBMS_AUFHEBUNG_CCMI, "ecId": ecId, "consentEnd": 1759269600000,
		"meter":        map[string]string{"meteringPoint": meter, "consentId": "C-MAIL"},
		"responseData": []map[string]interface{}{{"meteringPoint": meter, "responseCode": []int{1099}, "consentEnd": 1759269600000, "consentId": "C-MAIL"}},
	})
	require.NoError(t, err)
	return body
}

// A slow mail server must not hold the broker loop: API-triggered EDA requests go through the same
// loop. The message runs through the real Listen; without the worker SendMessage waits for the mail.
func TestSlowMailDoesNotBlockTheBrokerLoop(t *testing.T) {
	const tenant, ecId, meter = "TM000201", "AT00300000000TM000201000000000001", "AT0030000000000000000000000201001"
	mailFixture(t, tenant, ecId, meter, "slow@example.org", "ACTIVE")

	entered, release := make(chan struct{}), make(chan struct{})
	orig := sendInactiveMail
	sendInactiveMail = func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		close(entered)
		<-release
		return nil
	}
	t.Cleanup(func() { sendInactiveMail = orig })

	broker, err := mqttclient.NewMessageBroker()
	require.NoError(t, err)
	client := &routeClient{routes: map[string]mqtt.MessageHandler{}}
	broker.ConfigRoutes(client)
	broker.Subscribe(model.Subscriptions{Protocol: model.CM_REV_IMP, Handler: func(msg model.SubscribeMessage) {
		protocolCmRevImpHandler(context.Background(), msg)
	}})
	go broker.Listen()
	t.Cleanup(broker.Stop)

	client.routes["eda/response/+/protocol/#"](client, inboundMessage{
		topic: "eda/response/" + tenant + "/protocol/cm_rev_imp", payload: revokeMessage(t, ecId, meter)})
	select {
	case <-entered:
	case <-time.After(mailWait):
		t.Fatal("the mail was not sent")
	}

	// the mail hangs; an outbound EDA request still gets through the loop
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		broker.SendMessage(model.EbmsMessage{MessageCode: model.EBMS_ZP_LIST})
	}()
	select {
	case <-sent:
	case <-time.After(mailWait / 2):
		t.Error("the broker loop waited for the mail")
	}
	close(release)
	flushMails(t)
}

// A full queue must not stop the handler, and the community hears about it like about a failed mail.
func TestFullMailQueue_CommunityIsNotified(t *testing.T) {
	const tenant, ecId, meter = "TM000202", "AT00300000000TM000202000000000001", "AT0030000000000000000000000202001"
	mailFixture(t, tenant, ecId, meter, "full@example.org", "ACTIVE")

	orig, origSend := mailWorker, sendInactiveMail
	mailWorker = services.NewMailWorker(0) // never started, takes nothing
	sendInactiveMail = func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		t.Error("the mail must not be sent")
		return nil
	}
	t.Cleanup(func() { mailWorker, sendInactiveMail = orig, origSend })

	msg := model.SubscribeMessage{MessageCode: model.EBMS_AUFHEBUNG_CCMI, Protocol: model.CM_REV_IMP, Tenant: tenant}
	require.NoError(t, json.Unmarshal(revokeMessage(t, ecId, meter), &msg.Payload))
	protocolCmRevImpHandler(context.Background(), msg)

	var status, n string
	require.NoError(t, testDB.DbInstance.Get(&status, `SELECT status FROM base.meteringpoint WHERE metering_point_id = $1`, meter))
	assert.Equal(t, "INACTIVE", status, "the revocation stays applied")
	require.NoError(t, testDB.DbInstance.Get(&n, `SELECT notification FROM base.notification WHERE tenant = $1 ORDER BY id DESC LIMIT 1`, tenant))
	assert.Contains(t, n, services.ErrMailNotQueued.Error())
	assert.Contains(t, n, `"metering_point":"`+meter+`"`)
}

// The activation mail leaves after the handler has written the status.
func TestActivationMail_AfterTheStatusIsWritten(t *testing.T) {
	const tenant, ecId, meter = "TM000203", "AT00300000000TM000203000000000001", "AT0030000000000000000000000203001"
	mailFixture(t, tenant, ecId, meter, "activation@example.org", "INIT")

	var state, to string
	orig := sendActivationMail
	sendActivationMail = func(_, recipient, _ string, _ *string, _ *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		to = recipient
		return testDB.DbInstance.Get(&state, `SELECT process_state FROM base.meteringpoint WHERE metering_point_id = $1`, meter)
	}
	t.Cleanup(func() { sendActivationMail = orig })

	msg := model.SubscribeMessage{MessageCode: model.EBMS_ONLINE_REG_ANSWER, Protocol: model.EC_REQ_ONL, Tenant: tenant}
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"C%s","sender":"AT003000","messageCode":"%s","ecId":"%s",
		"responseData":[{"meteringPoint":"%s","responseCode":[99]}]}`, meter, model.EBMS_ONLINE_REG_ANSWER, ecId, meter)), &msg.Payload))
	protocolEcReqOnlHandler(context.Background(), msg)
	flushMails(t)

	assert.Equal(t, "activation@example.org", to)
	assert.Equal(t, string(model.PENDING), state)
}

// 261031-mm4: an answer with two metering points carries code 99 twice; each metering point gets
// one consent-request mail, not one per code (four mails in all).
func TestConsentRequestMail_OncePerMeteringPoint(t *testing.T) {
	const tenant, ecId = "TM000204", "AT00300000000TM000204000000000001"
	const meter1, meter2 = "AT0030000000000000000000000204001", "AT0030000000000000000000000204002"
	mailFixture(t, tenant, ecId, meter1, "consent@example.org", "INIT")
	mailFixture(t, tenant, ecId, meter2, "consent@example.org", "INIT")

	var bodies []string
	orig := sendActivationMail
	sendActivationMail = func(_, _, _ string, _ *string, body *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		bodies = append(bodies, body.String())
		return nil
	}
	t.Cleanup(func() { sendActivationMail = orig })

	msg := model.SubscribeMessage{MessageCode: model.EBMS_ONLINE_REG_ANSWER, Protocol: model.EC_REQ_ONL, Tenant: tenant}
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"C%s","sender":"AT003000","messageCode":"%s","ecId":"%s",
		"responseData":[{"meteringPoint":"%s","responseCode":[99]},{"meteringPoint":"%s","responseCode":[99]}]}`,
		meter1, model.EBMS_ONLINE_REG_ANSWER, ecId, meter1, meter2)), &msg.Payload))
	protocolEcReqOnlHandler(context.Background(), msg)
	flushMails(t)

	require.Len(t, bodies, 2)
	all := strings.Join(bodies, "\n")
	assert.Equal(t, 1, strings.Count(all, meter1))
	assert.Equal(t, 1, strings.Count(all, meter2))
}
