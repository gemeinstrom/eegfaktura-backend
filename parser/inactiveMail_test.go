package parser

import (
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
)

func TestFormatDate(t *testing.T) {
	assert.Equal(t, "05.03.2026", FormatDate(civil.DateFor(2026, 3, 5)))
	assert.Equal(t, "31.12.2999", FormatDate(civil.DateFor(2999, 12, 31)))
}

func TestDirectionText(t *testing.T) {
	assert.Equal(t, "Bezug", DirectionText(model.CONSUMPTION))
	assert.Equal(t, "Einspeisung", DirectionText(model.GENERATOR))
	assert.Equal(t, "", DirectionText(""))
}

func TestFormatDateMissing(t *testing.T) {
	// a missing consentEnd arrives as the Unix epoch
	assert.Equal(t, "", FormatDate(civil.DateFor(1970, 1, 1)))
	assert.Equal(t, "", FormatDate(civil.Date{}))
}
