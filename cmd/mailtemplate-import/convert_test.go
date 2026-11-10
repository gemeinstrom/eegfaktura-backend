package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var mailfileRef = regexp.MustCompile(`mailfile:([A-Za-z0-9-]+)`)

// The ten hand-made templates in public/<tenant>/templates: every one converts
// or stops with a clear finding.
func TestConvertTheTenHandMadeTemplates(t *testing.T) {
	blocked := map[string]string{
		"CC100438": "Logo_4c_cmyk.jpg", // 2.3 MB logo, must be made smaller first
		// link rules and limits of 2026-10-26: the community has to change the text first
		"RC100915": "Höchstens 3 Links pro Mail (jetzt 9)",
		"RC101162": "PDF-Anhänge sind derzeit nicht möglich",
		"RC101370": "Höchstens 3 Links pro Mail (jetzt 9)",
	}
	entries, err := os.ReadDir("../../public")
	require.NoError(t, err)
	count := 0
	for _, e := range entries {
		dir := filepath.Join("../../public", e.Name(), "templates")
		if e.Name() == "templates" || !e.IsDir() {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		count++
		t.Run(e.Name(), func(t *testing.T) {
			convs := ConvertTenant(os.DirFS(dir), e.Name())
			require.Len(t, convs, 1)
			c := convs[0]
			assert.Equal(t, mailtemplate.KeyConsentRequest, c.Key)
			if want, ok := blocked[e.Name()]; ok {
				require.True(t, c.Blocked())
				assert.Contains(t, findingTexts(c), want)
				return
			}
			require.False(t, c.Blocked(), findingTexts(c))
			assert.NotContains(t, c.Body, "{{")
			assert.NotContains(t, c.Body, "}}")
			assert.NotContains(t, c.Body, "cid:")
			assert.Equal(t, "Aktivierung im Serviceportal", c.Subject)

			raw, err := os.ReadFile(filepath.Join(dir, "AktivierungsEmail-template.html"))
			require.NoError(t, err)
			if strings.Contains(string(raw), "{{.Participant.FirstName}}") {
				assert.Contains(t, c.Body, `data-ph="member.firstName"`)
			}
			if strings.Contains(string(raw), "range .Meteringpoints") {
				assert.Contains(t, c.Body, `data-ph="meteringPoints"`)
			}

			// every image reference is an upload of this mail or the built-in logo
			ids := map[string]bool{mailtemplate.BuiltinLogoId: true}
			for _, f := range c.Files {
				ids[f.PreId] = true
			}
			for _, m := range mailfileRef.FindAllStringSubmatch(c.Body, -1) {
				assert.True(t, ids[m[1]], m[0])
			}

			// what the import saves renders with sample data
			_, err = mailtemplate.Render(mailtemplate.Lookup(c.Key), c.Subject, c.Body, c.AttachmentPreId(), mailtemplate.SampleData(nil), uploadsAsFiles(c), mailtemplate.ImagesDataURI)
			assert.NoError(t, err)
		})
	}
	assert.Equal(t, 10, count)
}

// Member mails allow no PDF attachment for now (2026-10-26): a template with one stops.
func TestConvert_RC101162StopsOnPdf(t *testing.T) {
	c := ConvertTenant(os.DirFS("../../public/RC101162/templates"), "RC101162")[0]
	require.True(t, c.Blocked())
	assert.Contains(t, findingTexts(c), "PDF-Anhänge sind derzeit nicht möglich")
	for _, f := range c.Files {
		assert.NotEqual(t, "attachment", f.Kind)
	}
}

func templateFS(html string, extra ...string) fstest.MapFS {
	fs := fstest.MapFS{
		"activation-mail-template.toml": {Data: []byte(`TemplateFile="t.html"` + "\n" + strings.Join(extra, "\n"))},
		"t.html":                        {Data: []byte("<html><body>" + html + "</body></html>")},
	}
	return fs
}

func TestConvert_Actions(t *testing.T) {
	c := ConvertTenant(templateFS(`<p>Hallo {{.Participant.FirstName}} {{ .Participant.LastName }}</p>
		{{if .Eeg.Contact.Phone.Valid}}<div>T: {{.Eeg.Contact.Phone.String}}</div>{{end}}
		<p>x</p>
		{{if .Eeg.ContactPerson.Valid}}<div>{{.Eeg.ContactPerson.String}}</div>{{end}}
		{{if .Eeg.Website.Valid}}<div>{{.Eeg.Website.String}}</div>{{end}}`), "TE000300")[0]
	require.False(t, c.Blocked(), findingTexts(c))
	assert.Contains(t, c.Body, `Hallo <span data-ph="member.firstName">Vorname</span> <span data-ph="member.lastName">Nachname</span>`)
	assert.Contains(t, c.Body, `T: <span data-ph="eeg.phone">Telefon der EEG</span><br/>`, "a single optional line keeps its placeholder")
	assert.Equal(t, 1, strings.Count(c.Body, `data-ph="eeg.contactBlock"`), "consecutive optional lines become one contact block")
	assert.NotContains(t, c.Body, `eeg.website`)
}

func TestConvert_UnmappableStops(t *testing.T) {
	for name, tpl := range map[string]string{
		"unknown field":       `<p>{{.Participant.Contact.Email.String}}</p>`,
		"condition":           `<p>{{if .Participant.FirstName}}Hallo{{end}}</p>`,
		"range other":         `<p>{{range .Participant.MeteringPoint}}{{.MeteringPoint}}{{end}}</p>`,
		"contact person bare": `<p>{{.Eeg.ContactPerson.String}}</p>`,
		"wrong mail key":      `<p>{{.Reason}}</p>`,
		"unclosed":            `<p>{{.Participant.FirstName</p>`,
		"in href":             `<p><a href="mailto:{{.Eeg.Contact.Email.String}}">Mail</a></p>`,
		"in img src":          `<p>x</p><img src="https://x.example/{{.Eeg.Name}}.png">`,
		"in style":            `<p style="color: {{.Eeg.Name}}">x</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			c := ConvertTenant(templateFS(tpl), "TE000300")[0]
			assert.True(t, c.Blocked(), name)
			assert.Empty(t, c.Body)
			assert.NotContains(t, findingTexts(c), "data-ph")
		})
	}
}

func TestConvert_ActionInAttributeIsNamed(t *testing.T) {
	c := ConvertTenant(templateFS(`<p>Hallo {{.Participant.FirstName}}, <a href="mailto:{{.Eeg.Contact.Email.String}}">schreib uns</a></p>`), "TE000300")[0]
	require.True(t, c.Blocked())
	assert.Contains(t, findingTexts(c), `href="mailto:{{.Eeg.Contact.Email.String}}"`)
}

// Links the save would refuse stop the import with the message of the rule.
func TestConvert_LinkRules(t *testing.T) {
	c := ConvertTenant(templateFS(`<p><a href="https://bit.ly/x">Portal</a></p>`), "TE000300")[0]
	require.True(t, c.Blocked())
	assert.Contains(t, findingTexts(c), "Links: Links zu Kurz-Link-Diensten sind nicht erlaubt: bit.ly")
}

func TestConvert_Images(t *testing.T) {
	png, err := os.ReadFile("../../public/templates/eegfaktura-logo.png") // 528x142
	require.NoError(t, err)
	fs := templateFS(`<p>a</p><img src="cid:logo" style="max-height: 71px"/><img src="cid:logo"/><img src="cid:eegfaktura-logo-1"/><img src="cid:missing"/>`,
		`InlinePictures = [ { Filepath="l.png", contentId="logo" }, { Filepath="eegfaktura-logo.png", contentId="eegfaktura-logo-1" }, { Filepath="u.png", contentId="unused" } ]`)
	fs["l.png"] = &fstest.MapFile{Data: png}
	c := ConvertTenant(fs, "TE000300")[0]
	require.False(t, c.Blocked(), findingTexts(c))
	require.Len(t, c.Files, 1, "an image used twice is uploaded once, the eegfaktura logo is built in")
	assert.Contains(t, c.Body, `<img src="mailfile:`+c.Files[0].PreId+`" width="264"/>`)
	assert.Contains(t, c.Body, `src="mailfile:eegfaktura-logo"`)
	assert.Contains(t, findingTexts(c), "cid:missing")
	assert.Contains(t, findingTexts(c), "cid:unused")

	fs["l.png"] = &fstest.MapFile{Data: []byte("not an image")}
	assert.True(t, ConvertTenant(fs, "TE000300")[0].Blocked(), "a disguised image stops the import")
}

// The legacy closing "versandt durch" + logo would say it twice next to the fixed footer: it is dropped with a Hinweis.
func TestConvert_DropsLegacySignature(t *testing.T) {
	logo := `InlinePictures = [ { Filepath="eegfaktura-logo.png", contentId="eegfaktura-logo-1" } ]`
	for name, tail := range map[string]string{
		"as in the old default": "<p>versandt durch</p>\n<img src=\"cid:eegfaktura-logo-1\" style=\"max-height: 90px\"/>\n",
		"logo in a paragraph":   "<p> Versandt durch </p><p><img src=\"cid:eegfaktura-logo-1\"/></p>",
	} {
		t.Run(name, func(t *testing.T) {
			c := ConvertTenant(templateFS("<p>Hallo</p><p>Gruß</p>"+tail, logo), "TE000300")[0]
			require.False(t, c.Blocked(), findingTexts(c))
			assert.Equal(t, "<p>Hallo</p><p>Gruß</p>", strings.TrimSpace(c.Body))
			assert.Contains(t, findingTexts(c), "„versandt durch“ mit eegfaktura-Logo entfällt")
		})
	}

	// a logo that is not the closing signature stays, as the built-in image
	for name, html := range map[string]string{
		"logo without versandt durch": `<p>Gruß</p><img src="cid:eegfaktura-logo-1"/>`,
		"text after the logo":         `<p>versandt durch</p><img src="cid:eegfaktura-logo-1"/><p>PS</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			c := ConvertTenant(templateFS(html, logo), "TE000300")[0]
			require.False(t, c.Blocked(), findingTexts(c))
			assert.Contains(t, c.Body, `src="mailfile:eegfaktura-logo"`)
			assert.NotContains(t, findingTexts(c), "versandt durch")
		})
	}
}

