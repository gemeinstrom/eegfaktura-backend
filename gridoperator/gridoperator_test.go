package gridoperator

import (
	"testing"

	"at.ourproject/vfeeg-backend/config/configtest"
	"github.com/stretchr/testify/assert"
)

func TestParseAlias(t *testing.T) {
	alias, errs := ParseAlias(map[string]string{
		"at008200": "at008000", // viper lower-cases keys
		"AT008230": "AT008000",
		"AT00XX00": "AT008000", // invalid source
		"AT008320": "8000",     // invalid target
		"AT008380": "AT008380", // self
		"AT003470": "AT008200", // chained: target is an alias itself
		"AT001111": "AT002222", // cycle: both entries are dropped
		"AT002222": "AT001111",
	})
	assert.Equal(t, map[string]string{"AT008200": "AT008000", "AT008230": "AT008000"}, alias)
	assert.Len(t, errs, 6)
}

func TestFromMeteringPoint(t *testing.T) {
	alias := map[string]string{"AT008200": "AT008000"}
	tests := []struct {
		name          string
		meteringPoint string
		want          Result
		ok            bool
	}{
		{"alias", "AT0082000816000000000000004269401", Result{Id: "AT008000", Prefix: "AT008200", Aliased: true}, true},
		{"own operator AT008210 is no alias", "AT0082100000000000000000000000001", Result{Id: "AT008210", Prefix: "AT008210"}, true},
		{"plain prefix", "AT0030000000000000000000000123456", Result{Id: "AT003000", Prefix: "AT003000"}, true},
		{"lower case and blanks", " at0082000816000000000000004269401 ", Result{Id: "AT008000", Prefix: "AT008200", Aliased: true}, true},
		{"too short", "AT00820", Result{}, false},
		{"typo in operator number", "AT00 300000000000000000000123456", Result{}, false},
		{"empty", "", Result{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromMeteringPoint(tt.meteringPoint, alias)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAliasFromConfig(t *testing.T) {
	configtest.Set(t, ConfigKey, map[string]interface{}{"at008200": "AT008000", "AT008230": "at008000"})
	assert.Equal(t, map[string]string{"AT008200": "AT008000", "AT008230": "AT008000"}, Alias())
}
