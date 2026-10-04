package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
	"strings"
	"testing"
	"time"
)

func Test_createMeteringEntries(t *testing.T) {
	type args struct {
		participantId string
		points        []*model.MeteringPoint
		state         *model.ProcessStatusType
	}

	getStatePtr := func(s model.ProcessStatusType) *model.ProcessStatusType {
		return &s
	}

	tests := []struct {
		name     string
		args     args
		validate func(t *testing.T, m []*meteringEntryType, mp []*partitionFactorRecord)
	}{
		{
			name: "create without state",
			args: args{
				participantId: "12345",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000001",
						Direction:     model.CONSUMPTION,
						State:         nil,
						PartFact:      10,
					},
				},
				state: nil,
			},
			validate: func(t *testing.T, m []*meteringEntryType, mp []*partitionFactorRecord) {
				assert.Equal(t, 1, len(m))
				assert.Nil(t, m[0].ActiveSince.Ptr())
				assert.Nil(t, m[0].InactiveSince.Ptr())
				assert.Equal(t, model.S_INIT, m[0].Status)
				assert.Equal(t, model.NEW, m[0].ProcessState)

				assert.Equal(t, 10, mp[0].PartFact)
				assert.Equal(t, m[0].Participant_id, mp[0].Participant_id)
			},
		},
		{
			name: "create NEW",
			args: args{
				participantId: "12345",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000001",
						Direction:     model.CONSUMPTION,
						PartFact:      15,
					},
				},
				state: getStatePtr(model.NEW),
			},
			validate: func(t *testing.T, m []*meteringEntryType, mp []*partitionFactorRecord) {
				assert.Equal(t, 1, len(m))
				assert.Nil(t, m[0].ActiveSince.Ptr())
				assert.Nil(t, m[0].InactiveSince.Ptr())
				assert.Equal(t, model.S_INIT, m[0].Status)
				assert.Equal(t, model.NEW, m[0].ProcessState)

				assert.Equal(t, 15, mp[0].PartFact)
				assert.Equal(t, m[0].Participant_id, mp[0].Participant_id)
			},
		},
		{
			name: "create ACTIVE",
			args: args{
				participantId: "12345",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000001",
						Direction:     model.CONSUMPTION,
						PartFact:      10,
					},
				},
				state: getStatePtr(model.ACTIVE),
			},
			validate: func(t *testing.T, m []*meteringEntryType, mp []*partitionFactorRecord) {
				assert.Equal(t, 1, len(m))
				assert.Equal(t, civil.Today(), m[0].ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m[0].InactiveSince.Date)
				assert.Equal(t, model.S_ACTIVE, m[0].Status)
				assert.Equal(t, model.ACTIVE, m[0].ProcessState)

				assert.Equal(t, 10, mp[0].PartFact)
				assert.Equal(t, m[0].Participant_id, mp[0].Participant_id)
			},
		},
		{
			name: "create ACTIVE - with activation Date",
			args: args{
				participantId: "12345",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000001",
						Direction:     model.CONSUMPTION,
						State: &model.MeterState{
							ActiveSince: civil.NullDate{Date: civil.DateFor(2024, 5, 10)},
						},
						PartFact: 10,
					},
				},
				state: getStatePtr(model.ACTIVE),
			},
			validate: func(t *testing.T, m []*meteringEntryType, mp []*partitionFactorRecord) {
				assert.Equal(t, 1, len(m))
				assert.Equal(t, civil.Today(), m[0].ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m[0].InactiveSince.Date)
				assert.Equal(t, model.S_ACTIVE, m[0].Status)
				assert.Equal(t, model.ACTIVE, m[0].ProcessState)

				assert.Equal(t, 10, mp[0].PartFact)
				assert.Equal(t, m[0].Participant_id, mp[0].Participant_id)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, mp := createMeteringEntries("TE000001", "test", tt.args.participantId, tt.args.points, tt.args.state)
			tt.validate(t, m, mp)
		})
	}
}

