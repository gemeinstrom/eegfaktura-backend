package eda

import (
	"at.ourproject/vfeeg-backend/model"
	"errors"
	"fmt"
	"github.com/jjeffery/civil"
	"slices"
	"time"
)

type responseCodesPerMeter struct {
	meter      string
	codes      []int16
	consentEnd civil.Date
	consentId  *string
}

type completionMeters struct {
	meter       string
	activeSince time.Time
	consentId   *string
}

func getConsentIdPtr(consentId string) *string {
	if len(consentId) == 0 {
		return nil
	}
	return &consentId
}

func extractResponseCodeAndMeteringPoint(ebmsMessage *model.EbmsMessage) ([]int16, []string, []string, error) {
	meters := []string{}
	consentIds := []string{}
	codes := []int16{}
	for _, rd := range ebmsMessage.ResponseData {
		if len(rd.ResponseCode) > 0 {
			meters = append(meters, rd.MeteringPoint)
			codes = append(codes, rd.ResponseCode...)
			consentIds = append(consentIds, rd.ConsentId)
		}
	}

	if len(codes) == 0 {
		return codes, meters, consentIds, errors.New("wrong Response from EDA")
	}

	return codes, meters, consentIds, nil
}

func extractResponseCodeAndMeteringPointV2(ebmsMessage *model.EbmsMessage) ([]responseCodesPerMeter, error) {
	codes := []int16{}
	response := []responseCodesPerMeter{}

	consentId := func(id string) *string {
		if len(id) == 0 {
			return nil
		}
		return &id
	}
	// fromMeterElement is the metering point of the <meter> element with the given codes
	fromMeterElement := func(codes []int16) []responseCodesPerMeter {
		return []responseCodesPerMeter{{
			meter:      ebmsMessage.Meter.MeteringPoint,
			codes:      codes,
			consentEnd: civil.DateOf(time.UnixMilli(ebmsMessage.ConsentEnd)),
			consentId:  consentId(ebmsMessage.Meter.ConsentID),
		}}
	}

	switch ebmsMessage.MessageCode {
	case model.EBMS_AUFHEBUNG_CCMS, model.EBMS_ABLEHNUNG_CCMS:
		// only the echo of the community's own request gets the synthetic 99
		if ebmsMessage.MessageCode == model.EBMS_AUFHEBUNG_CCMS && ebmsMessage.Meter != nil {
			codes = append(codes, 99)
			response = fromMeterElement([]int16{99})
		} else {
			// A rejection (ABLEHNUNG_CCMS) carries the metering point and reason codes in ResponseData; build from
			// there so the rejection can still be recorded (and to avoid a nil-pointer dereference on
			// ebmsMessage.Meter). A <meter> element only stands in for a metering point missing there; its codes
			// are never the reason (platform#238).
			for _, rd := range ebmsMessage.ResponseData {
				if len(rd.ResponseCode) > 0 {
					codes = append(codes, rd.ResponseCode...)
				}
				meter := rd.MeteringPoint
				if meter == "" && ebmsMessage.Meter != nil {
					meter = ebmsMessage.Meter.MeteringPoint
				}
				response = append(response, responseCodesPerMeter{
					meter:      meter,
					codes:      rd.ResponseCode,
					consentEnd: civil.DateOf(time.UnixMilli(rd.ConsentEnd)),
					consentId:  consentId(rd.ConsentId),
				})
			}
			// no ResponseData: the metering point without a reason, so the status stays and the rejection is recorded
			if len(response) == 0 && ebmsMessage.Meter != nil {
				response = fromMeterElement([]int16{})
			}
		}
	default:
		for _, rd := range ebmsMessage.ResponseData {
			if len(rd.ResponseCode) > 0 {
				codes = append(codes, rd.ResponseCode...)
			}

			response = append(response, responseCodesPerMeter{
				meter:      rd.MeteringPoint,
				codes:      codes,
				consentEnd: civil.DateOf(time.UnixMilli(rd.ConsentEnd)),
				consentId:  consentId(rd.ConsentId),
			})
		}
	}

	if len(codes) == 0 {
		return response, errors.New("wrong Response from EDA")
	}

	return response, nil
}

func extractMeterList(ebmsMessage *model.EbmsMessage) []*completionMeters {
	meters := []*completionMeters{}
	for _, m := range ebmsMessage.MeterList {
		meters = append(meters, &completionMeters{meter: m.MeteringPoint, activeSince: time.UnixMilli(m.From), consentId: getConsentIdPtr(m.ConsentID)})
	}
	return meters
}

func codesContains(expected, codes []int16) bool {
	return len(intersectCodes(expected, codes)) > 0
}

// rejectionCode picks the code stored for an ABLEHNUNG: the first candidate as before, but when it is
// one of model.REJECTED_KEEP_ACTIVE_CODES and another candidate concerns the metering point itself,
// that one wins. The DAO keeps an ACTIVE metering point ACTIVE by the stored code, so this holds only
// when all candidate codes concern the request. Candidates are the codes the old logic considered
// (INVALID: the REJECTED_INVALID_CODES intersection, REJECTED: all codes), so web keeps a known reason.
func rejectionCode(candidates []int16) int16 {
	for _, c := range candidates {
		if !slices.Contains(model.REJECTED_KEEP_ACTIVE_CODES, c) {
			return c
		}
	}
	return candidates[0]
}

func intersectCodes(expected, codes []int16) []int16 {
	var intersect []int16
	for _, element1 := range codes {
		for _, element2 := range expected {
			if element1 == element2 {
				intersect = append(intersect, element1)
			}
		}
	}
	return intersect
}

func convertCodes2Strings(codes []int16) []string {
	strCodes := []string{}
	for _, c := range codes {
		sc, ok := ECON_RESPONSE_CODES[c]
		if !ok {
			sc = fmt.Sprintf("%d", c)
		}
		strCodes = append(strCodes, sc)
	}
	return strCodes
}
