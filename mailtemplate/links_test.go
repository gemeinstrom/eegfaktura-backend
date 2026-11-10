package mailtemplate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkCases is mail-link-cases.json, shared byte-identically with eegfaktura-billing.
type linkCases struct {
	Version int `json:"version"`
	Cases   []struct {
		Name   string   `json:"name"`
		Html   string   `json:"html"`
		Ok     bool     `json:"ok"`
		Errors []string `json:"errors"`
	} `json:"cases"`
}

func TestSharedLinkCases(t *testing.T) {
	data, err := os.ReadFile("mail-link-cases.json")
	require.NoError(t, err)
	var lc linkCases
	require.NoError(t, json.Unmarshal(data, &lc))
	require.Equal(t, 1, lc.Version)
	require.GreaterOrEqual(t, len(lc.Cases), 24)

	def := Lookup(KeyZpActive)
	names := map[string]bool{}
	for _, c := range lc.Cases {
		require.False(t, names[c.Name], "duplicate case %q", c.Name)
		names[c.Name] = true
		require.NotEqual(t, c.Ok, len(c.Errors) > 0, "case %q: ok or errors", c.Name)
		t.Run(c.Name, func(t *testing.T) {
			// the cases are stored (sanitised) bodies; sanitising again must not change what is checked
			clean, err := SanitizeBody(def, c.Html, nil)
			require.NoError(t, err)
			for _, body := range []string{c.Html, clean} {
				var keys []string
				for _, p := range CheckLinks(body) {
					keys = append(keys, p.Key)
					assert.NotEmpty(t, p.Message)
				}
				if c.Ok {
					assert.Empty(t, keys)
				} else {
					assert.ElementsMatch(t, c.Errors, keys)
				}
			}
		})
	}
}

func TestCheckLinks_Messages(t *testing.T) {
	msgs := func(body string) string {
		var out []string
		for _, e := range LinkErrors(body) {
			assert.Equal(t, "bodyHtml", e.Field)
			out = append(out, e.Message)
		}
		return strings.Join(out, "\n")
	}
	assert.Equal(t, "Links zu Kurz-Link-Diensten sind nicht erlaubt: bit.ly", msgs(`<a href="https://bit.ly/a">x</a>`))
	assert.Equal(t, "Links zu IP-Adressen sind nicht erlaubt: 192.168.1.10", msgs(`<a href="https://192.168.1.10/">x</a>`))
	assert.Equal(t, "Der Linktext zeigt eine andere Adresse als das Ziel: „https://netz-noe.at“", msgs(`<a href="https://evil.example">https://netz-noe.at</a>`))
	assert.Equal(t, "Höchstens 3 Links pro Mail (jetzt 4).", msgs(strings.Repeat(`<a href="https://eeg.at">x</a>`, 4)))
	assert.Equal(t, "Links zu höchstens 2 verschiedenen Domains pro Mail (jetzt 3: a.at, b.at, c.at).",
		msgs(`<a href="https://a.at">a</a><a href="https://b.at">b</a><a href="https://www.c.at">c</a>`))
	assert.Equal(t, "Platzhalter dürfen nicht in einem Link stehen: Website der EEG, E-Mail der EEG",
		msgs(`<p><a href="https://eeg.at"><span data-ph="eeg.website">x</span></a> <a href="mailto:office@eeg.at"><span data-ph="eeg.email">y</span></a></p>`))
	assert.Equal(t, "Platzhalter dürfen nicht in einem Link stehen: unbekannt.key", msgs(`<p><a href="https://eeg.at"><span data-ph="unbekannt.key">x</span></a></p>`))
	assert.Equal(t, "Die Linkadresse enthält ungültige Zeichen: München.at", msgs(`<p><a href="https://München.at/x">x</a></p>`))
	assert.Empty(t, msgs(`<p>kein Link</p>`))
}

// No default text breaks a link rule, e.g. a placeholder inside a link.
func TestDefaultsKeepTheLinkRules(t *testing.T) {
	for _, def := range Catalog() {
		assert.Empty(t, CheckLinks(def.DefaultBodyHtml()), def.Key)
	}
}

// The limits of the API come from the policy file.
func TestLimitsFromPolicy(t *testing.T) {
	for _, def := range Catalog() {
		assert.Equal(t, 3, def.Limits.MaxLinks)
		assert.Equal(t, 2, def.Limits.MaxLinkDomains)
		assert.Equal(t, 3, def.Limits.MaxImages)
		assert.Equal(t, 0, def.Limits.MaxAttachments)
	}
}

// The fixed footer has a link of its own; it is added after the check and never counts.
func TestRender_FooterLinkNotCounted(t *testing.T) {
	body := `<p><a href="https://eeg.at/1">1</a> <a href="https://eeg.at/2">2</a> <a href="https://netz.at">3</a></p>`
	r, err := Render(Lookup(KeyZpActive), "S", body, "", &Data{}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(r.Html, "<a "), "three links of the text and the footer's")
}

// A stored text that breaks the link rules or the limits is not rendered (the sender falls back to the default).
func TestRender_RejectsLinkAndLimitViolations(t *testing.T) {
	def := Lookup(KeyZpActive)
	var verr *ValidationError
	_, err := Render(def, "S", `<p><a href="https://bit.ly/x">Portal</a></p>`, "", &Data{}, nil, ImagesInline)
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, verr.Message, "Kurz-Link")

	_, err = Render(def, "S", `<p><a href="https://eeg.at"><span data-ph="eeg.website">Website der EEG</span></a></p>`, "", &Data{}, nil, ImagesInline)
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, verr.Message, "Platzhalter dürfen nicht in einem Link stehen: Website der EEG")

	_, err = Render(def, "S", `<p>x</p>`, "pdf", &Data{}, nil, ImagesInline)
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "attachmentFileId", verr.Field)
	assert.Contains(t, verr.Message, "PDF-Anhänge sind derzeit nicht möglich")
}
