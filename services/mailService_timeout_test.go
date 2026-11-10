package services

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	protobuf "at.ourproject/vfeeg-backend/proto"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// every mail send waits 30 s for the mail server (before: 5 s, 30 s only above 256 KB)
func TestMailSendTimeout(t *testing.T) {
	assert.Equal(t, 30*time.Second, MailSendTimeout)
	assert.Equal(t, MailSendTimeout, mailTimeout)
}

// fakeMailServer answers after delay with reply, or with err.
type fakeMailServer struct {
	protobuf.UnimplementedSendMailServiceServer
	delay time.Duration
	reply *protobuf.SendMailReply
	err   error
}

func (f *fakeMailServer) answer() (*protobuf.SendMailReply, error) {
	time.Sleep(f.delay)
	return f.reply, f.err
}

func (f *fakeMailServer) SendMailWithInlineAttachment(context.Context, *protobuf.SendMailWithInlineAttachmentsRequest) (*protobuf.SendMailReply, error) {
	return f.answer()
}

func (f *fakeMailServer) SendMail(context.Context, *protobuf.SendMailRequest) (*protobuf.SendMailReply, error) {
	return f.answer()
}

// answerTimeout is the send timeout for a fake that answers at once: long enough that a loaded machine (full suite)
// does not turn its answer into a deadline (a 100 ms timeout made "gRPC error" an unconfirmed warning at times).
const answerTimeout = 10 * time.Second

// startFakeMailServer points services.mail-server at the fake and sets the send timeout.
func startFakeMailServer(t *testing.T, f *fakeMailServer, timeout time.Duration) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer()
	protobuf.RegisterSendMailServiceServer(s, f)
	go func() { _ = s.Serve(lis) }()
	origAddr, origTimeout := viper.GetString("services.mail-server"), mailTimeout
	viper.Set("services.mail-server", lis.Addr().String())
	mailTimeout = timeout
	t.Cleanup(func() {
		s.Stop()
		viper.Set("services.mail-server", origAddr)
		mailTimeout = origTimeout
	})
}

// sendBoth sends one mail through each send function of the service.
func sendBoth() map[string]error {
	body := func() *bytes.Buffer { return bytes.NewBufferString("<p>Hallo</p>") }
	att := &Attachment{Filename: "a.pdf", MimeType: "application/pdf", Filecontent: bytes.NewBufferString("%PDF")}
	return map[string]error{
		"SendMail":               SendMail("RC100001", "member@example.org", "Betreff", nil, body(), nil, nil),
		"SendMailWithAttachment": SendMailWithAttachment("rc100001@eegfaktura.at", "eeg@example.org", "Betreff", nil, body(), att),
	}
}

const testMeter = "AT0030000000000000000000000000001"

func TestSendMail_DeadlineExceededIsUnconfirmed(t *testing.T) {
	startFakeMailServer(t, &fakeMailServer{delay: 500 * time.Millisecond, reply: &protobuf.SendMailReply{Status: 200}}, 100*time.Millisecond)
	for fn, err := range sendBoth() {
		require.Error(t, err, fn)
		assert.ErrorIs(t, err, ErrMailUnconfirmed, fn)
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err), fn+": the original error is kept")
		lm := MailLogMessage(testMeter, err)
		assert.Equal(t, "Zeitüberschreitung – die Mail wurde vermutlich trotzdem verschickt. Vor einem erneuten Versand bitte beim Empfänger nachfragen.", lm.Message, fn)
		assert.Equal(t, "W_MAIL_UNCONFIRMED", lm.MessageCode, fn)
		assert.Equal(t, testMeter, lm.Identifier, fn)
		assert.Equal(t, "warning", MailErrorLevel(err).String(), fn)
	}
}

func TestSendMail_RealFailuresKeepTheirText(t *testing.T) {
	msg := "mailbox unavailable"
	smtpTimeout := "SMTP timeout: connect to relay"
	cases := map[string]*fakeMailServer{
		"status != 200":    {reply: &protobuf.SendMailReply{Status: 500, Message: &msg}},
		"gRPC error":       {err: status.Error(codes.Unavailable, "mail server down")},
		"rejected address": {reply: &protobuf.SendMailReply{Status: 200, RejectedRecipients: []string{"x@invalid"}}},
		"504 SMTP timeout": {reply: &protobuf.SendMailReply{Status: 504, Message: &smtpTimeout}},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			startFakeMailServer(t, f, answerTimeout)
			for fn, err := range sendBoth() {
				require.Error(t, err, fn)
				assert.NotErrorIs(t, err, ErrMailUnconfirmed, fn)
				lm := MailLogMessage(testMeter, err)
				assert.Equal(t, model.UserText(err), lm.Message, fn)
				assert.Equal(t, "E_UNDEFINED_0", lm.MessageCode, fn)
				assert.Equal(t, "error", MailErrorLevel(err).String(), fn)
			}
		})
	}
}