func Test_ImportMeteringPoints(t *testing.T) {
	type args struct {
		tenant        string
		participantId string
		points        []*model.MeteringPoint
	}

	log.SetLevel(log.DebugLevel)

	tests := []struct {
		name     string
		args     args
		validate func(t *testing.T, pUnderTest *model.EegParticipant)
	}{
		{
			name: "Import New Points",
			args: args{
				tenant:        "TE000006",
				participantId: "ea1142dc-03da-11ee-b82b-5a985b4b0306",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000001",
						Direction:     model.CONSUMPTION,
						State: &model.MeterState{
							ActiveSince: civil.NullDate{Date: civil.DateFor(2024, 5, 10), Valid: true},
						},
						PartFact: 10,
					},
				},
			},
			validate: func(t *testing.T, pUnderTest *model.EegParticipant) {
				assert.Equal(t, "ea1142dc-03da-11ee-b82b-5a985b4b0306", pUnderTest.Id.String())
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.ACTIVE, pUnderTest.Status)
				assert.Equal(t, model.NEW, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.S_INIT, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].RegisteredSince)
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.ActiveSince.Ptr())
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.InactiveSince.Ptr())
				assert.Equal(t, 10, pUnderTest.MeteringPoint[0].PartFact)
			},
		},
		{
			name: "Import New Points - missing partfact",
			args: args{
				tenant:        "TE000006",
				participantId: "ea1142dc-03da-11ee-b82b-5a985b4b0316",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000002",
						Direction:     model.CONSUMPTION,
					},
				},
			},
			validate: func(t *testing.T, pUnderTest *model.EegParticipant) {
				assert.Equal(t, "ea1142dc-03da-11ee-b82b-5a985b4b0316", pUnderTest.Id.String())
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.ACTIVE, pUnderTest.Status)
				assert.Equal(t, model.NEW, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.S_INIT, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].RegisteredSince)
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.ActiveSince.Ptr())
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.InactiveSince.Ptr())
				assert.Equal(t, 0, pUnderTest.MeteringPoint[0].PartFact)
			},
		},
		{
			name: "Import Activ Points",
			args: args{
				tenant:        "TE000006",
				participantId: "ea1142dc-03da-11ee-b82b-5a985b4b0326",
				points: []*model.MeteringPoint{
					{
						MeteringPoint: "AT0030000000000000000001000000003",
						Direction:     model.CONSUMPTION,
						ProcessState:  model.ACTIVE,
						State: &model.MeterState{
							ActiveSince: civil.NullDate{Date: civil.DateFor(2024, 5, 10), Valid: true},
						},
					},
				},
			},
			validate: func(t *testing.T, pUnderTest *model.EegParticipant) {
				assert.Equal(t, "ea1142dc-03da-11ee-b82b-5a985b4b0326", pUnderTest.Id.String())
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.ACTIVE, pUnderTest.Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].RegisteredSince)
				assert.Equal(t, civil.DateFor(2024, 5, 10), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, 0, pUnderTest.MeteringPoint[0].PartFact)
			},
		},
		{
			name: "Import Activ Points",
			args: args{
				tenant:        "TE000006",
				participantId: "ea1142dc-03da-11ee-b82b-5a985b4b0336",
				points: []*model.MeteringPoint{
					{
						MeteringPoint:   "AT0030000000000000000001000000004",
						Direction:       model.CONSUMPTION,
						ProcessState:    model.ACTIVE,
						RegisteredSince: civil.DateFor(2024, 5, 10),
					},
				},
			},
			validate: func(t *testing.T, pUnderTest *model.EegParticipant) {
				assert.Equal(t, "ea1142dc-03da-11ee-b82b-5a985b4b0336", pUnderTest.Id.String())
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.ACTIVE, pUnderTest.Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.DateFor(2024, 5, 10), pUnderTest.MeteringPoint[0].RegisteredSince)
				assert.Equal(t, pUnderTest.MeteringPoint[0].RegisteredSince, pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, 0, pUnderTest.MeteringPoint[0].PartFact)
			},
		},
	}
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err = db.ImportMeteringPoints(context.Background(), tt.args.tenant, "test", tt.args.participantId, tt.args.points)
			require.NoError(t, err)

			pUnderTest, err := db.QueryParticipant(context.Background(), tt.args.tenant, tt.args.participantId)
			require.NoError(t, err)

			tt.validate(t, pUnderTest)
		})
	}
}

