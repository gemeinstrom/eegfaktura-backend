package eda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/parser"
	"at.ourproject/vfeeg-backend/services"
	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type capturedMail struct {
	tenant, to, subject string
	cc                  *string
	body                string
}

// captureInactiveMails replaces the mail sender for one test and returns the sent mails.
func captureInactiveMails(t *testing.T, fail error) *[]capturedMail {
	mails := &[]capturedMail{}
	orig := sendInactiveMail
	sendInactiveMail = func(tenant, to, subject string, cc *string, body *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		*mails = append(*mails, capturedMail{tenant, to, subject, cc, body.String()})
		return fail
	}
	t.Cleanup(func() { sendInactiveMail = orig })
	return mails
}

// platform#116: the member gets a mail when a metering point is no longer part of the community.
func TestInactiveMail(t *testing.T) {
	exec := func(q string, args ...interface{}) {
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	// fixed ids and state: the test leaves nothing behind, so it survives -count=2
	t.Cleanup(func() {
		const tenants = `('TI000116', 'GI000116-001', 'GI000116-002')`
		for _, table := range []string{"notification", "processhistory", "metering_partition_factor", "meteringpoint"} {
			exec(`DELETE FROM base.` + table + ` WHERE tenant IN ` + tenants)
		}
		for _, table := range []string{"contactdetail", "address"} {
			exec(`DELETE FROM base.` + table + ` WHERE participant_id IN (SELECT id FROM base.participant WHERE tenant IN ` + tenants + `)`)
		}
		exec(`DELETE FROM base.participant WHERE tenant IN ` + tenants)
	})
	eeg := func(tenant, rc, ecId string) {
		exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name,
			"communityId", street, "streetNumber", city, zip, email)
			VALUES ($1, 'Sonnen-EEG', 'Sonnen-EEG Verein', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3, 'Weg', '1', 'Linz', '4020',
			'office@sonnen-eeg.at') ON CONFLICT (tenant) DO NOTHING`, tenant, rc, ecId)
	}
	member := func(id, tenant, email string) {
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, $2, 'Anna', 'Muster', 'ACTIVE', 'test', 'test')`, id, tenant)
		for _, typ := range []string{"BILLING", "RESIDENCE"} {
			exec(`INSERT INTO base.address (participant_id, type, street, "streetNumber", zip, city)
				VALUES ($1, $2, 'Weg', '1', '4020', 'Linz')`, id, typ)
		}
		if email != "" {
			exec(`INSERT INTO base.contactdetail (participant_id, email) VALUES ($1, $2)`, id, email)
		}
	}
	meter := func(id, participant, tenant, status, consent, direction string) {
		exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
			"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, consent_id)
			VALUES ($1, $2, $3, $4, $5, $5, now(), 'test', '2025-01-01', '2025-01-01', '2999-12-31', $6)`,
			id, participant, tenant, direction, status, consent)
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, tenant, participant_id, "partFact", "createdBy")
			VALUES ($1, $2, $3, 100, 'test')`, id, tenant, participant)
	}

	const ecId = "AT00300000000TI000116000000000001"
	eeg("TI000116", "TI000116", ecId)
	member("a1160000-0000-0000-0000-000000000001", "TI000116", "anna@example.org")
	member("a1160000-0000-0000-0000-000000000002", "TI000116", "")
	const (
		mActive  = "AT0030000000000000000000000116001"
		mInit    = "AT0030000000000000000000000116002"
		mNoMail  = "AT0030000000000000000000000116003"
		mCcms    = "AT0030000000000000000000000116004"
		mGea     = "AT0030000000000000000000000116005"
		mFailing = "AT0030000000000000000000000116006"
	)
	meter(mActive, "a1160000-0000-0000-0000-000000000001", "TI000116", "ACTIVE", "C-ACTIVE", "CONSUMPTION")
	meter(mInit, "a1160000-0000-0000-0000-000000000001", "TI000116", "INIT", "C-INIT", "CONSUMPTION")
	meter(mNoMail, "a1160000-0000-0000-0000-000000000002", "TI000116", "ACTIVE", "C-NOMAIL", "CONSUMPTION")
	meter(mCcms, "a1160000-0000-0000-0000-000000000001", "TI000116", "ACTIVE", "C-CCMS", "GENERATION")
	meter(mFailing, "a1160000-0000-0000-0000-000000000001", "TI000116", "ACTIVE", "C-FAIL", "CONSUMPTION")
	// GEA: one RC number, two tenants, the same member address
	eeg("GI000116-001", "GI000116", "AT00300000000GI000116000000000001")
	eeg("GI000116-002", "GI000116", "AT00300000000GI000116000000000002")
	member("a1160000-0000-0000-0000-000000000003", "GI000116-001", "anna@example.org")
	member("a1160000-0000-0000-0000-000000000004", "GI000116-002", "Anna@Example.org ")
	meter(mGea, "a1160000-0000-0000-0000-000000000003", "GI000116-001", "ACTIVE", "C-GEA", "CONSUMPTION")
	meter(mGea, "a1160000-0000-0000-0000-000000000004", "GI000116-002", "ACTIVE", "C-GEA", "CONSUMPTION")

	responseMsg := func(code model.EbMsMessageType, proto model.EdaProtocol, topic, meterId, consent string, codes string) model.SubscribeMessage {
		msg := model.SubscribeMessage{MessageCode: code, Protocol: proto, Tenant: topic}
		require.NoError(t, json.Unmarshal([]byte(`{"conversationId":"C`+meterId+string(code)+`","sender":"AT003000","receiver":"`+topic+`",
			"messageCode":"`+string(code)+`","ecId":"`+ecId+`","consentEnd":1759269600000,
			"meter":{"meteringPoint":"`+meterId+`","consentId":"`+consent+`"},
			"responseData":[{"meteringPoint":"`+meterId+`","responseCode":[`+codes+`],"consentEnd":1759269600000,"consentId":"`+consent+`"}]}`), &msg.Payload))
		return msg
	}
	// handle runs the handler and waits for the mail worker: mails and their notifications come after it
	handle := func(msg model.SubscribeMessage) {
		protocolCmRevImpHandler(context.Background(), msg)
		flushMails(t)
	}
	notifications := func(tenant string) int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1`, tenant))
		return n
	}
	// lastNotification is the newest notification of the tenant as stored (JSON).
	lastNotification := func(tenant string) string {
		var n string
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT notification FROM base.notification WHERE tenant = $1 ORDER BY id DESC LIMIT 1`, tenant))
		return n
	}

	t.Run("grid operator ends an active metering point: one mail with reason, date, direction, Cc", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "ti000116", mActive, "C-ACTIVE", "1099"))
		require.Len(t, *mails, 1)
		m := (*mails)[0]
		assert.Equal(t, "anna@example.org", m.to)
		assert.Equal(t, "Dein Zählpunkt ist nicht mehr Teil der Energiegemeinschaft", m.subject)
		require.NotNil(t, m.cc)
		assert.Equal(t, "office@sonnen-eeg.at", *m.cc)
		assert.Contains(t, m.body, mActive)
		assert.Contains(t, m.body, "(Bezug)")
		// same conversion as the handler (consentEnd in local time)
		assert.Contains(t, m.body, parser.FormatDate(civil.DateOf(time.UnixMilli(1759269600000))))
		assert.Contains(t, m.body, "Dein Netzbetreiber hat die Datenfreigabe")
		assert.Contains(t, m.body, "Sonnen-EEG")
	})

	t.Run("redelivery of the same message: no second mail", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "ti000116", mActive, "C-ACTIVE", "1099"))
		assert.Empty(t, *mails)
	})

	t.Run("metering point not active yet (INIT): no mail", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMC, model.CM_REV_CUS, "ti000116", mInit, "C-INIT", "1099"))
		assert.Empty(t, *mails)
	})

	t.Run("member ends the data release: one mail with the member sentence", func(t *testing.T) {
		const mMember = "AT0030000000000000000000000116007"
		meter(mMember, "a1160000-0000-0000-0000-000000000001", "TI000116", "ACTIVE", "C-MEMBER", "CONSUMPTION")
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMC, model.CM_REV_CUS, "ti000116", mMember, "C-MEMBER", "1099"))
		require.Len(t, *mails, 1)
		assert.Contains(t, (*mails)[0].body, "Du hast die Datenfreigabe für diesen Zählpunkt beendet")
	})

	t.Run("member without e-mail: revocation applied, no mail", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMC, model.CM_REV_CUS, "ti000116", mNoMail, "C-NOMAIL", "1099"))
		assert.Empty(t, *mails)
		var status string
		require.NoError(t, testDB.DbInstance.Get(&status, `SELECT status FROM base.meteringpoint WHERE metering_point_id = $1`, mNoMail))
		assert.Equal(t, "INACTIVE", status)
	})

	t.Run("EEG deregistration: no mail for its own request, one mail with the confirmation", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMS, model.CM_REV_SP, "ti000116", mCcms, "C-CCMS", "1099"))
		assert.Empty(t, *mails, "AUFHEBUNG_CCMS is the community's own request")
		handle(responseMsg(model.EBMS_ABLEHNUNG_CCMS, model.CM_REV_SP, "ti000116", mCcms, "C-CCMS", "56"))
		assert.Empty(t, *mails, "a rejection sends no mail")
		handle(responseMsg(model.EBMS_ANTWORT_CCMS, model.CM_REV_SP, "ti000116", mCcms, "C-CCMS", "99"))
		assert.Empty(t, *mails, "an answer without 176 sends no mail")
		handle(responseMsg(model.EBMS_ANTWORT_CCMS, model.CM_REV_SP, "ti000116", mCcms, "C-CCMS", "176"))
		require.Len(t, *mails, 1)
		assert.Contains(t, (*mails)[0].body, "Deine Energiegemeinschaft hat diesen Zählpunkt abgemeldet.")
		assert.Contains(t, (*mails)[0].body, "(Einspeisung)")
	})

	t.Run("GEA: the same member in two tenants of one RC number gets one mail", func(t *testing.T) {
		mails := captureInactiveMails(t, nil)
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "gi000116", mGea, "C-GEA", "1099"))
		assert.Len(t, *mails, 1)
	})

	t.Run("mail fails: revocation stays applied, the community gets an error notification", func(t *testing.T) {
		mails := captureInactiveMails(t, errors.New("smtp down"))
		before := notifications("TI000116")
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "ti000116", mFailing, "C-FAIL", "1099"))
		assert.Len(t, *mails, 1)
		var status string
		require.NoError(t, testDB.DbInstance.Get(&status, `SELECT status FROM base.meteringpoint WHERE metering_point_id = $1`, mFailing))
		assert.Equal(t, "INACTIVE", status)
		// one revocation notification plus one mail error notification
		assert.Equal(t, before+2, notifications("TI000116"))
		// a real failure keeps its text
		n := lastNotification("TI000116")
		assert.Contains(t, n, `"message":"smtp down"`)
		assert.NotContains(t, n, "Zeitüberschreitung")
	})

	t.Run("mail server answers too late: the notification says the mail was most likely delivered", func(t *testing.T) {
		const mLate = "AT0030000000000000000000000116008"
		meter(mLate, "a1160000-0000-0000-0000-000000000001", "TI000116", "ACTIVE", "C-LATE", "CONSUMPTION")
		timeout := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
		mails := captureInactiveMails(t, fmt.Errorf("%w (%w)", services.ErrMailUnconfirmed, timeout))
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "ti000116", mLate, "C-LATE", "1099"))
		assert.Len(t, *mails, 1)
		n := lastNotification("TI000116")
		assert.Contains(t, n, `"message":"Zeitüberschreitung – die Mail wurde vermutlich trotzdem verschickt. Vor einem erneuten Versand bitte beim Empfänger nachfragen."`)
		assert.Contains(t, n, `"message_code":"W_MAIL_UNCONFIRMED"`)
		assert.Contains(t, n, `"metering_point":"`+mLate+`"`)
		assert.NotContains(t, n, "DeadlineExceeded")
	})

	t.Run("GEA: a mail the server did not confirm is not sent again for the second tenant", func(t *testing.T) {
		const mGeaLate = "AT0030000000000000000000000116009"
		meter(mGeaLate, "a1160000-0000-0000-0000-000000000003", "GI000116-001", "ACTIVE", "C-GEA-LATE", "CONSUMPTION")
		meter(mGeaLate, "a1160000-0000-0000-0000-000000000004", "GI000116-002", "ACTIVE", "C-GEA-LATE", "CONSUMPTION")
		timeout := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
		mails := captureInactiveMails(t, fmt.Errorf("%w (%w)", services.ErrMailUnconfirmed, timeout))
		handle(responseMsg(model.EBMS_AUFHEBUNG_CCMI, model.CM_REV_IMP, "gi000116", mGeaLate, "C-GEA-LATE", "1099"))
		assert.Len(t, *mails, 1)
	})
}
