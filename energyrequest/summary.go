// Package energyrequest summarises an energy data request (ANFORDERUNG_PT) for several metering points in one
// message (platform#173). Nothing is stored outside base.notification: the request ("run") is a hidden notification
// row (process ENERGY_REQUEST), the answers are the existing ANTWORT_PT / ABLEHNUNG_PT notifications, and the
// summary is a visible notification. The state of each metering point is computed from these rows on every check.
// The energy data itself is not tracked here (the backend drops CR_MSG); web checks it from the summary on demand.
package energyrequest

import (
	"time"

	"github.com/jmoiron/sqlx/types"
)

const (
	RowTypeRun = "RUN"

	SummaryType = "ZUSAMMENFASSUNG_PT"

	// Deadline after which a run is summarised even if answers are missing.
	Deadline = 24 * time.Hour

	// Reasons why a requested metering point was not requested from the grid operator.
	ReasonNotFound          = "NOT_FOUND"
	ReasonNotRegistered     = "NOT_REGISTERED"
	ReasonNotAssigned       = "NOT_ASSIGNED"
	ReasonNoActivePeriod    = "NO_ACTIVE_PERIOD"
	ReasonNotActiveInPeriod = "NOT_ACTIVE_IN_PERIOD"
	// ReasonComplete: web found the energy data of the period complete (request field complete)
	ReasonComplete = "COMPLETE"
	// ReasonAlreadyRequested: requested in a run of the last LockWindow for an overlapping period (Skipped.Until)
	ReasonAlreadyRequested = "ALREADY_REQUESTED"

	answerAccepted = "ANTWORT_PT"
	answerRejected = "ABLEHNUNG_PT"
	// notification type of the answers and of the summary (model.N_TYPE_MESSAGE)
	messageRow = "MESSAGE"
)

// MeterRange is a requested metering point with the period actually requested (cut to its active period).
type MeterRange struct {
	MeteringPoint string `json:"meteringPoint"`
	From          int64  `json:"from"`
	To            int64  `json:"to"`
}

type Skipped struct {
	MeteringPoint string `json:"meteringPoint"`
	Reason        string `json:"reason"`
	// Until is when a metering point skipped as ALREADY_REQUESTED is free again (ms)
	Until int64 `json:"until,omitempty"`
}

// Run is the content of the hidden RUN row.
type Run struct {
	Type      string       `json:"type"`
	RunId     string       `json:"runId"`
	EcId      string       `json:"ecId"`
	From      int64        `json:"from"`
	To        int64        `json:"to"`
	CreatedAt int64        `json:"createdAt"`
	Meters    []MeterRange `json:"meters"`
	Skipped   []Skipped    `json:"skipped"`
	// NoSummary marks a request for a single metering point: it gets no summary, but its answers belong to it and
	// not to an older open run with that metering point
	NoSummary bool `json:"noSummary,omitempty"`
	// fitted: the period is the span of the requested metering points (FitPeriod), kept so by KeepSent
	fitted bool
}

type Rejected struct {
	MeteringPoint string   `json:"meteringPoint"`
	Codes         []string `json:"codes"`
}

// Summary is the content of the visible summary notification. Accepted carries the requested periods, so web can
// check the energy data of these metering points.
type Summary struct {
	Type       string       `json:"type"`
	RunId      string       `json:"runId"`
	From       int64        `json:"from"`
	To         int64        `json:"to"`
	Requested  int          `json:"requested"`
	Accepted   []MeterRange `json:"accepted"`
	Rejected   []Rejected   `json:"rejected"`
	NoAnswer   []string     `json:"noAnswer"`
	Skipped    []Skipped    `json:"skipped"`
	Superseded []string     `json:"superseded"`
	// Live marks the current state of an open run in the Messages list; a stored summary has no Live
	Live bool `json:"live,omitempty"`
}

// Row is a notification row as far as the evaluation needs it.
type Row struct {
	Id      int64          `db:"id"`
	Tenant  string         `db:"tenant"`
	Process string         `db:"process"`
	Type    string         `db:"type"`
	Message types.JSONText `db:"notification"`
}

// answer is the content of an existing ANTWORT_PT / ABLEHNUNG_PT notification (database.SaveNotification).
type answer struct {
	Type           string   `json:"type"`
	MeteringPoints []string `json:"meteringPoints"`
	ResponseCodes  []string `json:"responseCodes"`
}

// Evaluate computes the summary of the run in row runRowId. rows are all relevant rows (ascending id), the run row
// and the earlier runs that can still take answers included; tenants are the tenants of the run's community: the answers are stored
// under one of them (a GEA can share the community id). done is true when every metering point has an answer or the
// deadline has passed. Which answer belongs to which run: see attribute.
func Evaluate(run Run, runRowId int64, rows []Row, tenants map[string]bool, now time.Time) (Summary, bool) {
	return evaluate(run, runRowId, attribute(run.EcId, rows, tenants, summaries(rows)), now)
}

// evaluate is Evaluate on the attributed answers.
func evaluate(run Run, runRowId int64, a attribution, now time.Time) (Summary, bool) {
	s := Summary{Type: SummaryType, RunId: run.RunId, From: run.From, To: run.To, Requested: len(run.Meters),
		Accepted: []MeterRange{}, Rejected: []Rejected{}, NoAnswer: []string{}, Skipped: run.Skipped,
		Superseded: []string{}}
	if s.Skipped == nil {
		s.Skipped = []Skipped{}
	}

	for _, m := range run.Meters {
		belonging, superseded := a.belonging(runRowId, m.MeteringPoint)
		var answered, accepted bool
		var rejected []string
		for _, ans := range belonging {
			// the latest answer counts
			answered = true
			accepted = ans.answer.Type == answerAccepted
			rejected = nil
			if !accepted {
				rejected = ans.answer.ResponseCodes
				if rejected == nil {
					rejected = []string{}
				}
			}
		}

		switch {
		case !answered && superseded:
			s.Superseded = append(s.Superseded, m.MeteringPoint)
		case rejected != nil:
			s.Rejected = append(s.Rejected, Rejected{MeteringPoint: m.MeteringPoint, Codes: rejected})
		case accepted:
			s.Accepted = append(s.Accepted, m)
		default:
			s.NoAnswer = append(s.NoAnswer, m.MeteringPoint)
		}
	}
	return s, len(s.NoAnswer) == 0 || now.Sub(time.UnixMilli(run.CreatedAt)) >= Deadline
}
