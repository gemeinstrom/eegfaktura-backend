package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/gorilla/mux"
	"github.com/jjeffery/civil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

func init() {
	viper.Set("database.host", "localhost")
	viper.Set("database.port", 6432)
	viper.Set("database.user", "postgresuser")
	viper.Set("database.password", "postgresPW")
	viper.Set("database.dbname", "postgresdb")
}

func Test_registerParticipant(t *testing.T) {
	registerObject := `{
		"id": "",
		"participantNumber": "072",
		"participantSince": "2006-01-02T15:04:05Z",
		"firstname": "Helmut",
		"lastname": "Stieger",
		"status": "NEW",
		"titleBefore": "",
		"titleAfter": "",
		"residentAddress": {
		"street": "Lambacherstraße",
				"type": "RESIDENCE",
				"city": "Haag am Hausruck",
				"streetNumber": "39",
				"zip": "4680"
		},
		"billingAddress": {
		"street": "Lambacherstraße",
				"type": "BILLING",
				"city": "Haag am Hausruck",
				"streetNumber": "39",
				"zip": "4680"
	},
		"contact": {
			"email": "obermueller.peter@gmail.com",
			"phone": "06603611758"
	},
		"accountInfo": {
		"iban": "ATxxxxxxxxxxxxxxxxxx",
				"owner": "Helmut Stieger",
				"sepa": false
	},
		"businessRole": "EEG_PRIVATE",
			"role": "EEG_USER",
			"optionals": {
		"website": ""
	},
		"taxNumber": "",
			"tariffId": "",
			"meters": [
	{
	"direction": "CONSUMPTION",
	"status": "NEW",
	"meteringPoint": "AT0030000000000000000000000000011",
	"street": "Lambacherstraße",
	"streetNumber": "39",
	"zip": "4680",
	"city": "Haag am Hausruck"
	}
]
}`

	var p model.EegParticipant
	err := json.NewDecoder(strings.NewReader(registerObject)).Decode(&p)
	require.NoError(t, err)

	fmt.Printf("Part: %+v\n", p)
}

//func Test_ConfirmParticipant(t *testing.T) {
//	participantId := "ea9942da-03da-11ee-b82b-5a985b4b033a"
//	participant, err := database.QueryParticipant(participantId)
//
//	require.NoError(t, err)
//	fmt.Printf("P: %+v\n", participant)
//}

func Test_confirmParticipantOnline(t *testing.T) {
	type args struct {
		tenant      string
		request     string
		mqttReqFunc func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error
	}

	getRequest := func(registeredSince civil.Date) string {
		meters := []*model.MeteringPoint{
			{
				MeteringPoint:    "AT0030000000000000000000030041724",
				ConsentId:        null.String{},
				Transformer:      null.String{},
				Direction:        model.GENERATOR,
				Status:           model.S_INIT,
				StatusCode:       null.Int{},
				TariffId:         null.String{},
				EquipmentNumber:  null.String{},
				EquipmentName:    null.String{},
				InverterId:       null.String{},
				Street:           null.String{},
				StreetNumber:     null.String{},
				City:             null.String{},
				Zip:              null.String{},
				RegisteredSince:  registeredSince,
				ModifiedAt:       civil.DateTime{},
				ModifiedBy:       null.String{},
				GridOperatorId:   null.String{},
				GridOperatorName: null.String{},
				ProcessState:     model.INIT,
				State:            nil,
				PartFact:         10,
				ActivationMode:   model.ONLINE,
			},
		}
		b, err := json.Marshal(meters)
		if err != nil {
			return ""
		}
		return string(b)
	}

	//database.ConnectToDatabase = func() (*sqlx.DB, error) {
	//	return openTestDb()
	//}

	claims := &middleware.PlatformClaims{Username: "test"}

	findMeteringPoint := func(meters []*model.MeteringPoint, meterId string) *model.MeteringPoint {
		for _, m := range meters {
			if m.MeteringPoint == meterId {
				return m
			}
		}
		return nil
	}

	tests := []struct {
		name  string
		args  args
		check func(t *testing.T, recorder *httptest.ResponseRecorder, pUnderTest *model.EegParticipant)
	}{
		{
			name: "Confirming a participant in the future",
			args: args{
				tenant:  "TE000002",
				request: getRequest(civil.Today().AddDate(0, 0, 10)),
				mqttReqFunc: func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {
					fmt.Printf("Confirming participant %+v, %v (%v)\n", *meter, *from, civil.Today().AddDate(0, 0, 10).Unix()*1000)
					assert.Equal(t, civil.Today().AddDate(0, 0, 10).Unix()*1000, *from)
					return nil
				},
			},
			check: func(t *testing.T, recorder *httptest.ResponseRecorder, pUnderTest *model.EegParticipant) {
				require.Equal(t, http.StatusCreated, recorder.Code)

				var pUnderT model.EegParticipant
				err := json.NewDecoder(recorder.Body).Decode(&pUnderT)
				require.NoError(t, err)

				mUnderTest := findMeteringPoint(pUnderT.MeteringPoint, "AT0030000000000000000000030041724")
				require.NotNil(t, mUnderTest)
				assert.Equal(t, civil.DateFor(2023, 8, 16), mUnderTest.RegisteredSince)
			},
		},
		{
			name: "Confirming a participant with bad registration date",
			args: args{
				tenant:  "TE000002",
				request: getRequest(civil.Today().AddDate(0, 0, -10)),
				mqttReqFunc: func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {
					fmt.Printf("Confirming participant %+v, %v\n", *meter, from)
					assert.Equal(t, civil.Today().AddDate(0, 0, 1).Unix()*1000, *from)
					return nil
				},
			},
			check: func(t *testing.T, recorder *httptest.ResponseRecorder, pUnderTest *model.EegParticipant) {
				require.Equal(t, http.StatusCreated, recorder.Code)
			},
		},
	}
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewParticipantHandler(db)
	orig := mqttclient.RegistrationForParticipation
	t.Cleanup(func() { mqttclient.RegistrationForParticipation = orig })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/participant/ea9942db-03da-11ee-b82b-5a985b4b033a/confirm", strings.NewReader(tt.args.request))
			w := httptest.NewRecorder()
			calls := 0
			mqttclient.RegistrationForParticipation = func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {
				calls++
				return tt.args.mqttReqFunc(eeg, meter, from)
			}
			req = mux.SetURLVars(req, map[string]string{
				"id": "ea9942db-03da-11ee-b82b-5a985b4b033a",
			})

			h.confirmParticipant()(w, req, claims, tt.args.tenant)

			pUnderTest, err := db.QueryParticipant(context.Background(), tt.args.tenant, "ea9942db-03da-11ee-b82b-5a985b4b033a")
			require.NoError(t, err)
			tt.check(t, w, pUnderTest)
			// one ONLINE metering point in the request -> one registration via MQTT
			assert.Equal(t, 1, calls, "registrations sent via MQTT")
		})
	}
}
