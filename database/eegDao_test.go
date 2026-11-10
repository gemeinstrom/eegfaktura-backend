package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jjeffery/civil"
	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
	"strings"
	"testing"
)

func TestNewDatabase(t *testing.T) {

	// asserts the seed data: on the untouched copy, other tests change TE000001
	db := seedDB(t)

	eeg, err := db.GetEegByEcId(context.Background(), "AT00300000000TC000001000000000001")
	require.NoError(t, err)

	assert.Equal(t, "MY-TEST", eeg.Name)

	participants, err := db.GetParticipants(context.Background(), "TE000001")
	require.NoError(t, err)

	assert.Equal(t, 1, len(participants))

}

func TestGetEeg(t *testing.T) {
	// asserts the seed data: on the untouched copy, other tests change TE000001
	db := seedDB(t)

	eeg, err := db.GetEegById(context.Background(), "TE000001")
	assert.NoError(t, err)

	expectedEeg := &model.Eeg{
		Id:                 "TE000001",
		Name:               "MY-TEST",
		Description:        "Gemeinnütziger Verein",
		BusinessNr:         null.StringFrom("123456789"),
		Area:               "LOCAL",
		Legal:              "verein",
		OperatorName:       "Netz OOE",
		CommunityId:        "AT00300000000TC000001000000000001",
		GridOperator:       "AT003000",
		RcNumber:           "TE000001",
		AllocationMode:     "DYNAMIC",
		SettlementInterval: "MONTHLY",
		ProviderBusinessNr: null.Int{},
		TaxNumber:          null.StringFrom("11 123/4567"),
		VatNumber:          null.String{},
		ContactPerson:      null.StringFrom("Max Sonnenmann"),
		EegAddress: model.EegAddress{
			Street:       "Solarstraße",
			StreetNumber: "9",
			Zip:          "1111",
			City:         "Solarcity",
		},
		AccountInfo: model.AccountInfo{
			Iban:     null.StringFrom("AT011234000000321321"),
			Owner:    null.StringFrom("T-VIERE"),
			BankName: null.String{},
			Sepa:     false,
		},
		Contact: model.Contact{
			Phone: null.StringFrom("0043-664-1234567"),
			Email: null.StringFrom("test-eeg@gmx.at"),
		},
		Optionals: model.Optionals{Website: null.StringFrom("test-eeg.at")},
		//Periods:   nil,
		Online: false,
		// createdAt hat einen DB-Default (now()) — Fixture wird beim Testlauf angelegt.
		CreatedAt: civil.NullDate{Date: civil.Today(), Valid: true},
	}
	assert.Equal(t, expectedEeg, eeg)
}

func TestUpdateEeg(t *testing.T) {
	mDB, mock := newMockDatabase(t)

	eegJson := `{
            "id": "TE100100",
            "name": "T-VIERE",
            "businessNr": "123456789",
            "area": "LOCAL",
            "legal": "verein",
            "operatorName": "Netz OOE",
            "communityId": "AT00300000000TC100100000000000001",
            "gridOperator": "AT003000",
            "rcNumber": "TE100100",
            "allocationMode": "DYNAMIC",
            "settlementInterval": "MONTHLY",
            "providerBusinessNr": null,
            "taxNumber": "11 123/4567",
            "vatNumber": null,
            "contactPerson": "",
            "address": {
                "type": "",
                "street": "Solarstraße",
                "streetNumber": "9",
                "zip": "1111",
                "city": "Solarcity"
            },
            "accountInfo": {
                "iban": "AT011234000000321321",
                "owner": "T-VIERE",
                "sepa": false
            },
            "contact": {
                "phone": "0043-664-1234567",
                "email": "test-eeg@gmx.at"
            },
            "optionals": {
                "website": "test-eeg.at"
            },
            "periods": null,
            "online": false
        }`

	var eeg model.Eeg
	err := json.NewDecoder(strings.NewReader(eegJson)).Decode(&eeg)
	assert.NoError(t, err)

	type args struct {
		tenant string
		eeg    *model.Eeg
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "Update EEG",
			args:    args{tenant: "TE100100", eeg: &eeg},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ExpectQuery("SELECT EXISTS (.+)").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mock.ExpectExec("INSERT INTO (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
			tt.wantErr(t, mDB.InsertEeg(context.Background(), tt.args.tenant, tt.args.eeg), fmt.Sprintf("InsertEeg(%v, %+v)", tt.args.tenant, tt.args.eeg))
			assert.NoError(t, mock.ExpectationsWereMet())
			require.NoError(t, err)
		})
	}
}