func Test_RegisterMeteringPoint(t *testing.T) {
	type args struct {
		tenant        string
		participantId string
		point         *model.MeteringPoint
	}

	log.SetLevel(log.DebugLevel)

	tests := []struct {
		name string
		args args
	}{
		{
			name: "insert",
			args: args{tenant: "DR", participantId: "12", point: &model.MeteringPoint{
				MeteringPoint:   "",
				Transformer:     null.String{},
				Direction:       "",
				Status:          "",
				TariffId:        null.String{},
				EquipmentNumber: null.String{},
				EquipmentName:   null.String{},
				InverterId:      null.String{},
				Street:          null.String{},
				StreetNumber:    null.String{},
				City:            null.String{},
				Zip:             null.String{},
				RegisteredSince: civil.Date{},
				ModifiedAt:      civil.DateTime{},
				ModifiedBy:      null.String{},
				State:           nil,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mDB, mock, err := InitMockDatabase()
			if err != nil {
				t.Fatalf("An error occurred while creating mock: %s", err)
			}

			mock.ExpectBegin()
			mock.ExpectExec("INSERT (.+) \"base\".\"meteringpoint\"").WillReturnResult(sqlmock.NewResult(1, 1))
			//mock.Mock.ExpectExec("INSERT INTO \"base\".\"participant_meter_state\" (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("INSERT INTO \"base\".\"metering_partition_factor\" (.+)").WillReturnResult(sqlmock.NewResult(1, 1))

			assert.NoError(t, mDB.RegisterMeteringPoint(context.Background(), tt.args.tenant, "userId", tt.args.participantId, tt.args.point))
		})
	}
}

func Test_MeteringPointRevoke(t *testing.T) {
	consentEnd := civil.Today()
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	err = db.MeteringPointRevoke(context.Background(), "TE000015", "AT0030000000000000000000000153013", consentEnd)
	assert.NoError(t, err)

	meters, err := db.FindInactiveMeteringById(context.Background(), "TE000015", "AT0030000000000000000000000153013")
	assert.NoError(t, err)
	require.NotNil(t, meters)
	require.Equal(t, 1, len(meters))

	meter := meters[0]

	assert.Equal(t, consentEnd, meter.State.InactiveSince.Date)
	assert.Equal(t, model.S_INACTIVE, meter.Status)

	meters, err = db.FindActiveMeteringByIds(context.Background(), "TE000015", []string{"AT0030000000000000000000000153013"})
	assert.NoError(t, err)
	require.Nil(t, meters)

	meters, err = db.FindInactiveMeteringById(context.Background(), "TE000015", "AT0030000000000000000000000153013")
	assert.NoError(t, err)
	require.NotNil(t, meters)
	require.Equal(t, 1, len(meters))
	meter = meters[0]

	require.Equal(t, null.StringFrom("123456789015"), meter.ConsentId)
}

func Test_GetMeteringsByIds(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	meters, err := db.FindActiveMeteringByIds(context.Background(), "TE000015", []string{"AT0030000000000000000000000153012"})
	assert.NoError(t, err)
	require.NotNil(t, meters)
	require.Equal(t, 1, len(meters))
	meter := meters[0]

	require.Equal(t, null.StringFrom("123456789015"), meter.ConsentId)

	meters, err = db.FindActiveMeteringByIds(context.Background(), "TE000015", []string{"AT0030000000000000000000000153014"})
	assert.NoError(t, err)
	require.NotNil(t, meters)
	require.Equal(t, 1, len(meters))
	meter = meters[0]

	require.Equal(t, null.StringFrom("12345678901415"), meter.ConsentId)

	meters, err = db.FindActiveMeteringByIds(context.Background(), "TE000017", []string{"AT0030000000000000000000000153013"})
	assert.NoError(t, err)
	require.NotNil(t, meters)
	require.Equal(t, 1, len(meters))
	meter = meters[0]

	require.Equal(t, null.StringFrom("123456789017"), meter.ConsentId)
}

func Test_AddMultipleMeteringPoints(t *testing.T) {

	meter := &model.MeteringPoint{
		MeteringPoint: "AT0030000000000000000000030000100",
		Direction:     model.GENERATOR,
		Street:        null.StringFrom("Solargasse"),
		StreetNumber:  null.StringFrom("1"),
		City:          null.StringFrom("Solarcity"),
		Zip:           null.StringFrom("1111"),
	}

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	// Register new Meteringpoint
	err = db.RegisterMeteringPoint(context.Background(), "TE000001", "test", "ea9942da-03da-11ee-b82b-5a985b4b033a", meter)
	require.NoError(t, err)

	newParticipant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			BusinessRole:  "EEG_PRIVATE",
			Role:          "EEG_USER",
			FirstName:     "Michael",
			LastName:      "Obermüller",
			MeteringPoint: []*model.MeteringPoint{meter},
		},
		BillingAddress: model.Address{
			Type:         "BILLING",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
		ResidentAddress: model.Address{
			Type:         "RESIDENCE",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
	}

	// Try to register new Participant with already registerd meteringpoint -> should fail
	err = db.RegisterParticipant(context.Background(), "TE000001", "test", newParticipant)
	require.Error(t, err)

	// Send Revoke message to inactive meteringpoint
	consentEnd := civil.Today().Add(24 * time.Hour)
	err = db.MeteringPointRevoke(context.Background(), "TE000001", meter.MeteringPoint, consentEnd)
	assert.NoError(t, err)

	err = db.RegisterParticipant(context.Background(), "TE000001", "test", newParticipant)
	require.NoError(t, err)

	consentEnd = civil.Today().Add(24 * time.Hour)
	err = db.MeteringPointRevoke(context.Background(), "TE000001", meter.MeteringPoint, consentEnd)
	assert.NoError(t, err)

	// Register new participant with revoked meteringpoint
	newParticipant.FirstName = "Paula"
	err = db.RegisterParticipant(context.Background(), "TE000001", "test", newParticipant)
	require.NoError(t, err)

	var names []string
	stmt, _, err := pgDialect.From("base.meteringpoint").Select("metering_point_id").
		Where(goqu.C("metering_point_id").Eq(meter.MeteringPoint)).ToSQL()
	fmt.Printf("STMT: %v\n", stmt)
	err = db.Select(context.Background(), &names, stmt)
	require.NoError(t, err)
	require.Equal(t, 3, len(names))

	findP := func(p []*model.EegParticipant, firstname, lastname string) *model.EegParticipant {
		for i := range p {
			if p[i].FirstName == firstname && p[i].LastName == lastname {
				return p[i]
			}
		}
		return nil
	}

	p, err := db.GetParticipants(context.Background(), "TE000001")
	require.NoError(t, err)

	assert.Equal(t, 3, len(p))

	p1 := findP(p, "Peter", "Obermüller")
	assert.NotNil(t, p1)
	assert.Equal(t, 1, len(p1.MeteringPoint))
	assert.Equal(t, model.S_INACTIVE, p1.MeteringPoint[0].Status)

	p2 := findP(p, "Paula", "Obermüller")
	assert.NotNil(t, p2)
	assert.Equal(t, 1, len(p2.MeteringPoint))
	assert.Equal(t, model.NEW, p2.MeteringPoint[0].ProcessState)
	assert.Equal(t, model.S_INIT, p2.MeteringPoint[0].Status)
}

func Test_MeteringPointIntegration(t *testing.T) {
	meter := &model.MeteringPoint{
		MeteringPoint: "AT0030000000000000000000030001411",
		Direction:     model.GENERATOR,
		Street:        null.StringFrom("Solargasse"),
		StreetNumber:  null.StringFrom("1"),
		City:          null.StringFrom("Solarcity"),
		Zip:           null.StringFrom("1111"),
	}

	newParticipant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			BusinessRole:  "EEG_PRIVATE",
			Role:          "EEG_USER",
			FirstName:     "Registration",
			LastName:      "TestUser1",
			MeteringPoint: []*model.MeteringPoint{meter},
		},
		BillingAddress: model.Address{
			Type:         "BILLING",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
		ResidentAddress: model.Address{
			Type:         "RESIDENCE",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
	}

	secondParticipant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			BusinessRole:  "EEG_PRIVATE",
			Role:          "EEG_USER",
			FirstName:     "Registration",
			LastName:      "TestUser2",
			MeteringPoint: []*model.MeteringPoint{meter},
		},
		BillingAddress: model.Address{
			Type:         "BILLING",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
		ResidentAddress: model.Address{
			Type:         "RESIDENCE",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
	}

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	findParticipantUnderTest := func(pp []*model.EegParticipant, l string) *model.EegParticipant {
		for _, p := range pp {
			if p.FirstName == "Registration" && p.LastName == l {
				return p
			}
		}
		return nil
	}

	getParticipantUnderTest := func(t *testing.T, l string) *model.EegParticipant {
		db, err := GetDB(context.Background())
		require.NoError(t, err)

		p, err := db.GetParticipants(context.Background(), "TE000004")
		require.NoError(t, err)

		pUnderTest := findParticipantUnderTest(p, l)
		require.NotNil(t, pUnderTest)
		return pUnderTest
	}

	tenant := "TE000004"

	tests := []struct {
		name  string
		test  func(t *testing.T) error
		valid func(t *testing.T)
	}{
		{
			name: "Insert Participant",
			test: func(t *testing.T) error {
				err = db.RegisterParticipant(context.Background(), "TE000004", "test", newParticipant)
				require.NoError(t, err)
				return nil
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser1")

				assert.Equal(t, model.PENDING, pUnderTest.Status)
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_INIT, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.NEW, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.ActiveSince.Ptr())
				assert.Nil(t, pUnderTest.MeteringPoint[0].State.InactiveSince.Ptr())
			},
		},
		{
			name: "Confirm Participant",
			test: func(t *testing.T) error {
				p, err := db.GetParticipants(context.Background(), "TE000004")
				require.NoError(t, err)

				pUnderTest := findParticipantUnderTest(p, "TestUser1")
				require.NotNil(t, pUnderTest)

				return db.ConfirmParticipant(context.Background(), "TE000004", "test", pUnderTest.Id.String())
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser1")

				assert.Equal(t, model.ACTIVE, pUnderTest.Status)
				//assert.Equal(t, civil.Today(), pUnderTest.ParticipantSince.Date, pUnderTest.ParticipantSince.Date.String())
			},
		},
		{
			name: "Add same Metering point",
			test: func(t *testing.T) error {
				p, err := db.GetParticipants(context.Background(), tenant)
				require.NoError(t, err)

				pUnderTest := findParticipantUnderTest(p, "TestUser1")
				require.NotNil(t, pUnderTest)

				err = db.RegisterMeteringPoint(context.Background(), tenant, "test", pUnderTest.Id.String(), meter)
				require.Error(t, err)

				return nil
			},
			valid: func(t *testing.T) {},
		},
		{
			name: "Activate Metering point first participant",
			test: func(t *testing.T) error {
				now := civil.Today()
				consentId := "1234567890"
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.ACTIVE, nil, []string{meter.MeteringPoint}, &now, &consentId)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser1")

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 23, 59, 59, 0, time.UTC)), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
			},
		},
		{
			name: "Try new participant with same metering point",
			test: func(t *testing.T) error {
				err = db.RegisterParticipant(context.Background(), tenant, "test", secondParticipant)
				require.Error(t, err)
				return nil
			},
			valid: func(t *testing.T) {},
		},
		{
			name: "Revoke Metering Point",
			test: func(t *testing.T) error {
				return db.MeteringPointRevoke(context.Background(), 
					tenant,
					meter.MeteringPoint,
					civil.Today(),
				)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser1")

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.F_ASSIGNED, pUnderTest.MeteringPoint[0].State.Flag)
				assert.Equal(t, model.S_INACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)

			},
		},
		{
			name: "Add new Participant with same Meter",
			test: func(t *testing.T) error {
				err = db.RegisterParticipant(context.Background(), tenant, "test", secondParticipant)
				require.NoError(t, err)

				now := civil.Today().Add(2 * 24 * time.Hour)
				consentId := "0987654321"
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.ACTIVE, nil, []string{meter.MeteringPoint}, &now, &consentId)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, secondParticipant.LastName)

				assert.Equal(t, model.PENDING, pUnderTest.Status)
				assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.F_ASSIGNED, pUnderTest.MeteringPoint[0].State.Flag)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
				assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 23, 59, 59, 0, time.UTC)), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)

				pUnderTest1 := getParticipantUnderTest(t, newParticipant.LastName)
				assert.Equal(t, model.S_INACTIVE, pUnderTest1.MeteringPoint[0].Status)
				assert.Equal(t, model.F_MOVED, pUnderTest1.MeteringPoint[0].State.Flag)

			},
		},
		{
			name: "Revoke Metering Point Second Participant",
			test: func(t *testing.T) error {
				return db.MeteringPointRevoke(context.Background(), "TE000004", meter.MeteringPoint, civil.Today().Add(6*24*time.Hour))
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser2")

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_INACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, civil.Today().Add(6*24*time.Hour), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)

			},
		},
		{
			name: "Reactive Metering Point (PENDING)",
			test: func(t *testing.T) error {
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.PENDING, nil, []string{meter.MeteringPoint}, nil, nil)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, "TestUser2")

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_INACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.PENDING, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, civil.Today().Add(6*24*time.Hour), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
			},
		},
		{
			name: "Reactive Metering Point (APPROVED)",
			test: func(t *testing.T) error {
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.APPROVED, nil, []string{meter.MeteringPoint}, nil, nil)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, secondParticipant.LastName)

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_INACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.APPROVED, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, civil.Today().Add(6*24*time.Hour), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
			},
		},
		{
			name: "Reactive Metering Point (ACTIVE)",
			test: func(t *testing.T) error {
				now := civil.Today().Add(8 * 24 * time.Hour)
				consentId := "1234567890"
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.ACTIVE, nil, []string{meter.MeteringPoint}, &now, &consentId)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, secondParticipant.LastName)

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 23, 59, 59, 0, time.UTC)), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
			},
		},
		{
			name: "Send APPROVED after ACTIVE ",
			test: func(t *testing.T) error {
				now := civil.Today().Add(8 * 24 * time.Hour)
				consentId := "1234567891"
				return db.MeteringPointsSetStatus(context.Background(), tenant, model.APPROVED, nil, []string{meter.MeteringPoint}, &now, &consentId)
			},
			valid: func(t *testing.T) {
				pUnderTest := getParticipantUnderTest(t, secondParticipant.LastName)

				require.Equal(t, 1, len(pUnderTest.MeteringPoint))
				assert.Equal(t, model.S_ACTIVE, pUnderTest.MeteringPoint[0].Status)
				assert.Equal(t, model.ACTIVE, pUnderTest.MeteringPoint[0].ProcessState)
				assert.Equal(t, "1234567891", pUnderTest.MeteringPoint[0].ConsentId.String)
				assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 23, 59, 59, 0, time.UTC)), pUnderTest.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, civil.Today().Add(2*24*time.Hour), pUnderTest.MeteringPoint[0].State.ActiveSince.Date)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.test(t)
			require.NoError(t, err)
			test.valid(t)
		})
	}
}

