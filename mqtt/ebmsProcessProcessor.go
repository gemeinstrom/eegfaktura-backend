package mqttclient

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/util"
	log "github.com/sirupsen/logrus"
)

var RegistrationForParticipation = func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {

	receiver, err := getReceiverFrom(eeg, meter)
	if err != nil {
		return err
	}
	ebmsMessage := createEbmsMessage(eeg, receiver, model.EBMS_ONLINE_REG_INIT)
	ebmsMessage.Meter = &model.Meter{MeteringPoint: meter.MeteringPoint, Direction: meter.Direction, PartFact: meter.PartFact}

	log.WithField("tenant", eeg.Id).Infof("Start Meteringpoint %s ONLINE registration", meter.MeteringPoint)
	return sendRegistrationForParticipation(eeg, meter, from, ebmsMessage)
}

func sendRegistrationForParticipation(eeg *model.Eeg, meter *model.MeteringPoint, from *int64, ebmsMessage model.EbmsMessage) error {
	if from != nil {
		ebmsMessage.Meter.From = *from
	}

	if eeg.AllocationMode == model.STATIC && meter.AllocationFactor.Valid {
		ebmsMessage.Meter.Share = util.ToFixed(meter.AllocationFactor.Float64/100.0, 4)
	}

	//if err := SendEbmsMessage(ebmsMessage); err != nil {
	//	return model.ErrEdaCommunication(err)
	//}
	Broker().SendMessage(ebmsMessage)
	return nil
}

var RequestingEnergyData = func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error {

	receiver, err := getReceiverFrom(eeg, meter)
	if err != nil {
		return err
	}
	ebmsMessage := createEbmsMessage(eeg, receiver, model.EBMS_ZP_SYNC)
	ebmsMessage.Meter = &model.Meter{MeteringPoint: meter.MeteringPoint}
	ebmsMessage.Timeline = &model.Timeline{From: fromDate, To: toDate}

	log.WithField("tenant", eeg.Id).Info("Start Metering sync")
	//if err := SendEbmsMessage(ebmsMessage); err != nil {
	//	return err
	//}

	Broker().SendMessage(ebmsMessage)
	return nil
}

func RevokeMeteringPoint(eeg *model.Eeg, meter *model.MeteringPoint, consentEnd int64, reason *string) error {

	var reasonMsg string
	if reason != nil {
		reasonMsg = *reason
	}

	receiver, err := getReceiverFrom(eeg, meter)
	if err != nil {
		return err
	}
	ebmsMessage := createEbmsMessage(eeg, receiver, model.EBMS_AUFHEBUNG_CCMS)
	ebmsMessage.Meter = &model.Meter{MeteringPoint: meter.MeteringPoint, ConsentID: meter.ConsentId.ValueOrZero()}
	ebmsMessage.ConsentEnd = consentEnd
	ebmsMessage.Reason = reasonMsg

	log.WithField("tenant", eeg.Id).Info("Revoke Meteringpoint")
	//if err := SendEbmsMessage(ebmsMessage); err != nil {
	//	return err
	//}
	Broker().SendMessage(ebmsMessage)
	return nil
}

// RequestingMeteringPointList asks a grid operator for the metering point list of the community. operators are the
// grid operators of its metering points (used for BEG, P2P, EVA). An error means nothing was sent.
func RequestingMeteringPointList(eeg *model.Eeg, receiver string, operators []string, from, to int64) error {

	zpReceiver, err := zpListReceiver(eeg, receiver, operators)
	if err != nil {
		return err
	}
	ebmsMessage := createEbmsMessage(eeg, zpReceiver, model.EBMS_ZP_LIST)
	ebmsMessage.Meter = &model.Meter{MeteringPoint: eeg.CommunityId}
	ebmsMessage.Timeline = &model.Timeline{From: from, To: to}

	log.WithField("tenant", eeg.Id).Info("Request MeteringPointList")
	//if err := SendEbmsMessage(ebmsMessage); err != nil {
	//	return model.ErrEdaCommunication(err)
	//}
	Broker().SendMessage(ebmsMessage)
	RememberZpListRequester(eeg.CommunityId, eeg.Id)
	return nil
}

