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

var ErrOpenTx = PartialWrap(998)

var ErrGetEeg = PartialWrap(1000)

var ErrGetParticipant = PartialWrap(1100)
var ErrCompleteParticipant = PartialWrap(1101)
var ErrUpdateParticipant = PartialWrap(1102)
var ErrInsertParticipant = PartialWrap(1103)
var ErrRegisterParticipant = PartialWrap(1104)
var ErrFindParticipant = PartialWrap(1106)
var ErrDeleteParticipant = PartialWrap(1107)

// Anfrage ohne Mandant. Ein fremder Teilnehmer bekommt dieselbe Antwort wie ein unbekannter
// (ErrFindParticipant, "participant not found"); getrennt werden die beiden nur im Log.
var ErrForeignTenantParticipant = PartialWrap(1108)

// A metering point of the member was active or is on its way there: the delete would take its history along.
var ErrParticipantHasActivatedMeter = PartialWrap(1109)

var ErrRemoveMeteringPoint = PartialWrap(1201)
var ErrStatusMeter = PartialWrap(1202)
var ErrSaveMeteringPoint = PartialWrap(1203)
var ErrUnknownActivationMode = PartialWrap(1204)

// activationMode OFFLINE (ANFORDERUNG_ECOF with activation code): the grid operators no longer
// support it (decision 2026-10-30). Rejected before anything is written or sent.
var ErrOfflineRegistrationUnsupported = PartialWrap(1205)

// The metering point is already stored at this member (e.g. inactive after a revoke): it is registered again
// through the process "Zählpunkt aktivieren", not added a second time (platform#247). Answered with 409.
var ErrMeterExistsAtParticipant = PartialWrap(1206)

// MeterExistsAtParticipantText is the German text of ErrMeterExistsAtParticipant for the user: the meter is
// assigned to the member (shown in its list).
const MeterExistsAtParticipantText = "Dieser Zählpunkt ist bei diesem Mitglied schon vorhanden. Einen inaktiven Zählpunkt " +
	"meldest du unter Prozesse → Zählpunkt aktivieren wieder an."

// MeterHiddenAtParticipantText: the member still has a moved or archived row of the meter, hidden from its list,
// which the process cannot reach either.
const MeterHiddenAtParticipantText = "Dieser Zählpunkt war bei diesem Mitglied schon einmal angelegt (verschoben oder " +
	"archiviert) und kann hier nicht neu angelegt werden. Bitte wende dich an den Support."

// The metering point is active (or being registered) at another member of the community: the partial unique index
// idx_unique_meteringpoint_active allows one assigned row per metering point and tenant.
var ErrMeterActiveAtOtherParticipant = PartialWrap(1207)

// MeterActiveAtOtherParticipantText is the German text of ErrMeterActiveAtOtherParticipant; %s is the metering point.
const MeterActiveAtOtherParticipantText = "Der Zählpunkt %s ist schon einem anderen Mitglied dieser Gemeinschaft " +
	"zugeordnet. Verschiebe ihn dort zu diesem Mitglied oder melde ihn dort ab. Das Mitglied findest du über die " +
	"Suche in der Mitgliederliste."

// An invalid e-mail address of a member (create or update); the text names the address (model.AddressError).
var ErrInvalidParticipantEmail = PartialWrap(1110)

// A new member with direct debit (CORE or B2B) needs an IBAN.
var ErrDebitWithoutIban = PartialWrap(1112)

// DebitWithoutIbanText is the German text of ErrDebitWithoutIban.
const DebitWithoutIbanText = "Für die Einzugsart CORE oder B2B braucht das Mitglied eine IBAN."

// The sheet named for the master data import is not in the uploaded workbook.
var ErrImportSheetMissing = PartialWrap(1064)

// ImportSheetMissingText is the German text of ErrImportSheetMissing; %s is the sheet name.
const ImportSheetMissingText = "Das Blatt „%s“ gibt es in der Datei nicht. Trage unter „Sheet-Name“ den Namen des " +
	"Blatts mit den Stammdaten ein."

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
		lm.Message = UserText(e.Err)
	default:
		lm.MessageCode = "E_UNDEFINED_0"
		lm.Message = UserText(err)
	}
	return lm
}

type Log struct {
	Operation string        `json:"operation"`
	Messages  []*LogMessage `json:"messages"`
}