func Test_ActivateRevokedMeteringPoint(t *testing.T) {
	meter := &model.MeteringPoint{
		MeteringPoint: "AT0030000000000000000000030000400",
		Direction:     model.GENERATOR,
		Street:        null.StringFrom("Solargasse"),
		StreetNumber:  null.StringFrom("1"),
		City:          null.StringFrom("Solarcity"),
		Zip:           null.StringFrom("1111"),
	}

	newParticipant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			BusinessRole:  "EEG_PRIVATE",
			Role:          "EEG_USER",
			FirstName:     "Michael",
			LastName:      "Obermüller",
			MeteringPoint: []*model.MeteringPoint{meter},
		},
		BillingAddress: model.Address{
			Type:         "BILLING",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
		ResidentAddress: model.Address{
			Type:         "RESIDENCE",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
	}
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	findParticipantUnderTest := func(t *testing.T, tenant, f, l string) *model.EegParticipant {
		ps, err := db.GetParticipants(context.Background(), tenant)
		require.NoError(t, err)
		for _, p := range ps {
			if p.FirstName == f && p.LastName == l {
				return p
			}
		}
		require.NoError(t, errors.New("not found"))
		return nil
	}

	// Try to register new Participant with already registerd meteringpoint -> should not fail
	err = db.RegisterParticipant(context.Background(), "TE000004", "test", newParticipant)
	require.NoError(t, err)

	pUnderTest := findParticipantUnderTest(t, "TE000004", "Michael", "Obermüller")

	assert.Equal(t, model.PENDING, pUnderTest.Status)
	assert.Equal(t, 1, len(pUnderTest.MeteringPoint))
	assert.Equal(t, model.S_INIT, pUnderTest.MeteringPoint[0].Status)
	assert.Equal(t, model.NEW, pUnderTest.MeteringPoint[0].ProcessState)
	assert.Nil(t, pUnderTest.MeteringPoint[0].State.ActiveSince.Ptr())
	assert.Nil(t, pUnderTest.MeteringPoint[0].State.InactiveSince.Ptr())

	// Send Revoke message to inactive meteringpoint
	consentEnd := civil.Today()
	err = db.MeteringPointRevoke(context.Background(), "TE000004", meter.MeteringPoint, consentEnd)
	assert.NoError(t, err)

	p := findParticipantUnderTest(t, "TE000004", "Michael", "Obermüller")

	require.Equal(t, 1, len(p.MeteringPoint))
	assert.Equal(t, model.S_INACTIVE, p.MeteringPoint[0].Status)
	assert.Equal(t, consentEnd, p.MeteringPoint[0].State.InactiveSince.Date)
	assert.Nil(t, p.MeteringPoint[0].State.ActiveSince.Ptr())

	now := civil.Today()
	err = db.MeteringPointsSetStatus(context.Background(), "TE000004", model.ACTIVE, nil, []string{meter.MeteringPoint}, &now, nil)
	require.NoError(t, err)

	p = findParticipantUnderTest(t, "TE000004", "Michael", "Obermüller")

	require.Equal(t, 1, len(p.MeteringPoint))
	assert.Equal(t, model.S_ACTIVE, p.MeteringPoint[0].Status)
	assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 23, 59, 59, 0, time.UTC)), p.MeteringPoint[0].State.InactiveSince.Date)
	assert.Equal(t, civil.Today(), p.MeteringPoint[0].State.ActiveSince.Date)
}

