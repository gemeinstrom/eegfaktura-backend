package mailtemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownImage = "3f0c5a52-8f43-4c86-9a5e-3c0f7a1b2c3d"
const foreignImage = "9b2d1e44-1c55-4b7e-8f00-aa11bb22cc33"

func ownFiles(id string) bool { return id == ownImage }

func sanitizeOK(t *testing.T, in string) string {
	t.Helper()
	out, err := SanitizeBody(Lookup(KeyConsentRequest), in, ownFiles)
	require.NoError(t, err, in)
	return out
}

// The shared case list of mail-html-policy.json; eegfaktura-billing tests the same cases.
func TestSanitizeBody_SharedCases(t *testing.T) {
	removed := []struct{ name, in, mustNotContain string }{
		{"script", `<p>a</p><script>alert(1)</script>`, "alert"},
		{"style element", `<style>p{color:red}</style><p>a</p>`, "color"},
		{"iframe", `<p>a</p><iframe src="https://evil.example"></iframe>`, "iframe"},
		{"on* attribute", `<p onclick="alert(1)">a</p>`, "onclick"},
		{"img onerror", `<img src="mailfile:` + ownImage + `" onerror="alert(1)">`, "onerror"},
		{"javascript link", `<p><a href="javascript:alert(1)">x</a></p>`, "javascript"},
		{"data link", `<p><a href="data:text/html;base64,PHNjcmlwdD4=">x</a></p>`, "data:"},
		{"http link", `<p><a href="http://example.org">x</a></p>`, "http:"},
		{"external image", `<p>a</p><img src="https://tracker.example/p.png">`, "tracker"},
		{"data image", `<p>a</p><img src="data:image/png;base64,AAAA">`, "data:"},
		{"foreign mailfile image", `<p>a</p><img src="mailfile:` + foreignImage + `">`, foreignImage},
		{"class attribute", `<p class="x">a</p>`, "class"},
	}
	for _, tc := range removed {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizeOK(t, tc.in)
			assert.NotContains(t, out, tc.mustNotContain)
		})
	}

	t.Run("links without href keep their text", func(t *testing.T) {
		assert.Equal(t, `<p>x</p>`, sanitizeOK(t, `<p><a href="http://example.org">x</a></p>`))
	})
	t.Run("https link kept with rel", func(t *testing.T) {
		assert.Equal(t, `<p><a href="https://x.example/a?b=1" rel="noopener noreferrer">x</a></p>`,
			sanitizeOK(t, `<p><a href="https://x.example/a?b=1" target="_blank">x</a></p>`))
	})
	t.Run("mailto link kept with rel", func(t *testing.T) {
		assert.Equal(t, `<p><a href="mailto:info@eeg.example" rel="noopener noreferrer">mail</a></p>`,
			sanitizeOK(t, `<p><a href="mailto:info@eeg.example">mail</a></p>`))
	})
	t.Run("own mailfile image kept", func(t *testing.T) {
		assert.Equal(t, `<p>a</p><img src="mailfile:`+ownImage+`" alt="Logo" width="200"/>`,
			sanitizeOK(t, `<p>a</p><img src="mailfile:`+ownImage+`" alt="Logo" width="200" style="float:left">`))
	})
	t.Run("builtin logo kept", func(t *testing.T) {
		assert.Contains(t, sanitizeOK(t, `<p>a</p><img src="mailfile:eegfaktura-logo">`), `src="mailfile:eegfaktura-logo"`)
	})
	t.Run("text-align kept on p, h1-h3, li", func(t *testing.T) {
		for _, el := range []string{"p", "h1", "h2", "h3", "li"} {
			out := sanitizeOK(t, `<`+el+` style="text-align:center">a</`+el+`>`)
			assert.Contains(t, out, `style="text-align: center"`, el)
		}
	})
	t.Run("font-size 13px/20px kept alone and with text-align, other sizes removed", func(t *testing.T) {
		for in, want := range map[string]string{
			`<p style="font-size:13px">a</p>`:                      `<p style="font-size: 13px">a</p>`,
			`<li style="font-size: 20px">a</li>`:                   `<li style="font-size: 20px">a</li>`,
			`<h2 style="text-align:center;font-size:20px">a</h2>`:  `<h2 style="text-align: center; font-size: 20px">a</h2>`,
			`<p style="text-align: center; font-size: 20px">a</p>`: `<p style="text-align: center; font-size: 20px">a</p>`,
			`<p style="font-size: 13px; text-align: right;">a</p>`: `<p style="font-size: 13px; text-align: right">a</p>`,
			`<p style="text-align:center;font-size:30px">a</p>`:    `<p style="text-align: center">a</p>`,
			`<p style="font-size:1em">a</p>`:                       `<p>a</p>`,
			`<p>x <span style="font-size:20px">a</span></p>`:       `<p>x a</p>`,
		} {
			assert.Equal(t, want, sanitizeOK(t, in), in)
		}
	})
	t.Run("other styles removed", func(t *testing.T) {
		out := sanitizeOK(t, `<p style="color:red;text-align:right">a</p><p style="text-align:justify">b</p><blockquote style="text-align:center">c</blockquote>`)
		assert.Equal(t, `<p style="text-align: right">a</p><p>b</p><blockquote>c</blockquote>`, out)
	})
	t.Run("placeholder kept, content normalised to label", func(t *testing.T) {
		assert.Equal(t, `<p>Hallo <span data-ph="member.firstName">Vorname</span>,</p>`,
			sanitizeOK(t, `<p>Hallo <span data-ph="member.firstName" class="ph"><b>anything</b></span>,</p>`))
	})
	t.Run("block placeholder kept", func(t *testing.T) {
		assert.Equal(t, `<div data-ph="eeg.contactBlock">Kontaktblock der EEG</div>`,
			sanitizeOK(t, `<div data-ph="eeg.contactBlock">x</div>`))
	})
	t.Run("span and div without data-ph unwrapped", func(t *testing.T) {
		assert.Equal(t, `<p>a <strong>b</strong> c</p>`, sanitizeOK(t, `<div><p>a <span style="color:red"><strong>b</strong></span> c</p></div>`))
	})
	t.Run("block placeholder only at top level", func(t *testing.T) {
		// the HTML parser closes a paragraph before a div, so this one stands on its own
		assert.Equal(t, `<p>x </p><div data-ph="eeg.contactBlock">Kontaktblock der EEG</div>`,
			sanitizeOK(t, `<p>x <div data-ph="eeg.contactBlock">y</div>`))
		// a span with a block key at top level becomes a div, an inline key in a div a span
		out := sanitizeOK(t, `<span data-ph="meteringPoints">x</span><p>a <b><div data-ph="eeg.name">x</div></b></p>`)
		assert.Contains(t, out, `<div data-ph="meteringPoints">Zählpunkte (Liste)</div>`)
		assert.Contains(t, out, `<span data-ph="eeg.name">Name der EEG</span>`)
		assert.NotContains(t, out, `<div data-ph="eeg.name"`)
		for _, in := range []string{
			`<p>Hallo <span data-ph="eeg.contactBlock">x</span></p>`,
			`<ul><li><div data-ph="meteringPoints">x</div></li></ul>`,
			`<blockquote><div data-ph="eeg.contactBlock">x</div></blockquote>`,
			`<h1><span data-ph="meteringPoints">x</span></h1>`,
		} {
			_, err := SanitizeBody(Lookup(KeyConsentRequest), in, ownFiles)
			var verr *ValidationError
			assert.True(t, errors.As(err, &verr), in)
		}
	})
	t.Run("unknown placeholder rejected", func(t *testing.T) {
		_, err := SanitizeBody(Lookup(KeyConsentRequest), `<p><span data-ph="member.iban">IBAN</span></p>`, ownFiles)
		var verr *ValidationError
		require.True(t, errors.As(err, &verr))
		assert.Equal(t, "bodyHtml", verr.Field)
		assert.Contains(t, verr.Message, "member.iban")
	})
	t.Run("placeholder of another mail rejected", func(t *testing.T) {
		_, err := SanitizeBody(Lookup(KeyZpActive), `<p><span data-ph="reason">Grund</span></p>`, ownFiles)
		assert.Error(t, err)
	})
	t.Run("empty after cleaning rejected", func(t *testing.T) {
		for _, in := range []string{"", "   ", `<script>x</script>`, `<p> </p><p><br></p>`, `<img src="https://x.example/a.png">`} {
			_, err := SanitizeBody(Lookup(KeyConsentRequest), in, ownFiles)
			assert.Error(t, err, in)
		}
	})
	t.Run("too large rejected", func(t *testing.T) {
		_, err := SanitizeBody(Lookup(KeyConsentRequest), "<p>"+strings.Repeat("a", MaxBodyBytes)+"</p>", ownFiles)
		assert.Error(t, err)
	})
	t.Run("idempotent", func(t *testing.T) {
		for _, def := range Catalog() {
			once, err := SanitizeBody(&def, def.DefaultBody, nil)
			require.NoError(t, err)
			twice, err := SanitizeBody(&def, once, nil)
			require.NoError(t, err)
			assert.Equal(t, once, twice, def.Key)
		}
	})
}

