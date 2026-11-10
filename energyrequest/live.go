package energyrequest

import (
	"encoding/json"
	"strings"
	"time"
)

// LiveEntry is the current state of an open run for the Messages list: its row id and creation time place it there.
type LiveEntry struct {
	RowId     int64
	CreatedAt int64
	Summary   Summary
}

// Live evaluates the open runs of tenant in rows (ascending id, as database.EnergyRequestCommunity returns them) and
// returns their live entries and the ids of the answers that belong to them. A run is open while it has a summary
// (not NoSummary), no summary was written yet and it is younger than Deadline. tenants are as in Evaluate.
func Live(rows []Row, tenant string, tenants map[string]bool, now time.Time) ([]LiveEntry, map[int64]bool) {
	closed := summaries(rows)

	entries := []LiveEntry{}
	hidden := map[int64]bool{}
	attributions := map[string]attribution{}
	for _, r := range rows {
		if r.Type != RowTypeRun || !strings.EqualFold(r.Tenant, tenant) {
			continue
		}
		var run Run
		if json.Unmarshal(r.Message, &run) != nil || run.NoSummary || closed[run.RunId] != 0 ||
			now.Sub(time.UnixMilli(run.CreatedAt)) >= Deadline {
			continue
		}
		a, ok := attributions[strings.ToUpper(run.EcId)]
		if !ok {
			a = attribute(run.EcId, rows, tenants, closed)
			attributions[strings.ToUpper(run.EcId)] = a
		}
		s, _ := evaluate(run, r.Id, a, now)
		s.Live = true
		entries = append(entries, LiveEntry{RowId: r.Id, CreatedAt: run.CreatedAt, Summary: s})
		for _, m := range run.Meters {
			answers, _ := a.belonging(r.Id, m.MeteringPoint)
			for _, ans := range answers {
				hidden[ans.id] = true
			}
		}
	}
	return entries, hidden
}
