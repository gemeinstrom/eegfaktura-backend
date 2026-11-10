package model

var (
	ECON_RESPONSE_CODES = map[int16]string{
		99:  "Meldung erhalten",
		182: "Noch kein fernauslesbarer Zähler eingebaut",
		183: "Summe der gemeldeten Aufteilungsschlüssel übersteigt 100%",

		175: "Zustimmung erteilt",

		56:  "Zählpunkt nicht gefunden",
		184: "Kunde hat optiert",
		177: "Keine Datenfreigabe vorhanden",
		160: "Verteilmodell entspricht nicht der Vereinbarung",
		159: "Zu Prozessdatum ZP inaktiv bzw. noch kein Gerät eingebaut",
		158: "ZP ist nicht teilnahmeberechtigt",
		157: "ZP bereits einem Betreiber zugeordnet",
		156: "ZP bereits zugeordnet",
		86:  "konkurrierende Prozesse",
		181: "Gemeinschafts-ID nicht vorhanden",
		178: "Consent existiert bereits",
		174: "Angefragte Daten nicht lieferbar",
		173: "Kunde hat auf Datenfreigabe nicht reagiert (Timeout)",
		172: "Kunde hat Datenfreigabe abgelehnt",
		76:  "Ungültige Anforderungsdaten",
		57:  "Zählpunkt nicht versorgt",
		185: "Zählpunkt befindet sich nicht im Bereich der Energiegemeinschaft",
		37:  "Stornierung nicht möglich",

		55: "Zählpunkt nicht dem Lieferanten zugeordnet",
		70: "Änderung/Anforderung akzeptiert",
		82: "Prozessdatum falsch",
		90: "Kein Smart Meter",
		94: "Keine Daten im angeforderten Zeitraum vorhanden",
	}
	REJECTED_INVALID_CODES = []int16{56, 184, 177, 159, 158, 156, 86}
	// REJECTED_KEEP_ACTIVE_CODES: rejection codes that concern the request, not the metering point
	// itself — 172/173 consent refused / not answered, 183 sum of allocation keys > 100 %, 188
	// participation factor > 100 %. Such a rejection leaves an ACTIVE metering point ACTIVE
	// (REVOKED and other non-registration states stay); other codes (56 not found, 57 not supplied, 185 not in the community area, …) set
	// INVALID/REJECTED as before,
	// so the admin sees the inconsistency (konzept-zaehlpunkt-ausblenden.md, part 2, decision 24.10.2026).
	REJECTED_KEEP_ACTIVE_CODES = []int16{172, 173, 183, 188}
)

type EbMsMessageType string

