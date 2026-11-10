package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
)

// MailTemplateHandler serves /eeg/mailtemplates: the member mails a community
// customises (konzept-mail-texte-editierbar.md, section H).
type MailTemplateHandler struct {
	db       database.Database
	sendMail services.SendMailFunc
}

const (
	maxMailTemplateJSONBytes = 1 << 20
	// multipart overhead on top of the largest file
	maxMailFileUploadOverhead = 1 << 20
	testSubjectPrefix         = "[Test] "
)

func initMailTemplateRoutes(s *mux.Router, db database.Database) {
	registerMailTemplateRoutes(s, &MailTemplateHandler{db: db, sendMail: services.SendMail})
}

func registerMailTemplateRoutes(s *mux.Router, h *MailTemplateHandler) {
	s.HandleFunc("/mailtemplates", middleware.Protect(h.list())).Methods("GET")
	s.HandleFunc("/mailtemplates/{key}", middleware.Protect(h.get())).Methods("GET")
	s.HandleFunc("/mailtemplates/{key}", middleware.Protect(h.save())).Methods("PUT")
	s.HandleFunc("/mailtemplates/{key}", middleware.Protect(h.reset())).Methods("DELETE")
	s.HandleFunc("/mailtemplates/{key}/files", middleware.Protect(h.uploadFile())).Methods("POST")
	s.HandleFunc("/mailtemplates/{key}/files/{id}", middleware.Protect(h.getFile())).Methods("GET")
	s.HandleFunc("/mailtemplates/{key}/files/{id}", middleware.Protect(h.deleteFile())).Methods("DELETE")
	s.HandleFunc("/mailtemplates/{key}/preview", middleware.Protect(h.preview())).Methods("POST")
	s.HandleFunc("/mailtemplates/{key}/test", middleware.Protect(h.test())).Methods("POST")
}