func TestGetEegById(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	eeg, err := db.GetEegById(context.Background(), "TE000001")
	assert.NoError(t, err)

	println(eeg)
}

func TestUpdateEegPartial(t *testing.T) {
	input := map[string]interface{}{"Owner": "EEG VIERE", "ProviderBusinessNr": 11}
	var result model.Eeg

	cfg := &mapstructure.DecoderConfig{
		Result:     &result,
		DecodeHook: StringToNullStringHookFunc,
	}
	decoder, err := mapstructure.NewDecoder(cfg)
	require.NoError(t, err)
	err = decoder.Decode(input)

	//type Family struct {
	//	LastName string
	//}
	//type Location struct {
	//	City string
	//}
	//type Person struct {
	//	Family    `mapstructure:",squash"`
	//	Location  `mapstructure:",squash"`
	//	FirstName string
	//}
	//
	//input := map[string]interface{}{
	//	"FirstName": "Mitchell",
	//	"LastName":  "Hashimoto",
	//	"City":      "San Francisco",
	//}
	//
	//var result Person
	//err := mapstructure.Decode(input, &result)

	assert.NoError(t, err)

	fmt.Printf("%+v\n", result)
}

func TestEegOnline(t *testing.T) {
	input := map[string]interface{}{"online": true}
	var result model.Eeg

	cfg := &mapstructure.DecoderConfig{
		Result:     &result,
		DecodeHook: StringToNullStringHookFunc,
	}
	decoder, err := mapstructure.NewDecoder(cfg)
	require.NoError(t, err)
	err = decoder.Decode(input)

	assert.NoError(t, err)

	fmt.Printf("%+v\n", result)
}

func TestUpdateEegPartial1(t *testing.T) {
	var tests = []struct {
		name  string
		eeg   string
		param map[string]interface{}
		test  func(t *testing.T, eeg *model.Eeg)
	}{
		{
			name:  "Set EEG Business-Nr",
			eeg:   "TE000001",
			param: map[string]interface{}{"businessNr": "1234567890"},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, "1234567890", eeg.BusinessNr.String)
			},
		},
		{
			name:  "Set EEG IBAN",
			eeg:   "TE000001",
			param: map[string]interface{}{"iban": "AT11 1111 1111 1111 11"},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, "AT11 1111 1111 1111 11", eeg.Iban.String)
			},
		},
		{
			name:  "Set EEG Bankaccount Owner",
			eeg:   "TE000001",
			param: map[string]interface{}{"owner": "Max Mustermann"},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, "Max Mustermann", eeg.Owner.String)
			},
		},
		{
			name:  "Clear EEG Bankaccount Owner",
			eeg:   "TE000001",
			param: map[string]interface{}{"owner": nil},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, false, eeg.Owner.Valid)
			},
		},
		{
			name:  "Set EEG Bank creditorId",
			eeg:   "TE000001",
			param: map[string]interface{}{"creditor_id": "creditorId-1234"},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, "creditorId-1234", eeg.CreditorId.String)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := GetDB(context.Background())
			assert.NoError(t, err)

			err = db.UpdateEegPartial(context.Background(), tt.eeg, tt.param)
			assert.NoError(t, err)

			eeg, err := db.GetEegById(context.Background(), tt.eeg)
			assert.NoError(t, err)

			tt.test(t, eeg)
		})
	}
}

