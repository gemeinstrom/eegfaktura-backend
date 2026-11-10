package services

import (
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	protobuf "at.ourproject/vfeeg-backend/proto"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//func init() {
//	viper.Set("services.mail-server", "localhost:9092")
//}
//
//func TestSendMail(t *testing.T) {
//	var b bytes.Buffer
//	b.WriteString("Hallo")
//	b.WriteString("Jürgen")
//
//	err := SendMail("tenant", "obermueller.peter@gmail.com", "Ihre Anmeldung", &b, nil, nil)
//	require.NoError(t, err)
//}

func TestSendMail(t *testing.T) {

}

func TestRegisterEeg(t *testing.T) {
	service := &RegisterService{}
	eeg := &protobuf.RegisterEegRequest{
		RcNumber:           "TE100111",
		CommunityId:        "RC345124312545124312341234",
		Name:               "",
		Description:        "",
		Iban:               "",
		Owner:              "",
		Sepa:               false,
		Legal:              0,
		SettelmentInterval: 0,
		GridId:             "",
		GridName:           "",
		Area:               0,
		Allocation:         0,
		EegOwner:           "",
		Street:             "",
		StreetNumber:       "",
		City:               "",
		Zip:                "",
		Email:              "",
		Web:                nil,
		Phone:              nil,
		Online:             false,
	}

	service.Register(context.Background(), eeg)
}

// The contract with admin-backend (register.proto GRID_AREA, platform#229): every enum value is a known community
// type under its own name (P2P = 4, EVA = 5); a value this backend does not know reaches insertEeg as its number and is
// refused there (database.ErrInvalidEeg, see registerReply).
func TestRegisterEegArea(t *testing.T) {
	assert.Len(t, protobuf.RegisterEegRequest_GRID_AREA_name, len(model.AreaTypes()))
	for _, area := range model.AreaTypes() {
		v, ok := protobuf.RegisterEegRequest_GRID_AREA_value[string(area)]
		require.True(t, ok, area)
		assert.NoError(t, model.ValidateArea(model.AreaType(protobuf.RegisterEegRequest_GRID_AREA(v).String())), area)
	}
	assert.Equal(t, int32(4), protobuf.RegisterEegRequest_GRID_AREA_value["P2P"])
	assert.Equal(t, int32(5), protobuf.RegisterEegRequest_GRID_AREA_value["EVA"])
	assert.Error(t, model.ValidateArea(model.AreaType(protobuf.RegisterEegRequest_GRID_AREA(6).String())))
}

// The contract with admin-backend (RegisterService.scala): 409 without a gRPC error stops the registration (261015-ca1);
// 400 without a gRPC error for an unknown value (platform#229), admin-backend treats every status but 201 as failed.
func TestRegisterReply(t *testing.T) {
	if r, err := registerReply(nil); err != nil || r.Status != 201 {
		t.Errorf("registered: got status %d, err %v", r.GetStatus(), err)
	}
	if r, err := registerReply(fmt.Errorf("insert: %w", database.ErrEegExists)); err != nil || r.Status != 409 {
		t.Errorf("existing tenant: got status %d, err %v", r.GetStatus(), err)
	}
	if r, err := registerReply(fmt.Errorf("%w: unknown community type", database.ErrInvalidEeg)); err != nil || r.Status != 400 {
		t.Errorf("unknown value: got status %d, err %v", r.GetStatus(), err)
	}
	if r, err := registerReply(errors.New("db down")); err == nil || r.Status != 500 {
		t.Errorf("database error: got status %d, err %v", r.GetStatus(), err)
	}
}

// Pre-existing (review of backend#147): an allocation, settlement interval or legal form this backend does not know arrived as its
// number ("2", "4", "3") and was stored. Refused with reply status 400 and no gRPC error, before the database.
func TestRegisterEegRefusesUnknownEnums(t *testing.T) {
	for name, req := range map[string]*protobuf.RegisterEegRequest{
		"allocation": {Tenant: "TE000047", RcNumber: "TE000047", Area: protobuf.RegisterEegRequest_LOCAL, Allocation: 2},
		"settlement": {Tenant: "TE000047", RcNumber: "TE000047", Area: protobuf.RegisterEegRequest_LOCAL, SettelmentInterval: 4},
		"legal":      {Tenant: "TE000047", RcNumber: "TE000047", Area: protobuf.RegisterEegRequest_LOCAL, Legal: 3},
	} {
		r, err := (&RegisterService{}).Register(context.Background(), req)
		assert.NoError(t, err, name)
		assert.Equal(t, uint32(400), r.GetStatus(), name)
	}
}
