package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/public"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
)

func (h *MailTemplateHandler) uploadFile() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		limits := def.Limits
		r.Body = http.MaxBytesReader(w, r.Body, limits.MaxAttachmentBytes+maxMailFileUploadOverhead)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: "Die Datei ist zu groß oder die Anfrage ist ungültig."})
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()

		kind := r.FormValue("kind")
		var maxBytes int64
		switch kind {
		case model.MailFileKindImage:
			maxBytes = limits.MaxImageBytes
		case model.MailFileKindAttachment:
			if limits.MaxAttachments == 0 {
				respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: "PDF-Anhänge sind derzeit nicht möglich."})
				return
			}
			maxBytes = limits.MaxAttachmentBytes
		default:
			respondValidation(w, &mailtemplate.ValidationError{Field: "kind", Message: "kind muss image oder attachment sein."})
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: "Keine Datei angegeben."})
			return
		}
		defer func() { _ = file.Close() }()
		content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
		if err != nil {
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: "Die Datei konnte nicht gelesen werden."})
			return
		}
		if int64(len(content)) > maxBytes {
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: fmt.Sprintf("Die Datei ist zu groß (höchstens %s).", formatBytes(maxBytes))})
			return
		}
		if len(content) == 0 {
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: "Die Datei ist leer."})
			return
		}
		// the type is checked on the content, never on name or Content-Type of the client
		detected := mimetype.Detect(content)
		var mimeType string
		switch {
		case kind == model.MailFileKindImage && detected.Is("image/png"):
			mimeType = "image/png"
		case kind == model.MailFileKindImage && detected.Is("image/jpeg"):
			mimeType = "image/jpeg"
		case kind == model.MailFileKindAttachment && detected.Is("application/pdf"):
			mimeType = "application/pdf"
		default:
			msg := "Bilder müssen PNG oder JPEG sein."
			if kind == model.MailFileKindAttachment {
				msg = "Der Anhang muss ein PDF sein."
			}
			respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: msg})
			return
		}
		if kind == model.MailFileKindImage {
			// only the re-encoded pixels are stored: no EXIF/GPS, no hidden or appended data
			reencoded, msg := reencodeMailImage(content, mimeType)
			if msg != "" {
				respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: msg})
				return
			}
			if int64(len(reencoded)) > maxBytes {
				respondValidation(w, &mailtemplate.ValidationError{Field: "file", Message: fmt.Sprintf("Die Datei ist zu groß (höchstens %s).", formatBytes(maxBytes))})
				return
			}
			content = reencoded
		}

		f := &model.MailTemplateFile{
			Id: uuid.New(), Tenant: tenant, MailKey: def.Key, Kind: kind,
			Filename: cleanFilename(header.Filename, mimeType), MimeType: mimeType,
			Size: int64(len(content)), Content: content,
		}
		// uploads are drafts: the limits apply to the drafts here and to the saved version on PUT
		err = h.db.InsertMailTemplateFile(r.Context(), f, func(existing []model.MailTemplateFile) error {
			images, attachments, total := 0, 0, f.Size
			for _, e := range existing {
				if !strings.EqualFold(e.Tenant, tenant) {
					return errors.New("tenant mismatch")
				}
				if e.Committed {
					continue
				}
				total += e.Size
				if e.Kind == model.MailFileKindImage {
					images++
				} else {
					attachments++
				}
			}
			switch {
			case kind == model.MailFileKindImage && images >= limits.MaxImages:
				return &mailtemplate.ValidationError{Field: "file", Message: fmt.Sprintf("Höchstens %d neue Bilder je Mail vor dem Speichern.", limits.MaxImages)}
			case kind == model.MailFileKindAttachment && attachments >= limits.MaxAttachments:
				return &mailtemplate.ValidationError{Field: "file", Message: fmt.Sprintf("Höchstens %d neuer PDF-Anhang je Mail vor dem Speichern; bitte den anderen zuerst löschen.", limits.MaxAttachments)}
			case total > limits.MaxTotalBytes:
				return &mailtemplate.ValidationError{Field: "file", Message: fmt.Sprintf("Neue Dateien einer Mail dürfen zusammen höchstens %s groß sein.", formatBytes(limits.MaxTotalBytes))}
			}
			return nil
		})
		var verr *mailtemplate.ValidationError
		if errors.As(err, &verr) {
			respondValidation(w, verr)
			return
		}
		if err != nil {
			respondServerError(r.Context(), w, tenant, err)
			return
		}
		log.WithContext(r.Context()).WithField("tenant", tenant).Infof("mail template %s: %s uploaded (%d bytes)", def.Key, kind, f.Size)
		respondWithJSON(w, http.StatusCreated, f)
	}
}

