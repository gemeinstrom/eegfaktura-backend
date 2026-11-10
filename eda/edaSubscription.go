package eda

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"at.ourproject/vfeeg-backend/parser"
	"at.ourproject/vfeeg-backend/services"
	"github.com/jjeffery/civil"
	"github.com/sirupsen/logrus"
)

const (
	Stornierung_nicht_moeglich                                        = 37
	Zaehlpunkt_nicht_dem_Lieferanten_zugeordnet                       = 55
	ZP_NOT_FOUND                                                      = 56
	ZP_NOT_SUPPLIED                                                   = 57
	Aenderung_Anforderung_akzeptiert                                  = 70
	INVALID_REQUEST_DATA                                              = 76
	Prozessdatum_falsch                                               = 82
	COMPETING_PROCESSES                                               = 86
	Kein_Smart_Meter                                                  = 90
	Keine_Daten_im_angeforderten_Zeitraum_vorhanden                   = 94
	MESSAGE_RECEIVED                                                  = 99
	WRONG_ENERGY_DIRECTION                                            = 104
	ZP_ALREADY_ASSIGNED                                               = 156
	ZP_ALREADY_ASSIGNED_TO_AN_OPERATOR                                = 157
	ZP_IS_NOT_ELIGIBLE                                                = 158
	Zu_Prozessdatum_ZP_inaktiv_bzw_noch_kein_Geraet_eingebaut         = 159
	Verteilmodell_entspricht_nicht_der_Vereinbarung                   = 160
	CUSTOMER_HAS_REFUSED_DATA_RELEASE                                 = 172
	CUSTOMER_DID_NOT_RESPOND_TO_DATA_RELEASE                          = 173
	Angefragte_Daten_nicht_lieferbar                                  = 174
	CONSENT_GRANTED                                                   = 175
	Zustimmung_erfolgreich_entzogen                                   = 176
	Keine_Datenfreigabe_vorhanden                                     = 177
	Consent_existiert_bereits                                         = 178
	ConsentID_abgelaufen                                              = 180
	GemeinschaftsID_nicht_vorhanden                                   = 181
	NO_REMOTELY_READABLE_METER_INSTALLED_YET                          = 182
	SUM_OF_REPORTED_ALLOCATION_KEYS_EXCEEDS_100                       = 183
	Kunde_hat_optiert                                                 = 184
	Zaehlpunkt_befindet_sich_nicht_im_Bereich_der_Energiegemeinschaft = 185
	ConsentID_und_Zaehlpunkt_passen_nicht_zusammen                    = 187
	PARTICIPATION_FACTOR_OF_100_WOULD_BE_EXCEEDED                     = 188
	Zaehlpunkt_ist_der_GemeinschaftsID_nicht_zugeordnet               = 189
	TeilnahmeLimit_wird_ueberschritten                                = 196
	CONSENT_WAS_WITHDRAWN                                             = 203
	NO_STABLE_COMMUNICATION_POSSIBLE                                  = 204
)

