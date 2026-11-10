package mailtemplate

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plainText mirrors htmlToPlainText of eegfaktura-eda-xp (SendMailServiceImpl), which derives the
// text/plain part of every member mail from the HTML the backend sends. It is a copy and can drift
// from eda-xp: the real check is the stack E2E (eegfaktura-dev), which asserts that the text part
// of a sent member mail contains the footer.
func plainText(doc string) string {
	for _, r := range []struct{ re, repl string }{
		{`(?is)<script[^>]*>.*?</script>`, ""},
		{`(?is)<style[^>]*>.*?</style>`, ""},
		{`(?i)<br\s*/?>`, "\n"},
		{`(?i)</(p|div|tr|li|h[1-6]|table)>`, "\n"},
		{`(?s)<[^>]+>`, ""},
	} {
		doc = regexp.MustCompile(r.re).ReplaceAllString(doc, r.repl)
	}
	doc = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(doc)
	doc = regexp.MustCompile(`[ \t]+`).ReplaceAllString(doc, " ")
	doc = regexp.MustCompile(`(?m)^[ \t]+`).ReplaceAllString(doc, "")
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(doc, "\n\n"))
}

func TestFixedFooterText(t *testing.T) {
	assert.Equal(t, "versandt durch EEG Faktura, ein Service des VFEEG - https://vfeeg.org/", FixedFooterText)
	assert.Equal(t, FixedFooterText, plainText(FixedFooterHtml), "HTML and text form say the same")
}

func TestAppendFixedFooter(t *testing.T) {
	assert.Equal(t, "<html><body><p>a</p>"+FixedFooterHtml+"\n</body></html>", AppendFixedFooter("<html><body><p>a</p></body></html>"))
	assert.Equal(t, "<p>a</p>\n"+FixedFooterHtml, AppendFixedFooter("<p>a</p>"), "a fragment gets it at the end")
	assert.Equal(t, "<BODY><p>a</p>"+FixedFooterHtml+"\n</BODY>", AppendFixedFooter("<BODY><p>a</p></BODY>"))
	// characters that change their length under strings.ToLower must not shift the position
	assert.Equal(t, "<html><body><p>İlhan İnce K</p>"+FixedFooterHtml+"\n</body></html>",
		AppendFixedFooter("<html><body><p>İlhan İnce K</p></body></html>"))
	assert.Equal(t, "<p>a</body></p>"+FixedFooterHtml+"\n</Body>", AppendFixedFooter("<p>a</body></p></Body>"), "the last one")
}

// The footer follows the rendered body in every mode (send/test mail inline, preview data URIs), once,
// and is in the text part; it is never part of the stored text.
func TestRender_FixedFooter(t *testing.T) {
	def := Lookup(KeyZpActive)
	body := `<p>Hallo <span data-ph="member.firstName">Vorname</span></p><p>Ende</p>`
	stored, err := SanitizeBody(def, body, nil)
	require.NoError(t, err)
	assert.NotContains(t, stored, "versandt durch")

	for _, mode := range []ImageMode{ImagesInline, ImagesDataURI} {
		r, err := Render(def, "S", body, "", &Data{FirstName: "Maria"}, nil, mode)
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(r.Html, FixedFooterHtml))
		assert.Less(t, strings.Index(r.Html, "<p>Ende</p>"), strings.Index(r.Html, FixedFooterHtml), "after the body")
		assert.Less(t, strings.Index(r.Html, FixedFooterHtml), strings.Index(r.Html, "</body>"))
		text := plainText(r.Html)
		assert.True(t, strings.HasSuffix(text, "Ende\n\n"+FixedFooterText), text)
	}
}

// A text that still says "versandt durch" itself (saved before the footer) keeps it; the footer is added anyway.
func TestRender_FixedFooterAfterOwnSignature(t *testing.T) {
	def := Lookup(KeyZpActive)
	r, err := Render(def, "S", `<p>Gruß</p><p>versandt durch</p><img src="mailfile:eegfaktura-logo" alt="eegfaktura" width="335">`,
		"", &Data{}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Contains(t, r.Html, `<p>versandt durch</p><img src="cid:eegfaktura-logo-1"`)
	assert.Equal(t, 1, strings.Count(r.Html, FixedFooterHtml))
	require.Len(t, r.Inline, 1, "the built-in logo of an older text is still sent")
	assert.Equal(t, "eegfaktura-logo-1", *r.Inline[0].ContentId)
}

func TestSendStored_FixedFooter(t *testing.T) {
	store := &fakeStore{templates: map[string]*model.MailTemplate{
		"TE000300/zp-inactive": {Tenant: "TE000300", MailKey: KeyZpInactive, Version: 1, Subject: "S", BodyHtml: `<p>Eigener Text</p>`},
	}}
	var sent []sentMail
	handled, err := SendStored(context.Background(), store, capture(&sent), Lookup(KeyZpInactive), "TE000300",
		"maria@example.org", nil, &Data{}, "AT1")
	require.NoError(t, err)
	require.True(t, handled)
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].body, "<p>Eigener Text</p>\n"+FixedFooterHtml)
	assert.Equal(t, "Eigener Text\n\n"+FixedFooterText, plainText(sent[0].body[strings.Index(sent[0].body, "<body>"):]))
}