// loadFile resolves {id} and asserts the file belongs to the tenant and mail; false after an answer.
func (h *MailTemplateHandler) loadFile(w http.ResponseWriter, r *http.Request, tenant string, def *mailtemplate.Definition) (*model.MailTemplateFile, bool) {
	id := mux.Vars(r)["id"]
	if uuid.Parse(id) == nil {
		respondValidation(w, &mailtemplate.ValidationError{Field: "id", Message: "Ungültige Datei-ID."})
		return nil, false
	}
	f, err := h.db.GetMailTemplateFile(r.Context(), id)
	if err != nil {
		respondServerError(r.Context(), w, tenant, err)
		return nil, false
	}
	if f == nil {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"message": "Datei nicht gefunden"})
		return nil, false
	}
	if !strings.EqualFold(f.Tenant, tenant) {
		log.WithContext(r.Context()).WithField("tenant", tenant).Warn("mail template file of another tenant requested")
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	}
	if f.MailKey != def.Key {
		respondWithJSON(w, http.StatusNotFound, map[string]string{"message": "Datei nicht gefunden"})
		return nil, false
	}
	return f, true
}

func (h *MailTemplateHandler) getFile() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		var f *model.MailTemplateFile
		if mux.Vars(r)["id"] == mailtemplate.BuiltinLogoId {
			logo, err := public.Templates.ReadFile("templates/eegfaktura-logo.png")
			if err != nil {
				respondServerError(r.Context(), w, tenant, err)
				return
			}
			f = &model.MailTemplateFile{Filename: "eegfaktura-logo.png", MimeType: "image/png", Content: logo}
		} else if f, ok = h.loadFile(w, r, tenant, def); !ok {
			return
		}
		disposition := "inline"
		if f.Kind == model.MailFileKindAttachment {
			disposition = "attachment"
		}
		w.Header().Set("Content-Type", f.MimeType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Filename}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(f.Content)
	}
}

func (h *MailTemplateHandler) deleteFile() middleware.JWTHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, claims *middleware.PlatformClaims, tenant string) {
		def, ok := definition(w, r)
		if !ok {
			return
		}
		f, ok := h.loadFile(w, r, tenant, def)
		if !ok {
			return
		}
		// only drafts are deleted; a committed file belongs to the saved version (a save
		// deletes the ones it no longer uses), checked again under the save lock
		deleted, err := h.db.DeleteMailTemplateFile(r.Context(), tenant, def.Key, f.Id)
		if err != nil {
			respondServerError(r.Context(), w, tenant, err)
			return
		}
		if !deleted {
			respondWithJSON(w, http.StatusConflict, map[string]string{
				"message": "Die gespeicherte Fassung verwendet diese Datei; bitte zuerst ohne sie speichern."})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// imageCheck accepts the images among files.
func imageCheck(files []model.MailTemplateFile) mailtemplate.FileCheck {
	images := map[string]bool{}
	for _, f := range files {
		if f.Kind == model.MailFileKindImage {
			images[f.Id] = true
		}
	}
	return func(id string) bool { return images[id] }
}

// cleanFilename keeps the base name of an uploaded file without control characters.
func cleanFilename(name, mimeType string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[len(runes)-200:])
	}
	if name == "" || name == "." || name == "/" {
		ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "application/pdf": ".pdf"}[mimeType]
		name = "datei" + ext
	}
	return name
}

func formatBytes(n int64) string {
	if n >= 1024*1024 && n%(1024*1024) == 0 {
		return fmt.Sprintf("%d MB", n/(1024*1024))
	}
	return fmt.Sprintf("%d KB", n/1024)
}