var (
	ECON_RESPONSE_CODES = map[int16]string{
		37:   "Stornierung nicht möglich",
		55:   "Zählpunkt nicht dem Lieferanten zugeordnet",
		56:   "Zählpunkt nicht gefunden",
		57:   "Zählpunkt nicht versorgt",
		70:   "Änderung/Anforderung akzeptiert",
		76:   "Ungültige Anforderungsdaten",
		82:   "Prozessdatum falsch",
		86:   "konkurrierende Prozesse",
		90:   "Kein Smart Meter",
		94:   "Keine Daten im angeforderten Zeitraum vorhanden",
		99:   "Meldung erhalten",
		104:  "Falsche Energierichtung",
		156:  "ZP bereits zugeordnet",
		157:  "ZP bereits einem Betreiber zugeordnet",
		158:  "ZP ist nicht teilnahmeberechtigt",
		159:  "Zu Prozessdatum ZP inaktiv bzw. noch kein Gerät eingebaut",
		160:  "Verteilmodell entspricht nicht der Vereinbarung",
		172:  "Kunde hat Datenfreigabe abgelehnt",
		173:  "Kunde hat auf Datenfreigabe nicht reagiert (Timeout)",
		174:  "Angefragte Daten nicht lieferbar",
		175:  "Zustimmung erteilt",
		176:  "Zustimmung erfolgreich entzogen",
		177:  "Keine Datenfreigabe vorhanden",
		178:  "Consent existiert bereits",
		180:  "ConsentID abgelaufen",
		181:  "Gemeinschafts-ID nicht vorhanden",
		182:  "Noch kein fernauslesbarer Zähler eingebaut",
		183:  "Summe der gemeldeten Aufteilungsschlüssel übersteigt 100%",
		184:  "Kunde hat optiert",
		185:  "Zählpunkt befindet sich nicht im Bereich der Energiegemeinschaft",
		187:  "ConsentID und Zählpunkt passen nicht zusammen",
		188:  "Teilnahmefaktor von 100 % würde überschritten werden",
		189:  "Zählpunkt ist der Gemeinschafts-ID nicht zugeordnet",
		196:  "Teilnahme-Limit wird überschritten",
		203:  "Zustimmung wurde entzogen",
		204:  "Für ZP ist derzeit keine ausreichend stabile Kommunikation möglich",
		1000: "Mail konnte nicht gesendet werden.",
	}
	REJECTED_INVALID_CODES = []int16{ZP_NOT_FOUND, ZP_NOT_SUPPLIED, INVALID_REQUEST_DATA, WRONG_ENERGY_DIRECTION,
		ZP_ALREADY_ASSIGNED_TO_AN_OPERATOR, ZP_IS_NOT_ELIGIBLE, 159, 172, 173, 177, 181, 184, 185, 188, 196, NO_STABLE_COMMUNICATION_POSSIBLE}
	REJECTED_VALID_CODES  = []int16{ZP_ALREADY_ASSIGNED}
	REJECTED_IGNORE_CODES = []int16{COMPETING_PROCESSES}
)

//func InitEdaSubscription() {
//	mqttclient.Subscribe(getSubsriptions()...)
//}

func InitEdaSubscription(ctx context.Context) {
	mqttclient.Broker().Subscribe(getSubsriptions(ctx)...)
}

func getSubsriptions(ctx context.Context) []model.Subscriptions {

	return []model.Subscriptions{
		{
			Protocol: model.CR_MSG,
			Handler: func(msg model.SubscribeMessage) {
				protocolCrMsgHandler(ctx, msg)
			},
		},
		{
			Protocol: model.CR_REQ_PT,
			Handler: func(msg model.SubscribeMessage) {
				protocolCrReqPtHandler(ctx, msg)
			},
		},
		{
			Protocol: model.EC_REQ_ONL,
			Handler: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(ctx, msg)
			},
		},
		{
			Protocol: model.EC_REQ_OFF,
			Handler: func(msg model.SubscribeMessage) {
				protocolEcReqOnlHandler(ctx, msg)
			},
		},
		{
			Protocol: model.CM_REV_IMP,
			Handler: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(ctx, msg)
			},
		},
		{
			Protocol: model.CM_REV_CUS,
			Handler: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(ctx, msg)
			},
		},
		{
			Protocol: model.CM_REV_SP,
			Handler: func(msg model.SubscribeMessage) {
				protocolCmRevImpHandler(ctx, msg)
			},
		},
		{
			Protocol: model.EC_PRTFACT_CHANGE,
			Handler: func(msg model.SubscribeMessage) {
				protocolEcPrtChangeHandler(ctx, msg)
			},
		},
		{
			Protocol: model.EC_PODLIST,
			Handler: func(msg model.SubscribeMessage) {
				protocolEcPodListHandler(ctx, msg)
			},
		},
	}
}

func protocolCrMsgHandler(ctx context.Context, msg model.SubscribeMessage) {
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v-%v", msg.Protocol, msg.MessageCode)

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.WithField("tenant", msg.Tenant).Error(err)
		return
	}

	if !forReceivingRc(ctx, db, msg, "crmsg") {
		return
	}

	eeg, err := db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
	if err != nil {
		logrus.WithField("tenant", msg.Tenant).WithError(err).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
		return
	}

	if msg.Payload.Meter != nil && msg.Payload.Energy != nil {
		for i := range msg.Payload.Energy {
			energy := msg.Payload.Energy[i]
			historyValue := map[string]interface{}{"meter": msg.Payload.Meter.MeteringPoint, "from": energy.Start, "to": energy.End}
			_ = db.SaveHistory(eeg.Id, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", "CR_MSG", historyValue)
		}
	}
	return
}

