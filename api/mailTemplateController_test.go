package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/public"
	"at.ourproject/vfeeg-backend/services"
	"github.com/gorilla/mux"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mtTenant = "TE000300"
const mtOtherTenant = "TE000301"

type mtSent struct {
	tenant, to, subject string
	cc                  *string
	body                string
	inline              int
	attachment          bool
}

type mtClient struct {
	t       *testing.T
	router  *mux.Router
	sent    []mtSent
	sendErr error // returned by the mail sender
}

func newMailTemplateClient(t *testing.T) *mtClient {
	ctx := context.Background()
	db, err := database.GetDB(ctx)
	require.NoError(t, err)
	for _, tenant := range []string{mtTenant, mtOtherTenant} {
		require.NoError(t, db.DeleteMailTemplate(ctx, tenant, "consent-request"))
		require.NoError(t, db.DeleteMailTemplate(ctx, tenant, "zp-active"))
	}
	_, err = execTestSQL(ctx, db, `INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email, phone)
		VALUES ($1, 'MAIL-TEST', 'Mailtest Gemeinschaft', $2, 'LOCAL', 'AT009999', 'Test Netz', 'AT00999900000TC000300000000000001',
		 'Solarweg', '1', 'Linz', '4020', 'eeg-mail-test@example.org; office@example.org', '+43 1 300')
		ON CONFLICT (tenant) DO NOTHING`, mtTenant, mtTenant)
	require.NoError(t, err)

	orig := middleware.VerifyTokenClaims
	t.Cleanup(func() { middleware.VerifyTokenClaims = orig })
	middleware.VerifyTokenClaims = func(_ context.Context, token string) (*middleware.PlatformClaims, error) {
		c := &middleware.PlatformClaims{Username: token, Email: token + "@example.org", AccessGroups: middleware.AccessGroups{"/EEG_ADMIN"}}
		switch token {
		case "admin":
			c.Tenants = []string{strings.ToLower(mtTenant)} // compared case-insensitively
		case "admin-noemail":
			c.Tenants, c.Email = []string{mtTenant}, ""
		case "user":
			c.Tenants, c.AccessGroups = []string{mtTenant}, middleware.AccessGroups{"/EEG_USER"}
		case "other":
			c.Tenants = []string{mtOtherTenant}
		}
		return c, nil
	}

	c := &mtClient{t: t, router: mux.NewRouter()}
	s := c.router.PathPrefix("/eeg").Subrouter()
	registerMailTemplateRoutes(s, &MailTemplateHandler{db: db, sendMail: func(tenant, to, subject string, cc *string,
		body *bytes.Buffer, inline []*services.Attachment, att *services.Attachment) error {
		c.sent = append(c.sent, mtSent{tenant, to, subject, cc, body.String(), len(inline), att != nil})
		return c.sendErr
	}})
	return c
}

// execTestSQL runs a fixture statement through the Database interface, which has no Exec.
func execTestSQL(ctx context.Context, db database.Database, stmt string, args ...interface{}) (int, error) {
	var rows []int
	err := db.Select(ctx, &rows, stmt+" RETURNING 1", args...)
	return len(rows), err
}

func (c *mtClient) do(token, tenant, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("tenant", tenant)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	c.router.ServeHTTP(w, req)
	return w
}

func (c *mtClient) json(token, tenant, method, path string, payload interface{}) *httptest.ResponseRecorder {
	// a save names the PDF explicitly, as web always does (null = none); a missing field is a 400
	// (TestMailTemplates_SaveWithoutAttachmentField)
	if m, ok := payload.(map[string]interface{}); ok && method == "PUT" && !strings.Contains(path, "/files") {
		if _, set := m["attachmentFileId"]; !set {
			m["attachmentFileId"] = nil
		}
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		require.NoError(c.t, err)
		body = bytes.NewReader(b)
	}
	return c.do(token, tenant, method, path, body, "application/json")
}

func (c *mtClient) upload(token, tenant, key, kind, filename string, content []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(c.t, mw.WriteField("kind", kind))
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(c.t, err)
	_, _ = fw.Write(content)
	require.NoError(c.t, mw.Close())
	return c.do(token, tenant, "POST", "/eeg/mailtemplates/"+key+"/files", &buf, mw.FormDataContentType())
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), w.Body.String())
	return v
}

