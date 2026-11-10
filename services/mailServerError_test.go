package services

import (
	"errors"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
)

// A5: a mail the mail server refused gets a German explanation before the server's own text, which stays for the
// notification (it named the cause of the quota failures of 31.10.2026). The log text has the status code but not the
// server text, which can name the recipient address.
func TestMailServerError(t *testing.T) {
	for name, tc := range map[string]struct{ message, explanation, original, status string }{
		"sending quota": {"com.sun.mail.smtp.SMTPSendFailedException: 454 4.7.1 Quotas exceeded\n",
			"Der Mailserver hat sein Versandkontingent erreicht, die Mail wurde nicht verschickt. Bitte melde das dem Support.",
			"com.sun.mail.smtp.SMTPSendFailedException: 454 4.7.1 Quotas exceeded", "454 4.7.1"},
		"invalid recipient": {"javax.mail.SendFailedException: Invalid Addresses;\n  nested exception is:\n\tcom.sun.mail.smtp." +
			"SMTPAddressFailedException: 501 5.1.3 The recipient address <x@y> is not a valid RFC-5322 address\n",
			"Der Mailserver hat die Empfänger-Adresse abgelehnt, die Mail wurde nicht verschickt. Bitte prüfe die E-Mail-Adresse.",
			"javax.mail.SendFailedException: Invalid Addresses; nested exception is: com.sun.mail.smtp." +
				"SMTPAddressFailedException: 501 5.1.3 The recipient address <x@y> is not a valid RFC-5322 address", "501 5.1.3"},
		"recipient mailbox over quota (not ours)": {"552 5.2.2 Mailbox quota exceeded",
			"Das Postfach des Empfängers hat die Mail nicht angenommen (z. B. voll), sie wurde nicht verschickt.",
			"552 5.2.2 Mailbox quota exceeded", "552 5.2.2"},
		"sender rejected (not the recipient)": {"451 4.1.8 Sender address rejected: Domain not found",
			"Der Mailserver hat die Mail vorübergehend abgelehnt, sie wurde nicht verschickt.",
			"451 4.1.8 Sender address rejected: Domain not found", "451 4.1.8"},
		"permanent": {"554 5.7.1 Message rejected",
			"Der Mailserver hat die Mail abgelehnt, sie wurde nicht verschickt.", "554 5.7.1 Message rejected", "554 5.7.1"},
	} {
		t.Run(name, func(t *testing.T) {
			err := mailServerError(tc.message)
			assert.Equal(t, tc.explanation+" ("+tc.original+")", model.UserText(err), "notification")
			assert.Equal(t, tc.explanation+" ("+tc.status+")", err.Error(), "log")
		})
	}
	t.Run("no status code", func(t *testing.T) {
		err := mailServerError("connection reset ")
		assert.Equal(t, "connection reset", err.Error())
		var a *model.AddressError
		assert.False(t, errors.As(err, &a))
	})
}