type mailTemplateSummary struct {
	Key            string     `json:"key"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Customized     bool       `json:"customized"`
	UpdatedAt      *time.Time `json:"updatedAt"`
	UpdatedBy      *string    `json:"updatedBy"`
	DefaultChanged bool       `json:"defaultChanged"`
	// FixedFooter is the line the server appends to every mail (not editable, not stored)
	FixedFooter string `json:"fixedFooter"`
}

type mailTemplateDTO struct {
	Key             string                     `json:"key"`
	Name            string                     `json:"name"`
	Description     string                     `json:"description"`
	Subject         string                     `json:"subject"`
	BodyHtml        string                     `json:"bodyHtml"`
	Customized      bool                       `json:"customized"`
	Version         int                        `json:"version"`
	DefaultVersion  int                        `json:"defaultVersion"`
	DefaultChanged  bool                       `json:"defaultChanged"`
	DefaultSubject  string                     `json:"defaultSubject"`
	DefaultBodyHtml string                     `json:"defaultBodyHtml"`
	UpdatedAt       *time.Time                 `json:"updatedAt"`
	UpdatedBy       *string                    `json:"updatedBy"`
	AttachmentFile  *string                    `json:"attachmentFileId"`
	Placeholders    []mailtemplate.Placeholder `json:"placeholders"`
	Files           []model.MailTemplateFile   `json:"files"`
	Limits          mailtemplate.Limits        `json:"limits"`
	FixedFooter     string                     `json:"fixedFooter"`
}

type mailTemplateRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	BodyHtml  string `json:"bodyHtml"`
	Version   int    `json:"version"`
	// AttachmentFileId: the PDF; null = none. Preview and test mail without the field use the saved version's PDF.
	AttachmentFileId json.RawMessage `json:"attachmentFileId"`
}

// attachment returns the PDF id of a request; present=false when the field is missing.
func (req *mailTemplateRequest) attachment() (id *string, present bool, err *mailtemplate.ValidationError) {
	raw := strings.TrimSpace(string(req.AttachmentFileId))
	if raw == "" {
		return nil, false, nil
	}
	if raw == "null" {
		return nil, true, nil
	}
	var v string
	if json.Unmarshal(req.AttachmentFileId, &v) != nil || uuid.Parse(v) == nil {
		return nil, true, &mailtemplate.ValidationError{Field: "attachmentFileId", Message: "Ungültige Datei-ID."}
	}
	v = strings.ToLower(v)
	return &v, true, nil
}

func summaryOf(def *mailtemplate.Definition, t *model.MailTemplate) mailTemplateSummary {
	s := mailTemplateSummary{Key: def.Key, Name: def.Name, Description: def.Description, FixedFooter: mailtemplate.FixedFooterText}
	if t != nil {
		s.Customized = true
		s.UpdatedAt = &t.UpdatedAt
		s.UpdatedBy = &t.UpdatedBy
		s.DefaultChanged = t.DefaultVersion < def.DefaultVersion
	}
	return s
}

func dtoOf(def *mailtemplate.Definition, t *model.MailTemplate, files []model.MailTemplateFile) mailTemplateDTO {
	s := summaryOf(def, t)
	dto := mailTemplateDTO{
		Key: def.Key, Name: def.Name, Description: def.Description,
		Subject: def.DefaultSubject, BodyHtml: def.DefaultBodyHtml(),
		Customized: s.Customized, DefaultVersion: def.DefaultVersion, DefaultChanged: s.DefaultChanged,
		DefaultSubject: def.DefaultSubject, DefaultBodyHtml: def.DefaultBodyHtml(),
		UpdatedAt: s.UpdatedAt, UpdatedBy: s.UpdatedBy,
		Placeholders: def.Placeholders, Files: files, Limits: def.Limits, FixedFooter: s.FixedFooter,
	}
	if t != nil {
		dto.Subject, dto.BodyHtml, dto.Version, dto.AttachmentFile = t.Subject, t.BodyHtml, t.Version, t.AttachmentFileId
	}
	return dto
}

// definition resolves the {key} path parameter; false after a 404 answer.
func definition(w http.ResponseWriter, r *http.Request) (*mailtemplate.Definition, bool) {
	def := mailtemplate.Lookup(mux.Vars(r)["key"])
	if def == nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"message": "Unbekannte Mail"})
		return nil, false
	}
	return def, true
}

func respondValidation(w http.ResponseWriter, errs ...*mailtemplate.ValidationError) {
	respondWithJSON(w, http.StatusBadRequest, map[string]interface{}{"errors": errs})
}

func respondServerError(ctx context.Context, w http.ResponseWriter, tenant string, err error) {
	log.WithContext(ctx).WithField("tenant", tenant).WithError(err).Error("mail template")
	respondWithJSON(w, http.StatusInternalServerError, map[string]string{"message": "Interner Fehler"})
}

// loadTemplate loads the customised mail and asserts it belongs to the request tenant.
func (h *MailTemplateHandler) loadTemplate(w http.ResponseWriter, r *http.Request, tenant string, def *mailtemplate.Definition) (*model.MailTemplate, bool) {
	t, err := h.db.GetMailTemplate(r.Context(), tenant, def.Key)
	if err != nil {
		respondServerError(r.Context(), w, tenant, err)
		return nil, false
	}
	if t != nil && !strings.EqualFold(t.Tenant, tenant) {
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	}
	return t, true
}

// loadFiles loads the files of a mail and asserts they belong to the request tenant.
func (h *MailTemplateHandler) loadFiles(w http.ResponseWriter, r *http.Request, tenant string, def *mailtemplate.Definition, withContent bool) ([]model.MailTemplateFile, bool) {
	files, err := h.db.ListMailTemplateFiles(r.Context(), tenant, def.Key, withContent)
	if err != nil {
		respondServerError(r.Context(), w, tenant, err)
		return nil, false
	}
	for _, f := range files {
		if !strings.EqualFold(f.Tenant, tenant) {
			w.WriteHeader(http.StatusForbidden)
			return nil, false
		}
	}
	return files, true
}

func (h *MailTemplateHandler) list() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		stored, err := h.db.ListMailTemplates(r.Context(), tenant)
		if err != nil {
			respondServerError(r.Context(), w, tenant, err)
			return
		}
		byKey := map[string]*model.MailTemplate{}
		for i := range stored {
			if !strings.EqualFold(stored[i].Tenant, tenant) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			byKey[stored[i].MailKey] = &stored[i]
		}
		out := []mailTemplateSummary{}
		for _, def := range mailtemplate.Catalog() {
			def := def
			out = append(out, summaryOf(&def, byKey[def.Key]))
		}
		respondWithJSON(w, http.StatusOK, out)
	}
}

func (h *MailTemplateHandler) get() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		t, ok := h.loadTemplate(w, r, tenant, def)
		if !ok {
			return
		}
		files, ok := h.loadFiles(w, r, tenant, def, false)
		if !ok {
			return
		}
		respondWithJSON(w, http.StatusOK, dtoOf(def, t, files))
	}
}

// decodeRequest reads the JSON body; false after a 400 answer.
func decodeRequest(w http.ResponseWriter, r *http.Request) (*mailTemplateRequest, bool) {
	var req mailTemplateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMailTemplateJSONBytes)).Decode(&req); err != nil {
		respondValidation(w, &mailtemplate.ValidationError{Field: "body", Message: "Ungültige Anfrage."})
		return nil, false
	}
	return &req, true
}

// sanitize checks subject and body of a request; false after a 400 answer.
func sanitize(w http.ResponseWriter, def *mailtemplate.Definition, req *mailTemplateRequest, fileOK mailtemplate.FileCheck) (string, string, bool) {
	var errs []*mailtemplate.ValidationError
	var verr *mailtemplate.ValidationError
	subject, err := mailtemplate.SanitizeSubject(def, req.Subject)
	if errors.As(err, &verr) {
		errs = append(errs, verr)
	}
	body, err := mailtemplate.SanitizeBody(def, req.BodyHtml, fileOK)
	if errors.As(err, &verr) {
		errs = append(errs, verr)
	} else if err == nil {
		errs = append(errs, mailtemplate.LinkErrors(body)...)
	}
	if len(errs) > 0 {
		respondValidation(w, errs...)
		return "", "", false
	}
	return subject, body, true
}

func (h *MailTemplateHandler) save() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		req, ok := decodeRequest(w, r)
		if !ok {
			return
		}
		if req.Version < 0 {
			respondValidation(w, &mailtemplate.ValidationError{Field: "version", Message: "Ungültige Version."})
			return
		}
		// every well-formed image id stays in the text; whether the file exists (also as
		// an older draft) is decided under the lock of the save, so a vanished image is
		// a 400 and never silently dropped
		subject, body, ok := sanitize(w, def, req, func(id string) bool { return uuid.Parse(id) != nil })
		if !ok {
			return
		}
		attachmentId, present, aerr := req.attachment()
		if aerr != nil {
			respondValidation(w, aerr)
			return
		}
		if !present {
			// a save sets the whole version: without the field a saved PDF would be removed silently
			respondValidation(w, &mailtemplate.ValidationError{Field: "attachmentFileId",
				Message: "attachmentFileId fehlt (null = kein PDF-Anhang)."})
			return
		}
		images := mailtemplate.ReferencedImages(body)
		saved, err := h.db.SaveMailTemplate(r.Context(), &model.MailTemplate{
			Tenant: tenant, MailKey: def.Key, Subject: subject, BodyHtml: body,
			DefaultVersion: def.DefaultVersion, UpdatedBy: claims.Username, AttachmentFileId: attachmentId,
		}, req.Version, images, func(all []model.MailTemplateFile) error {
			return checkVersionFiles(def, tenant, images, attachmentId, all)
		})
		var verr *mailtemplate.ValidationError
		if errors.As(err, &verr) {
			respondValidation(w, verr)
			return
		}
		var conflict *database.MailTemplateVersionError
		if errors.As(err, &conflict) {
			respondWithJSON(w, http.StatusConflict, map[string]interface{}{
				"message":        "Die Mail wurde inzwischen geändert.",
				"currentVersion": conflict.Current,
			})
			return
		}
		if err != nil {
			respondServerError(r.Context(), w, tenant, err)
			return
		}
		files, ok := h.loadFiles(w, r, tenant, def, false)
		if !ok {
			return
		}
		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("mail template %s saved, version %d", def.Key, saved.Version)
		respondWithJSON(w, http.StatusOK, dtoOf(def, saved, files))
	}
}

// checkVersionFiles checks the files a version will use: they exist for this
// mail and tenant, have the right kind and stay within the limits.
func checkVersionFiles(def *mailtemplate.Definition, tenant string, images []string, attachmentId *string, all []model.MailTemplateFile) error {
	byId := map[string]*model.MailTemplateFile{}
	for i := range all {
		if !strings.EqualFold(all[i].Tenant, tenant) {
			return errors.New("tenant mismatch")
		}
		byId[all[i].Id] = &all[i]
	}
	var total int64
	seen := map[string]bool{}
	for _, id := range images {
		f := byId[id]
		if f == nil || f.Kind != model.MailFileKindImage {
			return &mailtemplate.ValidationError{Field: "bodyHtml", Message: "Ein Bild im Text gibt es nicht (mehr); bitte erneut einfügen."}
		}
		if !seen[id] {
			seen[id] = true
			total += f.Size
		}
	}
	if len(seen) > def.Limits.MaxImages {
		return &mailtemplate.ValidationError{Field: "bodyHtml", Message: fmt.Sprintf("Höchstens %d Bilder je Mail.", def.Limits.MaxImages)}
	}
	if attachmentId != nil && def.Limits.MaxAttachments == 0 {
		return &mailtemplate.ValidationError{Field: "attachmentFileId", Message: mailtemplate.NoAttachmentMessage}
	}
	if attachmentId != nil {
		f := byId[*attachmentId]
		if f == nil || f.Kind != model.MailFileKindAttachment {
			return &mailtemplate.ValidationError{Field: "attachmentFileId", Message: "Den PDF-Anhang gibt es nicht (mehr)."}
		}
		total += f.Size
	}
	if total > def.Limits.MaxTotalBytes {
		return &mailtemplate.ValidationError{Field: "files", Message: fmt.Sprintf("Alle Dateien einer Mail zusammen dürfen höchstens %s groß sein.", formatBytes(def.Limits.MaxTotalBytes))}
	}
	return nil
}

func (h *MailTemplateHandler) reset() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		if _, ok = h.loadTemplate(w, r, tenant, def); !ok {
			return
		}
		if err := h.db.DeleteMailTemplate(r.Context(), tenant, def.Key); err != nil {
			respondServerError(r.Context(), w, tenant, err)
			return
		}
		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("mail template %s reset to default", def.Key)
		w.WriteHeader(http.StatusNoContent)
	}
}
