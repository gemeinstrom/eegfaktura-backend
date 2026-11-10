package parser

import (
	"bytes"
	"context"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"

	"at.ourproject/vfeeg-backend/config"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/public"
	"at.ourproject/vfeeg-backend/services"
	"github.com/gabriel-vasile/mimetype"
	log "github.com/sirupsen/logrus"
)

// ParseTemplate renders the HTML template named name from fsys with data.
func ParseTemplate(fsys fs.FS, name string, data interface{}) (*bytes.Buffer, error) {

	t, err := template.ParseFS(fsys, name)
	if err != nil {
		return nil, err
	}
	buf := new(bytes.Buffer)
	if err = t.Execute(buf, data); err != nil {
		return nil, err
	}
	return buf, nil
}

// embeddedTemplates returns the default mail templates and their inline assets
// embedded in the binary (public/templates). Hand-made templates on the data
// volume are no longer read: a community's own text is its saved version
// (base.mail_template); cmd/mailtemplate-import converts the volume files once.
func embeddedTemplates() fs.FS {
	embedded, err := fs.Sub(public.Templates, "templates")
	if err != nil {
		// A fixed sub-path of an embed.FS never fails; fall back defensively.
		return public.Templates
	}
	return embedded
}

// SendActivationMailFromTemplate sends the consent-request or the zp-active mail (by template
// config name). store holds the community's customised mails; nil sends the built-in default.
func SendActivationMailFromTemplate(sendMail services.SendMailFunc, store mailtemplate.Store,
	tenant, subject string, eeg *model.Eeg, participant *model.EegParticipant, templateConfigName string) error {

	meterIds := []string{}
	gridOperator := ""
	for i := range participant.MeteringPoint {
		meterIds = append(meterIds, participant.MeteringPoint[i].MeteringPoint)
		if gridOperator == "" {
			gridOperator = participant.MeteringPoint[i].GridOperatorName.String
		}
	}
	templateData := struct {
		Eeg            *model.Eeg
		Participant    *model.EegParticipant
		Meteringpoints []string
		MeteringPoint  string
	}{eeg, participant, meterIds, strings.Join(meterIds, ", ")}

	mailData := memberMailData(eeg, participant)
	mailData.MeteringPoints = meterIds
	mailData.GridOperator = gridOperator
	return sendTemplateMail(sendMail, store, tenant, subject, templateConfigName, eeg, participant, templateData, mailData)
}

// memberMailData returns the placeholder values of a member mail.
func memberMailData(eeg *model.Eeg, participant *model.EegParticipant) *mailtemplate.Data {
	return &mailtemplate.Data{
		FirstName: participant.FirstName,
		LastName:  participant.LastName,
		Number:    participant.ParticipantNumber.String,
		Eeg:       eeg,
	}
}

// sendTemplateMail sends a member mail to the participant with the community in Cc: the
// community's customised version when it has one (store), else the built-in default (the
// template config embedded in public/templates). A participant without an e-mail address is
// skipped. Shared by the activation and the inactive mail.
func sendTemplateMail(sendMail services.SendMailFunc, store mailtemplate.Store, tenant, subject, templateConfigName string,
	eeg *model.Eeg, participant *model.EegParticipant, data interface{}, mailData *mailtemplate.Data) error {

	if store != nil && participant.Contact.Email.Valid && strings.TrimSpace(participant.Contact.Email.String) != "" {
		handled, err := mailtemplate.SendStored(context.Background(), store, sendMail,
			mailtemplate.LookupByLegacyConfig(templateConfigName), tenant, participant.Contact.Email.String,
			eeg.Email.Ptr(), mailData, strings.Join(mailData.MeteringPoints, ", "))
		if handled {
			return err
		}
	}

	tmplFS := embeddedTemplates()
	templateConfig, err := config.ReadActivationMailTemplateConfig(tmplFS, templateConfigName)
	if err != nil {
		return err
	}

	if !participant.Contact.Email.Valid || strings.TrimSpace(participant.Contact.Email.String) == "" {
		log.Warnf("Participant without email contact: %s", participant.Id)
		return nil
	}

	buf, err := ParseTemplate(tmplFS, templateConfig.TemplateFile, data)
	if err != nil {
		return err
	}

	// the built-in default ends with the same fixed footer as a customised mail
	return sendMail(tenant, participant.Contact.Email.String,
		subject, eeg.Email.Ptr(), bytes.NewBufferString(mailtemplate.AppendFixedFooter(buf.String())),
		buildInlineContent(tmplFS, templateConfig.InlinePictures),
		buildAttachment(tmplFS, templateConfig.Attachment.Name, templateConfig.Attachment.Mime),
	)
}

func buildInlineContent(tmplFS fs.FS, a []model.InlinePicture) []*services.Attachment {
	attachments := []*services.Attachment{}
	for i := range a {
		att := a[i]
		data, err := fs.ReadFile(tmplFS, att.Filepath)
		if err != nil {
			log.Errorf("Read Attachment. Reason: %+v", err)
			continue
		}
		mime := mimetype.Detect(data)
		attachments = append(attachments, &services.Attachment{
			Type:        "INLINE",
			Filename:    filepath.Base(att.Filepath),
			Filecontent: bytes.NewBuffer(data),
			MimeType:    mime.String(),
			ContentId:   &att.ContentId,
		})
	}
	return attachments
}

func buildAttachment(tmplFS fs.FS, fileName string, mime string) *services.Attachment {
	if len(fileName) == 0 {
		return nil
	}

	buff, err := fs.ReadFile(tmplFS, fileName) // read the content of file
	if err != nil {
		log.Error(err)
		return nil
	}

	return &services.Attachment{
		Type:        "DEFAULT",
		Filename:    fileName,
		Filecontent: bytes.NewBuffer(buff),
		MimeType:    mime,
		ContentId:   nil,
	}
}
