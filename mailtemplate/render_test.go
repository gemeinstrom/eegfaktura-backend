package mailtemplate

import (
	"bytes"
	"errors"
	"html/template"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/public"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"gopkg.in/guregu/null.v4"
)

func testEeg() *model.Eeg {
	return &model.Eeg{
		Id:          "TE000300",
		Name:        "TE-EEG",
		Description: "Test Energiegemeinschaft",
		EegAddress:  model.EegAddress{Street: "Solargasse", StreetNumber: "2", Zip: "1111", City: "Solarcity"},
		Contact:     model.Contact{Phone: null.StringFrom("+43 1 234"), Email: null.StringFrom("info@eeg.example")},
		Optionals:   model.Optionals{Website: null.StringFrom("https://eeg.example")},
	}
}

func testParticipant() *model.EegParticipant {
	return &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{FirstName: "Maria", LastName: "Muster"},
		Contact:            model.ContactInfo{Email: null.StringFrom("maria@example.org")},
	}
}

// visible returns what a reader sees: the body text with collapsed whitespace and the image sources.
func visible(t *testing.T, doc string) (string, []string) {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	var words []string
	var images []string
	var walk func(n *html.Node, inBody bool)
	walk = func(n *html.Node, inBody bool) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			inBody = true
		}
		if inBody && n.Type == html.TextNode {
			words = append(words, strings.Fields(n.Data)...)
		}
		if inBody && n.Type == html.ElementNode && n.DataAtom == atom.Img {
			images = append(images, attr(n, "src"))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inBody)
		}
	}
	walk(root, false)
	return strings.Join(words, " "), images
}

func renderLegacy(t *testing.T, file string, data interface{}) string {
	t.Helper()
	tpl, err := template.ParseFS(public.Templates, "templates/"+file)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tpl.Execute(&buf, data))
	return buf.String()
}

// Saving the default unchanged must send what the legacy embedded templates send today.
func TestDefaultMatchesLegacyTemplates(t *testing.T) {
	eeg, p := testEeg(), testParticipant()
	meter := "AT0030000000000000000000000012345"
	activation := struct {
		Eeg            *model.Eeg
		Participant    *model.EegParticipant
		Meteringpoints []string
		MeteringPoint  string
	}{eeg, p, []string{meter}, meter}
	inactive := struct {
		Eeg                                       *model.Eeg
		Participant                               *model.EegParticipant
		MeteringPoint, Direction, EndDate, Reason string
	}{eeg, p, meter, "Bezug", "31.12.2026", "Deine Energiegemeinschaft hat diesen Zählpunkt abgemeldet."}

	data := &Data{FirstName: "Maria", LastName: "Muster", MeteringPoints: []string{meter},
		Direction: "Bezug", EndDate: "31.12.2026", Reason: inactive.Reason, Eeg: eeg}

	cases := []struct {
		key, legacy string
		legacyData  interface{}
	}{
		{KeyConsentRequest, "AktivierungsEmail-template.html", activation},
		{KeyZpActive, "zp-complete-mail-template.html", activation},
		{KeyZpInactive, "zp-inactive-mail-template.html", inactive},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			def := Lookup(tc.key)
			r, err := Render(def, def.DefaultSubject, def.DefaultBody, "", data, nil, ImagesInline)
			require.NoError(t, err)

			// the parser sends the legacy default with the same fixed footer (AppendFixedFooter)
			wantText, wantImages := visible(t, AppendFixedFooter(renderLegacy(t, tc.legacy, tc.legacyData)))
			gotText, gotImages := visible(t, r.Html)
			assert.Equal(t, wantText, gotText)
			assert.True(t, strings.HasSuffix(gotText, FixedFooterText), gotText)
			assert.Equal(t, wantImages, gotImages)
			// since default version 2 without "versandt durch" and logo: the fixed footer says it
			assert.Empty(t, gotImages)
			assert.Empty(t, r.Inline)
			assert.Equal(t, 1, strings.Count(gotText, "versandt durch"), gotText)
			assert.NotContains(t, def.DefaultBody, "versandt durch")
			assert.NotContains(t, def.DefaultBody, BuiltinLogoId)
			assert.Nil(t, r.Attachment)
			assert.Equal(t, def.DefaultSubject, r.Subject)
		})
	}
}