// The import uploads drafts, swaps the ids and saves with the current version;
// on an error the community's saved version stays and the drafts are deleted.
func TestImportMail(t *testing.T) {
	png, err := os.ReadFile("../../public/templates/eegfaktura-logo.png")
	require.NoError(t, err)
	fs := templateFS(`<p>Hallo {{.Participant.FirstName}}</p><img src="cid:logo"/><img src="cid:logo2"/>`,
		`InlinePictures = [ { Filepath="l.png", contentId="logo" }, { Filepath="l2.png", contentId="logo2" } ]`)
	fs["l.png"] = &fstest.MapFile{Data: png}
	fs["l2.png"] = &fstest.MapFile{Data: png}
	c := ConvertTenant(fs, "TE000300")[0]
	require.False(t, c.Blocked(), findingTexts(c))

	var calls []string
	var saved map[string]interface{}
	failPut, customized := false, false
	ids := []string{"11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000"}
	uploads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		assert.Equal(t, "TE000300", r.Header.Get("tenant"))
		switch {
		case r.Method == "GET":
			if customized {
				_, _ = io.WriteString(w, `{"customized":true,"version":3}`)
			} else {
				_, _ = io.WriteString(w, `{"customized":false,"version":0}`)
			}
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/files"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"`+ids[uploads%2]+`"}`)
			uploads++
		case r.Method == "PUT":
			if failPut {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			saved = nil
			_ = json.NewDecoder(r.Body).Decode(&saved)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/test"):
			_, _ = io.WriteString(w, `{"sentTo":"eeg@example.org"}`)
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	client := &apiClient{base: srv.URL, token: "secret", http: srv.Client()}
	const p = "/eeg/mailtemplates/consent-request"

	require.NoError(t, client.importMail(c, false, "eeg"))
	assert.Equal(t, []string{"GET " + p, "POST " + p + "/files", "POST " + p + "/files", "PUT " + p, "POST " + p + "/test"}, calls)
	assert.Contains(t, saved["bodyHtml"], "mailfile:"+ids[0])
	assert.Contains(t, saved["bodyHtml"], "mailfile:"+ids[1])
	assert.NotContains(t, saved["bodyHtml"], c.Files[0].PreId)
	assert.Nil(t, saved["attachmentFileId"])
	assert.Equal(t, float64(0), saved["version"])

	// a customised mail is skipped without -overwrite
	calls, customized, uploads = nil, true, 0
	assert.Error(t, client.importMail(c, false, ""))
	assert.Equal(t, []string{"GET " + p}, calls)

	// -overwrite saves over the current version, the saved version is never deleted first
	calls = nil
	require.NoError(t, client.importMail(c, true, ""))
	assert.Equal(t, []string{"GET " + p, "POST " + p + "/files", "POST " + p + "/files", "PUT " + p}, calls)
	assert.Equal(t, float64(3), saved["version"])

	// a failed save leaves the saved version alone and deletes only the drafts
	calls, failPut, uploads = nil, true, 0
	assert.Error(t, client.importMail(c, true, ""))
	assert.Equal(t, []string{"GET " + p, "POST " + p + "/files", "POST " + p + "/files", "PUT " + p,
		"DELETE " + p + "/files/" + ids[0], "DELETE " + p + "/files/" + ids[1]}, calls)
}

func findingTexts(c *Conversion) string {
	var out []string
	for _, f := range c.Findings {
		out = append(out, f.Text)
	}
	return strings.Join(out, "\n")
}

func uploadsAsFiles(c *Conversion) []model.MailTemplateFile {
	var out []model.MailTemplateFile
	for _, f := range c.Files {
		out = append(out, model.MailTemplateFile{Id: f.PreId, Kind: f.Kind, Filename: f.Filename, MimeType: f.MimeType, Content: f.Content})
	}
	return out
}
