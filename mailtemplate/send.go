package mailtemplate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	log "github.com/sirupsen/logrus"
)

// Store is what sending a customised mail needs from the database.
type Store interface {
	GetMailTemplate(ctx context.Context, tenant, key string) (*model.MailTemplate, error)
	GetCommittedMailTemplateFiles(ctx context.Context, tenant, key string, ids []string) ([]model.MailTemplateFile, error)
	SaveNotificationFromMap(notificationValue map[string]interface{}, tenant string, notificationType model.NotificationType, process model.NotificationProcess, role string) error
}

// SendStored sends the customised version of a member mail when the community
// has one. handled=false means the caller sends the built-in default: either
// there is no customised version, or it could not be used — then the community
// gets an error notification and the member still gets the default mail.
// A send error after a successful render is returned with handled=true.
func SendStored(ctx context.Context, store Store, sendMail services.SendMailFunc, def *Definition,
	tenant, to string, cc *string, data *Data, identifier string) (handled bool, err error) {

	if store == nil || def == nil {
		return false, nil
	}
	t, err := store.GetMailTemplate(ctx, tenant, def.Key)
	if err != nil {
		log.WithContext(ctx).WithField("tenant", tenant).WithError(err).Errorf("mail template %s: can not load, using default", def.Key)
		return false, nil
	}
	if t == nil {
		return false, nil
	}
	if !strings.EqualFold(t.Tenant, tenant) {
		log.WithContext(ctx).WithField("tenant", tenant).Errorf("mail template %s: tenant mismatch, using default", def.Key)
		return false, nil
	}
	// only the files the saved version references, and only committed ones
	ids := ReferencedImages(t.BodyHtml)
	attachmentId := ""
	if t.AttachmentFileId != nil {
		attachmentId = *t.AttachmentFileId
		ids = append(ids, attachmentId)
	}
	files, err := store.GetCommittedMailTemplateFiles(ctx, tenant, def.Key, ids)
	if err != nil {
		notifyFallback(ctx, store, def, tenant, identifier, err)
		return false, nil
	}
	r, err := Render(def, t.Subject, t.BodyHtml, attachmentId, data, files, ImagesInline)
	if err != nil {
		notifyFallback(ctx, store, def, tenant, identifier, err)
		return false, nil
	}
	log.WithContext(ctx).WithField("tenant", tenant).Infof("Mail %s rendered from the customised version %d", def.Key, t.Version)
	return true, sendMail(tenant, to, r.Subject, cc, bytes.NewBufferString(r.Html), r.Inline, r.Attachment)
}

// notifyFallback tells the community that its customised mail was not used.
func notifyFallback(ctx context.Context, store Store, def *Definition, tenant, identifier string, cause error) {
	log.WithContext(ctx).WithField("tenant", tenant).WithError(cause).Errorf("mail template %s: can not render, sending default", def.Key)
	msg := fmt.Sprintf("Ihre angepasste Mail „%s“ konnte nicht verwendet werden; es wurde der Standardtext verschickt. Grund: %s", def.Name, fallbackReason(def, cause))
	_ = store.SaveNotificationFromMap(database.CreateNotificationMessageFromLog(&model.Log{
		Operation: "Mail",
		Messages:  []*model.LogMessage{model.NewLogMessage("ERROR", identifier, "E_MAIL_TEMPLATE", msg)},
	}), tenant, model.N_TYPE_ERROR, model.N_PROCESS_EDA_PROCESS, "ADMIN")
}

// fallbackReason is what the community reads about a failed customised mail:
// the texts written for it (validation, size), otherwise a generic sentence.
// The full error is only logged.
func fallbackReason(def *Definition, cause error) string {
	var verr *ValidationError
	switch {
	case errors.As(cause, &verr):
		return verr.Message
	case errors.Is(cause, ErrTooLarge):
		return fmt.Sprintf("Die Dateien der Mail sind zusammen größer als %d MB.", def.Limits.MaxTotalBytes/(1024*1024))
	default:
		return "Die angepasste Fassung konnte nicht verarbeitet werden; bitte im Editor prüfen und erneut speichern."
	}
}

// NoticeStore is what the default-change notice needs from the database.
type NoticeStore interface {
	NotifyMailTemplateDefaultChange(ctx context.Context, key string, defaultVersion int, notification func(tenant string) map[string]interface{}) ([]string, error)
}

// NotifyDefaultChanges tells each community once that the default of a mail it
// customised has changed (concept G.2). Notification and "notified" mark are
// written together; on an error nothing is marked and the next start retries.
func NotifyDefaultChanges(ctx context.Context, store NoticeStore) {
	for _, def := range Catalog() {
		def := def
		msg := fmt.Sprintf("Der Standardtext der Mail „%s“ wurde geändert. Ihre angepasste Fassung wird weiter verwendet; den neuen Standard sehen Sie unter Einstellungen → E-Mail-Texte.", def.Name)
		tenants, err := store.NotifyMailTemplateDefaultChange(ctx, def.Key, def.DefaultVersion, func(string) map[string]interface{} {
			return database.CreateNotificationMessageFromLog(&model.Log{
				Operation: "Mail",
				Messages:  []*model.LogMessage{model.NewLogMessage("INFO", def.Key, "I_MAIL_TEMPLATE_DEFAULT", msg)},
			})
		})
		if err != nil {
			log.WithContext(ctx).WithError(err).Errorf("mail template %s: default change notice failed, retried on the next start", def.Key)
			continue
		}
		if len(tenants) > 0 {
			log.WithContext(ctx).Infof("mail template %s: default change notice for %d communities", def.Key, len(tenants))
		}
	}
}
