package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mailTestWait = 5 * time.Second

// fakeNotifier records the stored notifications.
type fakeNotifier struct {
	mu      sync.Mutex
	tenants []string
	entries []*model.LogMessage
}

func (f *fakeNotifier) SaveNotificationFromMap(v map[string]interface{}, tenant string, typ model.NotificationType, process model.NotificationProcess, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenant)
	f.entries = append(f.entries, v["messages"].([]*model.LogMessage)...)
	return nil
}

func oneMail(n MailNotifier, send func() error) MailJob {
	return MailJob{Ctx: context.Background(), Notifier: n, Attempts: []MailAttempt{{Tenant: "TE000001", Identifier: "AT001", Send: send}}}
}

// blockedWorker returns a started worker whose goroutine is busy until release is called.
func blockedWorker(t *testing.T, size int, n MailNotifier) (w *MailWorker, release func()) {
	w = NewMailWorker(size)
	w.Start()
	started, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(unblock) }) }
	t.Cleanup(release)
	w.Enqueue(oneMail(n, func() error {
		close(started)
		<-unblock
		return nil
	}))
	select {
	case <-started:
	case <-time.After(mailTestWait):
		t.Fatal("the worker did not start the job")
	}
	return w, release
}

func TestMailWorker_SendsAndReportsNothing(t *testing.T) {
	n := &fakeNotifier{}
	w := NewMailWorker(1)
	w.Start()
	sent := false
	w.Enqueue(oneMail(n, func() error { sent = true; return nil }))
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	assert.True(t, sent)
	assert.Empty(t, n.entries)
}

func TestMailWorker_KeepsTheOrder(t *testing.T) {
	w := NewMailWorker(100)
	w.Start()
	var order []int
	for i := range 100 {
		w.Enqueue(oneMail(&fakeNotifier{}, func() error { order = append(order, i); return nil }))
	}
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	require.Len(t, order, 100)
	for i, v := range order {
		assert.Equal(t, i, v)
	}
}

func TestMailWorker_ResultHandling(t *testing.T) {
	n := &fakeNotifier{}
	w := NewMailWorker(2)
	w.Start()
	w.Enqueue(oneMail(n, func() error { return errors.New("smtp down") }))
	w.Enqueue(oneMail(n, func() error { return fmt.Errorf("%w (late)", ErrMailUnconfirmed) }))
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))

	require.Len(t, n.entries, 2)
	assert.Equal(t, "smtp down", n.entries[0].Message)
	assert.Equal(t, "AT001", n.entries[0].Identifier)
	assert.Equal(t, "W_MAIL_UNCONFIRMED", n.entries[1].MessageCode)
	assert.Equal(t, ErrMailUnconfirmed.Error(), n.entries[1].Message)
	assert.Equal(t, []string{"TE000001", "TE000001"}, n.tenants)
}

func TestMailWorker_JobContextOutlivesTheCaller(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request-1"))
	w := NewMailWorker(1)
	job := oneMail(&fakeNotifier{}, func() error { return nil })
	job.Ctx = ctx
	w.Enqueue(job)
	cancel()

	queued := <-w.queue
	assert.NoError(t, queued.Ctx.Err())
	assert.Equal(t, "request-1", queued.Ctx.Value(key{}))
}

func TestMailWorker_FullQueueDoesNotBlockAndIsReported(t *testing.T) {
	n := &fakeNotifier{}
	w, release := blockedWorker(t, 1, n)
	queued, dropped := false, false
	w.Enqueue(oneMail(n, func() error { queued = true; return nil }))

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		w.Enqueue(oneMail(n, func() error { dropped = true; return nil }))
	}()
	select {
	case <-returned:
	case <-time.After(mailTestWait):
		t.Fatal("Enqueue blocked on a full queue")
	}

	release()
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	assert.True(t, queued)
	assert.False(t, dropped)
	require.Len(t, n.entries, 1)
	assert.Equal(t, "Die Mail wurde nicht verschickt, weil gerade zu viele Mails anstehen oder der Dienst neu startet. Bitte später noch einmal versuchen.", n.entries[0].Message)
	assert.Equal(t, "AT001", n.entries[0].Identifier)
	assert.NotEqual(t, "W_MAIL_UNCONFIRMED", n.entries[0].MessageCode, "nothing went out: a failure, not an unconfirmed send")
}

func TestMailWorker_StopDrainsTheQueue(t *testing.T) {
	n := &fakeNotifier{}
	w, release := blockedWorker(t, 3, n)
	sent := 0
	for range 3 {
		w.Enqueue(oneMail(n, func() error { sent++; return nil }))
	}
	release()
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	assert.Equal(t, 3, sent)
}

