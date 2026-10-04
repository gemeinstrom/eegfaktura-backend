package model

import (
	"errors"
	"fmt"
)

type VfeegError struct {
	Code int
	Err  error
}

func (r *VfeegError) Error() string {
	return r.Err.Error()
}

func Wrap(err error, code int) *VfeegError {
	return &VfeegError{
		Code: code,
		Err:  err,
	}
}

func PartialWrap(code int) func(err error) *VfeegError {
	return func(err error) *VfeegError {
		return &VfeegError{
			Code: code,
			Err:  err,
		}
	}
}

var ErrParseJson = PartialWrap(2000)

var ErrConnectDatabase = PartialWrap(999)
var ErrOpenTx = PartialWrap(998)

var ErrGetEeg = PartialWrap(1000)

var ErrGetParticipant = PartialWrap(1100)
var ErrCompleteParticipant = PartialWrap(1101)
var ErrUpdateParticipant = PartialWrap(1102)
var ErrInsertParticipant = PartialWrap(1103)
var ErrRegisterParticipant = PartialWrap(1104)
var ErrArchiveParticipant = PartialWrap(1105)
var ErrFindParticipant = PartialWrap(1106)
var ErrDeleteParticipant = PartialWrap(1107)

// Der Teilnehmer existiert, gehoert aber nicht zur Gemeinschaft des Aufrufers.
// Bewusst eigener Code, damit sich Fremdzugriff im Log von "nicht gefunden" trennen laesst.
var ErrForeignTenantParticipant = PartialWrap(1108)

var ErrRemoveMeteringPoint = PartialWrap(1201)
var ErrStatusMeter = PartialWrap(1202)
var ErrSaveMeteringPoint = PartialWrap(1203)
var ErrWrongActivationCode = PartialWrap(1204)

var ErrEdaCommunication = PartialWrap(5000)
var ErrRequestEnergyData = PartialWrap(5100)
var ErrRevokeMeter = PartialWrap(5010)

var ErrFindMeter = PartialWrap(3100)
var ErrUpdateMeter = PartialWrap(3101)

var ErrGetTariff = PartialWrap(4000)
var ErrUpdateTariff = PartialWrap(4001)
var ErrTariffUtilized = PartialWrap(4002)

var ErrGetUser = PartialWrap(9000)

type LogMessage struct {
	Kind        string `json:"kind"`
	Identifier  string `json:"metering_point"`
	MessageCode string `json:"message_code"`
	Message     string `json:"message"`
}

func NewLogMessage(kind, identifier string, messageCode, messageDesc string) *LogMessage {
	return &LogMessage{
		Kind:        kind,
		Identifier:  identifier,
		MessageCode: messageCode,
		Message:     messageDesc,
	}
}

func NewLogMessageFromVfeegError(identifier string, err error) *LogMessage {
	lm := &LogMessage{Kind: "ERROR", Identifier: identifier}
	var e *VfeegError
	switch {
	case errors.As(err, &e):
		lm.MessageCode = fmt.Sprintf("E_DB_%d", e.Code)
		lm.Message = e.Err.Error()
	default:
		lm.MessageCode = "E_UNDEFINED_0"
		lm.Message = err.Error()
	}
	return lm
}

type Log struct {
	Operation string        `json:"operation"`
	Messages  []*LogMessage `json:"messages"`
}
