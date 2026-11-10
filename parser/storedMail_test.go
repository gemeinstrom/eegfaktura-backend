package parser

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/config/configtest"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

type storeStub struct {
	template *model.MailTemplate
	notices  []string
}

func (s *storeStub) GetMailTemplate(context.Context, string, string) (*model.MailTemplate, error) {
	return s.template, nil
}
func (s *storeStub) GetCommittedMailTemplateFiles(context.Context, string, string, []string) ([]model.MailTemplateFile, error) {
	return nil, nil
}
func (s *storeStub) SaveNotificationFromMap(_ map[string]interface{}, tenant string, _ model.NotificationType, _ model.NotificationProcess, _ string) error {
	s.notices = append(s.notices, tenant)
	return nil
}

func storedMailFixture() (*model.Eeg, *model.EegParticipant) {
	eeg := &model.Eeg{Id: "TE000300", Name: "TE-EEG", Description: "TEST EEG", Contact: model.Contact{Email: null.StringFrom("eeg@example.org")}}
	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{FirstName: "Maria",
			MeteringPoint: []*model.MeteringPoint{{MeteringPoint: "AT0030000000000000000000000012345"}}},
		Contact: model.ContactInfo{Email: null.StringFrom("maria@example.org")},
	}
	return eeg, participant
}

func TestActivationMail_UsesStoredTemplate(t *testing.T) {
	configtest.Set(t, "file-content.templates", t.TempDir())
	eeg, participant := storedMailFixture()
	store := &storeStub{template: &model.MailTemplate{Tenant: "TE000300", MailKey: "consent-request", Version: 1,
		Subject: "Bitte freigeben", BodyHtml: `<p>Liebe <span data-ph="member.firstName">Vorname</span>, Zählpunkt <span data-ph="meteringPoint">x</span></p>`}}

	var subject, body string
	var cc *string
	send := func(tenant, to, s string, c *string, b *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		subject, body, cc = s, b.String(), c
		return nil
	}
	require.NoError(t, SendActivationMailFromTemplate(send, store, "TE000300", "Aktivierung im Serviceportal", eeg, participant, "activation-mail-template.toml"))
	assert.Equal(t, "Bitte freigeben", subject)
	assert.Contains(t, body, "<p>Liebe Maria, Zählpunkt AT0030000000000000000000000012345</p>")
	require.NotNil(t, cc)
	assert.Equal(t, "eeg@example.org", *cc)
	assert.Empty(t, store.notices)
}

// A stored version that can not be rendered must never cost the member the mail:
// the legacy default goes out and the community gets a notification.
func TestActivationMail_BrokenStoredTemplateFallsBackToDefault(t *testing.T) {
	configtest.Set(t, "file-content.templates", t.TempDir())
	eeg, participant := storedMailFixture()
	store := &storeStub{template: &model.MailTemplate{Tenant: "TE000300", MailKey: "zp-active", Version: 1,
		Subject: "x", BodyHtml: `<p><span data-ph="no.such.placeholder">x</span></p>`}}

	var subject, body string
	send := func(tenant, to, s string, _ *string, b *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		subject, body = s, b.String()
		return nil
	}
	require.NoError(t, SendActivationMailFromTemplate(send, store, "TE000300", "Dein Zählpunkt ist aktiv", eeg, participant, "zp-complete-mail-template.toml"))
	assert.Equal(t, "Dein Zählpunkt ist aktiv", subject)
	assert.Contains(t, body, "wurde erfolgreich aktiviert")
	assert.Equal(t, []string{"TE000300"}, store.notices)
}

func TestInactiveMail_UsesStoredTemplate(t *testing.T) {
	configtest.Set(t, "file-content.templates", t.TempDir())
	eeg, participant := storedMailFixture()
	store := &storeStub{template: &model.MailTemplate{Tenant: "TE000300", MailKey: "zp-inactive", Version: 1,
		Subject: "Inaktiv", BodyHtml: `<p><span data-ph="meteringPoint">x</span> <span data-ph="direction">x</span> <span data-ph="endDate">x</span> <span data-ph="reason">x</span></p>`}}
	var body string
	send := func(tenant, to, s string, _ *string, b *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		body = b.String()
		return nil
	}
	require.NoError(t, SendMeteringPointInactiveMail(send, store, "TE000300", InactiveMailData{
		Eeg: eeg, Participant: participant, MeteringPoint: "AT1", Direction: "Bezug", EndDate: "01.02.2026", Reason: "Weil."}))
	assert.Contains(t, body, "<p>AT1 Bezug 01.02.2026 Weil.</p>")
}