func Test_RegistrationProcess(t *testing.T) {
	meter := &model.MeteringPoint{
		MeteringPoint: "AT0030000000000000000000030000401",
		Direction:     model.GENERATOR,
		Street:        null.StringFrom("Solargasse"),
		StreetNumber:  null.StringFrom("1"),
		City:          null.StringFrom("Solarcity"),
		Zip:           null.StringFrom("1111"),
	}

	newParticipant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			BusinessRole:  "EEG_PRIVATE",
			Role:          "EEG_USER",
			FirstName:     "Registration",
			LastName:      "Test",
			MeteringPoint: []*model.MeteringPoint{meter},
		},
		BillingAddress: model.Address{
			Type:         "BILLING",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
		ResidentAddress: model.Address{
			Type:         "RESIDENCE",
			Street:       null.String{},
			StreetNumber: null.String{},
			Zip:          null.String{},
			City:         null.String{},
		},
	}

	findParticipantUnderTest := func(pp []*model.EegParticipant) *model.EegParticipant {
		var pUt *model.EegParticipant
		for _, p := range pp {
			if p.FirstName == "Registration" && p.LastName == "Test" {
				pUt = p
				break
			}
		}
		return pUt
	}

	db, err := GetDB(context.Background())

	// Try to register new Participant with already registerd meteringpoint -> should fail
	err = db.RegisterParticipant(context.Background(), "TE000004", "test", newParticipant)

	pp, err := db.GetParticipants(context.Background(), "TE000004")
	require.NoError(t, err)
	assert.Less(t, 0, len(pp))

	pUnderTest := findParticipantUnderTest(pp)
	require.NotNil(t, pUnderTest)

	err = db.ConfirmParticipant(context.Background(), "TE000004", "test", pUnderTest.Id.String())
	require.NoError(t, err)

	pp, err = db.GetParticipants(context.Background(), "TE000004")
	require.NoError(t, err)
	assert.Less(t, 0, len(pp))

	//expectedRegistrationDate := time.Now()

	pUnderTest = findParticipantUnderTest(pp)
	require.NotNil(t, pUnderTest)
	require.Equal(t, model.ACTIVE, pUnderTest.Status)
	require.Equal(t, 1, len(pUnderTest.MeteringPoint))
	require.Equal(t, model.NEW, pUnderTest.MeteringPoint[0].ProcessState)
	require.Equal(t, model.S_INIT, pUnderTest.MeteringPoint[0].Status)
	require.Equal(t, civil.Today(), pUnderTest.MeteringPoint[0].RegisteredSince)
	require.Nil(t, pUnderTest.MeteringPoint[0].State.ActiveSince.Ptr())

	now := civil.Today()
	err = db.MeteringPointsSetStatus(context.Background(), "TE000004", model.PENDING, nil, []string{meter.MeteringPoint}, &now, nil)
	require.NoError(t, err)

	m, err := db.FindMeteringByStatus(context.Background(), "TE000004", meter.MeteringPoint, model.S_INIT)
	require.NoError(t, err)
	assert.Equal(t, model.PENDING, m.ProcessState)

	err = db.MeteringPointsSetStatus(context.Background(), "TE000004", model.APPROVED, nil, []string{meter.MeteringPoint}, &now, nil)
	require.NoError(t, err)
	m, err = db.FindMeteringByStatus(context.Background(), "TE000004", meter.MeteringPoint, model.S_INIT)
	require.NoError(t, err)
	assert.Equal(t, model.APPROVED, m.ProcessState)

	err = db.MeteringPointsSetStatus(context.Background(), "TE000004", model.ACTIVE, nil, []string{meter.MeteringPoint}, &now, nil)
	require.NoError(t, err)
	m, err = db.FindMeteringByStatus(context.Background(), "TE000004", meter.MeteringPoint, model.S_ACTIVE)
	require.NoError(t, err)
	assert.Equal(t, model.ACTIVE, m.ProcessState)
	assert.Equal(t, civil.Today(), m.RegisteredSince)
	assert.Equal(t, now, m.State.ActiveSince.Date)
	assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)
}