const (
	EBMS_ENERGY_FILE_RESPONSE   EbMsMessageType = "DATEN_CRMSG"
	EBMS_ONLINE_REG_INIT        EbMsMessageType = "ANFORDERUNG_ECON"
	EBMS_ONLINE_REG_ANSWER      EbMsMessageType = "ANTWORT_ECON"
	EBMS_ONLINE_REG_REJECTION   EbMsMessageType = "ABLEHNUNG_ECON"
	EBMS_ONLINE_REG_APPROVAL    EbMsMessageType = "ZUSTIMMUNG_ECON"
	EBMS_ONLINE_REG_COMPLETION  EbMsMessageType = "ABSCHLUSS_ECON"
	EBMS_ONLINE_REG_ABORT       EbMsMessageType = "ABBRUCH_ECON"
	EBMS_ZP_LIST                EbMsMessageType = "ANFORDERUNG_ECP"
	EBMS_ZP_LIST_RESPONSE       EbMsMessageType = "SENDEN_ECP"
	EBMS_ZP_LIST_REJECTION      EbMsMessageType = "ABLEHNUNG_ECP"
	EBMS_ZP_SYNC                EbMsMessageType = "ANFORDERUNG_PT"
	EBMS_ZP_RES                 EbMsMessageType = "ANTWORT_PT"
	EBMS_ZP_REJ                 EbMsMessageType = "ABLEHNUNG_PT"
	EBMS_AUFHEBUNG_CCMI         EbMsMessageType = "AUFHEBUNG_CCMI"
	EBMS_AUFHEBUNG_CCMS         EbMsMessageType = "AUFHEBUNG_CCMS"
	EBMS_AUFHEBUNG_CCMC         EbMsMessageType = "AUFHEBUNG_CCMC"
	EBMS_ABLEHNUNG_CCMS         EbMsMessageType = "ABLEHNUNG_CCMS"
	EBMS_ANTWORT_CCMS           EbMsMessageType = "ANTWORT_CCMS"
	EBMS_REQ_CHANGE_PARTFACT    EbMsMessageType = "ANFORDERUNG_CPF"
	EBMS_ANS_CHANGE_PARTFACT    EbMsMessageType = "ANTWORT_CPF"
	EBMS_REJ_CHANGE_PARTFACT    EbMsMessageType = "ABLEHNUNG_CPF"
	EBMS_OFFLINE_REG_INIT       EbMsMessageType = "ANFORDERUNG_ECOF"
	EBMS_OFFLINE_REG_ANSWER     EbMsMessageType = "ANTWORT_ECOF"
	EBMS_OFFLINE_REG_REJECTION  EbMsMessageType = "ABLEHNUNG_ECOF"
	EBMS_OFFLINE_REG_APPROVAL   EbMsMessageType = "ZUSTIMMUNG_ECOF"
	EBMS_OFFLINE_REG_COMPLETION EbMsMessageType = "ABSCHLUSS_ECOF"
	EBMS_OFFLINE_REG_ABORT      EbMsMessageType = "ABBRUCH_ECOF"
	EBMS_EEG_BASE_DATA          EbMsMessageType = "ANFORDERUNG_GN"
	EBMS_ERROR_MESSAGE          EbMsMessageType = "ERROR_MESSAGE"
	EBMS_ERROR_PROCESS          EbMsMessageType = "ERROR_PROCESS"
)

type EdaProtocol string

const (
	CR_MSG_ORG        EdaProtocol = "CR_MSG"
	CR_MSG            EdaProtocol = "CR_MSG_HISTORY"
	CR_REQ_PT         EdaProtocol = "CR_REQ_PT"
	EC_PODLIST        EdaProtocol = "EC_PODLIST"
	EC_REQ_ONL        EdaProtocol = "EC_REQ_ONL"
	EC_REQ_OFF        EdaProtocol = "EC_REQ_OFF"
	CM_REV_IMP        EdaProtocol = "CM_REV_IMP"
	CM_REV_CUS        EdaProtocol = "CM_REV_CUS"
	CM_REV_SP         EdaProtocol = "CM_REV_SP"
	EC_PRT_CHANGE     EdaProtocol = "EC_PRT_CHANGE"
	EC_PRTFACT_CHANGE EdaProtocol = "EC_PRTFACT_CHANGE"
	ERROR             EdaProtocol = "ERROR"
)

type Timeline struct {
	From int64 `json:"from"` // Date
	To   int64 `json:"to"`   // Date
}

type EnergyValue struct {
	From   int64   `json:"from"`
	To     int64   `json:"to,omitempty"`
	Method string  `json:"method,omitempty"`
	Value  float64 `json:"value"`
}

type EnergyData struct {
	MeterCode string        `json:"meterCode"`
	Value     []EnergyValue `json:"value"`
}

type Energy struct {
	Start     int64        `json:"start"`
	End       int64        `json:"end"`
	Interval  string       `json:"interval"`
	NInterval int64        `json:"NInterval"`
	Data      []EnergyData `json:"data"`
}