// zpListReceiver: with several grid operators possible (BEG, P2P, EVA) the list is requested from the chosen one, which
// must be the grid operator of one of the community's metering points (operators) or the community's own
// (eegfaktura-dev#56 #7); else from the community's grid operator.
func zpListReceiver(eeg *model.Eeg, receiver string, operators []string) (string, error) {
	if model.HasMultipleGridOperators(eeg.Area) {
		receiver = strings.ToUpper(strings.TrimSpace(receiver))
		if receiver != "" && (receiver == strings.ToUpper(strings.TrimSpace(eeg.GridOperator)) ||
			slices.Contains(operators, receiver)) {
			return receiver, nil
		}
		return "", model.ErrEdaCommunication(errors.New("grid operator is not one of the community"))
	}
	if eeg.GridOperator == "" {
		return "", model.ErrEdaCommunication(errors.New("no Grid Operator specified"))
	}
	return eeg.GridOperator, nil
}

// zpListRequesters remembers which tenant asked for the metering point list of a community id. In a GEA several
// tenants share the community id and the answer carries only that, so the answer goes to the tenant that asked
// (261028-mm1). Kept in memory: after a restart the answer goes to the GEA's base tenant.
var zpListRequesters sync.Map

// RememberZpListRequester notes that tenant asked for the metering point list of ecId.
func RememberZpListRequester(ecId, tenant string) {
	zpListRequesters.Store(ecId, tenant)
}

// TakeZpListRequester returns and forgets the tenant that last asked for the metering point list of ecId.
func TakeZpListRequester(ecId string) (string, bool) {
	v, ok := zpListRequesters.LoadAndDelete(ecId)
	if !ok {
		return "", false
	}
	return v.(string), true
}

var ChangePartitionFactor = func(eeg *model.Eeg, meterReq []*model.ChangePartitionFactorRequest) error {
	operators := map[string][]model.Meter{}
	var gridId string
	//meterList := []model.Meter{}
	for _, m := range meterReq {
		gridId = eeg.GridOperator
		if m.GridOperatorId.Valid && len(m.GridOperatorId.ValueOrZero()) > 0 {
			gridId = m.GridOperatorId.String
		}
		if gridId == "" {
			return model.ErrEdaCommunication(errors.New("no Grid Operator specified"))
		}

		if _, ok := operators[gridId]; !ok {
			operators[gridId] = []model.Meter{}
		}

		operators[gridId] = append(operators[gridId],
			model.Meter{
				MeteringPoint: m.MeteringPoint,
				Direction:     m.Direction,
				Activation:    m.Activation.Unix() * 1000,
				PartFact:      m.PartFact,
			})
	}

	for k, v := range operators {
		ebmsMessage := createEbmsMessage(eeg, k, model.EBMS_REQ_CHANGE_PARTFACT)
		ebmsMessage.EcType = eeg.Area
		ebmsMessage.EcDisModel = model.AllocationModeType(eeg.AllocationMode)
		ebmsMessage.MeterList = v

		log.WithField("tenant", eeg.Id).Infof("Change Partition Factor. Grid operator %s, %d metering point(s)", k, len(v))
		Broker().SendMessage(ebmsMessage)
	}
	return nil
}

// getReceiverFrom is the grid operator a message about the metering point goes to: with several grid operators
// possible (BEG, P2P, EVA) the one of the metering point, else, or when the metering point has none, the
// community's; without either nothing is sent.
func getReceiverFrom(eeg *model.Eeg, meter *model.MeteringPoint) (string, error) {
	receiver := eeg.GridOperator
	if meter != nil && model.HasMultipleGridOperators(eeg.Area) && meter.GridOperatorId.String != "" {
		receiver = meter.GridOperatorId.String
	}
	if receiver == "" {
		return "", model.ErrEdaCommunication(errors.New("no Grid Operator specified"))
	}
	return strings.ToUpper(receiver), nil
}

// ReceiverFor is getReceiverFrom for callers that check all metering points before they write or send anything.
func ReceiverFor(eeg *model.Eeg, meter *model.MeteringPoint) (string, error) {
	return getReceiverFrom(eeg, meter)
}

func createEbmsMessage(eeg *model.Eeg, receiver string, code model.EbMsMessageType) model.EbmsMessage {
	return model.EbmsMessage{
		Sender:   strings.ToUpper(eeg.RcNumber),
		Receiver: receiver,
		//Sender:      strings.ToUpper("sepp.gaug"),
		//Receiver:    "obermueller.peter",
		MessageCode: code,
		EcId:        eeg.CommunityId,
	}
}
