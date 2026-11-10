package eda

import "at.ourproject/vfeeg-backend/services"

// mailWorker sends the mails of the EDA handlers. The handlers run inside the single MQTT broker
// loop: a mail sent there holds up every other message and API-triggered EDA request behind it, up
// to 35 s per mail (services.MailSendTimeout plus the connect wait).
var mailWorker = services.NewMailWorker(services.MailQueueSize)

// The mail senders are variables so tests can capture the mails. A handler reads them once, when
// it queues the job.
var (
	sendActivationMail services.SendMailFunc = services.SendMail
	sendZpListMail                           = services.SendMailWithAttachment
)

// StartMailWorker starts sending queued mails; call it before the EDA subscriptions.
func StartMailWorker() {
	mailWorker.Start()
}

// StopMailWorker sends what is queued within services.MailStopLimit and reports the rest; call it
// after the broker stopped and before the database closes.
func StopMailWorker() {
	mailWorker.Stop(services.MailStopLimit, services.MailReportLimit)
}
