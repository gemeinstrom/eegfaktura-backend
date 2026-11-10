package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailTemplates_Validation(t *testing.T) {
	c := newMailTemplateClient(t)
	put := func(payload map[string]interface{}) *httptest.ResponseRecorder {
		return c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/consent-request", payload)
	}

	cases := map[string]struct {
		payload map[string]interface{}
		field   string
	}{
		"unknown placeholder":    {map[string]interface{}{"subject": "S", "bodyHtml": `<p><span data-ph="member.iban">x</span></p>`}, "bodyHtml"},
		"placeholder wrong mail": {map[string]interface{}{"subject": "S", "bodyHtml": `<p><span data-ph="reason">x</span></p>`}, "bodyHtml"},
		"empty body":             {map[string]interface{}{"subject": "S", "bodyHtml": `<script>alert(1)</script>`}, "bodyHtml"},
		"empty subject":          {map[string]interface{}{"subject": " ", "bodyHtml": `<p>x</p>`}, "subject"},
		"unknown subject token":  {map[string]interface{}{"subject": "Hallo {member.iban}", "bodyHtml": `<p>x</p>`}, "subject"},
		"block in subject":       {map[string]interface{}{"subject": "{eeg.contactBlock}", "bodyHtml": `<p>x</p>`}, "subject"},
		"block in paragraph":     {map[string]interface{}{"subject": "S", "bodyHtml": `<p>Gruß <span data-ph="eeg.contactBlock">x</span></p>`}, "bodyHtml"},
		"subject too long":       {map[string]interface{}{"subject": strings.Repeat("x", 201), "bodyHtml": `<p>x</p>`}, "subject"},
		"body too large":         {map[string]interface{}{"subject": "S", "bodyHtml": "<p>" + strings.Repeat("x", 100*1024) + "</p>"}, "bodyHtml"},
		"negative version":       {map[string]interface{}{"subject": "S", "bodyHtml": `<p>x</p>`, "version": -1}, "version"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := put(tc.payload)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			errs := decode[mtErrors](t, w)
			require.NotEmpty(t, errs.Errors)
			assert.Equal(t, tc.field, errs.Errors[0].Field)
		})
	}
	w := c.do("admin", mtTenant, "PUT", "/eeg/mailtemplates/consent-request", strings.NewReader("{no json"), "application/json")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// unsafe HTML is removed, not rejected
	w = put(map[string]interface{}{"subject": "S", "bodyHtml": `<p onclick="x()">a <a href="javascript:x()">b</a> <img src="https://t.example/p.gif"></p>`})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, `<p>a b </p>`, decode[mtDTO](t, w).BodyHtml)

	// uploads: type checked on the content, limits per mail
	w = c.upload("admin", mtTenant, "zp-active", "image", "fake.png", []byte("this is not an image"))
	assert.Equal(t, http.StatusBadRequest, w.Code, "a disguised file is rejected")
	w = c.upload("admin", mtTenant, "zp-active", "image", "doc.png", minimalPDF)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = c.upload("admin", mtTenant, "zp-active", "attachment", "img.pdf", logoPNG(t))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = c.upload("admin", mtTenant, "zp-active", "video", "a.png", logoPNG(t))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	big := append(append([]byte{}, logoPNG(t)...), make([]byte, 1024*1024)...)
	w = c.upload("admin", mtTenant, "zp-active", "image", "big.png", big)
	assert.Equal(t, http.StatusBadRequest, w.Code, "image over 1 MB")

	w = c.upload("admin", mtTenant, "zp-active", "attachment", "a.pdf", minimalPDF)
	assert.Equal(t, http.StatusBadRequest, w.Code, "no PDF for member mails for now")
	for i := 0; i < 3; i++ {
		w = c.upload("admin", mtTenant, "zp-active", "image", "a.png", logoPNG(t))
		require.Equal(t, http.StatusCreated, w.Code)
	}
	w = c.upload("admin", mtTenant, "zp-active", "image", "a.png", logoPNG(t))
	assert.Equal(t, http.StatusBadRequest, w.Code, "at most 3 images")

	// test mail: only "me" or "eeg", never a free address
	body := map[string]interface{}{"recipient": "someone@example.org", "subject": "S", "bodyHtml": "<p>x</p>"}
	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	body["recipient"] = "me"
	w = c.json("admin-noemail", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "login without e-mail")
	assert.Empty(t, c.sent)

	w = c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/billing-document", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/zp-active/files/not-a-uuid", nil, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMailTemplates_VersionConflict(t *testing.T) {
	c := newMailTemplateClient(t)
	payload := map[string]interface{}{"subject": "S", "bodyHtml": "<p>x</p>", "version": 0}
	w := c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", payload)
	require.Equal(t, http.StatusOK, w.Code)

	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", payload)
	require.Equal(t, http.StatusConflict, w.Code)
	conflict := decode[map[string]interface{}](t, w)
	assert.Equal(t, float64(1), conflict["currentVersion"])
	assert.NotEmpty(t, conflict["message"])

	payload["version"] = 1
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", payload)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 2, decode[mtDTO](t, w).Version)
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", payload)
	assert.Equal(t, http.StatusConflict, w.Code, "stale version")
}