func protocolCrReqPtHandler(ctx context.Context, msg model.SubscribeMessage) {
	//var err error
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v-%v", msg.Protocol, msg.MessageCode)

	codes := []int16{}

	switch msg.MessageCode {
	case model.EBMS_ZP_RES, model.EBMS_ZP_REJ, model.EBMS_ZP_SYNC:
		codes, _, _, _ = extractResponseCodeAndMeteringPoint(&msg.Payload)
	default:
		return
	}

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.WithField("tenant", msg.Tenant).Error(err)
		return
	}

	if !forReceivingRc(ctx, db, msg, "energyrequest") {
		return
	}

	eeg, err := db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
	if err != nil {
		logrus.WithField("error", err.Error()).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
		return
	}

	_ = db.SaveNotification(eeg.Id, msg.MessageCode, msg.Payload.Meters(), convertCodes2Strings(codes), msg.Protocol)
	_ = db.SaveHistory(eeg.Id, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", msg.Protocol, msg.Payload)
}

// protocolEcReqOnlHandler executing the EC_REQ_ONL eda process (Online Meteringpoint registration). This process exist of the following steps
// - ANFORDERUNG_ECON: Initial request to open the process
// - ANTWORT_ECON: Request. The process was successfully started
// - ZUSTIMMUNG_ECON: Participant accepts the request in the user portal of the grid operator
// - ABSCHLUSS_ECON: Metering point is part of the EEG
// - ABLEHNUNG_ECON: The process was aborted
// - ABBRUCH_ECON: The membership was aborted by customer or grid operator
//
// During the process the Meteringpoint is taged with different status flags.
// - NEW: Meteringpoint is assosiated to a participant. The process is not started.
// - PENDING: ANFORDERUNG_ECON already sent and confirmed by partner with ANSWER_ECON message
// - APPROVED: ZUSTIMMUNG_ECON. Participant accept it in the grid operator portal
// - ACTIVE: ABSCHLUSS_ECON. Metering is activ
// - INVALID: Meteringpoint is rejected accourding to wrong attributes
// - REJECTED: Meteringpoint is rejected
// - ARCHIVED: Meteringpoint was archived by the participant
func protocolEcReqOnlHandler(ctx context.Context, msg model.SubscribeMessage) {
	//var err error
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v-%v", msg.Protocol, msg.MessageCode)
	getConsentId := func(consentId *string) *string {
		if consentId == nil {
			return nil
		}
		cId := strings.TrimSpace(*consentId)
		if len(cId) == 0 {
			return nil
		}
		return &cId
	}

	// the mails are queued after the status is written below, also on its early returns, but only for a message
	// that passed the receiving RC number check
	var queueMails func()
	checked := false
	defer func() {
		if checked && queueMails != nil {
			queueMails()
		}
	}()

	codes, meters, consentIds, _ := extractResponseCodeAndMeteringPoint(&msg.Payload)
	var status model.ProcessStatusType
	var statusCode *int16
	var activeSince civil.NullDate
	var consentId *string

	switch msg.MessageCode {
	case model.EBMS_ONLINE_REG_COMPLETION, model.EBMS_OFFLINE_REG_COMPLETION:
		codes = []int16{}
		completeMeters := extractMeterList(&msg.Payload)
		if len(completeMeters) == 1 {
			meters = []string{completeMeters[0].meter}
			ax := civil.DateOf(completeMeters[0].activeSince)
			activeSince = civil.NullDateFrom(&ax)
			consentId = completeMeters[0].consentId
			status = model.ACTIVE
			queueMails = func() {
				if err := meteringPointPerformAnswerMsg(ctx, sendActivationMail, msg.Payload.EcId, meters, "Dein Zählpunkt ist aktiv", "zp-complete-mail-template.toml"); err != nil {
					logrus.WithField("tenant", msg.Tenant).Errorf("complete mail message for %+v return with error. %v", meters, err.Error())
				}
			}
		} else {
			status = ""
			meters = []string{}
		}

	case model.EBMS_ONLINE_REG_REJECTION, model.EBMS_OFFLINE_REG_REJECTION:
		if codesContains(REJECTED_IGNORE_CODES, codes) {
			status = model.RESTORE
			codes = []int16{0}
		} else if codesContains(REJECTED_VALID_CODES, codes) {
			status = model.ACTIVE
			activeSince = civil.NullDate{Date: civil.Today(), Valid: true}
			codes = []int16{0}
			statusCode = &codes[0]
		} else if codesContains(REJECTED_INVALID_CODES, codes) {
			status = model.INVALID
			c := rejectionCode(intersectCodes(REJECTED_INVALID_CODES, codes))
			statusCode = &c
		} else {
			status = model.REJECTED
			if len(codes) > 0 {
				c := rejectionCode(codes)
				statusCode = &c
			}
		}
	case model.EBMS_ONLINE_REG_APPROVAL, model.EBMS_OFFLINE_REG_APPROVAL:
		for i, c := range codes {
			switch c {
			case CONSENT_GRANTED:
				status = model.APPROVED
				consentId = &consentIds[i]
			case CUSTOMER_HAS_REFUSED_DATA_RELEASE, CUSTOMER_DID_NOT_RESPOND_TO_DATA_RELEASE:
				status = model.INVALID
				statusCode = &c
			}
		}
	case model.EBMS_ONLINE_REG_ANSWER, model.EBMS_OFFLINE_REG_ANSWER:
		for _, c := range codes {
			if c == MESSAGE_RECEIVED {
				status = model.PENDING
				// one code per metering point of the message, one pass over all of them (261031-mm4)
				queueMails = func() {
					if err := meteringPointPerformAnswerMsg(ctx, sendActivationMail, msg.Payload.EcId, meters, "Aktivierung im Serviceportal", "activation-mail-template.toml"); err != nil {
						logrus.WithField("tenant", msg.Tenant).Errorf("Perform Answer Message %+v. %v", meters, err.Error())
					}
				}
			} else if c == NO_REMOTELY_READABLE_METER_INSTALLED_YET || c == SUM_OF_REPORTED_ALLOCATION_KEYS_EXCEEDS_100 {
				status = model.INVALID
				statusCode = &c
			}
		}
	case model.EBMS_ONLINE_REG_INIT, model.EBMS_OFFLINE_REG_INIT:
		meters = msg.Payload.Meters()
		codes = []int16{0}
		status = model.INIT

	case model.EBMS_ONLINE_REG_ABORT, model.EBMS_OFFLINE_REG_ABORT:
		meters = msg.Payload.Meters()
		//status = model.ABORTED
		status = model.REJECTED
	default:
		return
	}

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.WithField("tenant", msg.Tenant).Error(err)
		return
	}

	if !forReceivingRc(ctx, db, msg, "registration") {
		return
	}
	checked = true

	eeg, err := db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
	if err != nil {
		logrus.WithError(err).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
		return
	}

	if len(meters) > 0 && len(status) > 0 {
		switch status {
		case model.RESTORE:
			if err = db.RestoreMeteringPointProcessState(ctx, eeg.Id, meters[0]); err != nil {
				logrus.WithError(err).Errorf("can not restore MeteringPointProcessState")
			}
			break
		default:
			if err = db.MeteringPointsSetStatus(ctx, eeg.Id, status, statusCode, meters, activeSince.Ptr(), getConsentId(consentId)); err != nil {
				logrus.WithError(err).Errorf("can not change metering point status of meters: %+v", meters)
			}
			if err = db.SaveNotification(eeg.Id, msg.MessageCode, meters, convertCodes2Strings(codes), msg.Protocol); err != nil {
				logrus.WithError(err).Error("can not save notification")
			}
		}
	}
	_ = db.SaveHistory(eeg.Id, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", msg.Protocol, msg.Payload)
}

