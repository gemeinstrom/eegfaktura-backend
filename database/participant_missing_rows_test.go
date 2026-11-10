package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

// 261029-mm1: the member list LEFT JOINs both addresses, the bank account and the
// contact row. One member without one of these rows must not break the list of the
// whole community, and the single read must still work.
func TestGetParticipantsToleratesMissingChildRows(t *testing.T) {
	cases := []struct {
		name   string
		tenant string
		delete string
	}{
		{"without billing address", "TE029901", `DELETE FROM base.address WHERE participant_id = $1 AND type = 'BILLING'`},
		{"without residence address", "TE029902", `DELETE FROM base.address WHERE participant_id = $1 AND type = 'RESIDENCE'`},
		{"without bank account", "TE029903", `DELETE FROM base.bankaccount WHERE participant_id = $1`},
		{"without contact", "TE029904", `DELETE FROM base.contactdetail WHERE participant_id = $1`},
	}

	db, err := GetTestDB(context.Background(), testDB)
	require.NoError(t, err)
	ctx := context.Background()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{FirstName: "Anna", LastName: "Berger", BusinessRole: "EEG_PRIVATE", Role: "EEG_USER", MeteringPoint: []*model.MeteringPoint{}},
				Contact:            model.ContactInfo{Email: null.StringFrom("anna.berger@example.at")},
				BillingAddress:     model.Address{City: null.StringFrom("Wien")},
				ResidentAddress:    model.Address{City: null.StringFrom("Wien")},
				BankAccount:        model.BankInfo{Owner: null.StringFrom("Anna Berger")},
			}
			require.NoError(t, db.RegisterParticipant(ctx, tc.tenant, "test", p))
			_, err := testDB.DbInstance.ExecContext(ctx, tc.delete, p.Id.String())
			require.NoError(t, err)

			list, err := db.GetParticipants(ctx, tc.tenant)
			require.NoError(t, err)
			require.Len(t, list, 1)
			assert.Equal(t, "Anna", list[0].FirstName)

			single, err := db.GetParticipant(ctx, tc.tenant, p.Id.String())
			require.NoError(t, err)
			assert.Equal(t, p.Id.String(), single.Id.String())
		})
	}
}

// 261029-mm1 follow-up: such a member can be edited; the full update creates the row
// that is missing instead of updating nothing.
func TestUpdateParticipantCreatesMissingChildRows(t *testing.T) {
	cases := []struct {
		name   string
		tenant string
		delete string
		check  func(t *testing.T, p *model.EegParticipant)
	}{
		{"without billing address", "TE029911", `DELETE FROM base.address WHERE participant_id = $1 AND type = 'BILLING'`,
			func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "Graz", p.BillingAddress.City.String)
				assert.Equal(t, model.BILLING, p.BillingAddress.Type)
			}},
		{"without residence address", "TE029912", `DELETE FROM base.address WHERE participant_id = $1 AND type = 'RESIDENCE'`,
			func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "Linz", p.ResidentAddress.City.String)
				assert.Equal(t, model.RESIDENCE, p.ResidentAddress.Type)
			}},
		{"without bank account", "TE029913", `DELETE FROM base.bankaccount WHERE participant_id = $1`,
			func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "Anna B.", p.BankAccount.Owner.String)
			}},
		{"without contact", "TE029914", `DELETE FROM base.contactdetail WHERE participant_id = $1`,
			func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "0664", p.Contact.Phone.String)
			}},
	}

	db, err := GetTestDB(context.Background(), testDB)
	require.NoError(t, err)
	ctx := context.Background()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{FirstName: "Anna", LastName: "Berger", BusinessRole: "EEG_PRIVATE", Role: "EEG_USER", MeteringPoint: []*model.MeteringPoint{}},
				Contact:            model.ContactInfo{Email: null.StringFrom("anna.berger@example.at")},
				BillingAddress:     model.Address{City: null.StringFrom("Wien")},
				ResidentAddress:    model.Address{City: null.StringFrom("Wien")},
				BankAccount:        model.BankInfo{Owner: null.StringFrom("Anna Berger")},
			}
			require.NoError(t, db.RegisterParticipant(ctx, tc.tenant, "test", p))
			_, err := testDB.DbInstance.ExecContext(ctx, tc.delete, p.Id.String())
			require.NoError(t, err)

			edit, err := db.GetParticipant(ctx, tc.tenant, p.Id.String())
			require.NoError(t, err)
			edit.BillingAddress.City = null.StringFrom("Graz")
			edit.ResidentAddress.City = null.StringFrom("Linz")
			edit.BankAccount.Owner = null.StringFrom("Anna B.")
			edit.Contact.Phone = null.StringFrom("0664")
			require.NoError(t, db.UpdateParticipant(ctx, tc.tenant, "test", edit))

			after, err := db.GetParticipant(ctx, tc.tenant, p.Id.String())
			require.NoError(t, err)
			tc.check(t, after)

			// exactly one row per kind afterwards
			var n int
			require.NoError(t, testDB.DbInstance.GetContext(ctx, &n,
				`SELECT (SELECT count(*) FROM base.address WHERE participant_id = $1 AND type = 'BILLING')
				      + (SELECT count(*) FROM base.address WHERE participant_id = $1 AND type = 'RESIDENCE')
				      + (SELECT count(*) FROM base.bankaccount WHERE participant_id = $1)
				      + (SELECT count(*) FROM base.contactdetail WHERE participant_id = $1)`, p.Id.String()))
			assert.Equal(t, 4, n)
		})
	}
}
