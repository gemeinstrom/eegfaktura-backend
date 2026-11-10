package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/model"
	protobuf "at.ourproject/vfeeg-backend/proto"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// MailSendTimeout is the gRPC timeout for every mail send. The mail server (xp-connector) answers
// only after it has handed the mail on; that took more than the former 5 s at times (30.10.2026).
const MailSendTimeout = 30 * time.Second

// mailTimeout is a variable so tests can shorten it.
var mailTimeout = MailSendTimeout

// connectTimeout is how long a send waits for the connection to the mail server (variable for tests).
var connectTimeout = 5 * time.Second

// ErrMailUnconfirmed: the request reached the mail server, but it did not answer within the
// timeout. The mail was probably sent anyway, so the community must not simply send it again
// (duplicates). The error wraps the original gRPC error. web shows it after "Mailversand nicht bestätigt:".
var ErrMailUnconfirmed = errors.New("Zeitüberschreitung – die Mail wurde vermutlich trotzdem verschickt. Vor einem erneuten Versand bitte beim Empfänger nachfragen.")

// ErrMailPartlyDelivered: the mail server delivered the mail but refused some recipients.
var ErrMailPartlyDelivered = errors.New("mail delivered, some recipients rejected")

// statusUnconfirmed is the answer of the xp-connector when its SMTP read timeout hit after the mail
// body was sent: the relay probably accepted the mail (contract with eegfaktura-eda-xp#60).
const statusUnconfirmed = 202

// sendError turns the error of a mail server call into the error for the caller: a timeout
// becomes ErrMailUnconfirmed, every other error stays as it is. The caller logs it.
func sendError(err error) error {
	if status.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w (%w)", ErrMailUnconfirmed, err)
	}
	return err
}

// waitReady connects to the mail server before the request is sent, so a later timeout means the
// request reached it. A server that does not get ready within connectTimeout is a real failure. A
// failed connect attempt (e.g. connection refused) is left to the request, which fails at once.
func waitReady(conn *grpc.ClientConn) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	conn.Connect()
	for {
		s := conn.GetState()
		if s == connectivity.Ready || s == connectivity.TransientFailure {
			return nil
		}
		if !conn.WaitForStateChange(ctx, s) {
			return fmt.Errorf("mail server not reachable within %v (state %s)", connectTimeout, s)
		}
	}
}

// MailErrorLevel is the log level for a failed mail send: warning when it is unconfirmed.
func MailErrorLevel(err error) log.Level {
	if errors.Is(err, ErrMailUnconfirmed) {
		return log.WarnLevel
	}
	return log.ErrorLevel
}

// smtpStatusRe finds the SMTP reply of the mail server in its text, e.g. "454 4.7.1": the reply class (4 temporary,
// 5 permanent) and the enhanced status code "subject.detail" (RFC 3463).
var smtpStatusRe = regexp.MustCompile(`\b([45])\d\d [45]\.(\d{1,3}\.\d{1,3})\b`)

// mailServerError is the error for a mail the mail server refused: a German explanation before the server's own
// text, which stays for finding the cause (e.g. "454 4.7.1 Quotas exceeded"). The server text can name the recipient
// address, so it is the detail for the notification only; the log gets the explanation and the status code
// (model.AddressError). A text without a status code stays as it is.
func mailServerError(message string) error {
	original := strings.Join(strings.Fields(message), " ")
	m := smtpStatusRe.FindStringSubmatch(original)
	if m == nil {
		return errors.New(original)
	}
	var explanation string
	switch status := m[2]; {
	case strings.HasPrefix(status, "7.") && strings.Contains(strings.ToLower(original), "quota"):
		explanation = "Der Mailserver hat sein Versandkontingent erreicht, die Mail wurde nicht verschickt. Bitte melde das dem Support."
	case slices.Contains([]string{"1.1", "1.2", "1.3", "1.4", "1.5", "1.6"}, status): // x.1.7/x.1.8: the sender
		explanation = "Der Mailserver hat die Empfänger-Adresse abgelehnt, die Mail wurde nicht verschickt. Bitte prüfe die E-Mail-Adresse."
	case strings.HasPrefix(status, "2."):
		explanation = "Das Postfach des Empfängers hat die Mail nicht angenommen (z. B. voll), sie wurde nicht verschickt."
	case m[1] == "4":
		explanation = "Der Mailserver hat die Mail vorübergehend abgelehnt, sie wurde nicht verschickt."
	default:
		explanation = "Der Mailserver hat die Mail abgelehnt, sie wurde nicht verschickt."
	}
	return &model.AddressError{
		Safe:   fmt.Errorf("%s (%s)", explanation, m[0]),
		Detail: fmt.Sprintf("%s (%s)", explanation, original),
	}
}

// MailLogMessage is the notification entry for a failed mail send. An unconfirmed send gets the
// text of ErrMailUnconfirmed (code W_MAIL_UNCONFIRMED) without the technical cause; every
// other error keeps its text.
func MailLogMessage(identifier string, err error) *model.LogMessage {
	if errors.Is(err, ErrMailUnconfirmed) {
		return model.NewLogMessage("WARNING", identifier, "W_MAIL_UNCONFIRMED", ErrMailUnconfirmed.Error())
	}
	return model.NewLogMessageFromVfeegError(identifier, err)
}

type SendMailFunc func(tenant, to, subject string, cc *string, body *bytes.Buffer, inlineContent []*Attachment, attachment *Attachment) error

type Attachment struct {
	Type        string
	Filename    string
	Filecontent *bytes.Buffer
	MimeType    string
	ContentId   *string
}

