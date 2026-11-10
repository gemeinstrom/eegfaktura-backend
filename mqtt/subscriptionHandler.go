package mqttclient

import (
	"regexp"
	"strings"

	"at.ourproject/vfeeg-backend/model"
	"github.com/sirupsen/logrus"
)

func GetSubsriptions() []model.Subscriptions {
	return []model.Subscriptions{
		{
			Protocol: model.ERROR,
			Handler:  errorHandler,
		},
	}
}

// errorHandler logs an ERROR message of eegfaktura-eda-xp with its reason (errorMessage), e.g. a refused request
// (eegfaktura-dev#56 #4). The user does not see it yet.
func errorHandler(msg model.SubscribeMessage) {
	// The reason is free text: eda-xp's own texts, the grid operator's ReasonText (may name metering point numbers,
	// which are ids and may be logged, workspace decision 2026-11-02) and the subject of a mail without attachment,
	// which anyone can write to the mailbox. E-mail addresses in it are masked like eda-xp masks its own lines.
	reason := withoutEmailAddresses(msg.Payload.ErrorMessage)
	logrus.WithFields(logrus.Fields{"tenant": msg.Tenant, "event.action": "eda.error.received", "code": string(msg.MessageCode),
		"error.message": reason}).
		Errorf("error message from eda-xp, conversation %s", msg.Payload.ConversationId)
}

// addressRun is a run of characters between separators, as in eda-xp's LogText.withoutEmailAddresses.
var addressRun = regexp.MustCompile(`[^\s<>"'(),;:` + "`" + `]+`)

// withoutEmailAddresses replaces every run with an @ inside it (not at either end) by <address>, the rule of eda-xp.
func withoutEmailAddresses(text string) string {
	if !strings.Contains(text, "@") {
		return text
	}
	return addressRun.ReplaceAllStringFunc(text, func(run string) string {
		if at := strings.Index(run[1:], "@"); at >= 0 && at+1 < len(run)-1 {
			return "<address>"
		}
		return run
	})
}

func InitErrorSubscriptions() {
	Broker().Subscribe(GetSubsriptions()...)
}