func TestSanitizeSubject(t *testing.T) {
	def := Lookup(KeyZpInactive)
	out, err := SanitizeSubject(def, "  Dein Zählpunkt {meteringPoint}\n ist <b>inaktiv</b> & {eeg.name} {x  ")
	require.NoError(t, err)
	assert.Equal(t, `Dein Zählpunkt {meteringPoint} ist <b>inaktiv</b> & {eeg.name} {x`, out, "plain text, no HTML processing")

	_, err = SanitizeSubject(def, "Hallo {member.iban}")
	assert.Error(t, err, "unknown key")
	_, err = SanitizeSubject(def, "Zählpunkte {meteringPoints}")
	assert.Error(t, err, "block placeholders are not allowed in the subject")
	_, err = SanitizeSubject(def, "Lone { and } and {} and { meteringPoint } stay")
	assert.NoError(t, err)
	// only the catalog key syntax is a placeholder; other braces are text
	for _, text := range []string{"Neu bei der {EEG}", "{Eeg.name}", "{x1}", "{member.first_name}", "{.eeg}", "{eeg.}"} {
		out, err := SanitizeSubject(def, text)
		assert.NoError(t, err, text)
		assert.Equal(t, text, out)
	}
	_, err = SanitizeSubject(def, " \n ")
	assert.Error(t, err)
	_, err = SanitizeSubject(def, strings.Repeat("ä", MaxSubjectChars+1))
	assert.Error(t, err)
	_, err = SanitizeSubject(def, strings.Repeat("ä", MaxSubjectChars))
	assert.NoError(t, err)
}