func protocolCmRevImpHandler(ctx context.Context, msg model.SubscribeMessage) {
	//var err error
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v Code: %s", msg.Protocol, msg.MessageCode)

	meters, _ := extractResponseCodeAndMeteringPointV2(&msg.Payload)

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.WithField("tenant", msg.Tenant).Error(err)
		return
	}

	var eeg *model.Eeg
	// tenants receive notification and history; one RC number can own several tenants (GEA)
	var tenants []string
	// notify is false when the message is only recorded in the history (revocation not applied)
	notify := true
	// mailTenants get the "no longer part of the community" mail (platform#116): CCMI/CCMC only
	// where the metering point really went from ACTIVE to INACTIVE, an EEG deregistration only with
	// the grid operator's confirmation (ANTWORT_CCMS 176)
	var mailTenants []string
	// CCMC/CCMI carry no community id and are applied within the receiving RC number (MeteringPointRevokeByConsentId);
	// every other message finds its community by the community id and is checked against the receiving RC number
	if msg.MessageCode != model.EBMS_AUFHEBUNG_CCMC && msg.MessageCode != model.EBMS_AUFHEBUNG_CCMI &&
		!forReceivingRc(ctx, db, msg, "revoke") {
		return
	}
	switch msg.MessageCode {
	case model.EBMS_AUFHEBUNG_CCMS:
		// the community's own deregistration (eda-xp's echo of the sent request): the metering point stays as it
		// is until the grid operator answers (platform#238); notification and history only
		eeg, err = db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
		if err != nil {
			logrus.WithField("tenant", msg.Tenant).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
			return
		}

		if len(meters) == 0 {
			logrus.WithField("tenant", msg.Tenant).Errorf("no metering point in %s message -> conversation %s", msg.MessageCode, msg.Payload.ConversationId)
			return
		}

	case model.EBMS_ABLEHNUNG_CCMS:
		// The grid operator rejected the deregistration. With 177/180 there is no data release (any more) and the
		// metering point becomes INACTIVE; with any other code it stays as it is. The rejection is always
		// persisted as a notification (see below) and stays visible to the user.
		eeg, err = db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
		if err != nil {
			logrus.WithField("tenant", msg.Tenant).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
			return
		}
		inactiveSince, mail, ok := revokeAfterAnswer(ctx, db, eeg.Id, msg, meters)
		if !ok {
			return
		}
		if mail {
			mailTenants = []string{eeg.Id}
			meters[0].consentEnd = inactiveSince // the date in the mail
		}

	case model.EBMS_AUFHEBUNG_CCMC, model.EBMS_AUFHEBUNG_CCMI:

		if len(meters) == 0 {
			logrus.WithField("tenant", msg.Tenant).Errorf("no metering point in %s message -> conversation %s", msg.MessageCode, msg.Payload.ConversationId)
			return
		}

		// msg.Tenant is the receiving RC number (MQTT topic); it decides when the metering point
		// is assigned in several communities.
		receiver := strings.ToUpper(msg.Tenant)
		var revoked []database.RevokedTenant
		if revoked, err = db.MeteringPointRevokeByConsentId(ctx, receiver, meters[0].consentId, meters[0].meter, meters[0].consentEnd); err != nil {
			logrus.WithField("tenant", msg.Tenant).Errorf("can not revoke metering point %+v - %+v", meters, err)
			// keep the message visible in the history of the receiving community
			if tenants, err = db.GetTenantsByRcNumber(ctx, receiver); err != nil || len(tenants) == 0 {
				logrus.WithField("tenant", msg.Tenant).Errorf("can not fetch eeg by rc number %s (REVOKE metering point) - %v", msg.Tenant, err)
				return
			}
			notify = false
		} else {
			tenants = database.TenantNames(revoked)
			for _, r := range revoked {
				if r.WasActive {
					mailTenants = append(mailTenants, r.Tenant)
				}
			}
		}

	case model.EBMS_ANTWORT_CCMS:
		// fetch the EEG for every answer, so answers without code 176 reach the history as well
		eeg, err = db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
		if err != nil {
			logrus.WithField("tenant", msg.Tenant).Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
			return
		}
		// only code 176 (consent ended) revokes and notifies; other answers go to the history only
		notify = len(meters) > 0 && codesContains([]int16{Zustimmung_erfolgreich_entzogen}, meters[0].codes)
		inactiveSince, mail, ok := revokeAfterAnswer(ctx, db, eeg.Id, msg, meters)
		if !ok {
			return
		}
		if mail {
			mailTenants = []string{eeg.Id}
			meters[0].consentEnd = inactiveSince // the date in the mail
		}
	default:
		return
	}

	if eeg != nil {
		tenants = []string{eeg.Id}
	}
	if len(tenants) == 0 {
		logrus.WithField("tenant", msg.Tenant).Errorf("no tenant for message -> conversation %s", msg.Payload.ConversationId)
		return
	}
	for _, tenant := range tenants {
		if notify && len(meters) > 0 {
			err = db.SaveNotification(tenant, msg.MessageCode, []string{meters[0].meter}, convertCodes2Strings(meters[0].codes), msg.Protocol)
			if err != nil {
				logrus.WithError(err).Error("can not save notification")
			}
		}
		_ = db.SaveHistory(tenant, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", msg.Protocol, msg.Payload)
	}
	if len(mailTenants) > 0 && len(meters) > 0 {
		sendMeteringPointInactiveMails(ctx, db, mailTenants, meters[0].meter, meters[0].consentEnd, inactiveReasonFor(msg.MessageCode))
	}
}