func SendMail(tenant, to, subject string, cc *string, body *bytes.Buffer, inlineContent []*Attachment, attachment *Attachment) error {
	// no subject in the log: a customised subject can contain member data
	log.WithField("tenant", tenant).Infof("Send Mail: inline: %d, att: %v", len(inlineContent), attachment != nil)
	to, cc, err := normalizedRecipients(to, cc)
	if err != nil {
		return err
	}
	return sendHtmlInlineAttachment(tenant, to, subject, cc, body, inlineContent, attachment)
}

// normalizedRecipients trims each ';'-separated part of to/cc
// (model.NormalizeEmailList) and validates against the shared address
// rule. The normalized values are what actually gets sent — validating
// alone would let a green check pass while the raw string still fails
// downstream. An empty to after normalization is an error (a mail
// needs a recipient); an empty cc becomes nil.
func normalizedRecipients(to string, cc *string) (string, *string, error) {
	to, err := model.ValidateEmailList(to)
	if err != nil {
		return "", nil, err
	}
	if to == "" {
		return "", nil, errors.New("no valid recipient address")
	}
	if cc != nil {
		normalizedCc, err := model.ValidateEmailList(*cc)
		if err != nil {
			return "", nil, err
		}
		if normalizedCc == "" {
			cc = nil
		} else {
			cc = &normalizedCc
		}
	}
	return to, cc, nil
}

// checkRejectedRecipients surfaces recipients the mail server refused
// (reported via the additive SendMailReply.rejectedRecipients field) so
// callers can raise an admin notification instead of losing them
// silently. IMPORTANT: delivery to the remaining recipients has already
// happened — the message must read as a partial delivery, not as a
// failed send, or admins will re-send and produce duplicates. The
// addresses are for the notification, not for the log (model.AddressError).
func checkRejectedRecipients(rejected []string) error {
	if len(rejected) > 0 {
		return &model.AddressError{
			Safe:   ErrMailPartlyDelivered,
			Detail: fmt.Sprintf("Mail an gültige Empfänger zugestellt, aber NICHT an (Adresse ungültig): %s — Adresse korrigieren, kein erneuter Versand an die übrigen Empfänger nötig", strings.Join(rejected, ";")),
		}
	}
	return nil
}

func sendHtmlInlineAttachment(sender, recipient, subject string, cc *string, htmlBody *bytes.Buffer, iContent []*Attachment, attachment *Attachment) error {
	//conn, err := grpc.Dial(viper.GetString("services.mail-server"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(viper.GetString("services.mail-server"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := waitReady(conn); err != nil {
		return err
	}
	c := protobuf.NewSendMailServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()

	inlineContent := []*protobuf.Attachment{}
	for i := range iContent {
		inlineContent = append(inlineContent, &protobuf.Attachment{
			MimeType:  iContent[i].MimeType,
			Filename:  iContent[i].Filename,
			Content:   iContent[i].Filecontent.Bytes(),
			ContentId: iContent[i].ContentId,
		})
	}
	request := &protobuf.SendMailWithInlineAttachmentsRequest{
		Sender:        sender,
		Recipient:     recipient,
		Subject:       subject,
		HtmlBody:      htmlBody.String(),
		InlineContent: inlineContent,
	}
	if attachment != nil {
		request.Attachment = &protobuf.Attachment{
			MimeType: attachment.MimeType,
			Filename: attachment.Filename,
			Content:  attachment.Filecontent.Bytes(),
		}
	}

	if cc != nil {
		request.Cc = cc
	}

	r, err := c.SendMailWithInlineAttachment(ctx, request)
	if err != nil {
		return sendError(err)
	}
	log.Infof("Response from MAIL-SERVER: status %d, rejected recipients %d", r.GetStatus(), len(r.GetRejectedRecipients()))
	if r == nil {
		return errors.New("error Send Mail")
	}
	if r.Status == statusUnconfirmed {
		return fmt.Errorf("%w (%s)", ErrMailUnconfirmed, r.GetMessage())
	}
	if r.Status != 200 {
		return mailServerError(r.GetMessage())
	}
	return checkRejectedRecipients(r.GetRejectedRecipients())
}

func SendMailWithAttachment(sender, recipient, subject string, cc *string, htmlBody *bytes.Buffer, attachment *Attachment) error {
	recipient, cc, err := normalizedRecipients(recipient, cc)
	if err != nil {
		return err
	}
	conn, err := grpc.Dial(viper.GetString("services.mail-server"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := waitReady(conn); err != nil {
		return err
	}
	c := protobuf.NewSendMailServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()

	request := &protobuf.SendMailRequest{
		Sender:    sender,
		Recipient: recipient,
		Subject:   subject,
		Body:      htmlBody.Bytes(),
		Attachment: &protobuf.Attachment{
			MimeType:  attachment.MimeType,
			Filename:  attachment.Filename,
			Content:   attachment.Filecontent.Bytes(),
			ContentId: attachment.ContentId,
		},
	}

	if cc != nil {
		request.Cc = cc
	}

	r, err := c.SendMail(ctx, request)
	if err != nil {
		return sendError(err)
	}
	log.Infof("Response from MAIL-SERVER: status %d, rejected recipients %d", r.GetStatus(), len(r.GetRejectedRecipients()))
	if r == nil {
		return errors.New("error Send Mail")
	}
	if r.Status == statusUnconfirmed {
		return fmt.Errorf("%w (%s)", ErrMailUnconfirmed, r.GetMessage())
	}
	if r.Status != 200 {
		return mailServerError(r.GetMessage())
	}
	return checkRejectedRecipients(r.GetRejectedRecipients())
}
