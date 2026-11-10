package services

import (
	"context"
	"strconv"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	protobuf "at.ourproject/vfeeg-backend/proto"
	"github.com/jjeffery/civil"
	"github.com/sirupsen/logrus"
)

type AdminService struct {
	protobuf.UnimplementedAdminEegServiceServer
}

func (r *AdminService) UpdateValue(ctx context.Context, request *protobuf.UpdateEegRequest) (*protobuf.UpdateEegReply, error) {

	var activeSince civil.NullDate
	var inactiveSince civil.NullDate
	var exists bool

	var activeSinceValue string
	if activeSinceValue, exists = request.Value["activeSince"]; exists {
		timestamp, err := strconv.ParseInt(activeSinceValue, 10, 64)
		if err == nil {
			_ = activeSince.Scan(time.UnixMilli(timestamp))
		}
	}

	var inactiveSinceValue string
	if inactiveSinceValue, exists = request.Value["inactiveSince"]; exists {
		timestamp, err := strconv.ParseInt(inactiveSinceValue, 10, 64)
		if err == nil {
			_ = inactiveSince.Scan(time.UnixMilli(timestamp))
		}
	}

	db, err := database.GetDB(context.Background())
	if err != nil {
		return nil, err
	}

	switch request.UpdateClass {
	case protobuf.UpdateEegRequest_PROCESSSTATUS:
		var processState string
		if processState, exists = request.Value["processState"]; !exists {
			return &protobuf.UpdateEegReply{Status: 502, Message: "Can not update PROCESSSTATUS due to ProcessStatus Value is missing!"}, nil
		}

		if request.MeteringPoint == nil {
			return &protobuf.UpdateEegReply{Status: 502, Message: "Can not update PROCESSSTATUS due to MeteringPoint Id is missing!"}, nil
		}

		if err := db.UpdateProcessStatus(
			ctx,
			request.Tenant,
			[]string{*request.MeteringPoint},
			model.ProcessStatusType(processState), nil,
			activeSince.Ptr(), inactiveSince.Ptr(), nil); err != nil {
			return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update PROCESSSTATUS due to a database issue!"}, err
		}
		return &protobuf.UpdateEegReply{Status: 201, Message: "Process Status updated successfully"}, nil
	case protobuf.UpdateEegRequest_ACTIVESINCE:
		if request.ParticipantId == nil || request.MeteringPoint == nil || !activeSince.Valid {
			return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update ACTIVESINCE due to MeteringPoint Id or Participant Id is missing!"}, nil
		}
		if err := db.UpdateActiveSinceDate(
			ctx,
			request.Tenant,
			*request.ParticipantId,
			*request.MeteringPoint, "admin", activeSince.Ptr()); err != nil {
			logrus.Error(err)
			return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update ACTIVESINCE due to a database issue!"}, err
		}
		return &protobuf.UpdateEegReply{Status: 201, Message: "ActiveSince updated successfully"}, nil
	case protobuf.UpdateEegRequest_INACTIVESINCE:
		if request.ParticipantId == nil || request.MeteringPoint == nil || !inactiveSince.Valid {
			return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update INACTIVESINCE due to MeteringPoint Id or Participant Id is missing!"}, nil
		}

		if err := db.UpdateInActiveSinceDate(
			ctx,
			request.Tenant,
			*request.ParticipantId,
			*request.MeteringPoint, "admin", inactiveSince.Ptr()); err != nil {
			logrus.Error(err)
			return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update INACTIVESINCE due to a database issue!"}, err
		}
		return &protobuf.UpdateEegReply{Status: 201, Message: "InactiveSince updated successfully"}, nil
	case protobuf.UpdateEegRequest_PARTICIPANT:
		if request.ParticipantId == nil {
			return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update PARTICIPANT due to Participant Id is missing!"}, nil
		}

		if err := db.UpdateParticipantValues(
			ctx,
			*request.ParticipantId,
			request.Tenant,
			request.Value); err != nil {
			logrus.Error(err)
			return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update PARTICIPANT due to a database issue!"}, err
		}
		return &protobuf.UpdateEegReply{Status: 201, Message: "PARTICIPANT updated successfully"}, nil
	case protobuf.UpdateEegRequest_EEG:
		if len(request.Tenant) == 0 {
			return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update EEG due to Tenant is missing!"}, nil
		}

		if len(request.Value) == 0 {
			return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update EEG due to Values is missing!"}, nil
		}

		fields := map[string]interface{}{}
		for k, v := range request.Value {
			fields[k] = v
		}

		// The generic EEG update refuses "online" (write-protected). After the
		// Ponton registration admin-backend sets it through this call, so it
		// goes through the dedicated online-state update instead.
		if v, ok := request.Value["online"]; ok {
			online, perr := strconv.ParseBool(v)
			if perr != nil {
				return &protobuf.UpdateEegReply{Status: 501, Message: "Can not update EEG due to an invalid online value!"}, nil
			}
			if err = db.UpdateEegOnlineState(ctx, request.Tenant, online); err != nil {
				logrus.Error(err)
				return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update EEG due to a database issue!"}, err
			}
			delete(fields, "online")
			if len(fields) == 0 {
				return &protobuf.UpdateEegReply{Status: 201, Message: "EEG updated successfully"}, nil
			}
		}

		if err = db.UpdateEegPartial(ctx, request.Tenant, fields); err != nil {
			logrus.Error(err)
			return &protobuf.UpdateEegReply{Status: 500, Message: "Can not update EEG due to a database issue!"}, err
		}
		return &protobuf.UpdateEegReply{Status: 201, Message: "EEG updated successfully"}, nil
	}
	return &protobuf.UpdateEegReply{Status: 501, Message: "Cound not handle the update request"}, nil
}