func protocolEcPrtChangeHandler(ctx context.Context, msg model.SubscribeMessage) {
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v Code: %s", msg.Protocol, msg.MessageCode)

	var meters []model.Meter
	var errCode int16
	switch msg.MessageCode {
	case model.EBMS_REJ_CHANGE_PARTFACT:
		if len(msg.Payload.ResponseData) > 0 && len(msg.Payload.ResponseData[0].ResponseCode) > 0 {
			errCode = msg.Payload.ResponseData[0].ResponseCode[0]
		} else {
			errCode = 1000
		}
		break
	case model.EBMS_ANS_CHANGE_PARTFACT:
		meters = msg.Payload.MeterList
		errCode = 0
		break
	case model.EBMS_REQ_CHANGE_PARTFACT:
		meters = nil
		break
	default:
		logrus.WithField("tenant", msg.Tenant).Warnf("Unknown Messagecode: %v", msg.MessageCode)
		return
	}

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.Error(err)
		return
	}

	if !forReceivingRc(ctx, db, msg, "partfact") {
		return
	}

	eeg, err := db.GetEegByEcIdAndMeters(ctx, msg.Payload.EcId, messageMeters(&msg.Payload))
	if err != nil {
		logrus.Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
		return
	}

	if len(meters) > 0 && errCode == 0 {
		// no participant in the EDA answer: the current entry of each metering point
		if err := db.MeteringPointChangePartFactor(ctx, eeg.Id, "", meters); err != nil {
			logrus.WithField("tenant", eeg.Id).Errorf("can not change partition factor. %v", err)
			return
		}
	}

	if errCode > 0 {
		_ = db.SaveNotification(eeg.Id, msg.MessageCode, getMeterIdSlice(meters), convertCodes2Strings([]int16{errCode}), msg.Protocol)
	}
	_ = db.SaveHistory(eeg.Id, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", msg.Protocol, msg.Payload)
}

