package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	log "github.com/sirupsen/logrus"
)

// render renders an unsaved version with the sample member and the real community; false after an answer.
func (h *MailTemplateHandler) render(w http.ResponseWriter, r *http.Request, tenant string, def *mailtemplate.Definition,
	req *mailTemplateRequest, mode mailtemplate.ImageMode) (*mailtemplate.Rendered, *model.Eeg, bool) {

	// an unsaved text may use the saved version's files and the drafts of this mail
	files, ok := h.loadFiles(w, r, tenant, def, true)
	if !ok {
		return nil, nil, false
	}
	subject, body, ok := sanitize(w, def, req, imageCheck(files))
	if !ok {
		return nil, nil, false
	}
	attachmentId, present, aerr := req.attachment()
	if aerr != nil {
		respondValidation(w, aerr)
		return nil, nil, false
	}
	if !present {
		t, ok := h.loadTemplate(w, r, tenant, def)
		if !ok {
			return nil, nil, false
		}
		if t != nil {
			attachmentId = t.AttachmentFileId
		}
	}
	attachment := ""
	if attachmentId != nil {
		attachment = *attachmentId
	}
	eeg, err := h.db.GetEegById(r.Context(), tenant)
	if err != nil || eeg == nil || !strings.EqualFold(eeg.Id, tenant) {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"message": "EEG nicht gefunden"})
		return nil, nil, false
	}
	rendered, err := mailtemplate.Render(def, subject, body, attachment, mailtemplate.SampleData(eeg), files, mode)
	if errors.Is(err, mailtemplate.ErrMissingFile) {
		respondValidation(w, &mailtemplate.ValidationError{Field: "attachmentFileId", Message: "Den PDF-Anhang gibt es nicht (mehr)."})
		return nil, nil, false
	}
	if errors.Is(err, mailtemplate.ErrTooLarge) {
		respondValidation(w, &mailtemplate.ValidationError{Field: "files", Message: fmt.Sprintf("Die Dateien der Mail sind zusammen größer als %s.", formatBytes(def.Limits.MaxTotalBytes))})
		return nil, nil, false
	}
	var verr *mailtemplate.ValidationError
	if errors.As(err, &verr) {
		respondValidation(w, verr)
		return nil, nil, false
	}
	if err != nil {
		respondServerError(r.Context(), w, tenant, err)
		return nil, nil, false
	}
	return rendered, eeg, true
}

func (h *MailTemplateHandler) preview() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		req, ok := decodeRequest(w, r)
		if !ok {
			return
		}
		rendered, _, ok := h.render(w, r, tenant, def, req, mailtemplate.ImagesDataURI)
		if !ok {
			return
		}
		respondWithJSON(w, http.StatusOK, map[string]string{"subject": rendered.Subject, "html": rendered.Html})
	}
}

func (h *MailTemplateHandler) test() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		req, ok := decodeRequest(w, r)
		if !ok {
			return
		}
		if req.Recipient != "me" && req.Recipient != "eeg" {
			respondValidation(w, &mailtemplate.ValidationError{Field: "recipient", Message: "recipient muss me oder eeg sein."})
			return
		}
		rendered, eeg, ok := h.render(w, r, tenant, def, req, mailtemplate.ImagesInline)
		if !ok {
			return
		}
		// the recipient is chosen by the service, never given by the browser
		to := strings.TrimSpace(claims.Email)
		if req.Recipient == "eeg" {
			to = strings.TrimSpace(eeg.Email.String)
		}
		if to == "" {
			msg := "Für deinen Login ist keine E-Mail-Adresse hinterlegt."
			if req.Recipient == "eeg" {
				msg = "Für die Gemeinschaft ist keine E-Mail-Adresse hinterlegt."
			}
			respondValidation(w, &mailtemplate.ValidationError{Field: "recipient", Message: msg})
			return
		}
		if err := h.sendMail(tenant, to, testSubjectPrefix+rendered.Subject, nil,
			bytes.NewBufferString(rendered.Html), rendered.Inline, rendered.Attachment); err != nil {
			log.WithContext(r.Context()).WithField("tenant", tenant).WithError(err).Log(services.MailErrorLevel(err), "mail template: test mail failed")
			code, msg := http.StatusBadGateway, "Die Testmail konnte nicht verschickt werden."
			if errors.Is(err, services.ErrMailUnconfirmed) {
				code, msg = http.StatusGatewayTimeout, services.ErrMailUnconfirmed.Error()
			}
			respondWithJSON(w, code, map[string]string{"message": msg})
			return
		}
		respondWithJSON(w, http.StatusOK, map[string]string{"sentTo": to})
	}
}