type mtDTO struct {
	Key              string  `json:"key"`
	Subject          string  `json:"subject"`
	BodyHtml         string  `json:"bodyHtml"`
	Customized       bool    `json:"customized"`
	Version          int     `json:"version"`
	DefaultVersion   int     `json:"defaultVersion"`
	DefaultChanged   bool    `json:"defaultChanged"`
	DefaultSubject   string  `json:"defaultSubject"`
	DefaultBodyHtml  string  `json:"defaultBodyHtml"`
	AttachmentFileId *string `json:"attachmentFileId"`
	FixedFooter      string  `json:"fixedFooter"`
	UpdatedBy        *string
	Placeholders     []struct {
		Key   string `json:"key"`
		Block bool   `json:"block"`
	} `json:"placeholders"`
	Files []struct {
		Id        string `json:"id"`
		Kind      string `json:"kind"`
		Filename  string `json:"filename"`
		MimeType  string `json:"mimeType"`
		Size      int64  `json:"size"`
		Committed bool   `json:"committed"`
	} `json:"files"`
	Limits map[string]int64 `json:"limits"`
}

type mtErrors struct {
	Errors []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"errors"`
}

func logoPNG(t *testing.T) []byte {
	png, err := public.Templates.ReadFile("templates/eegfaktura-logo.png")
	require.NoError(t, err)
	return png
}

var minimalPDF = []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")

func TestMailTemplates_HappyPath(t *testing.T) {
	c := newMailTemplateClient(t)

	w := c.json("admin", mtTenant, "GET", "/eeg/mailtemplates", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	list := decode[[]map[string]interface{}](t, w)
	require.Len(t, list, 3)
	assert.Equal(t, "consent-request", list[0]["key"])
	assert.Equal(t, false, list[0]["customized"])
	assert.Nil(t, list[0]["updatedAt"])
	for _, entry := range list {
		assert.Equal(t, "versandt durch EEG Faktura, ein Service des VFEEG - https://vfeeg.org/", entry["fixedFooter"])
	}

	w = c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request", nil)
	require.Equal(t, http.StatusOK, w.Code)
	def := decode[mtDTO](t, w)
	assert.False(t, def.Customized)
	assert.Equal(t, 0, def.Version)
	assert.Equal(t, 2, def.DefaultVersion)
	assert.Equal(t, "versandt durch EEG Faktura, ein Service des VFEEG - https://vfeeg.org/", def.FixedFooter)
	assert.NotContains(t, def.DefaultBodyHtml, "versandt durch", "the footer is not part of the editable text")
	assert.NotContains(t, def.DefaultBodyHtml, "eegfaktura-logo")
	assert.Equal(t, "Aktivierung im Serviceportal", def.Subject)
	assert.Equal(t, def.DefaultBodyHtml, def.BodyHtml)
	assert.Contains(t, def.BodyHtml, `data-ph="eeg.contactBlock"`)
	assert.Empty(t, def.Files)
	assert.Equal(t, int64(3), def.Limits["maxImages"])
	assert.Equal(t, int64(0), def.Limits["maxAttachments"])
	assert.Equal(t, int64(3), def.Limits["maxLinks"], "from mail-html-policy.json")
	assert.Equal(t, int64(2), def.Limits["maxLinkDomains"])
	assert.Equal(t, int64(6*1024*1024), def.Limits["maxTotalBytes"])

	// the builtin logo of the default text can be shown by the editor
	w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request/files/eegfaktura-logo", nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))

	w = c.upload("admin", mtTenant, "consent-request", "image", `..\..\Logo "neu".png`, logoPNG(t))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	img := decode[map[string]interface{}](t, w)
	imgId := img["id"].(string)
	assert.Equal(t, "image/png", img["mimeType"])
	assert.Equal(t, "Logo neu.png", img["filename"])

	assert.Equal(t, false, img["committed"], "an upload is a draft")

	// no PDF attachment for member mails for now (2026-10-26)
	w = c.upload("admin", mtTenant, "consent-request", "attachment", "anleitung.pdf", minimalPDF)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "PDF-Anhänge sind derzeit nicht möglich.", decode[mtErrors](t, w).Errors[0].Message)

	body := `<p>Hallo <span data-ph="member.firstName">Vorname</span>!</p><img src="mailfile:` + imgId + `" alt="Logo"><div data-ph="eeg.contactBlock">x</div>`
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/consent-request", map[string]interface{}{
		"subject": `Hallo {member.firstName}`, "bodyHtml": body, "version": 0, "attachmentFileId": nil})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	saved := decode[mtDTO](t, w)
	assert.True(t, saved.Customized)
	assert.Equal(t, 1, saved.Version)
	assert.Equal(t, mailtemplate.FixedFooterText, saved.FixedFooter)
	assert.NotContains(t, saved.BodyHtml, "versandt durch", "the footer is never stored")
	assert.Nil(t, saved.AttachmentFileId)
	require.Len(t, saved.Files, 1)
	for _, f := range saved.Files {
		assert.True(t, f.Committed, f.Filename)
	}
	require.NotNil(t, saved.UpdatedBy)
	assert.Equal(t, "admin", *saved.UpdatedBy)

	w = c.json("admin", mtTenant, "GET", "/eeg/mailtemplates", nil)
	list = decode[[]map[string]interface{}](t, w)
	assert.Equal(t, true, list[0]["customized"])
	assert.NotNil(t, list[0]["updatedAt"])
	assert.Equal(t, false, list[0]["defaultChanged"])

	w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request/files/"+imgId, nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, decodedPNG(t, logoPNG(t)), decodedPNG(t, w.Body.Bytes()), "the re-encoded image has the same pixels")

	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/preview", map[string]interface{}{
		"subject": "Vorschau {eeg.name}", "bodyHtml": body})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	preview := decode[map[string]string](t, w)
	assert.Equal(t, "Vorschau Mailtest Gemeinschaft", preview["subject"]) // {eeg.name} = designation (25.10.2026)
	assert.Contains(t, preview["html"], "<p>Hallo Max!</p>")
	assert.Contains(t, preview["html"], `src="data:image/png;base64,`)
	assert.Contains(t, preview["html"], "<div>Mailtest Gemeinschaft</div>")
	assert.Contains(t, preview["html"], "<div>T: +43 1 300</div>")
	assert.Equal(t, 1, strings.Count(preview["html"], mailtemplate.FixedFooterHtml), "the preview shows the fixed footer")

	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", map[string]interface{}{
		"recipient": "me", "subject": "Test {member.lastName}", "bodyHtml": body})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "admin@example.org", decode[map[string]string](t, w)["sentTo"])
	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", map[string]interface{}{
		"recipient": "eeg", "subject": "Test", "bodyHtml": body})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "eeg-mail-test@example.org; office@example.org", decode[map[string]string](t, w)["sentTo"])

	require.Len(t, c.sent, 2)
	assert.Equal(t, "admin@example.org", c.sent[0].to)
	assert.Equal(t, "[Test] Test Mustermann", c.sent[0].subject)
	assert.Nil(t, c.sent[0].cc, "a test mail has no copy to the community")
	assert.Equal(t, mtTenant, c.sent[0].tenant)
	assert.Contains(t, c.sent[0].body, `src="cid:`+imgId+`"`)
	assert.Equal(t, 1, strings.Count(c.sent[0].body, mailtemplate.FixedFooterHtml), "the test mail ends with the fixed footer")
	assert.Equal(t, 1, c.sent[0].inline)
	assert.False(t, c.sent[0].attachment)
	assert.Equal(t, "eeg-mail-test@example.org; office@example.org", c.sent[1].to)

	w = c.json("admin", mtTenant, "DELETE", "/eeg/mailtemplates/consent-request", nil)
	require.Equal(t, http.StatusNoContent, w.Code)
	w = c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request", nil)
	reset := decode[mtDTO](t, w)
	assert.False(t, reset.Customized)
	assert.Empty(t, reset.Files, "reset deletes the files")
}

