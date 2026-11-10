package eda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/jjeffery/civil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#238: a deregistration by the community (AUFHEBUNG_CCMS) leaves the metering point as it is; the grid
// operator's answer decides. 176, and the rejections 177/180, set it INACTIVE since the consent end of the request;
// any other answer leaves it. The member gets the mail on 176 only.
func TestRevokeOnlyAfterAnswer(t *testing.T) {
	requested := civil.DateFor(2026, 10, 15)
	requestedMs := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local).UnixMilli()

	var mails atomic.Int32
	orig := sendInactiveMail
	sendInactiveMail = func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		mails.Add(1)
		return nil
	}
	t.Cleanup(func() { sendInactiveMail = orig })

	// the same metering point number in another community must stay untouched by every answer
	const other = "TR238099"
	ecId := func(tenant string) string { return "AT00300000000" + tenant + "000000000001" }
	const meter = "AT0030000000000000000000000238001"
	inboundFixture(t, other, other, ecId(other), meter, 238099, 1)

	handle := func(tenant string, code model.EbMsMessageType, conversation, body string) {
		msg := model.SubscribeMessage{MessageCode: code, Protocol: model.CM_REV_SP, Tenant: strings.ToLower(tenant)}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"%s","sender":"AT003000","receiver":"%s",
			"messageCode":"%s","ecId":"%s",%s}`, conversation, tenant, code, ecId(tenant), body)), &msg.Payload))
		protocolCmRevImpHandler(context.Background(), msg)
		flushMails(t)
	}
	// request is eda-xp's echo of the sent deregistration
	request := func(tenant, conversation string) {
		handle(tenant, model.EBMS_AUFHEBUNG_CCMS, conversation, fmt.Sprintf(
			`"meter":{"meteringPoint":"%s","consentId":"C-S4"},"consentEnd":%d`, meter, requestedMs))
	}
	// answer as eda-xp delivers it: codes in responseData, consentEnd copied from the stored request (0: none)
	answer := func(tenant string, code model.EbMsMessageType, conversation, codes string, consentEnd int64) {
		body := fmt.Sprintf(`"responseData":[{"meteringPoint":"%s","responseCode":[%s],"consentId":"C-S4"}]`, meter, codes)
		if consentEnd != 0 {
			body += fmt.Sprintf(`,"consentEnd":%d`, consentEnd)
		}
		handle(tenant, code, conversation, body)
	}
	type state struct{ status, process, inactiveSince string }
	stateOf := func(tenant string) state {
		var s state
		require.NoError(t, testDB.DbInstance.QueryRow(`SELECT status, process_state, to_char(inactivesince, 'YYYY-MM-DD')
			FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter).
			Scan(&s.status, &s.process, &s.inactiveSince))
		return s
	}
	unchanged := state{"ACTIVE", "INIT", "2999-12-31"}
	inactiveSince := func(d civil.Date) state { return state{"INACTIVE", "INACTIVE", d.String()} }
	// inactiveToday expects the day the answer arrives; assertState accepts any day from "from" (taken before the
	// handler ran) to the end of the check, so a run across midnight passes
	const today = "today"
	inactiveToday := state{"INACTIVE", "INACTIVE", today}
	assertState := func(t *testing.T, want, got state, from civil.Date) {
		t.Helper()
		if want.inactiveSince == today {
			assert.True(t, got.inactiveSince >= from.String() && got.inactiveSince <= civil.Today().String(),
				"inactive since %s, expected the day of the answer", got.inactiveSince)
			want.inactiveSince = got.inactiveSince
		}
		assert.Equal(t, want, got)
	}
	// lastNotification is the type and codes of the newest notification of the tenant
	lastNotification := func(tenant string) (string, []string) {
		var raw string
		require.NoError(t, testDB.DbInstance.Get(&raw, `SELECT notification FROM base.notification WHERE tenant = $1
			ORDER BY id DESC LIMIT 1`, tenant))
		var n struct {
			Type          string   `json:"type"`
			ResponseCodes []string `json:"responseCodes"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &n))
		return n.Type, n.ResponseCodes
	}
	codeTexts := func(codes string) []string {
		texts := []string{}
		for _, c := range strings.Split(codes, ",") {
			if c == "" {
				continue
			}
			n, err := strconv.Atoi(c)
			require.NoError(t, err)
			texts = append(texts, ECON_RESPONSE_CODES[int16(n)])
		}
		return texts
	}

	cases := []struct {
		name       string
		code       model.EbMsMessageType
		codes      string
		noRequest  bool  // the request is missing from the history
		want       state // after the answer
		notifies   bool
		mailsAfter int32
	}{
		{"confirmation 176", model.EBMS_ANTWORT_CCMS, "176", false, inactiveSince(requested), true, 1},
		{"received 99", model.EBMS_ANTWORT_CCMS, "99", false, unchanged, false, 0},
		{"rejection 177 (no data release)", model.EBMS_ABLEHNUNG_CCMS, "177", false, inactiveSince(requested), true, 0},
		{"rejection 180 (consent expired)", model.EBMS_ABLEHNUNG_CCMS, "180", false, inactiveSince(requested), true, 0},
		{"rejection 82 (process date wrong)", model.EBMS_ABLEHNUNG_CCMS, "82", false, unchanged, true, 0},
		{"rejection 187 (consent and metering point do not match)", model.EBMS_ABLEHNUNG_CCMS, "187", false, unchanged, true, 0},
		{"rejection with another code", model.EBMS_ABLEHNUNG_CCMS, "37", false, unchanged, true, 0},
		{"rejection without code", model.EBMS_ABLEHNUNG_CCMS, "", false, unchanged, true, 0},
		{"176 without the request in the history and without a date in the answer: the day of the answer", model.EBMS_ANTWORT_CCMS, "176", true, inactiveToday, true, 1},
		{"177 without the request in the history and without a date in the answer: the day of the answer", model.EBMS_ABLEHNUNG_CCMS, "177", true, inactiveToday, true, 0},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tenant := fmt.Sprintf("TR2380%02d", i)
			conversation := "RC238" + tenant
			inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238000+i, 1)
			mails.Store(0)

			if !c.noRequest {
				request(tenant, conversation)
				assert.Equal(t, unchanged, stateOf(tenant), "sending the deregistration keeps the metering point")
				assert.Equal(t, 1, historyCount(t, tenant, conversation))
				typ, _ := lastNotification(tenant)
				assert.Equal(t, string(model.EBMS_AUFHEBUNG_CCMS), typ)
			}
			before := notificationCount(t, tenant)
			logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })

			// eda-xp copies the request's consent end into the answer; without the request it has none
			consentEnd := requestedMs
			if c.noRequest {
				consentEnd = 0
			}
			from := civil.Today()
			answer(tenant, c.code, conversation, c.codes, consentEnd)

			assertState(t, c.want, stateOf(tenant), from)
			assert.Equal(t, unchanged, stateOf(other), "the other community keeps its metering point")
			assert.Equal(t, c.mailsAfter, mails.Load())
			if c.notifies {
				require.Equal(t, before+1, notificationCount(t, tenant))
				typ, codes := lastNotification(tenant)
				assert.Equal(t, string(c.code), typ)
				assert.Equal(t, codeTexts(c.codes), codes)
			} else {
				assert.Equal(t, before, notificationCount(t, tenant))
			}
			if c.noRequest {
				assert.Equal(t, 1, historyCount(t, tenant, conversation))
				assert.Contains(t, logged.String(), "event.action=eda.revoke.request_not_found")
			} else {
				assert.Equal(t, 2, historyCount(t, tenant, conversation))
			}
			if c.want != unchanged {
				out := logged.String()
				assert.Contains(t, out, "event.action=eda.revoke.inactive")
				assert.Contains(t, out, "(conversation "+conversation+")")
				assert.NotContains(t, out, meter, "no metering point in the status log")
			}
		})
	}

	t.Run("the same rejection 177 twice: no error, stays inactive", func(t *testing.T) {
		const tenant, conversation = "TR238050", "RC238TR238050"
		inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238050, 1)
		request(tenant, conversation)
		answer(tenant, model.EBMS_ABLEHNUNG_CCMS, conversation, "177", requestedMs)
		answer(tenant, model.EBMS_ABLEHNUNG_CCMS, conversation, "177", requestedMs)
		assert.Equal(t, inactiveSince(requested), stateOf(tenant))
		assert.Equal(t, 3, historyCount(t, tenant, conversation))
	})

	t.Run("answer with another date than the request: the requested date, the difference is logged", func(t *testing.T) {
		const tenant, conversation = "TR238051", "RC238TR238051"
		inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238051, 1)
		request(tenant, conversation)
		logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation, "176",
			time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local).UnixMilli())
		assert.Equal(t, inactiveSince(requested), stateOf(tenant))
		assert.Contains(t, logged.String(), "event.action=eda.revoke.date_mismatch")
	})

	// fallback chain of the date: the request in the history, the consent end of the answer, the day of the answer
	later := civil.DateFor(2026, 10, 20)
	laterMs := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local).UnixMilli()
	fallbacks := []struct {
		name, tenant string
		echo         string // body of the request echo, "" for none
		answerEnd    int64
		want         state
		event        string
	}{
		{"no request in the history: the consent end of the answer", "TR238052", "", laterMs, inactiveSince(later),
			"eda.revoke.request_not_found"},
		{"a request of another metering point in the conversation: the consent end of the answer", "TR238056",
			fmt.Sprintf(`"meter":{"meteringPoint":"AT0030000000000000000000000238999","consentId":"C-X"},"consentEnd":%d`, requestedMs),
			laterMs, inactiveSince(later), "eda.revoke.request_not_found"},
		{"a request without consent end: the consent end of the answer", "TR238057",
			fmt.Sprintf(`"meter":{"meteringPoint":"%s","consentId":"C-S4"}`, meter), laterMs, inactiveSince(later),
			"eda.revoke.request_without_end"},
		{"a request without consent end, an answer without one: the day of the answer", "TR238058",
			fmt.Sprintf(`"meter":{"meteringPoint":"%s","consentId":"C-S4"}`, meter), 0, inactiveToday,
			"eda.revoke.request_without_end"},
	}
	for i, c := range fallbacks {
		t.Run(c.name, func(t *testing.T) {
			conversation := "RC238" + c.tenant
			inboundFixture(t, c.tenant, c.tenant, ecId(c.tenant), meter, 238056+i, 1)
			if c.echo != "" {
				handle(c.tenant, model.EBMS_AUFHEBUNG_CCMS, conversation, c.echo)
				require.Equal(t, 1, historyCount(t, c.tenant, conversation))
			}
			logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
			from := civil.Today()
			answer(c.tenant, model.EBMS_ABLEHNUNG_CCMS, conversation, "180", c.answerEnd)
			assertState(t, c.want, stateOf(c.tenant), from)
			assert.Contains(t, logged.String(), "event.action="+c.event)
		})
	}

	t.Run("the history can not be read: the consent end of the answer, else the day; not reported as not found", func(t *testing.T) {
		db, err := database.GetDB(context.Background())
		require.NoError(t, err)
		msg := model.SubscribeMessage{MessageCode: model.EBMS_ANTWORT_CCMS,
			Payload: model.EbmsMessage{ConversationId: "RC238FAIL", ConsentEnd: laterMs}}
		logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
		assert.Equal(t, later, revokeInactiveSince(context.Background(), failingHistory{db}, "TR238059", msg, meter))
		msg.Payload.ConsentEnd = 0
		from := civil.Today()
		got := revokeInactiveSince(context.Background(), failingHistory{db}, "TR238059", msg, meter)
		assertState(t, inactiveToday, inactiveSince(got), from)
		assert.Contains(t, logged.String(), "event.action=eda.revoke.history_lookup_failed")
		assert.NotContains(t, logged.String(), "request_not_found")
	})

	// a rejection that also carries a <meter> element: the reason codes still come from ResponseData, not the 99 of
	// the request echo
	rejectionWithMeter := []struct {
		name, tenant, codes string
		want                state
	}{
		{"rejection 177 with a meter element: inactive", "TR238053", "177", inactiveSince(requested)},
		{"rejection 187 with a meter element: stays, notified with the code", "TR238054", "187", unchanged},
		{"rejection with a meter element and no ResponseData: stays, no reason", "TR238055", "", unchanged},
	}
	for i, c := range rejectionWithMeter {
		t.Run(c.name, func(t *testing.T) {
			conversation := "RC238" + c.tenant
			inboundFixture(t, c.tenant, c.tenant, ecId(c.tenant), meter, 238053+i, 1)
			request(c.tenant, conversation)
			body := fmt.Sprintf(`"meter":{"meteringPoint":"%s","consentId":"C-S4"},"consentEnd":%d`, meter, requestedMs)
			if c.codes != "" {
				body += fmt.Sprintf(`,"responseData":[{"meteringPoint":"%s","responseCode":[%s],"consentId":"C-S4"}]`, meter, c.codes)
			}
			before := notificationCount(t, c.tenant)
			handle(c.tenant, model.EBMS_ABLEHNUNG_CCMS, conversation, body)
			assert.Equal(t, c.want, stateOf(c.tenant))
			require.Equal(t, before+1, notificationCount(t, c.tenant))
			typ, codes := lastNotification(c.tenant)
			assert.Equal(t, string(model.EBMS_ABLEHNUNG_CCMS), typ)
			assert.Equal(t, codeTexts(c.codes), codes)
		})
	}

	// only an ACTIVE metering point is set inactive: the first end of the participation keeps its date
	ccmc := func(tenant string, consentEndMs int64) {
		msg := model.SubscribeMessage{MessageCode: model.EBMS_AUFHEBUNG_CCMC, Protocol: model.CM_REV_CUS, Tenant: strings.ToLower(tenant)}
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"conversationId":"CCMC%s","sender":"AT003000",
			"receiver":"%s","messageCode":"AUFHEBUNG_CCMC","responseData":[{"meteringPoint":"%s","responseCode":[1099],
			"consentEnd":%d,"consentId":"C-S4"}]}`, tenant, tenant, meter, consentEndMs)), &msg.Payload))
		protocolCmRevImpHandler(context.Background(), msg)
		flushMails(t)
	}

	t.Run("176 twice, then a second deregistration answered later: one mail, the first date stays", func(t *testing.T) {
		const tenant, conversation = "TR238060", "RC238TR238060"
		inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238060, 1)
		mails.Store(0)
		request(tenant, conversation)
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation, "176", requestedMs)
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation, "176", requestedMs)
		handle(tenant, model.EBMS_AUFHEBUNG_CCMS, conversation+"B", fmt.Sprintf(
			`"meter":{"meteringPoint":"%s","consentId":"C-S4"},"consentEnd":%d`, meter, laterMs))
		logged := logtest.Capture(t, func(w io.Writer) { logrus.SetOutput(w) })
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation+"B", "176", laterMs)
		assert.Equal(t, inactiveSince(requested), stateOf(tenant))
		assert.Equal(t, int32(1), mails.Load())
		assert.Contains(t, logged.String(), "event.action=eda.revoke.unchanged")
	})

	for i, c := range []struct{ name, codes string }{
		{"CCMC first, then 177: the date of the CCMC stays", "177"},
		{"CCMC first, then 176: the date of the CCMC stays, no second mail", "176"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tenant := fmt.Sprintf("TR23807%d", i)
			conversation := "RC238" + tenant
			inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238070+i, 1)
			request(tenant, conversation)
			ccmc(tenant, time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local).UnixMilli())
			// the CCMC sets the status, it leaves the process state of the fixture (INIT)
			afterCcmc := state{"INACTIVE", "INIT", "2026-10-01"}
			require.Equal(t, afterCcmc, stateOf(tenant))
			mails.Store(0)
			code := model.EBMS_ABLEHNUNG_CCMS
			if c.codes == "176" {
				code = model.EBMS_ANTWORT_CCMS
			}
			answer(tenant, code, conversation, c.codes, requestedMs)
			assert.Equal(t, afterCcmc, stateOf(tenant))
			assert.Equal(t, int32(0), mails.Load())
		})
	}

	// A re-registration after a CCMC: during it the point is INACTIVE (process INIT … APPROVED), ABSCHLUSS_ECON sets it
	// ACTIVE with the new activesince. A late answer to the old deregistration leaves the new participation either way.
	t.Run("late answer after a re-registration: the new participation stays", func(t *testing.T) {
		const tenant, conversation = "TR238080", "RC238TR238080"
		inboundFixture(t, tenant, tenant, ecId(tenant), meter, 238080, 1)
		request(tenant, conversation)
		ccmc(tenant, time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local).UnixMilli())
		_, err := testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET status = 'INACTIVE', process_state = 'PENDING'
			WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter)
		require.NoError(t, err)
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation, "176", requestedMs)
		assert.Equal(t, state{"INACTIVE", "PENDING", "2026-10-01"}, stateOf(tenant), "during the re-registration")

		_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET status = 'ACTIVE', process_state = 'ACTIVE',
			activesince = '2026-11-01', inactivesince = '2999-12-31' WHERE tenant = $1 AND metering_point_id = $2`, tenant, meter)
		require.NoError(t, err)
		mails.Store(0)
		answer(tenant, model.EBMS_ANTWORT_CCMS, conversation, "176", requestedMs)
		assert.Equal(t, state{"ACTIVE", "ACTIVE", "2999-12-31"}, stateOf(tenant), "after the re-registration")
		assert.Equal(t, int32(0), mails.Load())
	})
}

// failingHistory is a database whose process history can not be read.
type failingHistory struct{ database.Database }

func (failingHistory) FindHistoryMessage(context.Context, string, string, model.EbMsMessageType) (*model.EbmsMessage, error) {
	return nil, errors.New("connection refused")
}
