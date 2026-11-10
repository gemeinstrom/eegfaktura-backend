package model

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The settlement intervals of base.eeg."settlementInterval".
const (
	SettlementMonthly  = "MONTHLY"
	SettlementQuarter  = "QUARTER"
	SettlementBiannual = "BIANNUAL"
	SettlementAnnual   = "ANNUAL"
)

// periodMonths is the length of one period of each interval, in months.
var periodMonths = map[string]int{SettlementMonthly: 1, SettlementQuarter: 3, SettlementBiannual: 6, SettlementAnnual: 12}

// IsSettlementInterval reports whether v is one of the four intervals.
func IsSettlementInterval(v string) bool {
	_, ok := periodMonths[v]
	return ok
}

// BillingState is billing's settlementState, passed on by web (konzept-abrechnungsintervall.md): the clearing period
// identifiers of the final runs with documents and of the running runs.
type BillingState struct {
	FinalPeriods   []string `json:"finalPeriods"`
	RunningPeriods []string `json:"runningPeriods"`
}

// CheckBillingState reports why a billingState cannot be used (eegfaktura-dev#54 #9), nil when it can: missing, a
// list missing or null, or an identifier the rule cannot read. Before, such a state read as "nothing billed" and the
// change applied at once.
func CheckBillingState(s *BillingState) error {
	switch {
	case s == nil:
		return errors.New("Der Abrechnungsstand (billingState) fehlt")
	case s.FinalPeriods == nil || s.RunningPeriods == nil:
		return errors.New("Der Abrechnungsstand (billingState) ist unvollständig (finalPeriods, runningPeriods)")
	}
	var unreadable []string
	for _, list := range [][]string{s.FinalPeriods, s.RunningPeriods} {
		for _, id := range list {
			if _, ok := parseClearingPeriod(id); !ok {
				unreadable = append(unreadable, id)
			}
		}
	}
	switch {
	case len(unreadable) == 0:
		return nil
	case len(unreadable) == 1:
		return fmt.Errorf("Der Abrechnungsstand (billingState) enthält eine unlesbare Zeitraum-Kennung: %s", quoteIds(unreadable))
	}
	return fmt.Errorf("Der Abrechnungsstand (billingState) enthält unlesbare Zeitraum-Kennungen: %s", quoteIds(unreadable))
}

// maxNamed is how many identifiers or gaps a message names at most, so it stays short.
const maxNamed = 5

// quoteIds names at most maxNamed identifiers, each quoted and cut to 40 characters (they come from the client).
func quoteIds(ids []string) string {
	const length = 40
	quoted := make([]string, 0, maxNamed+1)
	for i, id := range ids {
		if i == maxNamed {
			quoted = append(quoted, "…")
			break
		}
		if r := []rune(id); len(r) > length {
			id = string(r[:length]) + "…"
		}
		quoted = append(quoted, strconv.Quote(id))
	}
	return strings.Join(quoted, ", ")
}

// SettlementIntervalDecision is the result of the change rule. Dates are ISO dates (2026-04-01); nil
// EffectiveFrom on an allowed change = applies at once.
type SettlementIntervalDecision struct {
	Allowed         bool    `json:"allowed"`
	EffectiveFrom   *string `json:"effectiveFrom"`
	LastFinalPeriod *string `json:"lastFinalPeriod"`
	Message         string  `json:"message"`
	EarliestFrom    *string `json:"earliestFrom"`
	// OldInterval is the interval the change was decided against, set by the database (for the log, not in the answer).
	OldInterval string `json:"-"`
}

// clearingPeriodPattern is billing's ClearingPeriodMonths.IDENTIFIER.
var clearingPeriodPattern = regexp.MustCompile(`^Abr_(Y|YH|YQ|YM)-(\d{4})(?:-(\d{1,2}))?$`)

// identifierMonths is the length of one period of each identifier type, in months.
var identifierMonths = map[string]int{"Y": 12, "YH": 6, "YQ": 3, "YM": 1}

// clearingPeriod is the months an identifier covers, counted as year*12 + month-1.
type clearingPeriod struct {
	kind        string
	first, last int
}

// The years an identifier may name (eegfaktura-dev#54 #9: Abr_Y-9999 answered effectiveFrom 10000-01-01).
const minClearingYear, maxClearingYear = 2000, 2100