func TestRender_PlaceholderValuesAreEscaped(t *testing.T) {
	def := Lookup(KeyConsentRequest)
	eeg := testEeg()
	eeg.Description = `<b>EEG</b> & Co`
	data := &Data{FirstName: `<script>alert("x")</script>`, MeteringPoints: []string{`<img src=x onerror=alert(1)>`}, Eeg: eeg}
	r, err := Render(def, `Hallo {member.firstName}`,
		`<p>Hallo <span data-ph="member.firstName">Vorname</span></p><div data-ph="meteringPoints">x</div><div data-ph="eeg.contactBlock">x</div>`,
		"", data, nil, ImagesInline)
	require.NoError(t, err)
	assert.NotContains(t, r.Html, "<script>")
	assert.NotContains(t, r.Html, "<img src=x")
	assert.NotContains(t, r.Html, "<b>EEG</b>")
	assert.Contains(t, r.Html, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`)
	assert.Contains(t, r.Html, `<li>&lt;img src=x onerror=alert(1)&gt;</li>`)
	assert.Contains(t, r.Html, `<div>&lt;b&gt;EEG&lt;/b&gt; &amp; Co</div>`)
	// the subject is plain text; eda-xp sets it as a header, not as HTML
	assert.Equal(t, `Hallo <script>alert("x")</script>`, r.Subject)
	assert.Contains(t, r.Html, `<title>Hallo &lt;script&gt;`)
}

func TestRender_MissingValuesAreEmpty(t *testing.T) {
	def := Lookup(KeyZpInactive)
	r, err := Render(def, def.DefaultSubject,
		`<p>Nr. <span data-ph="member.number">Mitgliedsnummer</span>, Ende <span data-ph="endDate">Enddatum</span>.</p><div data-ph="eeg.contactBlock">x</div>`,
		"", &Data{}, nil, ImagesInline)
	require.NoError(t, err)
	text, _ := visible(t, r.Html)
	assert.Equal(t, "Nr. , Ende . "+FixedFooterText, text, "only the fixed footer follows")
	assert.NotContains(t, r.Html, "data-ph")
}

// Without direction or end date the zp-inactive default must not read "()" or "ab dem ".
func TestRender_InactiveDefaultWithoutDirectionAndEndDate(t *testing.T) {
	def := Lookup(KeyZpInactive)
	r, err := Render(def, def.DefaultSubject, def.DefaultBody, "", &Data{FirstName: "Maria", MeteringPoints: []string{"AT1"},
		Reason: "Weil.", Eeg: testEeg()}, nil, ImagesInline)
	require.NoError(t, err)
	text, _ := visible(t, r.Html)
	assert.Contains(t, text, "dein Zählpunkt AT1 ist nicht mehr Teil der Energiegemeinschaft Test Energiegemeinschaft.")
	assert.NotContains(t, text, "()")
	assert.NotContains(t, text, "ab dem")

	r, err = Render(def, def.DefaultSubject, def.DefaultBody, "", &Data{MeteringPoints: []string{"AT1"}, Direction: "Einspeisung",
		Eeg: testEeg()}, nil, ImagesInline)
	require.NoError(t, err)
	text, _ = visible(t, r.Html)
	assert.Contains(t, text, "dein Zählpunkt AT1 (Einspeisung) ist nicht mehr Teil")
}

// withAttachment is a member mail definition that allows a PDF (member mails allow none for now, the code stays).
func withAttachment(key string) *Definition {
	def := *Lookup(key)
	def.Limits.MaxAttachments = 1
	return &def
}

func TestRender_ImagesAndAttachment(t *testing.T) {
	def := withAttachment(KeyZpActive)
	png := []byte("\x89PNG\r\n\x1a\nfake")
	files := []model.MailTemplateFile{
		{Id: ownImage, Kind: model.MailFileKindImage, Filename: "logo.png", MimeType: "image/png", Content: png},
		{Id: foreignImage, Kind: model.MailFileKindImage, Filename: "unused.png", MimeType: "image/png", Content: png},
		{Id: "a1", Kind: model.MailFileKindAttachment, Filename: "anleitung.pdf", MimeType: "application/pdf", Content: []byte("%PDF-1.4")},
	}
	body := `<p>a</p><img src="mailfile:` + ownImage + `" alt="Logo"><p><img src="mailfile:` + ownImage + `"></p>`

	r, err := Render(def, "Betreff", body, "a1", &Data{}, files, ImagesInline)
	require.NoError(t, err)
	_, images := visible(t, r.Html)
	assert.Equal(t, []string{"cid:" + ownImage, "cid:" + ownImage}, images)
	require.Len(t, r.Inline, 1, "an image used twice is attached once, unused images not at all")
	assert.Equal(t, ownImage, *r.Inline[0].ContentId)
	require.NotNil(t, r.Attachment)
	assert.Equal(t, "anleitung.pdf", r.Attachment.Filename)
	assert.Equal(t, "application/pdf", r.Attachment.MimeType)

	r, err = Render(def, "Betreff", body, "a1", &Data{}, files, ImagesDataURI)
	require.NoError(t, err)
	_, images = visible(t, r.Html)
	assert.True(t, strings.HasPrefix(images[0], "data:image/png;base64,"))
	assert.Empty(t, r.Inline)

	// an image that is not among the files (deleted, another mail or community) fails the mail
	_, err = Render(def, "Betreff", `<p>a</p><img src="mailfile:00000000-0000-0000-0000-000000000000">`, "", &Data{}, files, ImagesInline)
	assert.ErrorIs(t, err, ErrMissingFile)
	_, err = Render(def, "Betreff", `<p>a</p>`, "missing-pdf", &Data{}, files, ImagesInline)
	assert.ErrorIs(t, err, ErrMissingFile)
}

// A stored text with more images than allowed (saved before the limit of 3) is not rendered.
func TestRender_TooManyImages(t *testing.T) {
	def := Lookup(KeyZpActive)
	var files []model.MailTemplateFile
	body := ""
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444"} {
		files = append(files, model.MailTemplateFile{Id: id, Kind: model.MailFileKindImage, MimeType: "image/png", Content: []byte("png")})
		body += `<img src="mailfile:` + id + `">`
	}
	_, err := Render(def, "S", "<p>a</p>"+body, "", &Data{}, files, ImagesInline)
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "Höchstens 3 Bilder je Mail (jetzt 4).", verr.Message)

	// the same image three times and the built-in logo are fine
	_, err = Render(def, "S", "<p>a</p>"+strings.Repeat(`<img src="mailfile:`+files[0].Id+`">`, 3)+
		`<img src="mailfile:eegfaktura-logo">`+`<img src="mailfile:`+files[1].Id+`">`+`<img src="mailfile:`+files[2].Id+`">`,
		"", &Data{}, files, ImagesInline)
	assert.NoError(t, err)
}

func TestRender_TooLarge(t *testing.T) {
	def := withAttachment(KeyZpActive)
	big := make([]byte, def.Limits.MaxTotalBytes/2+1)
	files := []model.MailTemplateFile{
		{Id: ownImage, Kind: model.MailFileKindImage, Filename: "a.png", MimeType: "image/png", Content: big},
		{Id: "a1", Kind: model.MailFileKindAttachment, Filename: "b.pdf", MimeType: "application/pdf", Content: big},
	}
	_, err := Render(def, "Betreff", `<img src="mailfile:`+ownImage+`">`, "a1", &Data{}, files, ImagesInline)
	assert.True(t, errors.Is(err, ErrTooLarge), "%v", err)
}

func TestRender_UnknownPlaceholderFails(t *testing.T) {
	_, err := Render(Lookup(KeyZpActive), "Betreff", `<p><span data-ph="reason">x</span></p>`, "", &Data{}, nil, ImagesInline)
	assert.Error(t, err)
}

func TestCatalog(t *testing.T) {
	keys := map[string]bool{}
	for _, def := range Catalog() {
		keys[def.Key] = true
		assert.Equal(t, 2, def.DefaultVersion, "2: fixed footer instead of \"versandt durch\" and logo in the text")
		assert.NotNil(t, LookupByLegacyConfig(def.LegacyConfig))
		_, err := public.Templates.ReadFile("templates/" + def.LegacyConfig)
		assert.NoError(t, err, def.LegacyConfig)
	}
	assert.Equal(t, map[string]bool{KeyConsentRequest: true, KeyZpActive: true, KeyZpInactive: true}, keys)
	assert.Nil(t, Lookup("billing-document"))
}

func TestTemplateBracesAreBrokenUp(t *testing.T) {
	def := Lookup(KeyZpActive)
	stored, err := SanitizeBody(def, `<p>Hallo {{name}} und {{{x}}}</p><p><a href="https://x.example/?q={{1}}">{{link}}</a></p>`, nil)
	require.NoError(t, err)
	assert.Contains(t, stored, `Hallo {<!-- -->{name}} und {<!-- -->{<!-- -->{x}}}`)
	assert.Contains(t, stored, `href="https://x.example/?q={{1}}"`, "attributes stay")
	assert.Contains(t, stored, `>{<!-- -->{link}}</a>`)
	again, err := SanitizeBody(def, stored, nil)
	require.NoError(t, err)
	assert.Equal(t, stored, again, "idempotent")

	r, err := Render(def, "S", stored, "", &Data{FirstName: "{{constructor}}", Eeg: testEeg()}, nil, ImagesInline)
	require.NoError(t, err)
	assert.NotContains(t, strings.ReplaceAll(r.Html, `href="https://x.example/?q={{1}}"`, ""), "{{")
	r, err = Render(def, "S", `<p><span data-ph="member.firstName">x</span></p>`, "", &Data{FirstName: "{{constructor}}"}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Contains(t, r.Html, "{<!-- -->{constructor}}", "member values are broken up too")
}

// {eeg.name} is the community's designation in every member mail, as in the billing mail (decision 25.10.2026);
// the short name only without a designation. eeg.description is no longer offered but texts saved with it render.
func TestRender_EegNameIsTheDesignation(t *testing.T) {
	def := Lookup(KeyZpActive)
	body := `<p><span data-ph="eeg.name">x</span></p>`
	r, err := Render(def, "Hallo {eeg.name}", body, "", &Data{Eeg: testEeg()}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Equal(t, "Hallo Test Energiegemeinschaft", r.Subject)
	assert.Contains(t, r.Html, "Test Energiegemeinschaft")
	assert.NotContains(t, r.Html, "TE-EEG")

	short := testEeg()
	short.Description = " "
	r, err = Render(def, "Hallo {eeg.name}", body, "", &Data{Eeg: short}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Equal(t, "Hallo TE-EEG", r.Subject)

	r, err = Render(def, "Hallo {eeg.description}", `<p><span data-ph="eeg.description">x</span></p>`, "",
		&Data{Eeg: testEeg()}, nil, ImagesInline)
	require.NoError(t, err)
	assert.Equal(t, "Hallo Test Energiegemeinschaft", r.Subject)
	assert.Contains(t, r.Html, "Test Energiegemeinschaft")

	for _, ph := range def.Placeholders {
		assert.NotEqual(t, "eeg.description", ph.Key, "no longer offered")
	}
}