func Test_UpdateMeteringPoint(t *testing.T) {
	jsonObj := `{
"meteringPoint":"AT0030000000000000000000030041724",
"transformer":null,"direction":"GENERATION","status":"ACTIVE","tariff_id":"f9b640dc-efe3-11ed-9f81-6ad19f4af00f",
"equipmentNumber":null,"equipmentName":"HARI PV","inverterid":null,"street":"Fellingerstraße","streetNumber":"9","city":"Waizenkirchen","zip":"4730",
"registeredSince":"2023-08-16","modifiedAt":"2023-08-16T16:36:09","modifiedBy":null,"gridOperatorId":null,"gridOperatorName":null,
"participantState":{"activeSince":"2023-01-01","inactiveSince":"2999-12-31"}}`

	m := model.MeteringPoint{}
	err := json.NewDecoder(strings.NewReader(jsonObj)).Decode(&m)
	require.NoError(t, err)

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	expectedRegistrationDate := civil.DateFor(2023, 8, 16)
	expectedactiveDate := civil.DateFor(2023, 1, 1)
	err = db.UpdateMeteringPoint(context.Background(), "TE000002", "test", "ea9942db-03da-11ee-b82b-5a985b4b033a", m.MeteringPoint, &m)
	require.NoError(t, err)

	mUnderTest, err := db.FindMeteringById(context.Background(), "TE000002", "AT0030000000000000000000030041724")
	require.NoError(t, err)

	require.Equal(t, expectedRegistrationDate, mUnderTest.RegisteredSince)
	require.Equal(t, expectedactiveDate, mUnderTest.State.ActiveSince.Date)
}

func Test_MeteringPointChangePartFact(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	type args struct {
		tenant string
		meters []model.Meter
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "test1", args: args{tenant: "testrc", meters: []model.Meter{
			{
				MeteringPoint: "AT11111111111111111111",
				Direction:     model.GENERATOR,
				Activation:    0,
				PartFact:      20,
			},
		}}, wantErr: assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, db.MeteringPointChangePartFactor(context.Background(), tt.args.tenant, tt.args.meters), fmt.Sprintf("MeteringPointChangePartFact(%v, %v)", tt.args.tenant, tt.args.meters))
		})
	}
}