func TestMailWorker_StopGivesUpAtTheLimit(t *testing.T) {
	n := &fakeNotifier{}
	w, _ := blockedWorker(t, 2, n)
	notSent := func() error { t.Error("must not be sent"); return nil }
	w.Enqueue(oneMail(n, notSent))
	w.Enqueue(oneMail(n, notSent))

	began := time.Now()
	assert.Equal(t, 2, w.Stop(20*time.Millisecond, mailTestWait))
	assert.Less(t, time.Since(began), mailTestWait)

	// what could not be sent is reported, so the community knows
	require.Len(t, n.entries, 2)
	assert.Equal(t, ErrMailNotQueued.Error(), n.entries[0].Message)
	assert.Equal(t, []string{"TE000001", "TE000001"}, n.tenants)
}

func TestMailWorker_ReportingEndsAtItsLimit(t *testing.T) {
	n := &fakeNotifier{}
	w, _ := blockedWorker(t, 2, n)
	w.Enqueue(oneMail(n, func() error { return nil }))
	w.Enqueue(oneMail(n, func() error { return nil }))

	assert.Equal(t, 2, w.Stop(20*time.Millisecond, -time.Second))
	assert.Empty(t, n.entries)
}

func TestMailWorker_AfterStopAMailIsReported(t *testing.T) {
	n := &fakeNotifier{}
	w := NewMailWorker(1)
	w.Start()
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	w.Enqueue(oneMail(n, func() error { t.Error("must not be sent"); return nil }))
	require.Len(t, n.entries, 1)
	assert.Equal(t, ErrMailNotQueued.Error(), n.entries[0].Message)
}

func TestMailWorker_PanicKeepsTheWorkerRunning(t *testing.T) {
	n := &fakeNotifier{}
	w := NewMailWorker(2)
	w.Start()
	sent := false
	w.Enqueue(oneMail(n, func() error { panic("bad template") }))
	w.Enqueue(oneMail(n, func() error { sent = true; return nil }))
	assert.Equal(t, 0, w.Stop(mailTestWait, mailTestWait))
	assert.True(t, sent)
	require.Len(t, n.entries, 1, "the community hears about the mail that panicked")
	assert.Equal(t, errMailPanic.Error(), n.entries[0].Message)
	assert.Equal(t, "AT001", n.entries[0].Identifier)
}

// A panic ends one attempt, not the job: the next tenant of a GEA still gets its turn.
func TestMailJob_PanicEndsOneAttempt(t *testing.T) {
	n := &fakeNotifier{}
	sends := 0
	MailJob{Ctx: context.Background(), Notifier: n, Attempts: []MailAttempt{
		{Tenant: "GC000001-001", Identifier: "AT001", DedupeKey: "a@example.org|AT001", Send: func() error { panic("bad template") }},
		{Tenant: "GC000001-002", Identifier: "AT001", DedupeKey: "a@example.org|AT001", Send: func() error { sends++; return nil }},
	}}.run()
	assert.Equal(t, 1, sends, "a panicked attempt does not count as sent")
	assert.Equal(t, []string{"GC000001-001"}, n.tenants)
}

func TestMailJob_Dedupe(t *testing.T) {
	unconfirmed := fmt.Errorf("%w (late)", ErrMailUnconfirmed)
	for name, tt := range map[string]struct {
		first error
		sends int
		notes int
	}{
		"sent: the next tenant does not send it again":        {nil, 1, 0},
		"not confirmed: the next tenant does not send it":     {unconfirmed, 1, 1},
		"partly delivered (261031-mm5): not sent again":       {checkRejectedRecipients([]string{"x@invalid"}), 1, 1},
		"real failure: the next tenant tries, so it can work": {errors.New("smtp down"), 2, 1},
	} {
		t.Run(name, func(t *testing.T) {
			n := &fakeNotifier{}
			sends := 0
			attempt := func(tenant string, err error) MailAttempt {
				return MailAttempt{Tenant: tenant, Identifier: "AT001", DedupeKey: "a@example.org|AT001", Send: func() error { sends++; return err }}
			}
			MailJob{Ctx: context.Background(), Notifier: n, Attempts: []MailAttempt{attempt("GC000001-001", tt.first), attempt("GC000001-002", nil)}}.run()
			assert.Equal(t, tt.sends, sends)
			assert.Len(t, n.entries, tt.notes)
		})
	}

	t.Run("another member or metering point is not a duplicate", func(t *testing.T) {
		sends := 0
		send := func() error { sends++; return nil }
		MailJob{Ctx: context.Background(), Notifier: &fakeNotifier{}, Attempts: []MailAttempt{
			{DedupeKey: "a@example.org|AT001", Send: send},
			{DedupeKey: "b@example.org|AT001", Send: send},
			{DedupeKey: "a@example.org|AT002", Send: send},
			{Send: send},
			{Send: send},
		}}.run()
		assert.Equal(t, 5, sends)
	})
}