func protocolEcPodListHandler(ctx context.Context, msg model.SubscribeMessage) {
	logrus.WithField("tenant", msg.Tenant).Printf("Handle Subscriptions: %+v Code: %s", msg.Protocol, msg.MessageCode)

	db, err := database.GetDB(ctx)
	if err != nil {
		logrus.Error(err)
		return
	}

	historyTenant := msg.Tenant
	switch msg.MessageCode {
	case model.EBMS_ZP_LIST:
	case model.EBMS_ZP_LIST_RESPONSE:
		if !forReceivingRc(ctx, db, msg, "podlist") {
			return
		}
		buf, err := database.ExportZPListToExcel(ctx, &msg.Payload)
		if err != nil {
			return
		}

		eeg, err := zpListEeg(ctx, db, msg.Payload.EcId)
		if err != nil {
			logrus.Errorf("can not fetch eeg with message -> conversation %s", msg.Payload.ConversationId)
			return
		}
		historyTenant = eeg.Id

		// the list covers the whole community: every tenant of the community id updates the metering points it holds
		// (a GEA has several, 261028-mm1; a sole tenant may be named unlike the RC number of the topic)
		syncTenants := []string{msg.Tenant}
		if tenants, err := db.TenantsByEcId(ctx, msg.Payload.EcId); err != nil {
			logrus.WithField("tenant", msg.Tenant).WithError(err).Error("can not fetch the tenants of the community id")
		} else if len(tenants) > 0 {
			syncTenants = tenants
		}
		dropInvalidCodes(ctx, eeg.Id, &msg.Payload)
		for _, tenant := range syncTenants {
			if err = services.SyncMeteringPoints(tenant, &msg.Payload); err != nil {
				logrus.WithField("tenant", tenant).Error(err)
			}
		}
		zpListCommunityData(ctx, db, eeg, &msg.Payload)

		if eeg.Email.Valid {
			now := time.Now()
			attm := &services.Attachment{
				Type:        "DEFAULT",
				Filename:    fmt.Sprintf("%s_%.4d%.2d%.2d-%.2d%.2d_ZP_PODLIST.xlsx", eeg.RcNumber, now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute()),
				Filecontent: buf,
				MimeType:    "application/vnd.ms-excel",
				ContentId:   nil,
			}
			var b bytes.Buffer
			b.WriteString(fmt.Sprintf("Zählpunktlist für EEG - %s", eeg.RcNumber))
			send := sendZpListMail
			mailWorker.Enqueue(services.MailJob{Ctx: ctx, Notifier: db, Attempts: []services.MailAttempt{{
				Tenant:     eeg.Id,
				Identifier: "Zählpunktliste",
				Send: func() error {
					return send(fmt.Sprintf("%s@eegfaktura.at", eeg.RcNumber),
						eeg.Email.String,
						fmt.Sprintf("%s Zählpunktliste %.4d%.2d%.2d-%.2d%.2d", eeg.RcNumber, now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute()), nil, &b, attm)
				},
			}}})
		}

	case model.EBMS_ZP_LIST_REJECTION:
		// the request is answered: a later list must not go to this requester (261028-mm1). Only for a community id
		// of the receiving RC number; without one there is no requester to clear, the history entry stays.
		if msg.Payload.EcId != "" && forReceivingRc(ctx, db, msg, "podlist") {
			mqttclient.TakeZpListRequester(msg.Payload.EcId)
		}
	default:
		logrus.WithField("tenant", msg.Tenant).Warnf("Unknown Messagecode: %v", msg.MessageCode)
		return
	}
	_ = db.SaveHistory(historyTenant, msg.MessageCode, msg.Payload.ConversationId, "ADMIN", "IN", msg.Protocol, msg.Payload)
}