// 261031-mm7: the community reads the address, the log does not. Both address errors of the send path.
func TestMailAddressesStayOutOfTheLog(t *testing.T) {
	var logged bytes.Buffer
	logger := log.StandardLogger()
	orig := logger.Out
	logger.SetOutput(&logged)
	t.Cleanup(func() { logger.SetOutput(orig) })

	rejected := checkRejectedRecipients([]string{"x@invalid"})
	invalid := SendMail("RC100001", "kaputt@adresse", "Betreff", nil, bytes.NewBufferString("<p>Hallo</p>"), nil, nil)
	require.Error(t, invalid)

	n := &fakeNotifier{}
	for _, err := range []error{rejected, invalid} {
		MailJob{Ctx: context.Background(), Notifier: n, Attempts: []MailAttempt{{Tenant: "TE000001", Identifier: testMeter, Send: func() error { return err }}}}.run()
	}

	require.Len(t, n.entries, 2)
	assert.Contains(t, n.entries[0].Message, "x@invalid")
	assert.Contains(t, n.entries[0].Message, "kein erneuter Versand", "the text of the notification stays")
	assert.Contains(t, n.entries[1].Message, "kaputt@adresse")
	assert.Contains(t, logged.String(), "error sending mail", "the failure is logged")
	assert.NotContains(t, logged.String(), "x@invalid")
	assert.NotContains(t, logged.String(), "kaputt@adresse")
}

// 261031-mm6: the proto field is optional; a failure answer without a message must not panic.
func TestSendMail_FailureWithoutMessage(t *testing.T) {
	startFakeMailServer(t, &fakeMailServer{reply: &protobuf.SendMailReply{Status: 500}}, answerTimeout)
	for fn, err := range sendBoth() {
		require.Error(t, err, fn)
		assert.NotErrorIs(t, err, ErrMailUnconfirmed, fn)
	}
}

func TestSendMail_ConnectionRefusedIsNotUnconfirmed(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close()) // nothing listens there any more
	orig := viper.GetString("services.mail-server")
	viper.Set("services.mail-server", addr)
	t.Cleanup(func() { viper.Set("services.mail-server", orig) })
	for fn, err := range sendBoth() {
		require.Error(t, err, fn)
		assert.NotErrorIs(t, err, ErrMailUnconfirmed, fn)
		assert.Equal(t, codes.Unavailable, status.Code(err), fn)
	}
}

// A mail server that takes the TCP connection but never answers the gRPC handshake: the request never
// reached it, so this is a real failure, not an unconfirmed send (review of backend#132).
func TestSendMail_NoHandshakeIsNotUnconfirmed(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err) // never accepts: the kernel completes the TCP handshake, gRPC gets no server preface
	origAddr, origTimeout, origConnect := viper.GetString("services.mail-server"), mailTimeout, connectTimeout
	viper.Set("services.mail-server", lis.Addr().String())
	mailTimeout, connectTimeout = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		_ = lis.Close()
		viper.Set("services.mail-server", origAddr)
		mailTimeout, connectTimeout = origTimeout, origConnect
	})
	for fn, err := range sendBoth() {
		require.Error(t, err, fn)
		assert.NotErrorIs(t, err, ErrMailUnconfirmed, fn)
		lm := MailLogMessage(testMeter, err)
		assert.Equal(t, "E_UNDEFINED_0", lm.MessageCode, fn)
		assert.Equal(t, "error", MailErrorLevel(err).String(), fn)
	}
}

// xp-connector answers 202 when its SMTP read timeout hit after the mail body was sent (eda-xp#60):
// the relay probably accepted the mail, so it is unconfirmed, not failed.
func TestSendMail_Status202IsUnconfirmed(t *testing.T) {
	msg := "SMTP unconfirmed: read timeout after DATA"
	startFakeMailServer(t, &fakeMailServer{reply: &protobuf.SendMailReply{Status: 202, Message: &msg}}, answerTimeout)
	for fn, err := range sendBoth() {
		require.Error(t, err, fn)
		assert.ErrorIs(t, err, ErrMailUnconfirmed, fn)
		assert.Contains(t, err.Error(), msg, fn)
		lm := MailLogMessage(testMeter, err)
		assert.Equal(t, ErrMailUnconfirmed.Error(), lm.Message, fn)
		assert.Equal(t, "W_MAIL_UNCONFIRMED", lm.MessageCode, fn)
		assert.Equal(t, "warning", MailErrorLevel(err).String(), fn)
	}
}
