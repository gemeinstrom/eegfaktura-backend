package mailtemplate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

type notice struct {
	tenant  string
	typ     model.NotificationType
	role    string
	message string
}

type storedFile struct {
	model.MailTemplateFile
	draft bool
}

type fakeStore struct {
	templates map[string]*model.MailTemplate
	files     []storedFile
	requested []string
	loadErr   error
	notices   []notice
	claimed   map[string][]string
	noticeErr error
}

func (s *fakeStore) GetMailTemplate(_ context.Context, tenant, key string) (*model.MailTemplate, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.templates[tenant+"/"+key], nil
}

func (s *fakeStore) GetCommittedMailTemplateFiles(_ context.Context, _, _ string, ids []string) ([]model.MailTemplateFile, error) {
	s.requested = append(s.requested, ids...)
	var out []model.MailTemplateFile
	for _, f := range s.files {
		for _, id := range ids {
			if f.Id == id && !f.draft {
				out = append(out, f.MailTemplateFile)
			}
		}
	}
	return out, nil
}

func (s *fakeStore) SaveNotificationFromMap(v map[string]interface{}, tenant string, typ model.NotificationType, _ model.NotificationProcess, role string) error {
	b, _ := json.Marshal(v)
	s.notices = append(s.notices, notice{tenant, typ, role, string(b)})
	return nil
}

func (s *fakeStore) NotifyMailTemplateDefaultChange(_ context.Context, key string, _ int, notification func(string) map[string]interface{}) ([]string, error) {
	if s.noticeErr != nil {
		return nil, s.noticeErr
	}
	for _, tenant := range s.claimed[key] {
		b, _ := json.Marshal(notification(tenant))
		s.notices = append(s.notices, notice{tenant, model.N_TYPE_NOTIFICATION, "ADMIN", string(b)})
	}
	return s.claimed[key], nil
}

type sentMail struct {
	tenant, to, subject string
	cc                  *string
	body                string
	inline              []*services.Attachment
	attachment          *services.Attachment
}

func capture(sent *[]sentMail) services.SendMailFunc {
	return func(tenant, to, subject string, cc *string, body *bytes.Buffer, inline []*services.Attachment, att *services.Attachment) error {
		*sent = append(*sent, sentMail{tenant, to, subject, cc, body.String(), inline, att})
		return nil
	}
}

func TestSendStored_UsesCustomisedVersion(t *testing.T) {
	store := &fakeStore{templates: map[string]*model.MailTemplate{
		"TE000300/zp-active": {Tenant: "TE000300", MailKey: KeyZpActive, Version: 2,
			Subject:  `Aktiv: {meteringPoint}`,
			BodyHtml: `<p>Servus <span data-ph="member.firstName">Vorname</span></p><img src="mailfile:` + ownImage + `">`},
	}, files: []storedFile{
		{MailTemplateFile: model.MailTemplateFile{Id: ownImage, Tenant: "TE000300", Kind: model.MailFileKindImage, Filename: "x.png", MimeType: "image/png", Content: []byte("png")}},
		{MailTemplateFile: model.MailTemplateFile{Id: "unused", Tenant: "TE000300", Kind: model.MailFileKindImage, Content: []byte("big")}},
	}}
	var sent []sentMail
	cc := "eeg@example.org"
	handled, err := SendStored(context.Background(), store, capture(&sent), Lookup(KeyZpActive), "TE000300",
		"maria@example.org", &cc, &Data{FirstName: "Maria", MeteringPoints: []string{"AT1"}}, "AT1")
	require.NoError(t, err)
	assert.True(t, handled)
	require.Len(t, sent, 1)
	assert.Equal(t, "Aktiv: AT1", sent[0].subject)
	assert.Equal(t, "maria@example.org", sent[0].to)
	assert.Equal(t, &cc, sent[0].cc)
	assert.Contains(t, sent[0].body, "<p>Servus Maria</p>")
	assert.Contains(t, sent[0].body, `src="cid:`+ownImage+`"`)
	require.Len(t, sent[0].inline, 1)
	assert.Nil(t, sent[0].attachment)
	assert.Empty(t, store.notices)
	assert.ElementsMatch(t, []string{ownImage}, store.requested, "only the referenced files are loaded")
}

func TestSendStored_NoCustomisedVersion(t *testing.T) {
	var sent []sentMail
	for _, store := range []*fakeStore{{}, {loadErr: errors.New("db down")}} {
		handled, err := SendStored(context.Background(), store, capture(&sent), Lookup(KeyZpActive), "TE000300", "m@example.org", nil, &Data{}, "AT1")
		assert.NoError(t, err)
		assert.False(t, handled)
		assert.Empty(t, store.notices)
	}
	assert.Empty(t, sent)
}

