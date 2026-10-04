package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
	"strings"
	"testing"
	"time"
)

func TestUpdateParticipant(t *testing.T) {
	var tests = []struct {
		name     string
		line     func(table string, param interface{}) (sql string, params []interface{}, err error)
		params   interface{}
		database string
		result   []float64
	}{
		{
			name: "Test One",
			line: func(table string, param interface{}) (sql string, params []interface{}, err error) {
				sql, params, err = goqu.Insert("base.participant").Rows(param).ToSQL()
				return
			},
			params:   map[string]interface{}{"firstname": "hans"},
			database: "participant",
			result:   []float64{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, _ := tt.line(tt.database, tt.params)
			println(sql)
		})
	}
}

func TestRegisterParticipant(t *testing.T) {

	mockDb, err := GetMockDb()
	require.NoError(t, err)

	participantJson := `{"businessRole":"EEG_PRIVATE","firstname":"Peter","lastname":"Obermüller","residentAddress":{"street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck","type":"RESIDENCE"},"contact":{"phone":"06603611758","email":"obermueller.peter@gmail.com"},"accountInfo":{},"optionals":{},"status":"NEW","id":"e98b8619-7b6a-4836-baff-5489fb539535","role":"EEG_USER","billingAddress":{"street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck","type":"BILLING"},"meters":[{"direction":"CONSUMPTION","status":"NEW","meteringPoint":"AT48124817243712897412","participantId":"e98b8619-7b6a-4836-baff-5489fb539535","tariffId":"a48d1990-a5a2-40c9-8d0a-77bed8e7dbcd","street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck"}]}`

	var p model.EegParticipant
	err = json.NewDecoder(strings.NewReader(participantJson)).Decode(&p)
	assert.NoError(t, err)

	fmt.Printf("Participant: %+v\n", p)

	mockDb.Mock.ExpectBegin()
	mockDb.Mock.ExpectQuery("INSERT (.+)").WillReturnRows(sqlmock.NewRows([]string{"id"}).FromCSVString("1")) //.WillReturnResult(sqlmock.NewResult(1, 1)) //.WithArgs("firstname", "lastname")
	mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectCommit()

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	err = db.RegisterParticipant(context.Background(), "RC200200", "petero", &p)
	assert.NoError(t, err)
}

// A caller that omits or mislabels an address block must not be able to create
// an address row the read paths cannot find: they join on type = 'RESIDENCE' /
// 'BILLING', so a row with an empty type is invisible and unrepairable.
func TestEnforceAddressTypes(t *testing.T) {
	var tests = []struct {
		name string
		json string
		// what the caller supplied — against main these values reach the INSERT
		residentBefore model.AddressType
		billingBefore  model.AddressType
	}{
		{
			name:          "residentAddress fehlt komplett",
			json:          `{"firstname":"Anna","lastname":"Berger","billingAddress":{"street":"Hauptstrasse","streetNumber":"1","zip":"1010","city":"Wien","type":"BILLING"}}`,
			billingBefore: model.BILLING,
		},
		{
			name: "beide Blöcke ohne type",
			json: `{"firstname":"Anna","lastname":"Berger","billingAddress":{"street":"Hauptstrasse"},"residentAddress":{"street":"Hauptstrasse"}}`,
		},
		{
			name:           "Client vertauscht die Typen",
			json:           `{"firstname":"Anna","lastname":"Berger","billingAddress":{"type":"RESIDENCE"},"residentAddress":{"type":"BILLING"}}`,
			residentBefore: model.BILLING,
			billingBefore:  model.RESIDENCE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p model.EegParticipant
			require.NoError(t, json.NewDecoder(strings.NewReader(tt.json)).Decode(&p))

			require.Equal(t, tt.residentBefore, p.ResidentAddress.Type)
			require.Equal(t, tt.billingBefore, p.BillingAddress.Type)

			enforceAddressTypes(&p)

			assert.Equal(t, model.RESIDENCE, p.ResidentAddress.Type)
			assert.Equal(t, model.BILLING, p.BillingAddress.Type)

			// the row that actually reaches base.address must carry the type
			extra := map[string]interface{}{"participant_id": "p1"}
			assert.Equal(t, model.RESIDENCE, toRecord(p.ResidentAddress, extra)["type"])
			assert.Equal(t, model.BILLING, toRecord(p.BillingAddress, extra)["type"])
		})
	}
}