// Hand-made templates on the volume (per tenant and global) are ignored: a community without a saved
// version gets the built-in default for every member mail.
func TestMemberMails_IgnoreVolumeTemplates(t *testing.T) {
	vol := t.TempDir()
	for _, dir := range []string{filepath.Join(vol, "TE000300", "templates"), filepath.Join(vol, "templates")} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for _, cfg := range []string{"activation-mail-template.toml", "zp-complete-mail-template.toml", InactiveMailTemplate} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, cfg), []byte(`TemplateFile="t.html"`), 0o644))
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "t.html"), []byte(`<p>VOLUME {{.Eeg.Name}}</p>`), 0o644))
	}
	configtest.Set(t, "file-content.templates", vol)
	assert.Len(t, IgnoredVolumeTemplates(vol), 6, "listed for the startup warning")

	eeg, participant := storedMailFixture()
	store := &storeStub{} // no saved version
	var body string
	var inline int
	send := func(_, _, _ string, _ *string, b *bytes.Buffer, in []*services.Attachment, _ *services.Attachment) error {
		body, inline = b.String(), len(in)
		return nil
	}
	reset := func() { body, inline = "", -1 }
	for cfg, want := range map[string]string{
		"activation-mail-template.toml":  "damit dein Zählpunkt tatsächlich Energie",
		"zp-complete-mail-template.toml": "wurde erfolgreich aktiviert",
	} {
		reset()
		require.NoError(t, SendActivationMailFromTemplate(send, store, "TE000300", "s", eeg, participant, cfg))
		assert.NotContains(t, body, "VOLUME", cfg)
		assert.Contains(t, body, want, cfg)
		assert.Equal(t, 0, inline, cfg+": no logo since default version 2")
	}
	reset()
	require.NoError(t, SendMeteringPointInactiveMail(send, store, "TE000300", InactiveMailData{
		Eeg: eeg, Participant: participant, MeteringPoint: "AT1", Direction: "Bezug", EndDate: "01.02.2026", Reason: "Weil."}))
	assert.NotContains(t, body, "VOLUME")
	assert.Contains(t, body, "nicht mehr Teil")
	assert.GreaterOrEqual(t, inline, 0, "the inactive mail was sent")
	assert.Empty(t, store.notices)
}

// Every member mail ends with the fixed footer: the built-in default (no saved text) and a saved text.
func TestMemberMails_FixedFooter(t *testing.T) {
	configtest.Set(t, "file-content.templates", t.TempDir())
	eeg, participant := storedMailFixture()
	var body string
	send := func(_, _, _ string, _ *string, b *bytes.Buffer, _ []*services.Attachment, _ *services.Attachment) error {
		body = b.String()
		return nil
	}
	saved := &model.MailTemplate{Tenant: "TE000300", MailKey: "zp-active", Version: 1, Subject: "S", BodyHtml: "<p>Eigener Text</p>"}
	for _, store := range []*storeStub{{}, {template: saved}} {
		for _, cfg := range []string{"activation-mail-template.toml", "zp-complete-mail-template.toml"} {
			body = ""
			require.NoError(t, SendActivationMailFromTemplate(send, store, "TE000300", "s", eeg, participant, cfg))
			assert.Equal(t, 1, strings.Count(body, mailtemplate.FixedFooterHtml), cfg)
			assert.Contains(t, body, mailtemplate.FixedFooterHtml+"\n</body>", cfg+": before </body>")
		}
		body = ""
		require.NoError(t, SendMeteringPointInactiveMail(send, store, "TE000300", InactiveMailData{
			Eeg: eeg, Participant: participant, MeteringPoint: "AT1", Direction: "Bezug", EndDate: "01.02.2026", Reason: "Weil."}))
		assert.Equal(t, 1, strings.Count(body, mailtemplate.FixedFooterHtml), "inactive")
		assert.Contains(t, body, mailtemplate.FixedFooterHtml+"\n</body>", "inactive")
	}
}
