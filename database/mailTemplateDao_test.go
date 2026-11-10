package database

import (
	"context"
	"errors"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailTemplateDao(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	const tenant, key = "TE000310", "zp-active"
	require.NoError(t, db.DeleteMailTemplate(ctx, tenant, key))

	none, err := db.GetMailTemplate(ctx, tenant, key)
	require.NoError(t, err)
	assert.Nil(t, none)

	img := func() *model.MailTemplateFile {
		return &model.MailTemplateFile{Id: uuid.New(), Tenant: tenant, MailKey: key, Kind: model.MailFileKindImage,
			Filename: "a.png", MimeType: "image/png", Size: 3, Content: []byte("png")}
	}
	ok := func([]model.MailTemplateFile) error { return nil }
	keepImg, dropImg := img(), img()
	require.NoError(t, db.InsertMailTemplateFile(ctx, keepImg, ok))
	require.NoError(t, db.InsertMailTemplateFile(ctx, dropImg, ok))
	pdf := &model.MailTemplateFile{Id: uuid.New(), Tenant: tenant, MailKey: key, Kind: model.MailFileKindAttachment,
		Filename: "a.pdf", MimeType: "application/pdf", Size: 4, Content: []byte("%PDF")}
	require.NoError(t, db.InsertMailTemplateFile(ctx, pdf, ok))

	// the check sees the existing files and can refuse the upload
	refused := errors.New("limit")
	err = db.InsertMailTemplateFile(ctx, img(), func(existing []model.MailTemplateFile) error {
		assert.Len(t, existing, 3)
		return refused
	})
	assert.ErrorIs(t, err, refused)

	// a stale draft of an abandoned edit
	staleImg := img()
	require.NoError(t, db.InsertMailTemplateFile(ctx, staleImg, ok))
	_, err = testDB.DbInstance.Exec(`UPDATE base.mail_template_file SET created_at = now() - interval '25 hours' WHERE id = $1`, staleImg.Id)
	require.NoError(t, err)
	files, err := db.ListMailTemplateFiles(ctx, tenant, key, false)
	require.NoError(t, err)
	assert.Len(t, files, 3, "a draft older than 24 h is not listed")

	// the check sees all files of the mail and can refuse the save
	_, err = db.SaveMailTemplate(ctx, &model.MailTemplate{Tenant: tenant, MailKey: key, Subject: "S", BodyHtml: "<p>B</p>",
		DefaultVersion: 1, UpdatedBy: "admin"}, 0, nil, func(all []model.MailTemplateFile) error {
		assert.Len(t, all, 4)
		return refused
	})
	assert.ErrorIs(t, err, refused)
	none, err = db.GetMailTemplate(ctx, tenant, key)
	require.NoError(t, err)
	assert.Nil(t, none, "a refused save writes nothing")

	pdfId := pdf.Id
	saved, err := db.SaveMailTemplate(ctx, &model.MailTemplate{Tenant: tenant, MailKey: key, Subject: "S", BodyHtml: "<p>B</p>",
		DefaultVersion: 1, UpdatedBy: "admin", AttachmentFileId: &pdfId}, 0, []string{keepImg.Id}, ok)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.Version)
	assert.Equal(t, 1, saved.NotifiedDefaultVersion)
	assert.Equal(t, "admin", saved.UpdatedBy)
	require.NotNil(t, saved.AttachmentFileId)
	assert.Equal(t, pdf.Id, *saved.AttachmentFileId)

	state := func() map[string]bool {
		all, err := listMailTemplateFiles(ctx, testDB.DbInstance, tenant, key, false, true)
		require.NoError(t, err)
		out := map[string]bool{}
		for _, f := range all {
			out[f.Id] = f.Committed
			assert.Nil(t, f.Content)
		}
		return out
	}
	assert.Equal(t, map[string]bool{keepImg.Id: true, pdf.Id: true, dropImg.Id: false}, state(),
		"used files are committed, a fresh draft of another editor stays, the stale draft is deleted")

	loaded, err := db.GetCommittedMailTemplateFiles(ctx, tenant, key, []string{keepImg.Id, dropImg.Id, "not-a-uuid", pdf.Id, keepImg.Id})
	require.NoError(t, err)
	require.Len(t, loaded, 2, "only committed files, each once")
	for _, f := range loaded {
		assert.NotEmpty(t, f.Content)
	}
	loaded, err = db.GetCommittedMailTemplateFiles(ctx, "TE000399", key, []string{keepImg.Id})
	require.NoError(t, err)
	assert.Empty(t, loaded, "never files of another tenant")

	_, err = db.SaveMailTemplate(ctx, &model.MailTemplate{Tenant: tenant, MailKey: key, Subject: "S2", BodyHtml: "<p>B</p>", DefaultVersion: 1, UpdatedBy: "x"}, 0, nil, ok)
	var conflict *MailTemplateVersionError
	require.True(t, errors.As(err, &conflict))
	assert.Equal(t, 1, conflict.Current)

	// the next version drops the PDF and uses the other image: the old committed files go
	saved, err = db.SaveMailTemplate(ctx, &model.MailTemplate{Tenant: tenant, MailKey: key, Subject: "S2", BodyHtml: "<p>B</p>", DefaultVersion: 1, UpdatedBy: "x"}, 1, []string{dropImg.Id}, ok)
	require.NoError(t, err)
	assert.Equal(t, 2, saved.Version)
	assert.Equal(t, "S2", saved.Subject)
	assert.Nil(t, saved.AttachmentFileId)
	assert.Equal(t, map[string]bool{dropImg.Id: true}, state())
	keepImg = dropImg

	f, err := db.GetMailTemplateFile(ctx, keepImg.Id)
	require.NoError(t, err)
	assert.Equal(t, []byte("png"), f.Content)
	assert.Equal(t, tenant, f.Tenant)

	deleted, err := db.DeleteMailTemplateFile(ctx, "TE000399", key, keepImg.Id)
	require.NoError(t, err)
	assert.False(t, deleted, "a file is only deleted for its own tenant")
	deleted, err = db.DeleteMailTemplateFile(ctx, tenant, key, keepImg.Id)
	require.NoError(t, err)
	assert.False(t, deleted, "a committed file of the saved version is never deleted as a draft")
	draft := img()
	require.NoError(t, db.InsertMailTemplateFile(ctx, draft, ok))
	deleted, err = db.DeleteMailTemplateFile(ctx, tenant, key, draft.Id)
	require.NoError(t, err)
	assert.True(t, deleted)

	// default change notice: notification and claim in one transaction, once
	_, err = testDB.DbInstance.Exec(`UPDATE base.mail_template SET default_version = 0, notified_default_version = 0 WHERE tenant = $1 AND mail_key = $2`, tenant, key)
	require.NoError(t, err)
	countNotices := func() int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.notification WHERE tenant = $1 AND notification::text LIKE '%I_MAIL_TEMPLATE_DEFAULT%'`, tenant))
		return n
	}
	msg := func(string) map[string]interface{} {
		return map[string]interface{}{"type": "Mail", "code": "I_MAIL_TEMPLATE_DEFAULT"}
	}
	// a notification that can not be written leaves the row unclaimed
	_, err = db.NotifyMailTemplateDefaultChange(ctx, key, 1, func(string) map[string]interface{} {
		return map[string]interface{}{"bad": func() {}}
	})
	assert.Error(t, err)
	assert.Equal(t, 0, countNotices())
	claimed, err := db.NotifyMailTemplateDefaultChange(ctx, key, 1, msg)
	require.NoError(t, err)
	assert.Contains(t, claimed, tenant)
	assert.Equal(t, 1, countNotices())
	claimed, err = db.NotifyMailTemplateDefaultChange(ctx, key, 1, msg)
	require.NoError(t, err)
	assert.NotContains(t, claimed, tenant)
	assert.Equal(t, 1, countNotices())

	require.NoError(t, db.DeleteMailTemplate(ctx, tenant, key))
	none, err = db.GetMailTemplate(ctx, tenant, key)
	require.NoError(t, err)
	assert.Nil(t, none)
	files, err = db.ListMailTemplateFiles(ctx, tenant, key, false)
	require.NoError(t, err)
	assert.Empty(t, files, "reset deletes the files too")
}
