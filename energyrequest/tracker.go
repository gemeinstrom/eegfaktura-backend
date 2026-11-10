package energyrequest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/pborman/uuid"
	"github.com/sirupsen/logrus"
)

// CheckInterval is how often open runs are evaluated.
const CheckInterval = 5 * time.Minute

// LookBack is how far back every evaluation (Check, lock, live entry) reads run rows and summaries; the answers are
// read from the oldest of these runs on. A run that can still be open (Deadline, plus an hour for a check after a
// restart) may compete for answers with runs up to a Deadline older, so those are read too: the owner of an answer
// stays in the set. All three read the rows from the same bound, so they attribute alike.
const LookBack = 2*Deadline + time.Hour

// Store is the part of the database the summary needs (implemented by database.Database).
type Store interface {
	SaveEnergyRequestRow(ctx context.Context, tenant, rowType string, value interface{}) (int64, error)
	UpdateEnergyRequestRow(ctx context.Context, id int64, value interface{}) error
	DeleteEnergyRequestRow(ctx context.Context, id int64) error
	// EnergyRequestRows returns the run rows and summaries of the last LookBack and the answers from the oldest of
	// these runs on (ascending id), of the given tenants or of all tenants (nil).
	EnergyRequestRows(ctx context.Context, tenants []string) ([]Row, error)
	TenantsByEcId(ctx context.Context, ecId string) ([]string, error)
	CloseEnergyRequest(ctx context.Context, tenant string, runRowId int64, summary Summary) (bool, error)
}

// checkMu keeps checks of this instance from overlapping.
var checkMu sync.Mutex

// NewRun starts collecting a run for a request of the community ecId for the period from..to.
func NewRun(ecId string, from, to int64) *Run {
	return &Run{Type: RowTypeRun, RunId: uuid.New(), EcId: ecId, From: from, To: to,
		Meters: []MeterRange{}, Skipped: []Skipped{}}
}

// Add records a metering point that is requested for from..to. A metering point that occurs twice (several
// participations in the period) is merged to the widest period.
func (r *Run) Add(meter string, from, to int64) {
	r.dropSkipped(meter)
	for i := range r.Meters {
		if r.Meters[i].MeteringPoint == meter {
			if from < r.Meters[i].From {
				r.Meters[i].From = from
			}
			if to > r.Meters[i].To {
				r.Meters[i].To = to
			}
			return
		}
	}
	r.Meters = append(r.Meters, MeterRange{MeteringPoint: meter, From: from, To: to})
}

// FitPeriod sets the period of the run to the span of its requested metering points (for requests with a period per
// metering point). A run without requested metering points keeps its period.
func (r *Run) FitPeriod() {
	r.fitted = true
	for i, m := range r.Meters {
		if i == 0 || m.From < r.From {
			r.From = m.From
		}
		if i == 0 || m.To > r.To {
			r.To = m.To
		}
	}
}

// Skip records a metering point that is not requested, unless it is requested for another participation. Of
// several participations (owner change), the reason that tells most counts, whatever the order of the entries (see
// weakness); a metering point locked for two of them reports the later until.
func (r *Run) Skip(meter, reason string) {
	r.SkipUntil(meter, reason, 0)
}

// SkipUntil is Skip with the time (ms) from which the metering point is free again (reason ALREADY_REQUESTED).
func (r *Run) SkipUntil(meter, reason string, until int64) {
	for _, m := range r.Meters {
		if m.MeteringPoint == meter {
			return
		}
	}
	for i, s := range r.Skipped {
		if s.MeteringPoint == meter {
			switch {
			case weakness(s.Reason) > weakness(reason):
				r.Skipped[i].Reason = reason
				r.Skipped[i].Until = until
			case s.Reason == reason && until > s.Until:
				r.Skipped[i].Until = until
			}
			return
		}
	}
	r.Skipped = append(r.Skipped, Skipped{MeteringPoint: meter, Reason: reason, Until: until})
}

// weakness ranks the reasons of several participations for SkipUntil: the higher, the sooner it gives way. A locked
// participation (ALREADY_REQUESTED) tells most; NOT_ASSIGNED (archived) and NOT_ACTIVE_IN_PERIOD (e.g. the old
// member's entry outside its period) least. NOT_FOUND (Save, for the metering points not found at all) never
// replaces a reason, nor does an unknown one. COMPLETE is recorded only for metering points not in meteringPoints.
func weakness(reason string) int {
	switch reason {
	case ReasonComplete:
		return 0
	case ReasonAlreadyRequested:
		return 1
	case ReasonNotRegistered:
		return 2
	case ReasonNoActivePeriod:
		return 3
	case ReasonNotActiveInPeriod:
		return 4
	case ReasonNotAssigned:
		return 5
	}
	return 6 // NOT_FOUND and unknown reasons
}

