package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

// platform#229: the six community types, nothing else (also no empty value).
func TestValidateArea(t *testing.T) {
	assert.ElementsMatch(t, []AreaType{"LOCAL", "REGIONAL", "BEG", "GEA", "P2P", "EVA"}, AreaTypes())
	for _, area := range AreaTypes() {
		assert.NoError(t, ValidateArea(area), area)
	}
	for _, area := range []AreaType{"", "6", "local", "PP", "SC", "RC_L"} {
		assert.Error(t, ValidateArea(area), area)
	}
}

// MQTT register-eeg: the schema enum (registration.schema.json) knows exactly the types of AreaTypes.
func TestRegisterEegRequestArea(t *testing.T) {
	decode := func(area string) (RegisterEegRequest, error) {
		var r RegisterEegRequest
		err := json.Unmarshal([]byte(`{"tenant":"TE000044","rcNumber":"TE000044","communityId":"AT1","name":"n",
			"description":"d","iban":"","owner":"","sepa":false,"legal":"verein","settelmentInterval":"MONTHLY",
			"gridId":"AT003000","gridName":"g","area":"`+area+`","allocation":"DYNAMIC","eegOwner":"o","street":"s",
			"streetNumber":"1","city":"c","zip":"4020","email":"e@example.org","online":false}`), &r)
		return r, err
	}
	for _, area := range AreaTypes() {
		r, err := decode(string(area))
		require.NoError(t, err, area)
		assert.Equal(t, string(area), string(r.Area))
	}
	_, err := decode("PP")
	assert.Error(t, err)
}

// platform#229 (I): BEG, P2P and EVA may span several grid operators; the EDA ECType of each type; Nahebereich 1–7.
func TestAreaRules(t *testing.T) {
	multi := map[AreaType]bool{BEG: true, P2P: true, EVA: true}
	eda := map[AreaType]string{GEA: "GC", LOCAL: "RC_L", REGIONAL: "RC_R", BEG: "CC", P2P: "PP", EVA: "SC"}
	for _, area := range AreaTypes() {
		assert.Equal(t, multi[area], HasMultipleGridOperators(area), area)
		code, ok := EdaEcType(area)
		assert.True(t, ok, area)
		assert.Equal(t, eda[area], code, area)
	}
	zones := map[AreaType]null.Int{GEA: null.IntFrom(1), LOCAL: null.IntFrom(3), REGIONAL: null.IntFrom(4), BEG: null.IntFrom(7)}
	for _, area := range AreaTypes() {
		assert.Equal(t, zones[area], DefaultEcZoneLevel(area), area)
	}
	_, ok := EdaEcType("6")
	assert.False(t, ok)
	for code, ok := range map[string]bool{"PV": true, "0123456789": true, "W.IND_1-A": true, "": false,
		"ABCDEFGHIJK": false, "P V": false, "E;1": false, "Ä": false} {
		assert.Equal(t, ok, ValidCode(code), code)
	}
	for level := -1; level <= 8; level++ {
		assert.Equal(t, level >= 1 && level <= 7, ValidEcZoneLevel(level), level)
	}
}

// The values of the grid operator's metering point list are read only: the generic updates drop or refuse them.
func TestZoneLevelAndCodesReadOnly(t *testing.T) {
	assert.Contains(t, SkipUpdateJSONKeys(Eeg{}), "ecZoneLevel")
	for _, key := range []string{"techCode", "tech_code", "fuelCode", "fuel_code"} {
		_, ok := AllowedUpdateColumn(MeteringPoint{}, key)
		assert.False(t, ok, key)
	}
}