func Test_UpdateMeteringPoints(t *testing.T) {
	tests := []struct {
		name       string
		testObject model.EbmsMessage
		validate   func(t *testing.T, meter *model.MeteringPoint)
	}{
		{
			name: "Update Metering",
			testObject: model.EbmsMessage{
				MeterList: []model.Meter{model.Meter{
					MeteringPoint: "AT0030000000000000000000000003013",
					Direction:     "GENERATION",
					ConsentID:     "AT00300020240617113044504B5ZO5IRS",
					PartFact:      13,
					Activation:    civil.Today().Unix() * 1000,
					To:            253402210800000,
					From:          1710198000000,
				}},
			},
			validate: func(t *testing.T, mUnderTest *model.MeteringPoint) {
				fmt.Printf("New ZP: %+v\n", mUnderTest)
				assert.Equal(t, model.S_ACTIVE, mUnderTest.Status)
				assert.Equal(t, model.ACTIVE, mUnderTest.ProcessState)
				assert.Equal(t, mUnderTest.ConsentId.String, "AT00300020240617113044504B5ZO5IRS")
				assert.Equal(t, civil.DateFor(2024, 3, 12), mUnderTest.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), mUnderTest.State.InactiveSince.Date)
				assert.Equal(t, 13, mUnderTest.PartFact)
			},
		},
		{
			name: "Update Metering - activesince greater",
			testObject: model.EbmsMessage{
				MeterList: []model.Meter{model.Meter{
					MeteringPoint: "AT0030000000000000000000000003013",
					Direction:     "GENERATION",
					ConsentID:     "AT00300020240617113044504B5ZO5IRS",
					PartFact:      13,
					Activation:    civil.Today().Unix() * 1000,
					To:            253402210800000,
					From:          1712872800000,
				}},
			},
			validate: func(t *testing.T, mUnderTest *model.MeteringPoint) {
				fmt.Printf("New ZP: %+v\n", mUnderTest)
				assert.Equal(t, mUnderTest.ConsentId.String, "AT00300020240617113044504B5ZO5IRS")
				assert.Equal(t, model.S_ACTIVE, mUnderTest.Status)
				assert.Equal(t, model.ACTIVE, mUnderTest.ProcessState)
				assert.Equal(t, civil.DateFor(2024, 3, 12), mUnderTest.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), mUnderTest.State.InactiveSince.Date)
				assert.Equal(t, 13, mUnderTest.PartFact)
			},
		},
		{
			name: "Update Metering - activesince lesser",
			testObject: model.EbmsMessage{
				MeterList: []model.Meter{model.Meter{
					MeteringPoint: "AT0030000000000000000000000003013",
					Direction:     "GENERATION",
					ConsentID:     "AT00300020240617113044504B5ZO5IRS",
					PartFact:      13,
					Activation:    civil.Today().Unix() * 1000,
					To:            253402210800000,
					From:          1710111600000,
				}},
			},
			validate: func(t *testing.T, mUnderTest *model.MeteringPoint) {
				fmt.Printf("New ZP: %+v\n", mUnderTest.State)
				assert.Equal(t, model.S_ACTIVE, mUnderTest.Status)
				assert.Equal(t, model.ACTIVE, mUnderTest.ProcessState)
				assert.Equal(t, mUnderTest.ConsentId.String, "AT00300020240617113044504B5ZO5IRS")
				assert.Equal(t, civil.DateFor(2024, 3, 11), mUnderTest.State.ActiveSince.Date, mUnderTest.State.ActiveSince)
				assert.Equal(t, civil.DateFor(2999, 12, 31), mUnderTest.State.InactiveSince.Date)
				assert.Equal(t, 13, mUnderTest.PartFact)
			},
		},
	}

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err = db.UpdateActiveMeteringPoints(context.Background(), "TE000005", tt.testObject.MeterList)
			require.NoError(t, err)

			mUnderTest, err := db.FindMeteringByStatus(context.Background(), "TE000005", "AT0030000000000000000000000003013", model.S_ACTIVE)
			require.NoError(t, err)

			tt.validate(t, mUnderTest)
		})
	}
}

func TestFindMeteringPointsForTenant(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	meters, err := db.FindMeteringPointsForTenant(context.Background(), "TE000002")
	require.NoError(t, err)
	require.Equal(t, 5, len(meters))

	fmt.Printf("MeteringPoints: %+v\n", meters)
}

func TestFindMeteringPointsActivePeriod(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	//from := int64(1713411836000) // Thu Apr 18 2024 05:43:56 GMT+0200 (Central European Summer Time)
	//to := int64(1718682236000)   // Tue Jun 18 2024 05:43:56 GMT+0200 (Central European Summer Time)
	//meters, err := FindMeteringPointsActivePeriod(db, "TE001006", from, to)
	//require.NoError(t, err)
	//require.Equal(t, 4, len(meters))
	//
	//fmt.Printf("MeteringPoints: %+v\n", meters)

	type args struct {
		from int64
		to   int64
	}

	tests := []struct {
		name     string
		args     args
		validate func(t *testing.T, meter []*model.MeteringPoint)
	}{
		{
			name: "Period 1",
			args: args{
				from: int64(1713411836000), // Thu Apr 18 2024 05:43:56 GMT+0200 (Central European Summer Time)
				to:   int64(1718682236000), // Tue Jun 18 2024 05:43:56 GMT+0200 (Central European Summer Time)
			},
			validate: func(t *testing.T, meters []*model.MeteringPoint) {
				require.Equal(t, 4, len(meters))
			},
		},
		{
			name: "Period 2",
			args: args{
				from: int64(1716933600000), // Wed May 29 2024 00:00:00 GMT+0200 (Central European Summer Time)
				to:   int64(1718682236000), // Tue Jun 18 2024 05:43:56 GMT+0200 (Central European Summer Time)
			},
			validate: func(t *testing.T, meters []*model.MeteringPoint) {
				require.Equal(t, 3, len(meters))
			},
		},
		{
			name: "Period 3",
			args: args{
				from: int64(1708210800000), // Sun Feb 18 2024 00:00:00 GMT+0100 (Central European Standard Time)
				to:   int64(1710716400000), // Mon Mar 18 2024 00:00:00 GMT+0100 (Central European Standard Time)
			},
			validate: func(t *testing.T, meters []*model.MeteringPoint) {
				require.Equal(t, 3, len(meters))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meters, err := db.FindMeteringPointsActivePeriod(context.Background(), "TE001006", tt.args.from, tt.args.to)
			require.NoError(t, err)
			tt.validate(t, meters)
		})
	}
}