type Meter struct {
	MeteringPoint string        `json:"meteringPoint"`
	Direction     DirectionType `json:"direction,omitempty"`
	Activation    int64         `json:"activation,omitempty"`
	PartFact      int           `json:"partFact,omitempty"`
	From          int64         `json:"from,omitempty"`
	To            int64         `json:"to,omitempty"`
	PlantCategory string        `json:"plantCategory,omitempty"`
	Share         float64       `json:"share,omitempty"`
	ConsentID     string        `json:"consentId,omitempty"`
	TechCode      string        `json:"techCode,omitempty"` // inbound ECMPList: TechCode of the metering point (platform#229)
	FuelCode      string        `json:"fuelCode,omitempty"` // inbound ECMPList: FuelCode of the metering point (platform#229)
}

type ResponseData struct {
	MeteringPoint string  `json:"meteringPoint,omitempty"`
	ResponseCode  []int16 `json:"responseCode"`
	ConsentEnd    int64   `json:"consentEnd,omitempty"`
	ConsentId     string  `json:"consentId,omitempty"`
}

type EdaHistoryData struct {
	Meter        string   `json:"meter"`
	ResponseCode []string `json:"responseCode"`
	To           int64    `json:"to,omitempty"`
	From         int64    `json:"from,omitempty"`
	Method       string   `json:"method,omitempty"`
}

type EbmsMessage struct {
	ConversationId     string             `json:"conversationId"`
	MessageId          string             `json:"messageId,omitempty"`
	Sender             string             `json:"sender"`
	Receiver           string             `json:"receiver"`
	MessageCode        EbMsMessageType    `json:"messageCode"`
	MessageCodeVersion string             `json:"messageCodeVersion"`
	RequestId          string             `json:"requestId,omitempty"`
	Meter              *Meter             `json:"meter,omitempty"`
	EcId               string             `json:"ecId,omitempty"` // Community ID
	EcType             AreaType           `json:"ecType,omitempty"`
	EcDisModel         AllocationModeType `json:"ecDisModel,omitempty"`
	ResponseData       []ResponseData     `json:"responseData,omitempty"`
	Energy             []*Energy          `json:"energy,omitempty"`
	Timeline           *Timeline          `json:"timeline,omitempty"`
	MeterList          []Meter            `json:"meterList,omitempty"`
	ErrorMessage       string             `json:"errorMessage,omitempty"`
	ConsentEnd         int64              `json:"consentEnd,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	EcZoneLevel        *int               `json:"ecZoneLevel,omitempty"` // inbound ECMPList: Nahebereich 1–7 (platform#229)
	EcTypeEda          string             `json:"ecTypeEda,omitempty"`   // inbound ECMPList: ECType as received, e.g. "PP"
}

func (ebms EbmsMessage) Meters() []string {
	if ebms.Meter != nil {
		return []string{ebms.Meter.MeteringPoint}
	}
	if ebms.MeterList != nil && len(ebms.MeterList) > 0 {
		meters := []string{}
		for _, m := range ebms.MeterList {
			meters = append(meters, m.MeteringPoint)
		}
		return meters
	}
	return []string{}
}

func (ebms EbmsMessage) HistoryData() []EdaHistoryData {
	data := []EdaHistoryData{}
	for _, m := range ebms.Meters() {
		data = append(data, EdaHistoryData{
			Meter:        m,
			ResponseCode: ebms.ResponseCodes(),
			To:           0,
			From:         0,
			Method:       "",
		})
	}
	return data
}

func (ebms EbmsMessage) ResponseCodes() []string {
	codes := []string{}
	for _, r := range ebms.ResponseData {
		for _, c := range r.ResponseCode {
			codes = append(codes, ECON_RESPONSE_CODES[c])
		}
	}
	if len(codes) == 0 {
		return nil
	}
	return codes
}

// SubscribeMessage aggregates the result from subscribing.
type SubscribeMessage struct {
	// Reports the index of corresponding SubscribeTopic.
	MessageCode EbMsMessageType

	Protocol EdaProtocol

	// Determine the tenantId.
	Tenant string

	// Reports the payload content.
	Payload EbmsMessage
}

type SubscribeHandler func(msg SubscribeMessage)

type Subscriptions struct {
	Protocol EdaProtocol
	Handler  SubscribeHandler
}
