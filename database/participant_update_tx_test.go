package database

import (
	"context"
	"errors"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"gopkg.in/guregu/null.v4"
)

// 261005-ca1: the full update writes the participant, the contact, both addresses and
// the bank account. A failure in the last write must roll back the earlier ones.
func TestUpdateParticipantRollsBackOnPartialFailure(t *testing.T) {
	db, mock := newMockDatabase(t)

	p := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{Id: uuid.NewRandom(), FirstName: "Anna"},
		Contact:            model.ContactInfo{Phone: null.StringFrom("0664")},
		BillingAddress:     model.Address{City: null.StringFrom("Wien")},
		ResidentAddress:    model.Address{City: null.StringFrom("Wien")},
		BankAccount:        model.BankInfo{Owner: null.StringFrom("Anna Berger")},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT "tenant" FROM "base"\."participant"`).
		WillReturnRows(sqlmock.NewRows([]string{"tenant"}).AddRow("RC100100"))
	mock.ExpectExec(`UPDATE "base"\."participant"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "base"\."contactdetail"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "base"\."address"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "base"\."address"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "base"\."bankaccount"`).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := db.UpdateParticipant(context.Background(), "RC100100", "test", p)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
