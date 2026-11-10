package energyrequest

import (
	"encoding/json"
	"strings"
	"time"
)

// Message is a listed notification as far as a Collapser needs it.
type Message struct {
	Id      int64
	Date    time.Time
	Content json.RawMessage
}

// collapsedTypes are the messages a summary stands for: the request echo per metering point and the answers.
var collapsedTypes = map[string]bool{"ANFORDERUNG_PT": true, answerAccepted: true, answerRejected: true}

// CollapseWindow is how long after a run its requests and answers can belong to it (the summary is written at
// Deadline at the latest).
const CollapseWindow = Deadline + time.Hour

// Collapsible reports whether a message is of a type a Collapser may leave out, so its runs have to be read.
func Collapsible(content json.RawMessage) bool {
	var m struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(content, &m) == nil && collapsedTypes[m.Type]
}

type collapseRun struct {
	id         int64
	runId      string
	createdAt  time.Time
	summarised bool // not a request for a single metering point
	own        bool // a run of the listed tenant with a summary
	meters     map[string]bool
}

// Collapser decides which requests (ANFORDERUNG_PT) and answers (ANTWORT_PT, ABLEHNUNG_PT) the Messages list of a
// tenant leaves out (platform#173, decision 28.10.2026): those a live entry or a stored summary of that tenant stands
// for. Answers carry no period and no reference to their request, so the rule is conservative: a message is left out
// only if, for each of its metering points, every run that can still own it is a run with a summary of the listed
// tenant. A run can own a message when it is older (lower row id), created at most CollapseWindow before it and sent
// the metering point; a run with a summary only while it is open or has its summary after the message. So kept are:
// messages a request for a single metering point (NoSummary) or another tenant's run in a GEA can own, an answer after
// the summary (the summary does not count it) and the messages of a run that got no summary after Deadline. Metering
// points compare exactly, as in the summary's attribution.
type Collapser struct {
	tenant    string
	now       time.Time
	runs      map[int64]collapseRun
	summaries map[string]int64 // run id → row id of its stored summary
}

func NewCollapser(tenant string, now time.Time) *Collapser {
	return &Collapser{tenant: tenant, now: now, runs: map[int64]collapseRun{}, summaries: map[string]int64{}}
}

// AddRuns adds RUN rows of the community; rows already added and other rows are ignored.
func (c *Collapser) AddRuns(rows []Row) {
	for _, r := range rows {
		if _, ok := c.runs[r.Id]; ok || r.Type != RowTypeRun {
			continue
		}
		var run Run
		if json.Unmarshal(r.Message, &run) != nil {
			continue
		}
		meters := map[string]bool{}
		for _, m := range run.Meters {
			meters[m.MeteringPoint] = true
		}
		c.runs[r.Id] = collapseRun{id: r.Id, runId: run.RunId, createdAt: time.UnixMilli(run.CreatedAt),
			summarised: !run.NoSummary, own: !run.NoSummary && strings.EqualFold(r.Tenant, c.tenant), meters: meters}
	}
}

// AddSummaries adds stored summaries (rows of any type; others are ignored).
func (c *Collapser) AddSummaries(rows []Row) {
	for _, r := range rows {
		var s struct {
			Type  string `json:"type"`
			RunId string `json:"runId"`
		}
		if json.Unmarshal(r.Message, &s) == nil && s.Type == SummaryType && s.RunId != "" {
			c.summaries[s.RunId] = r.Id
		}
	}
}

// eligible tells whether run r can own the message msg.
func (c *Collapser) eligible(r collapseRun, msg Message) bool {
	if r.id >= msg.Id || msg.Date.Sub(r.createdAt) > CollapseWindow {
		return false
	}
	if r.summarised {
		if summary := c.summaries[r.runId]; summary != 0 {
			return msg.Id < summary
		}
		return c.now.Sub(r.createdAt) < Deadline // open; no summary after Deadline: kept
	}
	return true
}

// Hide returns the ids of the messages left out, and for each run the highest id of the messages left out whose
// newest owner it is (its live entry is placed there).
func (c *Collapser) Hide(messages []Message) (hidden map[int64]bool, latest map[int64]int64) {
	hidden = map[int64]bool{}
	latest = map[int64]int64{}
	for _, msg := range messages {
		var m struct {
			Type           string   `json:"type"`
			MeteringPoints []string `json:"meteringPoints"`
		}
		if json.Unmarshal(msg.Content, &m) != nil || !collapsedTypes[m.Type] || len(m.MeteringPoints) == 0 {
			continue
		}
		var newest int64
		covered := true
		for _, mp := range m.MeteringPoints {
			owners := 0
			for _, r := range c.runs {
				if !r.meters[mp] || !c.eligible(r, msg) {
					continue
				}
				if !r.own {
					covered = false
					break
				}
				owners++
				newest = max(newest, r.id)
			}
			if owners == 0 {
				covered = false
			}
			if !covered {
				break
			}
		}
		if !covered {
			continue
		}
		hidden[msg.Id] = true
		latest[newest] = max(latest[newest], msg.Id)
	}
	return hidden, latest
}