func TestRenderSubject(t *testing.T) {
	def := Lookup(KeyZpInactive)
	got, err := renderSubject(def, "Zählpunkt {meteringPoint} von {member.firstName} {member.lastName} {x", &Data{
		FirstName: "Ma\nria", MeteringPoints: []string{"AT1"}})
	require.NoError(t, err)
	assert.Equal(t, "Zählpunkt AT1 von Ma ria {x", got)
	got, err = renderSubject(def, "{EEG}: {meteringPoint}", &Data{MeteringPoints: []string{"AT1"}})
	require.NoError(t, err)
	assert.Equal(t, "{EEG}: AT1", got)
}

// The policy file is a byte-identical copy of the one in eegfaktura-billing: a
// change here must be made there too (and this hash updated in both repos).
func TestPolicyFileIsTheSharedOne(t *testing.T) {
	data, err := os.ReadFile("mail-html-policy.json")
	require.NoError(t, err)
	data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
	sum := sha256.Sum256(data)
	assert.Equal(t, "3e6f9115e4e9e14f7f673ae52e1cc33093552b336ba4fdec798a445549912e6a", hex.EncodeToString(sum[:]))
	assert.Equal(t, 2, policy.Version)
	assert.Equal(t, "noopener noreferrer", policy.LinkRel)
	assert.Equal(t, 3, policy.Links.MaxLinks)
	assert.Equal(t, 2, policy.Links.MaxLinkDomains)
	assert.Contains(t, policy.Links.BlockedHosts, "bit.ly")
}