// The stamping has to be wired into the register path, not just available as a
// helper: this is the payload shape that produced the untyped rows in
// production — a member created through the API with no residentAddress block.
// With an empty meter list saveMeteringPoint returns before issuing SQL, so the
// statement order is exactly participant, contactdetail, bankaccount, address.
func TestRegisterParticipantStampsResidenceType(t *testing.T) {
	mockDb, err := GetMockDb()
	require.NoError(t, err)

	participantJson := `{"businessRole":"EEG_PRIVATE","firstname":"Anna","lastname":"Berger","contact":{"email":"anna.berger@example.at"},"accountInfo":{},"optionals":{},"status":"NEW","role":"EEG_USER","billingAddress":{"street":"Hauptstrasse","streetNumber":"1","zip":"1010","city":"Wien","type":"BILLING"},"meters":[]}`

	var p model.EegParticipant
	require.NoError(t, json.NewDecoder(strings.NewReader(participantJson)).Decode(&p))
	require.Empty(t, p.ResidentAddress.Type, "fixture must not carry a resident type")

	mockDb.Mock.ExpectBegin()
	mockDb.Mock.ExpectQuery("INSERT (.+)").WillReturnRows(sqlmock.NewRows([]string{"id"}).FromCSVString("1"))
	mockDb.Mock.ExpectExec(`INSERT INTO "base"\."contactdetail"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectExec(`INSERT INTO "base"\."bankaccount"`).WillReturnResult(sqlmock.NewResult(1, 1))
	// both tuples in order: without the fix the second one carries an empty type
	mockDb.Mock.ExpectExec(`INSERT INTO "base"\."address" .*'BILLING'.*'RESIDENCE'`).WillReturnResult(sqlmock.NewResult(1, 1))
	mockDb.Mock.ExpectCommit()

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	require.NoError(t, db.RegisterParticipant(context.Background(), "RC200200", "annab", &p))
	assert.NoError(t, mockDb.Mock.ExpectationsWereMet())
}

func TestGetParticipant(t *testing.T) {
	mockDb, err := GetMockDb()
	require.NoError(t, err)

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	participantRows := sqlmock.NewRows([]string{
		"id", "firstname", "lastname", "role", "businessRole", "titleBefore", "titleAfter", "participantSince",
		"vatNumber", "taxNumber", "companyRegisterNumber", "status", "createdBy", //"createdDate", "lastModifiedBy", "lastModifiedDate",
		"version", "tariffId", "participantNumber"}).
		AddRow(uuid.New(), "Sepp", "Huber", "EEG_USER", "EEG_PRIVATE", "", "", time.Now(),
			"", "", "", "NEW", "admin", //time.Now(), "petero", time.Now(),
			1, uuid.New(), "001")
	mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"participant\" (.+)").WillReturnRows(participantRows)

	//contactDetailsRows := sqlmock.NewRows([]string{"email", "phone"}).AddRow("mail@test.com", "+4325622 232311 32323")
	//mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"contactdetail\" (.+)").WillReturnRows(contactDetailsRows)
	//
	//bankaccountRows := sqlmock.NewRows([]string{"iban", "owner"}).AddRow("AT12 3456 7987 9887 7765", "Sepp Huber")
	//mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"bankaccount\" (.+)").WillReturnRows(bankaccountRows)
	//
	//addressRows := sqlmock.NewRows([]string{"city", "street", "streetNumber", "type", "zip"}).
	//	AddRow("Solarcity", "Energieweg", "12a", "BILLING", "1234")
	//mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"address\" (.+)").WillReturnRows(addressRows)
	//
	//addressResidenceRows := sqlmock.NewRows([]string{"city", "street", "streetNumber", "type", "zip"}).
	//	AddRow("Solarcity", "Energieweg", "12a", "RESIDENCE", "1234")
	//mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"address\" (.+)").WillReturnRows(addressResidenceRows)
	//
	meterRows := sqlmock.NewRows([]string{"city", "direction", "equipmentName", "equipmentNumber", "inverterid", "metering_point_id",
		"modifiedAt", "modifiedBy", "registeredSince", "status", "street", "streetNumber", "tariff_id", "transformer", "zip"}).
		AddRow("Solarcity", "GENERATOR", "", "", "", "AT0020001110000010011111001",
			time.Now(), "admin", time.Now(), "NEW", "Energieweg", "12a", uuid.New(), "", "1234")
	//mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"participant_meter_state\" (.+)").WillReturnRows(meterRows)

	mockDb.Mock.ExpectQuery("SELECT (.+) FROM \"base\".\"meteringpoint\", (.+)").WillReturnRows(meterRows)

	participants, err := db.GetParticipants(context.Background(), "RC100298")
	assert.NoError(t, err)

	assert.NotEmpty(t, participants)
	fmt.Printf("Participants: %+v\n", participants)
}

func Test_GetParticipants(t *testing.T) {
	//_, err := GetMockDb()
	//require.NoError(t, err)

	db, err := GetTestDB(context.Background(), testDB)
	require.NoError(t, err)

	participants, err := db.GetParticipants(context.Background(), "TE000002")
	require.NoError(t, err)

	require.Equal(t, 1, len(participants))
	p := participants[0]

	assert.Equal(t, "Peter", p.FirstName)
	assert.Equal(t, "Schulberg", p.ResidentAddress.Street.String)
	assert.Equal(t, "Sparberweg", p.BillingAddress.Street.String)
	assert.Nil(t, p.BankAccount.Iban.Ptr())

	assert.Equal(t, 5, len(p.MeteringPoint))

	findMeter := func(m []*model.MeteringPoint, mid string) *model.MeteringPoint {
		for i := range m {
			if m[i].MeteringPoint == mid {
				return m[i]
			}
		}
		return nil
	}

	expectedMeter := &model.MeteringPoint{
		MeteringPoint:    "AT0030000000000000000000030041725",
		Transformer:      null.String{},
		Direction:        model.GENERATOR,
		Status:           model.S_ACTIVE,
		ProcessState:     model.ACTIVE,
		TariffId:         null.StringFrom("f9b640dc-efe3-11ed-9f81-6ad19f4af00f"),
		EquipmentNumber:  null.StringFrom("GERZ02"),
		EquipmentName:    null.String{},
		InverterId:       null.String{},
		Street:           null.StringFrom("Imperndorf"),
		StreetNumber:     null.StringFrom("9"),
		City:             null.StringFrom("Waizenkirchen"),
		Zip:              null.StringFrom("4730"),
		RegisteredSince:  civil.DateFor(2023, 8, 16),
		ModifiedAt:       civil.DateTimeFor(2023, 11, 15, 17, 42, 41),
		ModifiedBy:       null.StringFrom("petero"),
		GridOperatorId:   null.String{},
		GridOperatorName: null.String{},
		State: &model.MeterState{
			ActiveSince:   civil.NullDate{Date: civil.DateOf(time.Date(2023, 1, 1, 0, 0, 0, 0, time.FixedZone("", 0))), Valid: true},
			InactiveSince: civil.NullDate{Date: civil.DateOf(time.Date(2999, 12, 31, 0, 0, 0, 0, time.FixedZone("", 0))), Valid: true},
			Active:        0,
			Flag:          1,
		},
		PartFact: 100,
	}
	m := findMeter(p.MeteringPoint, expectedMeter.MeteringPoint)
	m.ParticipantId = ""
	assert.NotNil(t, m)
	assert.Equal(t, *expectedMeter.State, *m.State)
	assert.Equal(t, expectedMeter, m)
}

func Test_saveParticipant(t *testing.T) {
	type args struct {
		db                         *sqlx.DB
		tenant                     string
		username                   string
		participant                *model.EegParticipant
		registerMeteringPointsFunc func(context.Context, *sqlx.Tx, string, string, string, []*model.MeteringPoint) error
	}

	mDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	participantJson := `{"businessRole":"EEG_PRIVATE","firstname":"Peter","lastname":"Obermüller","residentAddress":{"street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck","type":"RESIDENCE"},"contact":{"phone":"06603611758","email":"obermueller.peter@gmail.com"},"accountInfo":{},"optionals":{},"status":"NEW","id":"e98b8619-7b6a-4836-baff-5489fb539535","role":"EEG_USER","billingAddress":{"street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck","type":"BILLING"},"meters":[{"direction":"CONSUMPTION","status":"NEW","meteringPoint":"AT48124817243712897412","participantId":"e98b8619-7b6a-4836-baff-5489fb539535","tariffId":"a48d1990-a5a2-40c9-8d0a-77bed8e7dbcd","street":"Lambacherstraße","streetNumber":"39","zip":"4680","city":"Haag am Hausruck"}]}`

	var p model.EegParticipant
	err = json.NewDecoder(strings.NewReader(participantJson)).Decode(&p)
	assert.NoError(t, err)

	mdb := sqlx.NewDb(mDB, "mock")

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT (.+) \"base\".\"participant\"").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("11"))
	mock.ExpectExec("INSERT (.+) \"base\".\"contactdetail\"").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT (.+) \"base\".\"bankaccount\"").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT (.+) \"base\".\"address\"").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT (.+) \"base\".\"meteringpoint\"").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT (.+) \"base\".\"metering_partition_factor\"").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "Save Participant", // TODO: Add test cases.
			args:    args{db: mdb, tenant: "te100001", username: "tester", participant: &p, registerMeteringPointsFunc: ImportMeteringPoints},
			wantErr: assert.NoError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := tt.args.db.Beginx()
			assert.NoError(t, err)
			err = saveParticipant(context.Background(), tx, tt.args.tenant, tt.args.username, tt.args.participant, tt.args.registerMeteringPointsFunc)
			assert.NoError(t, tx.Commit())
			assert.NoError(t, mock.ExpectationsWereMet())
			require.NoError(t, err)

		})
	}
}

//func Test_findParticipantByMeteringPoint(t *testing.T) {
//	_, err := FindParticipantByMeteringPoint(nil, "TE100110", "AT0020000000000000000000020793777")
//	assert.NoError(t, err)
//}

func TestImportParticipant(t *testing.T) {

	var tests = []struct {
		name   string
		mp     string
		params *model.EegParticipant
		test   func(t *testing.T, p *model.EegParticipant)
	}{
		{
			name: "Test Import New Participant",
			mp:   "AT00300000000000000000000000000001",
			params: &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{
					ParticipantNumber: null.String{},
					FirstName:         "Max",
					LastName:          "Mustermann",
					MeteringPoint: []*model.MeteringPoint{&model.MeteringPoint{
						MeteringPoint: "AT00300000000000000000000000000001",
						Transformer:   null.String{},
						Direction:     model.GENERATOR,
						Street:        null.StringFrom("Solargasse"),
						StreetNumber:  null.StringFrom("11a"),
						City:          null.StringFrom("Solarcity"),
						Zip:           null.StringFrom("1111"),
					}},
					Status: model.NEW,
				},
				Contact: model.ContactInfo{},
				BillingAddress: model.Address{
					Type:         model.BILLING,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				ResidentAddress: model.Address{
					Type:         model.RESIDENCE,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				BankAccount: model.BankInfo{},
			},
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, 1, len(p.MeteringPoint))
				m := p.MeteringPoint[0]

				fmt.Printf("P: %+v\n", p.ParticipantSince)
				fmt.Printf("M: %+v\n", m)

				assert.Equal(t, civil.Today(), p.ParticipantSince.Date)
				assert.Equal(t, civil.Today(), m.RegisteredSince)
				assert.Nil(t, m.State.ActiveSince.Ptr())
				assert.Nil(t, m.State.InactiveSince.Ptr())

				assert.Equal(t, model.NEW, p.Status)
				assert.Equal(t, model.S_INIT, m.Status)

				assert.Equal(t, "Max", p.FirstName)
			},
		},
		{
			name: "Test Import Activated Participant",
			mp:   "AT00300000000000000000000000000002",
			params: &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{
					ParticipantNumber: null.String{},
					FirstName:         "Maria",
					LastName:          "Mustermann",
					ParticipantSince:  civil.NullDate{},
					MeteringPoint: []*model.MeteringPoint{&model.MeteringPoint{
						MeteringPoint:   "AT00300000000000000000000000000002",
						Transformer:     null.String{},
						Direction:       model.GENERATOR,
						Street:          null.StringFrom("Solargasse"),
						StreetNumber:    null.StringFrom("11a"),
						City:            null.StringFrom("Solarcity"),
						Zip:             null.StringFrom("1111"),
						ProcessState:    model.ACTIVE,
						RegisteredSince: civil.DateFor(2023, 10, 6),
					}},
					Status: model.ACTIVE,
				},
				Contact: model.ContactInfo{},
				BillingAddress: model.Address{
					Type:         model.BILLING,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				ResidentAddress: model.Address{
					Type:         model.RESIDENCE,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				BankAccount: model.BankInfo{},
			},
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, 1, len(p.MeteringPoint))
				m := p.MeteringPoint[0]

				fmt.Printf("P: %+v\n", p.ParticipantSince)
				fmt.Printf("M: %+v\n", m)

				require.NotNil(t, p.ParticipantSince.Ptr())
				assert.Equal(t, civil.Today(), p.ParticipantSince.Date)
				assert.Equal(t, civil.DateFor(2023, 10, 6), m.RegisteredSince)
				assert.Equal(t, civil.DateFor(2023, 10, 6), m.State.ActiveSince.Date)
				assert.Equal(t, civil.DateFor(2999, 12, 31), m.State.InactiveSince.Date)

				assert.Equal(t, model.ACTIVE, p.Status)
				assert.Equal(t, model.S_ACTIVE, m.Status)

				assert.Equal(t, "Maria", p.FirstName)
			},
		},
		{
			name: "Test Import Participant - empty state",
			mp:   "AT00300000000000000000000000000003",
			params: &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{
					ParticipantNumber: null.String{},
					FirstName:         "Helmut",
					LastName:          "Mustermann",
					MeteringPoint: []*model.MeteringPoint{&model.MeteringPoint{
						MeteringPoint: "AT00300000000000000000000000000003",
						Transformer:   null.String{},
						Direction:     model.GENERATOR,
						Street:        null.StringFrom("Solargasse"),
						StreetNumber:  null.StringFrom("11a"),
						City:          null.StringFrom("Solarcity"),
						Zip:           null.StringFrom("1111"),
					}},
				},
				Contact: model.ContactInfo{},
				BillingAddress: model.Address{
					Type:         model.BILLING,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				ResidentAddress: model.Address{
					Type:         model.RESIDENCE,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				BankAccount: model.BankInfo{},
			},
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, 1, len(p.MeteringPoint))
				m := p.MeteringPoint[0]

				fmt.Printf("P: %+v\n", p.ParticipantSince)
				fmt.Printf("M: %+v\n", m)

				assert.Equal(t, civil.Today(), p.ParticipantSince.Date)
				assert.Equal(t, civil.Today(), m.RegisteredSince)
				assert.Nil(t, m.State.ActiveSince.Ptr())
				assert.Nil(t, m.State.InactiveSince.Ptr())

				assert.Equal(t, model.NEW, p.Status)
				assert.Equal(t, model.S_INIT, m.Status)

				assert.Equal(t, "Helmut", p.FirstName)
			},
		},
		{
			name: "Test Import Participant - only billing address",
			mp:   "AT00300000000000000000000000000004",
			params: &model.EegParticipant{
				EegParticipantBase: model.EegParticipantBase{
					ParticipantNumber: null.String{},
					FirstName:         "Clara",
					LastName:          "Mustermann",
					MeteringPoint: []*model.MeteringPoint{&model.MeteringPoint{
						MeteringPoint: "AT00300000000000000000000000000004",
						Transformer:   null.String{},
						Direction:     model.GENERATOR,
						Street:        null.StringFrom("Solargasse"),
						StreetNumber:  null.StringFrom("11a"),
						City:          null.StringFrom("Solarcity"),
						Zip:           null.StringFrom("1111"),
					}},
				},
				Contact: model.ContactInfo{},
				BillingAddress: model.Address{
					Type:         model.BILLING,
					Street:       null.StringFrom("Solargasse"),
					StreetNumber: null.StringFrom("11a"),
					Zip:          null.StringFrom("1111"),
					City:         null.StringFrom("Solarcity"),
				},
				ResidentAddress: model.Address{
					Type: model.RESIDENCE,
				},
				BankAccount: model.BankInfo{},
			},
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, 1, len(p.MeteringPoint))
				m := p.MeteringPoint[0]

				fmt.Printf("P: %+v\n", p.ParticipantSince)
				fmt.Printf("M: %+v\n", m)

				assert.Equal(t, civil.Today(), p.ParticipantSince.Date)
				assert.Equal(t, civil.Today(), m.RegisteredSince)
				assert.Nil(t, m.State.ActiveSince.Ptr())
				assert.Nil(t, m.State.InactiveSince.Ptr())

				assert.Equal(t, model.NEW, p.Status)
				assert.Equal(t, model.S_INIT, m.Status)

				assert.Equal(t, "Clara", p.FirstName)
			},
		},
	}

	db, err := GetDB(context.Background())
	assert.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err = db.ImportParticipant(context.Background(), "TE000001", "test", tt.params)
			assert.NoError(t, err)

			p, err := db.FindParticipantByMeteringPoint(context.Background(), "TE000001", tt.mp)
			assert.NoError(t, err)

			tt.test(t, p)
		})
	}
}

func TestUpdateParticipant1(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	type args struct {
		tenant      string
		user        string
		participant *model.EegParticipant
	}
	tests := []struct {
		name    string
		args    args
		wantErr func(t *testing.T, p, e *model.EegParticipant)
	}{
		{
			name: "Update Participant",
			args: args{
				tenant: "TE000001",
				user:   "",
				participant: &model.EegParticipant{
					EegParticipantBase: model.EegParticipantBase{
						Id:                    uuid.Parse("ea9942da-03da-11ee-b82b-5a985b4b033a"),
						ParticipantNumber:     null.StringFrom("041"),
						BusinessRole:          "EEG_PRIVATE",
						Role:                  "EEG_USER",
						FirstName:             "Peter",
						LastName:              "Obermüller",
						TitleBefore:           null.String{},
						TitleAfter:            null.String{},
						ParticipantSince:      civil.NullDate{},
						VatNumber:             null.String{},
						TaxNumber:             null.String{},
						CompanyRegisterNumber: null.String{},
						TariffId:              null.String{},
						Status:                "ACTIVE",
						Version:               0,
						CreatedBy:             "petero",
					},
					Contact:         model.ContactInfo{},
					BillingAddress:  model.Address{Type: "BILLING"},
					ResidentAddress: model.Address{Type: "RESIDENT"},
					BankAccount:     model.BankInfo{},
				},
			},
			wantErr: func(t *testing.T, underTest, org *model.EegParticipant) {
				assert.Equal(t, "041", underTest.ParticipantNumber.String)
				fmt.Printf("ParticipantSince %v\n", underTest.ParticipantSince.Date.String())
				assert.Equal(t, civil.DateFor(2023, 10, 11), underTest.ParticipantSince.Date)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.UpdateParticipant(context.Background(), tt.args.tenant, tt.args.user, tt.args.participant)
			assert.NoError(t, err)

			pUnderTest, err := db.QueryParticipant(context.Background(), tt.args.tenant, tt.args.participant.Id.String())
			assert.NoError(t, err)

			tt.wantErr(t, pUnderTest, tt.args.participant)
		})
	}
}

// Der Fixture-Teilnehmer ea9942da-... gehoert zu TE000001. Ein Zugriff aus einer
// anderen Gemeinschaft muss abgewiesen werden - frueher lief er durch, weil die
// Abfragen nur nach der ID gefiltert haben.
func TestParticipantTenantScope(t *testing.T) {
	const (
		ownTenant     = "TE000001"
		foreignTenant = "TE000004"
		participantId = "ea9942da-03da-11ee-b82b-5a985b4b033a"
	)

	db, err := GetDB(context.Background())
	assert.NoError(t, err)

	t.Run("eigener Mandant liest", func(t *testing.T) {
		p, err := db.GetParticipant(context.Background(), ownTenant, participantId)
		assert.NoError(t, err)
		assert.NotNil(t, p)
	})

	t.Run("fremder Mandant wird abgewiesen", func(t *testing.T) {
		_, err := db.GetParticipant(context.Background(), foreignTenant, participantId)
		assert.Error(t, err)

		_, err = db.QueryParticipant(context.Background(), foreignTenant, participantId)
		assert.Error(t, err)

		err = db.UpdateParticipantPartial(context.Background(), foreignTenant, participantId, "contact.phone", "0000")
		assert.Error(t, err)

		err = db.ConfirmParticipant(context.Background(), foreignTenant, "test", participantId)
		assert.Error(t, err)

		err = db.DeleteParticipant(context.Background(), foreignTenant, participantId)
		assert.Error(t, err)
	})

	t.Run("leerer Mandant wird abgewiesen", func(t *testing.T) {
		_, err := db.GetParticipant(context.Background(), "", participantId)
		assert.Error(t, err)
	})
}

func TestUpdateParticipantPartial(t *testing.T) {
	var tests = []struct {
		name          string
		participantId string
		param         string
		value         interface{}
		test          func(t *testing.T, p *model.EegParticipant)
	}{
		{
			name:          "Set Mandate-Date to 2025-06-04",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "accountInfo.mandateDate",
			value:         "2025-06-04",
			test: func(t *testing.T, p *model.EegParticipant) {
				mandateDate, err := civil.ParseDate("2025-06-04")
				require.NoError(t, err)
				assert.Equal(t, civil.NullDateFrom(&mandateDate), p.BankAccount.MandateDate)
			},
		},
		{
			name:          "Clear Mandate-Date to 2025-06-04",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "accountInfo.mandateDate",
			value:         nil,
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, false, p.BankAccount.MandateDate.Valid)
			},
		},
		{
			name:          "Set Tariff-ID",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "tariffId",
			value:         "557d439d-4f61-42f2-ae54-9698e774381f",
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "557d439d-4f61-42f2-ae54-9698e774381f", p.TariffId.String)
			},
		},
		{
			name:          "Clear Tariff-ID",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "tariffId",
			value:         nil,
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, false, p.TariffId.Valid)
			},
		},
		{
			name:          "Set Phone-Nr",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "contact.phone",
			value:         "000101101",
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, "000101101", p.Contact.Phone.String)
			},
		},
		{
			name:          "Clear Phone-Nr",
			participantId: "ea9942da-03da-11ee-b82b-5a985b4b033a",
			param:         "contact.phone",
			value:         nil,
			test: func(t *testing.T, p *model.EegParticipant) {
				assert.Equal(t, false, p.Contact.Phone.Valid)
			},
		},
	}

	db, err := GetDB(context.Background())
	assert.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err = db.UpdateParticipantPartial(context.Background(), "TE000001", tt.participantId, tt.param, tt.value)
			assert.NoError(t, err)

			p, err := db.GetParticipant(context.Background(), "TE000001", tt.participantId)
			assert.NoError(t, err)

			tt.test(t, p)
		})
	}
}