func (r *Run) dropSkipped(meter string) {
	kept := r.Skipped[:0]
	for _, s := range r.Skipped {
		if s.MeteringPoint != meter {
			kept = append(kept, s)
		}
	}
	r.Skipped = kept
}

// Save stores the run before the requests are sent, so every answer has a higher row id. A run that sends nothing
// (everything skipped) is not stored: the caller reports the skipped metering points. A request with fewer than two
// metering points gets no summary; it is still stored when it sends a request, so its answer is not counted for an
// older open run. It returns the row id (0 when not stored). A run that cannot be stored must not be sent: nothing
// would lock its metering points or summarise its answers.
func Save(ctx context.Context, store Store, tenant string, requested []string, r *Run) (int64, error) {
	distinct := map[string]bool{}
	for _, m := range requested {
		if !distinct[m] {
			distinct[m] = true
			r.Skip(m, ReasonNotFound) // no-op for metering points that were found (requested or skipped)
		}
	}
	if len(r.Meters) == 0 {
		return 0, nil
	}
	if len(distinct) < 2 {
		r.NoSummary = true
	}
	r.CreatedAt = time.Now().UnixMilli()
	return store.SaveEnergyRequestRow(ctx, tenant, RowTypeRun, r)
}

// KeepSent reduces the run stored by Save in row rowId to the requests that were sent, after a send failed: each
// metering point to the span of its sent participations (Add merged them), without one it is taken out. What was not
// sent is not locked and not summarised; a run without a sent request is deleted. Errors are logged.
//
// A failed participation between two sent ones of the same metering point stays inside the span.
func KeepSent(ctx context.Context, store Store, rowId int64, r *Run, sent []MeterRange) {
	if rowId == 0 {
		return
	}
	spans := map[string]MeterRange{}
	for _, s := range sent {
		if span, ok := spans[s.MeteringPoint]; ok {
			s.From, s.To = min(s.From, span.From), max(s.To, span.To)
		}
		spans[s.MeteringPoint] = s
	}
	kept := r.Meters[:0]
	for _, m := range r.Meters {
		if span, ok := spans[m.MeteringPoint]; ok {
			kept = append(kept, span)
		}
	}
	r.Meters = kept
	if r.fitted {
		r.FitPeriod()
	}
	var err error
	if len(r.Meters) == 0 {
		err = store.DeleteEnergyRequestRow(ctx, rowId)
	} else {
		err = store.UpdateEnergyRequestRow(ctx, rowId, r)
	}
	if err != nil {
		logrus.WithContext(ctx).WithError(err).Error("energy request summary: cannot take unsent metering points out of the run")
	}
}

// Start evaluates open runs now and then every CheckInterval until ctx ends.
func Start(ctx context.Context, store Store) {
	go func() {
		ticker := time.NewTicker(CheckInterval)
		defer ticker.Stop()
		for {
			if err := Check(ctx, store, time.Now()); err != nil {
				logrus.WithError(err).Error("energy request summary: check failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Check evaluates the open runs and writes the summaries of the finished ones. It reads the rows of the last LookBack
// of all tenants and attributes the answers per community, as the lock and the live entry do on the rows of one
// community.
func Check(ctx context.Context, store Store, now time.Time) error {
	checkMu.Lock()
	defer checkMu.Unlock()

	rows, err := store.EnergyRequestRows(ctx, nil)
	if err != nil {
		return err
	}

	closed := summaries(rows)
	tenantsByEcId := map[string]map[string]bool{}
	attributions := map[string]attribution{}
	for _, r := range rows {
		if r.Type != RowTypeRun {
			continue
		}
		var run Run
		if err := json.Unmarshal(r.Message, &run); err != nil || closed[run.RunId] != 0 || run.NoSummary {
			continue
		}
		ecId := strings.ToUpper(run.EcId)
		tenants, ok := tenantsByEcId[ecId]
		if !ok {
			list, err := store.TenantsByEcId(ctx, run.EcId)
			if err != nil {
				return err
			}
			tenants = map[string]bool{r.Tenant: true}
			for _, t := range list {
				tenants[t] = true
			}
			tenantsByEcId[ecId] = tenants
			attributions[ecId] = attribute(run.EcId, rows, tenants, closed)
		}

		summary, done := evaluate(run, r.Id, attributions[ecId], now)
		if !done {
			continue
		}
		if _, err := store.CloseEnergyRequest(ctx, r.Tenant, r.Id, summary); err != nil {
			return err
		}
	}
	return nil
}