// TestUpdateEegPartialDropsProtectedFields deckt F6 ab: der generische EEG-Update
// darf geschuetzte Felder (tenant, rcNumber, online, communityId, settlementInterval, area, allocationMode) nicht setzen,
// auch wenn der Client sie mitschickt (die Web schickt das ganze Objekt zurueck).
func TestUpdateEegPartialDropsProtectedFields(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	before, err := db.GetEegById(context.Background(), "TE000001")
	require.NoError(t, err)

	otherMode := model.STATIC
	if before.AllocationMode == model.STATIC {
		otherMode = model.DYNAMIC
	}
	require.NotEqual(t, model.P2P, before.Area, "fixture")

	err = db.UpdateEegPartial(context.Background(), "TE000001", map[string]interface{}{
		"description": "F6-probe",
		"rcNumber":    "HIJACK",
		"online":      true,
		"communityId": "AT00000000000000000000000000HIJACK",
		// only through ChangeSettlementInterval with its rule (konzept-abrechnungsintervall.md)
		"settlementInterval": "ANNUAL",
		// set at registration, otherwise only by the operator (platform#229, E5)
		"area":           string(model.P2P),
		"allocationMode": string(otherMode),
	})
	require.NoError(t, err)

	after, err := db.GetEegById(context.Background(), "TE000001")
	require.NoError(t, err)

	assert.Equal(t, "F6-probe", after.Description, "normales Feld muss geaendert sein")
	assert.Equal(t, before.RcNumber, after.RcNumber, "rcNumber darf nicht geaendert sein")
	assert.Equal(t, before.CommunityId, after.CommunityId, "communityId darf nicht geaendert sein")
	assert.Equal(t, before.Online, after.Online, "online darf nicht geaendert sein")
	assert.Equal(t, before.SettlementInterval, after.SettlementInterval, "settlementInterval darf nicht geaendert sein")
	assert.Equal(t, before.Area, after.Area, "area darf nicht geaendert sein")
	assert.Equal(t, before.AllocationMode, after.AllocationMode, "allocationMode darf nicht geaendert sein")
}

// TestUpdateEegPartialRejectsUnknownField: ein unbekannter Schluessel wird als
// SQL-Bezeichner verwendet und darf deshalb nicht durchgereicht werden.
func TestUpdateEegPartialRejectsUnknownField(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	before, err := db.GetEegById(context.Background(), "TE000001")
	require.NoError(t, err)

	err = db.UpdateEegPartial(context.Background(), "TE000001", map[string]interface{}{
		"description":               "unknown-probe",
		`description"=(SELECT 1)--`: "x",
	})
	assert.Error(t, err)

	after, err := db.GetEegById(context.Background(), "TE000001")
	require.NoError(t, err)
	assert.Equal(t, before.Description, after.Description, "nichts darf geschrieben sein")
}

// TestUpdateEegOnlineState: online wird ueber den eigenen Weg gesetzt (admin-backend
// nach der Ponton-Registrierung), nicht ueber den generischen Update.
func TestUpdateEegOnlineState(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	for _, want := range []bool{true, false} {
		require.NoError(t, db.UpdateEegOnlineState(context.Background(), "TE000001", want))
		eeg, err := db.GetEegById(context.Background(), "TE000001")
		require.NoError(t, err)
		assert.Equal(t, want, eeg.Online)
	}
}

