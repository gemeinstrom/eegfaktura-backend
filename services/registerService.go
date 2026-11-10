package services

import (
	"context"
	"errors"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	protobuf "at.ourproject/vfeeg-backend/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/guregu/null.v4"
)

// statusEegExists in RegisteredEegReply.status: the tenant is already registered, nothing was written.
// admin-backend stops the registration on it (261015-ca1).
const statusEegExists = 409

// statusInvalidEeg in RegisteredEegReply.status: a value is unknown (e.g. an enum value this backend does not know
// yet, its name is the number), nothing was written. Without a gRPC error, like 409 (platform#229).
const statusInvalidEeg = 400

type RegisterService struct {
	protobuf.UnimplementedRegisterEegServiceServer
}

func (r *RegisterService) Register(ctx context.Context, eeg *protobuf.RegisterEegRequest) (*protobuf.RegisteredEegReply, error) {

	getOptionalField := func(field *string) null.String {
		if field == nil {
			return null.String{}
		}
		return null.StringFrom(*field)
	}

	newEeg := model.Eeg{
		Id:                 eeg.Tenant,
		Name:               eeg.Name,
		Description:        eeg.Description,
		BusinessNr:         null.String{},
		Area:               model.AreaType(eeg.Area.String()),
		Legal:              eeg.Legal.String(),
		OperatorName:       eeg.GridName,
		CommunityId:        eeg.CommunityId,
		GridOperator:       eeg.GridId,
		RcNumber:           eeg.RcNumber,
		AllocationMode:     model.AllocationModeType(eeg.Allocation.String()),
		SettlementInterval: eeg.SettelmentInterval.String(),
		ProviderBusinessNr: null.Int{},
		EegAddress: model.EegAddress{
			Street:       eeg.Street,
			StreetNumber: eeg.StreetNumber,
			Zip:          eeg.Zip,
			City:         eeg.City,
		},
		AccountInfo: model.AccountInfo{
			Iban:  null.StringFrom(eeg.Iban),
			Owner: null.StringFrom(eeg.Owner),
			Sepa:  eeg.Sepa,
		},
		Contact: model.Contact{
			Phone: getOptionalField(eeg.Phone),
			Email: null.StringFrom(eeg.Email),
		},
		Optionals: model.Optionals{
			Website: getOptionalField(eeg.Web),
		},
		//Periods:       nil,
		Online:        eeg.Online,
		ContactPerson: null.StringFrom(eeg.EegOwner),
	}

	// An allocation, settlement interval or legal form this backend does not know arrived as its number and was
	// stored: refused.
	if !knownEnum(protobuf.RegisterEegRequest_GRID_ALLOCATION_name, int32(eeg.Allocation)) ||
		!knownEnum(protobuf.RegisterEegRequest_EEG_SETTELMENT_name, int32(eeg.SettelmentInterval)) ||
		!knownEnum(protobuf.RegisterEegRequest_EEG_LEGAL_name, int32(eeg.Legal)) {
		log.WithField("event.action", "eeg.register.refused").Warnf("Register EEG refused: unknown allocation %d, settlement interval %d or legal form %d",
			eeg.Allocation, eeg.SettelmentInterval, eeg.Legal)
		return &protobuf.RegisteredEegReply{Status: statusInvalidEeg}, nil
	}

	log.Printf("Register EEG: %s", newEeg.Id)
	db, err := database.GetDB(ctx)
	if err != nil {
		log.Errorf("Database Error: %v", err)
		return &protobuf.RegisteredEegReply{Status: 500}, err
	}

	return registerReply(db.InsertEeg(ctx, eeg.RcNumber, &newEeg))
}

func knownEnum(names map[int32]string, v int32) bool {
	_, ok := names[v]
	return ok
}

func registerReply(err error) (*protobuf.RegisteredEegReply, error) {
	if errors.Is(err, database.ErrEegExists) {
		log.Warn("Register EEG refused: the tenant is already registered")
		return &protobuf.RegisteredEegReply{Status: statusEegExists}, nil
	}
	if errors.Is(err, database.ErrInvalidEeg) {
		log.WithField("event.action", "eeg.register.refused").Warnf("Register EEG refused: %v", err)
		return &protobuf.RegisteredEegReply{Status: statusInvalidEeg}, nil
	}
	if err != nil {
		log.Errorf("Could not create an EEG! %v", err.Error())
		return &protobuf.RegisteredEegReply{Status: 500},
			status.Errorf(codes.NotFound, "unknown service %v", err)
	}

	return &protobuf.RegisteredEegReply{Status: 201}, nil
}
