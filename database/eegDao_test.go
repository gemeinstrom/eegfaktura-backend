package database

import (
	"at.ourproject/vfeeg-backend/model"
	"github.com/jjeffery/civil"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
	"strings"
	"testing"
)

func TestNewDatabase(t *testing.T) {

	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)

	eeg, err := db.GetEegByEcId(context.Background(), "AT00300000000TC000001000000000001")
	require.NoError(t, err)

	assert.Equal(t, "MY-TEST", eeg.Name)

	participants, err := db.GetParticipants(context.Background(), "TE000001")
	require.NoError(t, err)

	assert.Equal(t, 1, len(participants))

}

func TestGetEeg(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

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
	mDB, mock, err := InitMockDatabase()
	require.NoError(t, err)

	eegJson := `{
            "id": "TE100100",
            "name": "T-VIERE",
            "businessNr": "123456789",
            "area": "",
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
	err = json.NewDecoder(strings.NewReader(eegJson)).Decode(&eeg)
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
			name:  "Set EEG Online true",
			eeg:   "TE000001",
			param: map[string]interface{}{"online": true},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, true, eeg.Online)
			},
		},
		{
			name:  "Set EEG Online false",
			eeg:   "TE000001",
			param: map[string]interface{}{"online": false},
			test: func(t *testing.T, eeg *model.Eeg) {
				assert.Equal(t, false, eeg.Online)
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