// parseClearingPeriod mirrors billing's ClearingPeriodMonths.of: false for an identifier of another form, and for a
// year outside minClearingYear..maxClearingYear.
func parseClearingPeriod(id string) (clearingPeriod, bool) {
	m := clearingPeriodPattern.FindStringSubmatch(id)
	if m == nil {
		return clearingPeriod{}, false
	}
	year, _ := strconv.Atoi(m[2])
	if year < minClearingYear || year > maxClearingYear {
		return clearingPeriod{}, false
	}
	period := 0
	if m[3] != "" {
		period, _ = strconv.Atoi(m[3])
	}
	length := identifierMonths[m[1]]
	if m[1] == "Y" {
		period = 1
	} else if period < 1 || period > 12/length {
		return clearingPeriod{}, false
	}
	first := year*12 + (period-1)*length
	return clearingPeriod{kind: m[1], first: first, last: first + length - 1}, true
}

var monthNames = [12]string{"Jänner", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September",
	"Oktober", "November", "Dezember"}

func monthName(m int) string { return fmt.Sprintf("%s %d", monthNames[m%12], m/12) }
func isoDate(m int) *string  { s := fmt.Sprintf("%04d-%02d-01", m/12, m%12+1); return &s }
func longDate(m int) string  { return fmt.Sprintf("1. %s %d", monthNames[m%12], m/12) }

// monthRange names the months first..last of one year: "März 2026", "März bis Mai 2026". Every interval starts a
// period on 1 January, so a blocked range never crosses the year end.
func monthRange(first, last int) string {
	if first == last {
		return monthName(first)
	}
	return fmt.Sprintf("%s bis %s", monthNames[first%12], monthName(last))
}

// name is the period in words: "März 2026", "1. Quartal 2026", "1. Halbjahr 2026", "2026".
func (p clearingPeriod) name() string {
	year, index := p.first/12, p.first%12
	switch p.kind {
	case "YM":
		return monthName(p.first)
	case "YQ":
		return fmt.Sprintf("%d. Quartal %d", index/3+1, year)
	case "YH":
		return fmt.Sprintf("%d. Halbjahr %d", index/6+1, year)
	default:
		return strconv.Itoa(year)
	}
}

// DecideSettlementInterval applies the change rule of konzept-abrechnungsintervall.md (Tech Design, "Regel im
// backend") to a valid new interval. Without a final period the change applies at once (R5). Otherwise L is the last
// month of the final periods and S the month after it: allowed from S when S starts a period of the new interval
// (R1, R2), else blocked with the next start after L; also blocked while a run for a period ending in S or later is
// running (R3), without a final period while any run is running. Before R2, a month of the 12 months up to L
// (L-11 … L) that no final period covers blocks (rule 6, eegfaktura-dev#54 #2, decided 06.11.2026); months before the
// first month of the first final period do not count. An older gap does not block: an allowed change names it in a
// hint after the message. state must have passed CheckBillingState: every identifier is readable.
func DecideSettlementInterval(newInterval string, state BillingState) SettlementIntervalDecision {
	first, last, lastId := -1, -1, ""
	covered := map[int]bool{}
	for _, id := range state.FinalPeriods {
		p, _ := parseClearingPeriod(id)
		if p.last > last {
			last, lastId = p.last, id
		}
		if first < 0 || p.first < first {
			first = p.first
		}
		for m := p.first; m <= p.last; m++ {
			covered[m] = true
		}
	}
	if last < 0 {
		// a final run finishing after a change at once would overlap the first period of the new interval: any
		// running run blocks (R3)
		if p, ok := runningFrom(state, 0); ok {
			return SettlementIntervalDecision{Message: runningMessage(p)}
		}
		return SettlementIntervalDecision{Allowed: true,
			Message: "Das neue Intervall gilt sofort, es gibt noch keine endgültige Abrechnung."}
	}

	start := last + 1
	length := periodMonths[newInterval]
	d := SettlementIntervalDecision{LastFinalPeriod: &lastId}
	recent, older := splitGaps(uncovered(covered, first, last), last-gapWindow+1)
	if len(recent) > 0 {
		d.Message = gapMessage(recent)
		return d
	}
	if start%length != 0 {
		earliest := (start/length + 1) * length
		d.EarliestFrom = isoDate(earliest)
		d.Message = fmt.Sprintf("Zuerst muss noch der Zeitraum %s im bisherigen Intervall abgerechnet werden. Ein Wechsel ist dann ab %s möglich.",
			monthRange(start, earliest-1), longDate(earliest))
		return d
	}
	if p, ok := runningFrom(state, start); ok {
		d.Message = runningMessage(p)
		return d
	}
	d.Allowed = true
	d.EffectiveFrom = isoDate(start)
	d.Message = fmt.Sprintf("Das neue Intervall gilt ab %s.", longDate(start))
	if len(older) > 0 {
		d.Message += " " + olderGapHint(older)
	}
	return d
}