// Hand-made templates on the volume (per tenant and global) are ignored: GET does not report them, and preview
// and test mail of the unchanged default show the built-in default.
func TestMailTemplates_VolumeTemplateIgnored(t *testing.T) {
	c := newMailTemplateClient(t)
	vol := t.TempDir()
	for _, dir := range []string{filepath.Join(vol, mtTenant, "templates"), filepath.Join(vol, "templates")} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "activation-mail-template.toml"), []byte(`TemplateFile="t.html"`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "t.html"), []byte(`<html><body><p>Servus {{.Participant.FirstName}}</p></body></html>`), 0o644))
	}
	orig := viper.GetString("file-content.templates")
	viper.Set("file-content.templates", vol)
	t.Cleanup(func() { viper.Set("file-content.templates", orig) })

	w := c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "legacyTemplateActive")
	def := decode[mtDTO](t, w)
	assert.False(t, def.Customized)

	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/preview", map[string]interface{}{
		"subject": def.DefaultSubject, "bodyHtml": def.DefaultBodyHtml})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	preview := decode[map[string]string](t, w)
	assert.NotContains(t, preview["html"], "Servus")
	assert.Contains(t, preview["html"], "damit dein Zählpunkt tatsächlich Energie")

	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", map[string]interface{}{
		"recipient": "me", "subject": def.DefaultSubject, "bodyHtml": def.DefaultBodyHtml})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, c.sent, 1)
	assert.NotContains(t, c.sent[0].body, "Servus")
	assert.Contains(t, c.sent[0].body, "damit dein Zählpunkt tatsächlich Energie")
}
