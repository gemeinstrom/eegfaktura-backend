package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

// expectGridOperatorLookup registers the two queries applyGridOperator runs before a metering
// point is written (base.gridoperators and the EEG's own grid operator).
func expectGridOperatorLookup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT "id", "name" FROM "base"."gridoperators"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow("AT003000", "Netz Oberösterreich GmbH").
			AddRow("AT008000", "Energienetze Steiermark GmbH"))
	mock.ExpectQuery(`FROM "base"."eeg"`).
		WillReturnRows(sqlmock.NewRows([]string{"gridoperator_code", "gridoperator_name"}).AddRow("AT008000", "Energienetze Steiermark"))
}

func Test_applyGridOperator(t *testing.T) {
	defer func(orig func() map[string]string) { gridOperatorAlias = orig }(gridOperatorAlias)
	gridOperatorAlias = func() map[string]string { return map[string]string{"AT008200": "AT008000"} }

	mDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mDB.Close()
	db := sqlx.NewDb(mDB, "mock")

	expectGridOperatorLookup(mock)

	points := []*model.MeteringPoint{
		// the client value is replaced
		{MeteringPoint: "AT0082000816000000000000004269401", GridOperatorId: null.StringFrom("AT008200"), GridOperatorName: null.StringFrom("alt")},
		{MeteringPoint: "AT0030000000000000000000000123456"},
		// unknown in base.gridoperators: name stays empty
		{MeteringPoint: "AT0082100000000000000000000000001"},
		// not derivable: the client value is dropped (stays empty on insert, omitted on update)
		{MeteringPoint: "AT00", GridOperatorId: null.StringFrom("AT009999"), GridOperatorName: null.StringFrom("manuell")},
	}
	require.NoError(t, applyGridOperator(context.Background(), db, "TE000009", points))

	assert.Equal(t, "AT008000", points[0].GridOperatorId.String)
	assert.Equal(t, "Energienetze Steiermark GmbH", points[0].GridOperatorName.String)
	assert.Equal(t, "AT003000", points[1].GridOperatorId.String)
	assert.Equal(t, "Netz Oberösterreich GmbH", points[1].GridOperatorName.String)
	assert.Equal(t, "AT008210", points[2].GridOperatorId.String)
	assert.False(t, points[2].GridOperatorName.Valid)
	assert.False(t, points[3].GridOperatorId.Valid)
	assert.False(t, points[3].GridOperatorName.Valid)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func Test_gridOperatorName(t *testing.T) {
	names := map[string]string{"AT003000": "Netz OÖ"}
	assert.Equal(t, "Netz OÖ", gridOperatorName("AT003000", names, "AT008000", "Steiermark"))
	assert.Equal(t, "Steiermark", gridOperatorName("AT008000", names, "AT008000", "Steiermark"))
	assert.Equal(t, "", gridOperatorName("AT004000", names, "AT008000", "Steiermark"))
	// the EEG's code may be stored in lower case
	assert.Equal(t, "Steiermark", gridOperatorName("AT008000", names, "at008000", "Steiermark"))
}
