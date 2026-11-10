package parser

import (
	"bytes"
	"testing"

	"at.ourproject/vfeeg-backend/config/configtest"
	"at.ourproject/vfeeg-backend/services"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

// 261008-ca1: a member without e-mail is logged by id, not by name.
func TestMemberWithoutEmailIsLoggedById(t *testing.T) {
	configtest.Set(t, "file-content.templates", t.TempDir())
	var out bytes.Buffer
	orig := log.StandardLogger().Out
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(orig) })

	eeg, participant := storedMailFixture()
	participant.LastName = "Obermueller"
	participant.Contact.Email = null.String{}
	send := func(string, string, string, *string, *bytes.Buffer, []*services.Attachment, *services.Attachment) error {
		return nil
	}
	require.NoError(t, SendActivationMailFromTemplate(send, nil, "TE000300", "Aktivierung", eeg, participant, "activation-mail-template.toml"))

	assert.Contains(t, out.String(), "Participant without email contact")
	assert.NotContains(t, out.String(), "Obermueller")
}
