package database

import (
	"context"
	"regexp"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
)

var selectParticipantTenant = regexp.QuoteMeta(`SELECT "tenant" FROM "base"."participant"`)

// UpdateParticipantValues (admin gRPC) prueft den Mandanten des Teilnehmers.
func TestUpdateParticipantValuesRejectsForeignTenant(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	mock.ExpectQuery(selectParticipantTenant).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("OTHER"))

	err := mDB.UpdateParticipantValues(context.Background(), "p-1", "TE000001", map[string]string{"firstname": "x"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "kein Schreibzugriff erwartet")
}

// UpdateParticipantValues weist unbekannte Schluessel ab, bevor etwas geschrieben wird.
func TestUpdateParticipantValuesRejectsUnknownKey(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	mock.ExpectQuery(selectParticipantTenant).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("TE000001"))

	err := mDB.UpdateParticipantValues(context.Background(), "p-1", "TE000001", map[string]string{
		"firstname":               "x",
		`firstname"=(SELECT 1)--`: "y",
	})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "kein Schreibzugriff erwartet")
}

// AddTariff mit der ID eines fremden Tarifs wird abgelehnt und schreibt nichts.
func TestAddTariffRejectsForeignTariffId(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "tenant" FROM "base"."tariff"`)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("OTHER"))
	mock.ExpectRollback()

	tariff := &model.Tariff{Id: uuid.NewUUID(), Version: 1}
	err := mDB.AddTariff(context.Background(), "TE000001", "user", tariff)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "kein INSERT/UPDATE erwartet")
}

// Eine neue Version eines eigenen Tarifs geht durch; die Deaktivierung der
// Vorversion ist auf den Mandanten beschraenkt.
func TestAddTariffOwnTariffNewVersion(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "tenant" FROM "base"."tariff"`)).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("te000001"))
	mock.ExpectExec(`INSERT INTO "base"."tariff"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE "base"."tariff" .*"tenant" = 'TE000001'`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tariff := &model.Tariff{Id: uuid.NewUUID(), Version: 1}
	err := mDB.AddTariff(context.Background(), "TE000001", "user", tariff)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