func TestMeteringPointRevokeByConsentId_INIT(t *testing.T) {
	jsonStr := `{"conversationId":"RC100130202407121427323390000087827","messageId":"RC100130202407121427323390000087826","sender":"RC100130","receiver":"AT003000","messageCode":"AUFHEBUNG_CCMS","messageCodeVersion":"","requestId":"MILNITLK","meter":{"meteringPoint":"AT0030000000000000000000000200822","consentId":"AT00300020221001105609117"},"ecId":"AT00300000000RC100130000000952832","consentEnd":1720994400000}`

	m := model.EbmsMessage{}
	err := json.NewDecoder(strings.NewReader(jsonStr)).Decode(&m)
	require.NoError(t, err)

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	meterId := m.Meter.MeteringPoint
	consentId := m.Meter.ConsentID
	consentEnd := civil.DateOf(time.UnixMilli(m.ConsentEnd))
	fmt.Printf("Consent-End: %+s\n", consentEnd)

	tenant, err := db.MeteringPointRevokeByConsentId(context.Background(), &consentId, meterId, consentEnd)
	require.NoError(t, err)
	require.NotNil(t, tenant)
	require.Equal(t, "TE100201", *tenant)

	meters, err := db.FindInactiveMeteringById(context.Background(), "TE100201", meterId)
	require.NoError(t, err)
	require.Equal(t, 0, len(meters))

	meters, err = db.FindNewMeteringById(context.Background(), "TE100201", meterId)
	require.NoError(t, err)
	require.Equal(t, 1, len(meters))

	mUnderTest := meters[0]
	assert.Equal(t, "AT0030000000000000000000000200822", mUnderTest.MeteringPoint)
	assert.Equal(t, "AT00300020221001105609117", mUnderTest.ConsentId.String)
	assert.Nil(t, mUnderTest.State.InactiveSince.Ptr())
	assert.Nil(t, mUnderTest.State.ActiveSince.Ptr())
	assert.Equal(t, 100, mUnderTest.PartFact)
	assert.Equal(t, model.NEW, mUnderTest.ProcessState)
	assert.Equal(t, model.S_INIT, mUnderTest.Status)
}

func TestMeteringPointRevokeByConsentId_ACTIVE(t *testing.T) {
	jsonStr := `{"conversationId":"RC100130202407121427323390000087827","messageId":"RC100130202407121427323390000087826","sender":"RC100130","receiver":"AT003000","messageCode":"AUFHEBUNG_CCMS","messageCodeVersion":"","requestId":"MILNITLK","meter":{"meteringPoint":"AT0030000000000000000000000200823","consentId":"AT00300020221001105609116"},"ecId":"AT00300000000RC100130000000952832","consentEnd":1720994400000}`

	m := model.EbmsMessage{}
	err := json.NewDecoder(strings.NewReader(jsonStr)).Decode(&m)
	require.NoError(t, err)

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	meterId := m.Meter.MeteringPoint
	consentId := m.Meter.ConsentID
	consentEnd := civil.DateOf(time.UnixMilli(m.ConsentEnd))
	fmt.Printf("Consent-End: %+s\n", consentEnd)

	tenant, err := db.MeteringPointRevokeByConsentId(context.Background(), &consentId, meterId, consentEnd)
	require.NoError(t, err)
	require.NotNil(t, tenant)
	require.Equal(t, "TE100201", *tenant)

	meters, err := db.FindNewMeteringById(context.Background(), "TE100201", meterId)
	require.NoError(t, err)
	require.Equal(t, 0, len(meters))

	meters, err = db.FindInactiveMeteringById(context.Background(), "TE100201", meterId)
	require.NoError(t, err)
	require.Equal(t, 1, len(meters))

	mUnderTest := meters[0]
	assert.Equal(t, "AT0030000000000000000000000200823", mUnderTest.MeteringPoint)
	assert.Equal(t, "AT00300020221001105609116", mUnderTest.ConsentId.String)
	assert.Equal(t, civil.DateFor(2024, 7, 15), mUnderTest.State.InactiveSince.Date)
	assert.Equal(t, civil.DateFor(2024, 03, 12), mUnderTest.State.ActiveSince.Date)
	assert.Equal(t, 100, mUnderTest.PartFact)
	assert.Equal(t, model.INACTIVE, mUnderTest.ProcessState)
	assert.Equal(t, model.S_INACTIVE, mUnderTest.Status)
	assert.Equal(t, model.F_ASSIGNED, mUnderTest.State.Flag)
}

func TestRemoveMeteringPoint(t *testing.T) {
	meter := &model.MeteringPoint{
		MeteringPoint: "AT0030000000000000000000030000999",
		Direction:     model.GENERATOR,
		Street:        null.StringFrom("Solargasse"),
		StreetNumber:  null.StringFrom("1"),
		City:          null.StringFrom("Solarcity"),
		Zip:           null.StringFrom("1111"),
	}

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	// Register new Meteringpoint
	err = db.RegisterMeteringPoint(context.Background(), "TE000001", "test", "ea9942da-03da-11ee-b82b-5a985b4b033a", meter)
	require.NoError(t, err)

	m, err := db.FindMeteringByStatus(context.Background(), "TE000001", "AT0030000000000000000000030000999", model.S_INIT)
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.NoError(t, db.MeteringPointsSetStatus(context.Background(), "TE000001", model.REVOKED, nil, []string{"AT0030000000000000000000030000999"}, nil, nil))
	assert.NoError(t, db.RemoveMeteringPoint(context.Background(), "TE000001", "ea9942da-03da-11ee-b82b-5a985b4b033a", "AT0030000000000000000000030000999"))

	m, err = db.FindMeteringByStatus(context.Background(), "TE000001", "AT0030000000000000000000030000999", model.S_INIT)
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.NoError(t, db.MeteringPointsSetStatus(context.Background(), "TE000001", model.INVALID, nil, []string{"AT0030000000000000000000030000999"}, nil, nil))
	assert.NoError(t, db.RemoveMeteringPoint(context.Background(), "TE000001", "ea9942da-03da-11ee-b82b-5a985b4b033a", "AT0030000000000000000000030000999"))

	m, err = db.FindMeteringByStatus(context.Background(), "TE000001", "AT0030000000000000000000030000999", model.S_INIT)
	require.Error(t, err)
	require.Nil(t, m)
}
