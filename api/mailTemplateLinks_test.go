package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Link rules (Variante A, 2026-10-26): save, preview and test mail answer 400 with one message per broken rule.
func TestMailTemplates_LinkRules(t *testing.T) {
	c := newMailTemplateClient(t)
	bad := `<p><a href="https://bit.ly/a">1</a> <a href="https://10.0.0.1">2</a> <a href="https://a.at">3</a> <a href="https://b.at">https://c.at</a></p>`
	want := []string{
		"Links zu IP-Adressen sind nicht erlaubt: 10.0.0.1",
		"Links zu Kurz-Link-Diensten sind nicht erlaubt: bit.ly",
		"Der Linktext zeigt eine andere Adresse als das Ziel: „https://c.at“",
		"Höchstens 3 Links pro Mail (jetzt 4).",
		"Links zu höchstens 2 verschiedenen Domains pro Mail (jetzt 4: bit.ly, 10.0.0.1, a.at, b.at).",
	}
	for _, r := range []struct{ method, path string }{
		{"PUT", "/eeg/mailtemplates/zp-active"},
		{"POST", "/eeg/mailtemplates/zp-active/preview"},
		{"POST", "/eeg/mailtemplates/zp-active/test"},
	} {
		w := c.json("admin", mtTenant, r.method, r.path, map[string]interface{}{
			"recipient": "me", "subject": "S", "bodyHtml": bad, "version": 0})
		require.Equal(t, http.StatusBadRequest, w.Code, r.path+" "+w.Body.String())
		var got []string
		for _, e := range decode[mtErrors](t, w).Errors {
			assert.Equal(t, "bodyHtml", e.Field)
			got = append(got, e.Message)
		}
		assert.Equal(t, want, got, r.path)
	}
	assert.Empty(t, c.sent, "no test mail")
	w := c.json("admin", mtTenant, "GET", "/eeg/mailtemplates/zp-active", nil)
	assert.False(t, decode[mtDTO](t, w).Customized, "nothing saved")

	// within the rules: saved; the footer's link is not counted
	ok := `<p><a href="https://eeg.at/1">eeg.at/1</a> <a href="https://www.eeg.at/2">zwei</a> <a href="mailto:office@netz.at">office@netz.at</a></p>`
	w = c.json("admin", mtTenant, "PUT", "/eeg/mailtemplates/zp-active", map[string]interface{}{"subject": "S", "bodyHtml": ok, "version": 0})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/zp-active/preview", map[string]interface{}{"subject": "S", "bodyHtml": ok})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
