package energyrequest

import (
	"encoding/json"
	"math"
	"strings"
)

type idAnswer struct {
	id     int64
	answer answer
}

// request is one metering point of a run, with its period and the row range in which it can take answers.
type request struct {
	rowId    int64 // the run row
	from, to int64
	// end is the row of the next request of the metering point for an overlapping period (it supersedes this one),
	// closed the row of the run's stored summary; math.MaxInt64 when there is none
	end, closed int64
	answers     []idAnswer
}

type requestKey struct {
	rowId int64
	meter string
}

// attribution is the answers of each requested metering point of the runs of one community.
type attribution map[requestKey]*request

// attribute assigns the answers in rows (ascending id) to the runs of the community ecId in rows. Answers carry
// neither a period nor a reference to the request (the conversation id is only in base.processhistory), so per
// metering point:
//   - a request takes answers after its run row, until a later run requests the metering point for an overlapping
//     period (that request supersedes it) and until its run's summary is stored;
//   - an answer goes to the oldest of these requests that has no answer yet, else to the newest (the latest answer
//     counts). The grid operator answers each request once, in order as a rule: two runs for different periods
//     (e.g. May, then April) each take one answer, the first answer the older run.
//
// tenants are the tenants of the community: its runs and answers are stored under them (see Evaluate); closed is
// summaries(rows), parsed once by the caller.
func attribute(ecId string, rows []Row, tenants map[string]bool, closed map[string]int64) attribution {
	a := attribution{}
	byMeter := map[string][]*request{}
	runIds := map[int64]string{}
	var answers []idAnswer
	for _, r := range rows {
		if !tenants[r.Tenant] {
			continue // another community: not parsed
		}
		switch {
		case r.Type == RowTypeRun:
			var run Run
			if json.Unmarshal(r.Message, &run) != nil || !strings.EqualFold(run.EcId, ecId) {
				continue
			}
			runIds[r.Id] = run.RunId
			for _, m := range run.Meters {
				req := &request{rowId: r.Id, from: m.From, to: m.To, end: math.MaxInt64, closed: math.MaxInt64}
				for _, earlier := range byMeter[m.MeteringPoint] {
					if earlier.end == math.MaxInt64 && overlaps(earlier.from, earlier.to, m.From, m.To) {
						earlier.end = r.Id
					}
				}
				byMeter[m.MeteringPoint] = append(byMeter[m.MeteringPoint], req)
				a[requestKey{rowId: r.Id, meter: m.MeteringPoint}] = req
			}
		case r.Type == messageRow:
			var ans answer
			if json.Unmarshal(r.Message, &ans) == nil && (ans.Type == answerAccepted || ans.Type == answerRejected) {
				answers = append(answers, idAnswer{id: r.Id, answer: ans})
			}
		}
	}
	for _, req := range a {
		if id, ok := closed[runIds[req.rowId]]; ok {
			req.closed = id
		}
	}

	for _, ans := range answers {
		for _, m := range ans.answer.MeteringPoints {
			var oldestOpen, newest *request
			for _, req := range byMeter[m] {
				if ans.id <= req.rowId || ans.id > req.end || ans.id > req.closed {
					continue
				}
				if oldestOpen == nil && len(req.answers) == 0 {
					oldestOpen = req
				}
				newest = req
			}
			if oldestOpen == nil {
				oldestOpen = newest
			}
			if oldestOpen != nil {
				oldestOpen.answers = append(oldestOpen.answers, ans)
			}
		}
	}
	return a
}

// belonging returns the answers (ascending id) for meter that belong to the run in row runRowId, and whether a later
// run requests meter again for an overlapping period.
func (a attribution) belonging(runRowId int64, meter string) ([]idAnswer, bool) {
	req, ok := a[requestKey{rowId: runRowId, meter: meter}]
	if !ok {
		return nil, false
	}
	return req.answers, req.end != math.MaxInt64
}

// summaries returns the row id of each stored summary in rows, by run id.
func summaries(rows []Row) map[string]int64 {
	ids := map[string]int64{}
	for _, r := range rows {
		if r.Type != messageRow {
			continue
		}
		var s struct {
			Type  string `json:"type"`
			RunId string `json:"runId"`
		}
		if json.Unmarshal(r.Message, &s) == nil && s.Type == SummaryType {
			ids[s.RunId] = r.Id
		}
	}
	return ids
}

// lastRejected tells whether the latest of the answers is a rejection.
func lastRejected(answers []idAnswer) bool {
	return len(answers) > 0 && answers[len(answers)-1].answer.Type == answerRejected
}