// gapWindow is how many months up to and including L a gap blocks the change (rule 6, decided 06.11.2026): an older
// gap would block a community for good.
const gapWindow = 12

// splitGaps splits gaps at month from: the months from on (recent) and the months before (older).
func splitGaps(gaps [][2]int, from int) (recent, older [][2]int) {
	for _, g := range gaps {
		switch {
		case g[0] >= from:
			recent = append(recent, g)
		case g[1] < from:
			older = append(older, g)
		default:
			older = append(older, [2]int{g[0], from - 1})
			recent = append(recent, [2]int{from, g[1]})
		}
	}
	return recent, older
}

// uncovered returns the runs of months first..last that covered lacks, as [first, last] pairs.
func uncovered(covered map[int]bool, first, last int) [][2]int {
	var gaps [][2]int
	for m := first; m <= last; m++ {
		switch {
		case covered[m]:
		case len(gaps) > 0 && gaps[len(gaps)-1][1] == m-1:
			gaps[len(gaps)-1][1] = m
		default:
			gaps = append(gaps, [2]int{m, m})
		}
	}
	return gaps
}

// gapMessage names the months not billed: "Februar 2026 ist …", "Februar und Mai 2026 sind …", "November 2025 bis
// Jänner 2026 sind …".
func gapMessage(gaps [][2]int) string {
	verb := "sind"
	if oneMonth(gaps) {
		verb = "ist"
	}
	return fmt.Sprintf("%s %s noch nicht endgültig abgerechnet.", gapList(gaps), verb)
}

// olderGapHint names the gaps before the window: "Hinweis: Februar 2024 wurde nicht endgültig abgerechnet.",
// "Hinweis: März bis Mai 2024 wurden nicht endgültig abgerechnet."
func olderGapHint(gaps [][2]int) string {
	verb := "wurden"
	if oneMonth(gaps) {
		verb = "wurde"
	}
	return fmt.Sprintf("Hinweis: %s %s nicht endgültig abgerechnet.", gapList(gaps), verb)
}

// gapList names the gaps, at most maxNamed of them: "Februar und Mai 2026", "Februar, April, Juni, August,
// Oktober 2024 und 2 weitere Zeiträume".
func gapList(gaps [][2]int) string {
	if len(gaps) > maxNamed {
		return fmt.Sprintf("%s und %d weitere Zeiträume", strings.Join(gapParts(gaps[:maxNamed]), ", "), len(gaps)-maxNamed)
	}
	parts := gapParts(gaps)
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " und " + parts[len(parts)-1]
}

// oneMonth reports whether gaps is a single month (singular in the texts).
func oneMonth(gaps [][2]int) bool { return len(gaps) == 1 && gaps[0][0] == gaps[0][1] }

// gapParts names each gap: "Februar 2026", "März bis Mai", "November 2025 bis Jänner 2026". The year is left out when
// the next gap ends in the same year, except after a range across the year end, which always names both years.
func gapParts(gaps [][2]int) []string {
	parts := make([]string, len(gaps))
	for i, g := range gaps {
		if g[0]/12 != g[1]/12 {
			parts[i] = fmt.Sprintf("%s bis %s", monthName(g[0]), monthName(g[1]))
			continue
		}
		year := ""
		if i == len(gaps)-1 || gaps[i+1][1]/12 != g[1]/12 {
			year = fmt.Sprintf(" %d", g[1]/12)
		}
		if g[0] == g[1] {
			parts[i] = monthNames[g[0]%12] + year
		} else {
			parts[i] = fmt.Sprintf("%s bis %s%s", monthNames[g[0]%12], monthNames[g[1]%12], year)
		}
	}
	return parts
}

// runningFrom returns the first running period that ends in month start or later.
func runningFrom(state BillingState, start int) (clearingPeriod, bool) {
	for _, id := range state.RunningPeriods {
		if p, _ := parseClearingPeriod(id); p.last >= start {
			return p, true
		}
	}
	return clearingPeriod{}, false
}

func runningMessage(p clearingPeriod) string {
	return fmt.Sprintf("Für %s läuft gerade eine Berechnung. Bitte nach deren Ende erneut versuchen.", p.name())
}
