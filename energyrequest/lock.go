package energyrequest

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// LockWindow is how long a requested metering point is not requested again for an overlapping period, unless the
// grid operator rejected it. It is independent of the Deadline of the summary.
const LockWindow = time.Hour

type lock struct {
	from, to, until int64
}

// Locks are the metering points requested by the runs of a tenant in the last LockWindow, with the requested period
// and the time (ms) they are free again.
type Locks map[string][]lock

// NewLocks collects the locks of tenant from rows (ascending id): the run rows and answers of its community, as
// database.EnergyRequestCommunity returns them: back to LookBack, not only LockWindow, so the answers are attributed
// as in Check. tenants are the tenants the answers are stored under (see Evaluate).
// A run locks each of its metering points for its period for LockWindow from its creation, unless the latest answer
// that belongs to the run for that metering point (the same attribution as in Evaluate) is a rejection.
func NewLocks(rows []Row, tenant string, tenants map[string]bool, now time.Time) Locks {
	locks := Locks{}
	attributions := map[string]attribution{}
	var closed map[string]int64
	for _, r := range rows {
		if r.Type != RowTypeRun || !strings.EqualFold(r.Tenant, tenant) {
			continue
		}
		var run Run
		if json.Unmarshal(r.Message, &run) != nil {
			continue
		}
		until := time.UnixMilli(run.CreatedAt).Add(LockWindow).UnixMilli()
		if now.UnixMilli() >= until {
			continue
		}
		a, ok := attributions[strings.ToUpper(run.EcId)]
		if !ok {
			if closed == nil {
				closed = summaries(rows)
			}
			a = attribute(run.EcId, rows, tenants, closed)
			attributions[strings.ToUpper(run.EcId)] = a
		}
		for _, m := range run.Meters {
			if answers, _ := a.belonging(r.Id, m.MeteringPoint); lastRejected(answers) {
				continue
			}
			locks[m.MeteringPoint] = append(locks[m.MeteringPoint], lock{from: m.From, to: m.To, until: until})
		}
	}
	return locks
}

// Until returns the time (ms) from which meter is free again for a request from..to, or 0 when it is free now. The
// periods are day-granular (the handler cuts them to start and end of day): they overlap when they share a day.
func (l Locks) Until(meter string, from, to int64) int64 {
	var until int64
	for _, k := range l[meter] {
		if overlaps(k.from, k.to, from, to) && k.until > until {
			until = k.until
		}
	}
	return until
}

// overlaps tells whether two day-granular periods share a day.
func overlaps(fromA, toA, fromB, toB int64) bool {
	return day(fromA) <= day(toB) && day(fromB) <= day(toA)
}

// Touch tells whether a day-granular period starting at from follows one ending at to without a free day between
// them: they overlap, or from is on the day after to (the handler merges such parts into one request).
func Touch(to, from int64) bool {
	y, m, d := time.UnixMilli(to).AddDate(0, 0, 1).Date()
	return day(from) <= y*10000+int(m)*100+d
}

// day is the local calendar day of ms as yyyymmdd, as the handler builds the periods in time.Local.
func day(ms int64) int {
	y, m, d := time.UnixMilli(ms).Date()
	return y*10000 + int(m)*100 + d
}

var tenantMu sync.Map

// LockTenant serialises the lock check and the saving of the run of one tenant within this instance, so a double
// click does not send twice. It returns the unlock function.
func LockTenant(tenant string) func() {
	mu, _ := tenantMu.LoadOrStore(strings.ToUpper(tenant), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}