// forReceivingRc: an inbound message is processed only for a community id of the RC number it came in for (msg.Tenant,
// from the MQTT topic); in a GEA several tenants share the RC number and the community id. A message without a
// community id, or with one of another RC number, is dropped with a warning (event.action eda.<process>.*).
func forReceivingRc(ctx context.Context, db database.Database, msg model.SubscribeMessage, process string) bool {
	entry := logrus.WithContext(ctx).WithField("tenant", msg.Tenant)
	if msg.Payload.EcId == "" {
		entry.WithField("event.action", "eda."+process+".no_community_id").
			Warnf("%s message without a community id, dropped", msg.MessageCode)
		return false
	}
	ok, err := db.EcIdBelongsToRc(ctx, msg.Payload.EcId, msg.Tenant)
	if err != nil {
		entry.WithField("event.action", "eda."+process+".community_check_failed").WithError(err).
			Errorf("can not check the community id of the %s message", msg.MessageCode)
		return false
	}
	if !ok {
		entry.WithField("event.action", "eda."+process+".foreign_community").WithField("ecId", msg.Payload.EcId).
			Warnf("%s message for a community id of another RC number, dropped", msg.MessageCode)
	}
	return ok
}

// zpListCommunityData takes the community data of a metering point list (platform#229): ECType RC_L/RC_R switches
// LOCAL and REGIONAL (the switch clears the Nahebereich), any other mismatch with the community type is logged; then
// the Nahebereich goes to every tenant of the community id, kept when the list has none, an invalid value is
// dropped. TechCode/FuelCode go with the metering points (SyncMeteringPoints).
func zpListCommunityData(ctx context.Context, db database.Database, eeg *model.Eeg, p *model.EbmsMessage) {
	entry := logrus.WithContext(ctx).WithField("tenant", eeg.Id)
	// the type first: a switch without ECZoneLevel in the list leaves the Nahebereich empty, not the old type's
	// (eegfaktura-dev#56 #10); with one the list's level is set below
	zpListArea(ctx, db, eeg, p, entry)
	if p.EcZoneLevel != nil {
		// UpdateEcZoneLevel refuses an invalid level
		if err := db.UpdateEcZoneLevel(ctx, p.EcId, *p.EcZoneLevel); err != nil {
			entry.WithField("event.action", "eda.podlist.zone_level_dropped").WithError(err).
				Warn("ECZoneLevel of the metering point list not stored")
		}
	}
}

// zpListArea: RC_L/RC_R from the grid operator decide between LOCAL and REGIONAL (decided 05.11.2026); every other
// mismatch is only a warning.
func zpListArea(ctx context.Context, db database.Database, eeg *model.Eeg, p *model.EbmsMessage, entry *logrus.Entry) {
	if p.EcTypeEda == "" {
		return
	}
	if listArea, ok := eegAreaFromList[p.EcTypeEda]; ok && (eeg.Area == model.LOCAL || eeg.Area == model.REGIONAL) {
		changes, err := db.UpdateEegAreaFromList(ctx, p.EcId, listArea)
		if err != nil {
			entry.WithField("event.action", "eeg.area.update_from_list_failed").WithError(err).
				Error("can not take the community type from the metering point list")
		}
		for _, c := range changes {
			logrus.WithContext(ctx).WithFields(logrus.Fields{"tenant": c.Tenant, "event.action": "eeg.area.updated_from_list",
				"eeg.area.old": string(c.Old), "eeg.area.new": string(listArea)}).Info("community type taken from the metering point list")
		}
		return
	}
	if want, ok := model.EdaEcType(eeg.Area); !ok || want != p.EcTypeEda {
		entry.WithField("event.action", "eda.podlist.ectype_mismatch").
			Warnf("metering point list with ECType %s, the community type %s means %s: check the type", p.EcTypeEda, eeg.Area, want)
	}
}

