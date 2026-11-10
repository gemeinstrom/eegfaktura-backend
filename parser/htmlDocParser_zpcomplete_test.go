package parser

import (
	"os"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/config"
	"at.ourproject/vfeeg-backend/config/configtest"
	"at.ourproject/vfeeg-backend/model"
	"gopkg.in/guregu/null.v4"
)

// Regression guard for the zp-complete (Zählpunkt-aktiv) mail: the template
// references {{.MeteringPoint}}, which must be provided by the template data
// struct built in sendMailFromTemplate. If the field is dropped again the
// render fails with "can't evaluate field MeteringPoint" and the mail is lost.
func TestParseTemplateZpCompleteMeteringPoint(t *testing.T) {
	configtest.Set(t, "file-content.templates", "../public")

	eeg := &model.Eeg{
		Name:          "TE-EEG",
		Description:   "TEST EEG",
		ContactPerson: null.StringFrom("Max Sonnenmann"),
		Contact:       model.Contact{Phone: null.StringFrom("123456789")},
	}
	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{FirstName: "Max"},
		Contact:            model.ContactInfo{Email: null.StringFrom("my@mail.com")},
	}
	meter := "AT0010000000000000000000000111"

	data := struct {
		Eeg            *model.Eeg
		Participant    *model.EegParticipant
		Meteringpoints []string
		MeteringPoint  string
	}{eeg, participant, []string{meter}, meter}

	buf, err := ParseTemplate(os.DirFS("../public/templates"), "zp-complete-mail-template.html", data)
	if err != nil {
		t.Fatalf("zp-complete template must render without error, got: %v", err)
	}
	if !strings.Contains(buf.String(), meter) {
		t.Errorf("rendered zp-complete mail should contain the metering point %q; got:\n%s", meter, buf.String())
	}
}

// TestZpCompleteMailRendersFromEmbeddedDefault: the zp-complete ("Zählpunkt
// aktiv") mail — config and template — renders from the defaults embedded in
// the binary, so a deployment sends it without anything on the PVC. Since
// default version 2 it has no inline logo; the fixed footer replaces it.
func TestZpCompleteMailRendersFromEmbeddedDefault(t *testing.T) {
	fsys := embeddedTemplates()

	cfg, err := config.ReadActivationMailTemplateConfig(fsys, "zp-complete-mail-template.toml")
	if err != nil {
		t.Fatalf("read embedded zp-complete config: %v", err)
	}

	meter := "AT0010000000000000000000000111"
	eeg := &model.Eeg{
		Name:        "TE-EEG",
		Description: "TEST EEG",
		Contact:     model.Contact{Phone: null.StringFrom("123456789")},
	}
	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{FirstName: "Max"},
		Contact:            model.ContactInfo{Email: null.StringFrom("my@mail.com")},
	}
	data := struct {
		Eeg            *model.Eeg
		Participant    *model.EegParticipant
		Meteringpoints []string
		MeteringPoint  string
	}{eeg, participant, []string{meter}, meter}

	buf, err := ParseTemplate(fsys, cfg.TemplateFile, data)
	if err != nil {
		t.Fatalf("render embedded zp-complete template: %v", err)
	}
	if body := buf.String(); !strings.Contains(body, meter) || !strings.Contains(body, "dein Zählpunkt") {
		t.Errorf("rendered mail missing expected content; got:\n%s", body)
	}

	if body := buf.String(); strings.Contains(body, "versandt durch") || strings.Contains(body, "eegfaktura-logo") {
		t.Errorf("the default must not contain \"versandt durch\" or the logo; got:\n%s", body)
	}
	if inline := buildInlineContent(fsys, cfg.InlinePictures); len(inline) != 0 {
		t.Errorf("expected no inline picture, got %d", len(inline))
	}
}
