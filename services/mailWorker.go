package services

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	log "github.com/sirupsen/logrus"
)

const (
	// MailQueueSize bounds the queue. A burst of EDA answers (one per metering point) fits; a job
	// holds what it needs in memory, so the queue must not grow without limit while the mail server
	// is slow.
	MailQueueSize = 1000
	// MailStopLimit is how long a stop waits for queued mails, MailReportLimit how long it then
	// reports the jobs that are left. Kubernetes gives the pod 30 s after SIGTERM (no
	// terminationGracePeriodSeconds in the gitops manifest), shared with the HTTP server and the broker.
	MailStopLimit   = 14 * time.Second
	MailReportLimit = 3 * time.Second
)

// ErrMailNotQueued: the mail was not sent because the worker takes no more jobs. Nothing went to
// the mail server, so the mail did not go out.
var ErrMailNotQueued = errors.New("Die Mail wurde nicht verschickt, weil gerade zu viele Mails anstehen oder der Dienst neu startet. Bitte später noch einmal versuchen.")

// errMailPanic is what the community reads when sending panicked; the cause is only logged.
var errMailPanic = errors.New("Mail konnte nicht gesendet werden.")

// MailNotifier stores the notification for a mail that was not sent.
type MailNotifier interface {
	SaveNotificationFromMap(notificationValue map[string]interface{}, tenant string, notificationType model.NotificationType, process model.NotificationProcess, role string) error
}

// MailAttempt is one mail of a job.
type MailAttempt struct {
	Tenant     string
	Identifier string // what the notification names, e.g. the metering point
	// DedupeKey: an attempt is skipped when an earlier attempt of the same job with this key was
	// sent or not confirmed (probably sent). Empty: never skipped.
	DedupeKey string
	Send      func() error
}

// MailJob is sent as one unit, attempt after attempt. Attempts that depend on each other's result
// (GEA: one mail per member across the tenants of an RC number) belong into the same job: the result
// of an attempt is known before the next one starts, so nothing is sent twice.
type MailJob struct {
	Ctx      context.Context // for logging; see Enqueue
	Notifier MailNotifier
	Attempts []MailAttempt
}

// MailWorker sends mails one at a time, in the order they were queued, outside the MQTT broker loop.
// The queue is in memory: a crash or kill loses what is queued, and the mail being sent has no known
// result. There are no retries.
type MailWorker struct {
	queue  chan MailJob
	mu     sync.RWMutex // closed and the send on queue: nothing is sent on a closed channel
	closed bool
	done   chan struct{}
}

func NewMailWorker(size int) *MailWorker {
	return &MailWorker{queue: make(chan MailJob, size), done: make(chan struct{})}
}

// Start runs the worker until Stop.
func (w *MailWorker) Start() {
	go func() {
		defer close(w.done)
		for job := range w.queue {
			job.run()
		}
	}()
}

// Enqueue never blocks. When the queue is full or the worker is stopped, every attempt of the job is
// reported like a failed mail. The job's context keeps its values for logging but is detached from
// the caller's cancellation.
func (w *MailWorker) Enqueue(job MailJob) {
	job.Ctx = context.WithoutCancel(job.Ctx)
	if !w.tryQueue(job) {
		for _, a := range job.Attempts {
			job.report(a, ErrMailNotQueued)
		}
	}
}

func (w *MailWorker) tryQueue(job MailJob) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return false
	}
	select {
	case w.queue <- job:
		return true
	default:
		return false
	}
}

// Stop takes no more jobs and waits up to limit for the queued ones. The jobs still queued then are
// not sent: each is reported with ErrMailNotQueued, for up to reportLimit, so the database must
// still be open. It returns the number of jobs that were not sent.
func (w *MailWorker) Stop(limit, reportLimit time.Duration) int {
	w.mu.Lock()
	w.closed = true
	close(w.queue)
	w.mu.Unlock()

	select {
	case <-w.done:
		return 0
	case <-time.After(limit):
	}

	deadline := time.Now().Add(reportLimit)
	reported, unreported := 0, 0
	for job := range w.queue {
		if time.Now().After(deadline) {
			unreported = 1 + len(w.queue)
			break
		}
		for _, a := range job.Attempts {
			job.report(a, ErrMailNotQueued)
		}
		reported++
	}
	log.Errorf("mail worker: stopped after %v, %d queued mail jobs not sent and reported, %d lost without a report; the mail being sent has no known result",
		limit, reported, unreported)
	return reported + unreported
}

func (j MailJob) run() {
	sent := map[string]bool{}
	for _, a := range j.Attempts {
		if a.DedupeKey != "" && sent[a.DedupeKey] {
			continue
		}
		if j.attempt(a) {
			sent[a.DedupeKey] = true
		}
	}
}

// attempt sends one mail and reports a failure. It returns whether the mail counts as sent.
func (j MailJob) attempt(a MailAttempt) (sent bool) {
	// same as the broker loop: one bad mail must not take the process down, nor the rest of the job
	defer func() {
		if r := recover(); r != nil {
			log.WithContext(j.Ctx).WithField("tenant", a.Tenant).Errorf("recovered from panic while sending a mail: %v\n%s", r, debug.Stack())
			j.report(a, errMailPanic)
			sent = false
		}
	}()

	err := a.Send()
	if err != nil {
		j.report(a, err)
	}
	// a mail the server did not confirm was probably sent; a partial delivery reached the valid recipients
	return err == nil || errors.Is(err, ErrMailUnconfirmed) || errors.Is(err, ErrMailPartlyDelivered)
}

// report writes the log line and the notification for the community.
func (j MailJob) report(a MailAttempt, err error) {
	log.WithContext(j.Ctx).WithField("tenant", a.Tenant).WithError(err).Log(MailErrorLevel(err), "mail: error sending mail")
	notification := database.CreateNotificationMessageFromLog(
		&model.Log{Operation: "Mail", Messages: []*model.LogMessage{MailLogMessage(a.Identifier, err)}})
	if err := j.Notifier.SaveNotificationFromMap(notification, a.Tenant, model.N_TYPE_ERROR, model.N_PROCESS_EDA_PROCESS, "ADMIN"); err != nil {
		log.WithContext(j.Ctx).WithField("tenant", a.Tenant).WithError(err).Error("mail: can not save the notification")
	}
}