func TestSendStored_BrokenVersionFallsBackWithNotification(t *testing.T) {
	broken := map[string]*model.MailTemplate{
		// a placeholder the catalog does not (or no longer) know
		"TE000300/zp-active": {Tenant: "TE000300", MailKey: KeyZpActive, Subject: "x", BodyHtml: `<p><span data-ph="member.iban">x</span></p>`},
	}
	png := model.MailTemplateFile{Id: ownImage, Tenant: "TE000300", Kind: model.MailFileKindImage, Content: []byte("png")}
	bigPng := png
	bigPng.Content = make([]byte, 7*1024*1024) // stored files are not limited per image here
	withImage := map[string]*model.MailTemplate{
		"TE000300/zp-active": {Tenant: "TE000300", MailKey: KeyZpActive, Subject: "x", BodyHtml: `<p>x</p><img src="mailfile:` + ownImage + `">`}}
	stored := func(body string, attachment *string) map[string]*model.MailTemplate {
		return map[string]*model.MailTemplate{
			"TE000300/zp-active": {Tenant: "TE000300", MailKey: KeyZpActive, Subject: "x", BodyHtml: body, AttachmentFileId: attachment}}
	}
	var fourImages string
	var four []storedFile
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444"} {
		fourImages += `<img src="mailfile:` + id + `">`
		f := png
		f.Id = id
		four = append(four, storedFile{MailTemplateFile: f})
	}
	pdf := []storedFile{{MailTemplateFile: model.MailTemplateFile{Id: "pdf", Tenant: "TE000300", Kind: model.MailFileKindAttachment, Content: []byte("%PDF")}}}
	for name, store := range map[string]*fakeStore{
		"unknown placeholder": {templates: broken},
		"too large":           {templates: withImage, files: []storedFile{{MailTemplateFile: bigPng}}},
		// saved before the link rules and limits of 2026-10-26
		"shortener link":                {templates: stored(`<p><a href="https://bit.ly/x">Portal</a></p>`, nil)},
		"four links":                    {templates: stored(strings.Repeat(`<a href="https://eeg.at">x</a>`, 4), nil)},
		"four images":                   {templates: stored(`<p>x</p>`+fourImages, nil), files: four},
		"pdf attachment":                {templates: stored(`<p>x</p>`, strPtr("pdf")), files: pdf},
		"referenced image missing":      {templates: withImage},
		"referenced image only a draft": {templates: withImage, files: []storedFile{{MailTemplateFile: png, draft: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []sentMail
			handled, err := SendStored(context.Background(), store, capture(&sent), Lookup(KeyZpActive), "TE000300", "m@example.org", nil, &Data{}, "AT1")
			assert.NoError(t, err)
			assert.False(t, handled, "the caller sends the default")
			assert.Empty(t, sent)
			require.Len(t, store.notices, 1)
			n := store.notices[0]
			assert.Equal(t, "TE000300", n.tenant, "the notification goes to the tenant, not the ecId")
			assert.Equal(t, model.N_TYPE_ERROR, n.typ)
			assert.Equal(t, "ADMIN", n.role)
			assert.Contains(t, n.message, `"type":"Mail"`)
			assert.Contains(t, n.message, "Zählpunkt aktiv")
			assert.NotContains(t, n.message, "image ", "internal details stay in the log")
			if name == "shortener link" {
				assert.Contains(t, n.message, "Links zu Kurz-Link-Diensten sind nicht erlaubt: bit.ly", "the community reads why")
			}
		})
	}
}

func TestNotifyDefaultChanges(t *testing.T) {
	store := &fakeStore{claimed: map[string][]string{KeyConsentRequest: {"TE000300", "TE000301"}}}
	NotifyDefaultChanges(context.Background(), store)
	require.Len(t, store.notices, 2)
	assert.Equal(t, "TE000300", store.notices[0].tenant)
	assert.Equal(t, model.N_TYPE_NOTIFICATION, store.notices[0].typ)
	assert.Contains(t, store.notices[0].message, "Freigabe-Aufforderung")

	// a failing store is logged, not fatal
	NotifyDefaultChanges(context.Background(), &fakeStore{noticeErr: errors.New("db down")})
}

func TestFallbackReason(t *testing.T) {
	def := Lookup(KeyZpActive)
	assert.Equal(t, "Unbekannter Platzhalter: x", fallbackReason(def, &ValidationError{Field: "bodyHtml", Message: "Unbekannter Platzhalter: x"}))
	assert.Equal(t, "Die Dateien der Mail sind zusammen größer als 6 MB.", fallbackReason(def, fmt.Errorf("%w: 7 bytes", ErrTooLarge)))
	generic := fallbackReason(def, errors.New(`pq: relation "base.mail_template_file" does not exist`))
	assert.NotContains(t, generic, "pq:")
	assert.Contains(t, generic, "nicht verarbeitet")
}
