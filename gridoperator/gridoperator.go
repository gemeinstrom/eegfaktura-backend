// Package gridoperator determines the grid operator (Netzbetreiber) of a metering point.
//
// The grid operator id is the first 8 characters of the metering point number ("AT" + 6 digits).
// Some grid operators took over networks whose metering points still carry the old operator
// number but are only reachable under the new one (e.g. Energienetze Steiermark: AT008200 ->
// AT008000). Those old numbers are translated with the alias list from the configuration
// (key "grid-operator-alias").
package gridoperator

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const ConfigKey = "grid-operator-alias"

var codePattern = regexp.MustCompile(`^AT[0-9]{6}$`)

// ParseAlias validates the configured alias list. Keys and values are upper-cased (viper stores
// keys lower-cased). Entries with an invalid code and chained entries (target is itself an alias)
// are dropped and reported.
func ParseAlias(raw map[string]string) (map[string]string, []error) {
	alias := map[string]string{}
	var errs []error
	for k, v := range raw {
		from, to := strings.ToUpper(strings.TrimSpace(k)), strings.ToUpper(strings.TrimSpace(v))
		if !codePattern.MatchString(from) || !codePattern.MatchString(to) {
			errs = append(errs, fmt.Errorf("invalid grid operator alias %q: %q (expected AT + 6 digits)", k, v))
			continue
		}
		if from == to {
			errs = append(errs, fmt.Errorf("grid operator alias %s points to itself", from))
			continue
		}
		alias[from] = to
	}
	// decide on the complete list first, so a cycle (A -> B, B -> A) drops both entries
	chained := []string{}
	for _, from := range sortedKeys(alias) {
		if to := alias[from]; alias[to] != "" {
			errs = append(errs, fmt.Errorf("grid operator alias %s -> %s is chained (%s is an alias itself)", from, to, to))
			chained = append(chained, from)
		}
	}
	for _, from := range chained {
		delete(alias, from)
	}
	return alias, errs
}

var (
	aliasOnce sync.Once
	aliasMap  map[string]string
)

// Alias returns the alias list from the configuration. It is read once; changes need a restart.
func Alias() map[string]string {
	aliasOnce.Do(func() {
		var errs []error
		aliasMap, errs = ParseAlias(viper.GetStringMapString(ConfigKey))
		for _, err := range errs {
			log.Error(err)
		}
		if len(aliasMap) == 0 {
			log.Warnf("Grid operator alias list %q is empty: retired grid operator numbers are not translated", ConfigKey)
		} else {
			log.Infof("Grid operator alias list: %d entries", len(aliasMap))
		}
	})
	return aliasMap
}

// Result of determining the grid operator of a metering point.
type Result struct {
	Id      string // grid operator id to use for EDA messages
	Prefix  string // operator number taken from the metering point number
	Aliased bool   // Prefix was translated via the alias list
}

// FromMeteringPoint derives the grid operator from the metering point number. ok is false when
// the number does not start with an operator number (AT + 6 digits), e.g. too short or a typo;
// the caller then keeps the value it has.
func FromMeteringPoint(meteringPoint string, alias map[string]string) (Result, bool) {
	mp := strings.ToUpper(strings.TrimSpace(meteringPoint))
	if len(mp) < 8 || !codePattern.MatchString(mp[:8]) {
		return Result{}, false
	}
	prefix := mp[:8]
	if to, found := alias[prefix]; found {
		return Result{Id: to, Prefix: prefix, Aliased: true}, true
	}
	return Result{Id: prefix, Prefix: prefix}, true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