// dropInvalidCodes drops a TechCode/FuelCode that model.ValidCode refuses (e.g. longer than 10 characters) with a
// warning; never stored truncated. An empty code means absent.
func dropInvalidCodes(ctx context.Context, tenant string, p *model.EbmsMessage) {
	for i := range p.MeterList {
		m := &p.MeterList[i]
		for name, code := range map[string]*string{"TechCode": &m.TechCode, "FuelCode": &m.FuelCode} {
			if *code != "" && !model.ValidCode(*code) {
				// without the metering point number (eegfaktura-dev#56 #12): the conversation finds the list in the history
				logrus.WithContext(ctx).WithFields(logrus.Fields{"tenant": tenant, "event.action": "eda.podlist.code_dropped"}).
					Warnf("metering point list with an invalid %s (%d characters), not stored, conversation %s",
						name, utf8.RuneCountInString(*code), p.ConversationId)
				*code = ""
			}
		}
	}
}

// eegAreaFromList: the ECTypes of a metering point list that set the community type (LOCAL/REGIONAL only).
var eegAreaFromList = map[string]model.AreaType{"RC_L": model.LOCAL, "RC_R": model.REGIONAL}

// zpListEeg returns the community a metering point list belongs to. In a GEA the list covers every tenant of the
// community id, so its mail goes to the tenant that asked for it — never to all, each would get the other tenants'
// members — else to the GEA's base tenant (decided 28.10.2026, 261028-mm1). One tenant: as before.
func zpListEeg(ctx context.Context, db database.Database, ecId string) (*model.Eeg, error) {
	requester, asked := mqttclient.TakeZpListRequester(ecId)
	tenants, err := db.TenantsByEcId(ctx, ecId)
	if err != nil || len(tenants) < 2 {
		return db.GetEegByEcId(ctx, ecId)
	}
	for _, t := range tenants {
		if asked && t == requester {
			return db.GetEegById(ctx, t)
		}
	}
	return db.GetBaseEegByEcId(ctx, ecId)
}

// messageMeters returns the metering points of an inbound message (meter, meter list and response data), used
// to find the tenant of a GEA the message belongs to (261028-mm1).
func messageMeters(p *model.EbmsMessage) []string {
	meters := p.Meters()
	for _, rd := range p.ResponseData {
		if rd.MeteringPoint != "" {
			meters = append(meters, rd.MeteringPoint)
		}
	}
	return meters
}

func getMeterIdSlice(meters []model.Meter) []string {
	ms := make([]string, len(meters))
	for _, m := range meters {
		ms = append(ms, m.MeteringPoint)
	}
	return ms
}

func meteringPointPerformAnswerMsg(ctx context.Context, sendMail services.SendMailFunc, ecId string, meterId []string, subject, templateConfigName string) error {

	db, err := database.GetDB(context.Background())
	if err != nil {
		return err
	}

	eeg, err := db.GetEegByEcIdAndMeters(ctx, ecId, meterId)
	if err != nil {
		return err
	}

	meterFilter := func(meters []*model.MeteringPoint, f func(string) bool) []*model.MeteringPoint {
		filtered := make([]*model.MeteringPoint, 0)
		for _, m := range meters {
			if f(m.MeteringPoint) {
				filtered = append(filtered, m)
			}
		}
		return filtered
	}

	for _, mid := range meterId {
		participant, err := db.FindParticipantByMeteringPoint(ctx, eeg.Id, mid)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			} else {
				logrus.WithField("tenant", eeg.Id).Warn(err)
			}
		}

		if participant != nil && participant.Contact.Email.Valid {

			participant.MeteringPoint = meterFilter(participant.MeteringPoint, func(s string) bool {
				return s == mid
			})

			if len(participant.MeteringPoint) > 0 {
				mailWorker.Enqueue(services.MailJob{Ctx: ctx, Notifier: db, Attempts: []services.MailAttempt{{
					Tenant:     eeg.Id,
					Identifier: mid,
					Send: func() error {
						return parser.SendActivationMailFromTemplate(sendMail, db, eeg.Id, subject, eeg, participant, templateConfigName)
					},
				}}})
			} else {
				logrus.WithField("tenant", eeg.Id).Warn("No MeteringPoint for activation mail")
			}
		}
	}
	return nil
}
