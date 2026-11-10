package api

import (
	"context"
	"net/http"
	"testing"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An image the text references must exist when saving: an old draft is kept and
// committed, a vanished one is a 400 instead of being dropped silently.
func TestMailTemplates_SaveReferencedImages(t *testing.T) {
	c := newMailTemplateClient(t)
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	upload := func() string {
		w := c.upload("admin", mtTenant, "zp-active", "image", "a.png", logoPNG(t))
		require.Equal(t, http.StatusCreated, w.Code)
		return decode[map[string]interface{}](t, w)["id"].(string)
	}
	old, gone := upload(), upload()
	_, err = execTestSQL(context.Background(), db,
		`UPDATE base.mail_template_file SET created_at = now() - interval '25 hours' WHERE id = $1`, old)
	require.NoError(t, err)

	w := c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + old + `">`, "version": 0})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	saved := decode[mtDTO](t, w)
	assert.Contains(t, saved.BodyHtml, "mailfile:"+old)
	found := false
	for _, f := range saved.Files {
		if f.Id == old {
			found = true
			assert.True(t, f.Committed, "a referenced old draft is committed, not purged")
		}
	}
	assert.True(t, found)

	w = c.do("admin", mtTenant, "DELETE", "/eeg/mailtemplates/zp-active/files/"+gone, nil, "")
	require.Equal(t, http.StatusNoContent, w.Code)
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + gone + `">`, "version": 1})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	errs := decode[mtErrors](t, w)
	assert.Equal(t, "bodyHtml", errs.Errors[0].Field)
	assert.Equal(t, "Ein Bild im Text gibt es nicht (mehr); bitte erneut einfügen.", errs.Errors[0].Message)
}

func TestMailTemplates_FileLifecycle(t *testing.T) {
	c := newMailTemplateClient(t)
	upload := func(kind string, content []byte) string {
		w := c.upload("admin", mtTenant, "zp-active", kind, "f", content)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		return decode[map[string]interface{}](t, w)["id"].(string)
	}
	committed := func() map[string]bool {
		w := c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/zp-active", nil)
		out := map[string]bool{}
		for _, f := range decode[mtDTO](t, w).Files {
			out[f.Id] = f.Committed
		}
		return out
	}
	img1, img2, other := upload("image", logoPNG(t)), upload("image", logoPNG(t)), upload("image", logoPNG(t))

	w := c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + img1 + `">`, "version": 0, "attachmentFileId": nil})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, map[string]bool{img1: true, img2: false, other: false}, committed(),
		"used files are committed; fresh drafts (also of other editors) stay")

	// a file the saved version uses can not be deleted; a draft can
	w = c.do("admin", mtTenant, "DELETE", "/eeg/mailtemplates/zp-active/files/"+img1, nil, "")
	assert.Equal(t, http.StatusConflict, w.Code)
	w = c.do("admin", mtTenant, "DELETE", "/eeg/mailtemplates/zp-active/files/"+other, nil, "")
	assert.Equal(t, http.StatusNoContent, w.Code)

	// the next version uses img2: img1 goes
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + img2 + `">`, "version": 1, "attachmentFileId": nil})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Nil(t, decode[mtDTO](t, w).AttachmentFileId)
	assert.Equal(t, map[string]bool{img2: true}, committed())

	// any PDF is refused while member mails allow none (2026-10-26)
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", img2, "not-a-uuid"} {
		w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
			"subject": "S", "bodyHtml": `<p>x</p>`, "version": 2, "attachmentFileId": id})
		require.Equal(t, http.StatusBadRequest, w.Code, id)
		errs := decode[mtErrors](t, w).Errors
		assert.Equal(t, "attachmentFileId", errs[0].Field)
		if id != "not-a-uuid" {
			assert.Equal(t, mailtemplate.NoAttachmentMessage, errs[0].Message)
		}
	}
}