// 261028-mm1: in a GEA the tenant holding the metering point wins, an assigned row before an archived one; with one
// tenant per community id the result is that of GetEegByEcId.
func TestGetEegByEcIdAndMeters(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)

	const ecId = "AT00300000000GC000043000000000001"
	const meter = "AT0030000000000000000000000043001"
	// archived with the „Archivieren“ action (flag 2) in the first tenant, moved (flag 0) in the third, assigned in the second
	for tenant, flag := range map[string]int{"GC000043": 2, "GC000043-002": 1, "GC000043-003": 0} {
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email)
			VALUES ($1, 'GEA-43', 'GEA-Test', 'GC000043', 'GEA', 'AT003000', 'Netz OÖ', $2,
			 'Weg', '1', 'Linz', '4020', 'gea-43@example.org')`, tenant, ecId)
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
			status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
			VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test',
			 '2026-10-01', $3)`, meter, tenant, flag)
		require.NoError(t, err)
	}

	eeg, err := db.GetEegByEcIdAndMeters(ctx, ecId, []string{meter})
	require.NoError(t, err)
	assert.Equal(t, "GC000043-002", eeg.Id)

	// revoked in the base tenant (row stays flag 1, INACTIVE) and registering again in the third: the live row wins
	const moved = "AT0030000000000000000000000043002"
	for tenant, status := range map[string]string{"GC000043": "INACTIVE", "GC000043-003": "INIT"} {
		_, err = testDB.DbInstance.Exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction,
			status, process_state, "modifiedAt", "modifiedBy", "registeredSince", flag)
			VALUES ($1, 'ea1142dc-03da-15ee-b82b-5a985b4b033a', $2, 'CONSUMPTION', $3, 'PENDING', now(), 'test',
			 '2026-10-01', 1)`, moved, tenant, status)
		require.NoError(t, err)
	}
	eeg, err = db.GetEegByEcIdAndMeters(ctx, ecId, []string{moved})
	require.NoError(t, err)
	assert.Equal(t, "GC000043-003", eeg.Id)

	// no metering point held by a tenant: the base tenant (named like the RC number), every time
	eeg, err = db.GetEegByEcIdAndMeters(ctx, ecId, []string{"AT0030000000000000000000000043999"})
	require.NoError(t, err)
	assert.Equal(t, "GC000043", eeg.Id)
	eeg, err = db.GetBaseEegByEcId(ctx, ecId)
	require.NoError(t, err)
	assert.Equal(t, "GC000043", eeg.Id)

	// one tenant per community id: unchanged, also for an unknown metering point
	eeg, err = db.GetEegByEcIdAndMeters(ctx, "AT00300000000TC000001000000000001", []string{"AT0030000000000000000000000043999"})
	require.NoError(t, err)
	assert.Equal(t, "TE000001", eeg.Id)

	_, err = db.GetEegByEcIdAndMeters(ctx, "AT00300000000XX000000000000000000", nil)
	assert.Error(t, err)
}

// EcIdBelongsToRc and GetTenantsByRcNumber follow one rule: a tenant belongs to an RC number by its RC number or by
// its name, case-insensitive (261105-mm5).
func TestEcIdBelongsToRc(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)
	const ecId, otherEc = "AT00300000000TC000331000000000001", "AT00300000000TC000332000000000001"
	t.Cleanup(func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant IN ('TE000331', 'GC000333-002', 'TE000332')`)
		require.NoError(t, err)
	})
	for _, e := range [][3]string{{"TE000331", "RC000331", ecId}, {"GC000333-002", "GC000333", ecId}, {"TE000332", "TE000332", otherEc}} {
		_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area,
			gridoperator_code, gridoperator_name, "communityId", street, "streetNumber", city, zip, email)
			VALUES ($1, 'RC-Test', 'RC-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3, 'Weg', '1', 'Linz', '4020',
			'rc-test@example.org') ON CONFLICT (tenant) DO NOTHING`, e[0], e[1], e[2])
		require.NoError(t, err)
	}
	for _, c := range []struct {
		rc   string
		want bool
	}{
		{"rc000331", true},     // RC number, lower case
		{"te000331", true},     // tenant name, as GetTenantsByRcNumber
		{"GC000333", true},     // GEA tenant by its RC number
		{"gc000333-002", true}, // GEA tenant by its name
		{"TE000332", false},    // a tenant of another community id
		{"RC000999", false},
	} {
		ok, err := db.EcIdBelongsToRc(ctx, ecId, c.rc)
		require.NoError(t, err)
		assert.Equal(t, c.want, ok, c.rc)
	}
}