func TestMailTemplates_TenantIsolationAndRole(t *testing.T) {
	c := newMailTemplateClient(t)
	w := c.upload("admin", mtTenant, "zp-active", "image", "a.png", logoPNG(t))
	require.Equal(t, http.StatusCreated, w.Code)
	imgId := decode[map[string]interface{}](t, w)["id"].(string)
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + imgId + `">`, "version": 0})
	require.Equal(t, http.StatusOK, w.Code)

	// an admin of another community can not use this tenant header
	for _, r := range []struct{ method, path string }{
		{"GET", "/eeg/mailtemplates"},
		{"GET", "/eeg/mailtemplates/zp-active"},
		{"PUT", "/eeg/mailtemplates/zp-active"},
		{"DELETE", "/eeg/mailtemplates/zp-active"},
		{"GET", "/eeg/mailtemplates/zp-active/files/" + imgId},
		{"DELETE", "/eeg/mailtemplates/zp-active/files/" + imgId},
		{"POST", "/eeg/mailtemplates/zp-active/preview"},
		{"POST", "/eeg/mailtemplates/zp-active/test"},
	} {
		w = c.json("other", mtTenant, r.method, r.path, map[string]interface{}{})
		assert.Equal(t, http.StatusForbidden, w.Code, r.method+" "+r.path)
		// a login without the EEG_ADMIN group gets no access (existing Protect: 401)
		w = c.json("user", mtTenant, r.method, r.path, map[string]interface{}{})
		assert.Equal(t, http.StatusUnauthorized, w.Code, r.method+" "+r.path)
	}

	// with its own tenant header it can not reach the file by a guessed id
	w = c.do("other", mtOtherTenant, "GET", "/eeg/mailtemplates/zp-active/files/"+imgId, nil, "")
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = c.do("other", mtOtherTenant, "DELETE", "/eeg/mailtemplates/zp-active/files/"+imgId, nil, "")
	assert.Equal(t, http.StatusForbidden, w.Code)
	// nor reference it in its own mail
	w = c.json("other", mtOtherTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{
		"subject": "S", "bodyHtml": `<p>x</p><img src="mailfile:` + imgId + `">`, "version": 0})
	require.Equal(t, http.StatusBadRequest, w.Code, "an image of another community does not exist for this one")
	assert.NotContains(t, w.Body.String(), mtTenant)
	w = c.json("other", mtOtherTenant, "GET", "/eeg/mailtemplates/zp-active", nil)
	assert.False(t, decode[mtDTO](t, w).Customized, "nothing saved")
	// the file of a mail is not served under another mail key
	w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/consent-request/files/"+imgId, nil, "")
	assert.Equal(t, http.StatusNotFound, w.Code)

	// the file is untouched
	w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/zp-active/files/"+imgId, nil, "")
	assert.Equal(t, http.StatusOK, w.Code)
}

// A save sets the whole version: without attachmentFileId a saved PDF would be removed silently, so it is a 400.
func TestMailTemplates_SaveWithoutAttachmentField(t *testing.T) {
	c := newMailTemplateClient(t)
	w := c.do("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active",
		strings.NewReader(`{"subject":"Betreff","bodyHtml":"<p>Hallo</p>","version":0}`), "application/json")
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "attachmentFileId")
}
