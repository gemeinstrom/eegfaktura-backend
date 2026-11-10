package database

import (
	"context"
	"errors"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"gopkg.in/guregu/null.v4"
)

// The transaction tests (BE-CQ-7) run on sqlmock: a failed COMMIT must reach the caller, and an
// error inside the transaction must roll it back, never commit it.

var errCommit = errors.New("commit failed")

func TestInTx_commitErrorReachesCaller(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errCommit)

	err := inTx(context.Background(), dbx, func(tx *sqlx.Tx) error { return nil })

	assert.ErrorIs(t, err, errCommit)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInTx_errorRollsBackAndKeepsTheError(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectRollback()
	want := model.ErrUpdateMeter(errors.New("boom"))

	err := inTx(context.Background(), dbx, func(tx *sqlx.Tx) error { return want })

	assert.Same(t, want, err, "the error of fn is returned unchanged, so it keeps its code")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTariff_commitErrorReachesCaller(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "base"."tariff" (.+)`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errCommit)

	tariff := model.Tariff{Name: "Sepp", BillingPeriod: "monthly", FreeKWh: null.IntFrom(100), CentPerKWh: 12}
	err := AddTariff(context.Background(), dbx, "TE000001", "sepp", &tariff)

	assert.ErrorIs(t, err, errCommit)
	assert.NoError(t, mock.ExpectationsWereMet())
}

var activeMeter = []model.Meter{{MeteringPoint: "AT0030000000000000000000000000001", Direction: "CONSUMPTION", From: 1}}

func TestUpdateActiveMeteringPoints_failedUpdateRollsBack(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE "base"."meteringpoint"`).WillReturnError(errors.New("update failed"))
	mock.ExpectRollback()

	err := UpdateActiveMeteringPoints(context.Background(), dbx, "TE000001", activeMeter)

	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "the transaction must be rolled back, not committed")
}

func TestUpdateActiveMeteringPoints_commitErrorReachesCaller(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE "base"."meteringpoint"`).WillReturnRows(sqlmock.NewRows([]string{"participant_id"}))
	mock.ExpectCommit().WillReturnError(errCommit)

	err := UpdateActiveMeteringPoints(context.Background(), dbx, "TE000001", activeMeter)

	assert.ErrorIs(t, err, errCommit)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// AddTariff and UpdateActiveMeteringPoints begin without the cancel of the caller's context, as on master: a client
// that went away or the timeout of SyncMeteringPoints does not roll the save back.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestAddTariff_cancelledContextStillCommits(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "base"."tariff" (.+)`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tariff := model.Tariff{Name: "Sepp", BillingPeriod: "monthly", FreeKWh: null.IntFrom(100), CentPerKWh: 12}
	assert.NoError(t, AddTariff(cancelledCtx(), dbx, "TE000001", "sepp", &tariff))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateActiveMeteringPoints_cancelledContextStillCommits(t *testing.T) {
	dbx, mock := newTxMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE "base"."meteringpoint"`).WillReturnRows(sqlmock.NewRows([]string{"participant_id"}))
	mock.ExpectCommit()

	assert.NoError(t, UpdateActiveMeteringPoints(cancelledCtx(), dbx, "TE000001", activeMeter))
	assert.NoError(t, mock.ExpectationsWereMet())
}
