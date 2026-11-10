package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"at.ourproject/vfeeg-backend/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A test mail the mail server did not confirm in time was probably sent anyway: the answer (504)
// says so instead of "could not be sent"; a real failure keeps its 502 and text.
func TestMailTemplates_TestMailUnconfirmed(t *testing.T) {
	c := newMailTemplateClient(t)
	send := func(code int) map[string]string {
		w := c.json("admin", mtTenant, "POST", "/eeg/mailtemplates/consent-request/test", map[string]interface{}{
			"recipient": "me", "subject": "Test", "bodyHtml": "<p>Hallo</p>"})
		require.Equal(t, code, w.Code, w.Body.String())
		return decode[map[string]string](t, w)
	}

	timeout := status.Error(codes.DeadlineExceeded, "context deadline exceeded")
	c.sendErr = fmt.Errorf("%w (%w)", services.ErrMailUnconfirmed, timeout)
	assert.Equal(t, "Zeitüberschreitung – die Mail wurde vermutlich trotzdem verschickt. Vor einem erneuten Versand bitte beim Empfänger nachfragen.", send(http.StatusGatewayTimeout)["message"])

	c.sendErr = errors.New("mailbox unavailable")
	assert.Equal(t, "Die Testmail konnte nicht verschickt werden.", send(http.StatusBadGateway)["message"])
}
