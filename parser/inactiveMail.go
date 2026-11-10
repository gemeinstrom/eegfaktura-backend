package parser

import (
	"fmt"

	"at.ourproject/vfeeg-backend/mailtemplate"
	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/jjeffery/civil"
)

// InactiveMailTemplate is the template config of the mail that tells a member that a metering point
// is no longer part of the community (platform#116).
const InactiveMailTemplate = "zp-inactive-mail-template.toml"

// InactiveMailSubject is the subject of that mail.
const InactiveMailSubject = "Dein Zählpunkt ist nicht mehr Teil der Energiegemeinschaft"

// InactiveMailData is what the inactive mail template renders.
type InactiveMailData struct {
	Eeg           *model.Eeg
	Participant   *model.EegParticipant
	MeteringPoint string
	Direction     string // "Bezug" / "Einspeisung", empty when unknown
	EndDate       string // TT.MM.JJJJ
	Reason        string // one sentence: who ended the data release
}

// FormatDate renders a date as TT.MM.JJJJ; "" for a missing date (zero value or the Unix epoch,
// which is what a missing consentEnd in an EDA message turns into).
func FormatDate(d civil.Date) string {
	if d.Year() <= 1970 {
		return ""
	}
	return fmt.Sprintf("%02d.%02d.%04d", d.Day(), int(d.Month()), d.Year())
}

// DirectionText renders the direction of a metering point for members.
func DirectionText(d model.DirectionType) string {
	switch d {
	case model.CONSUMPTION:
		return "Bezug"
	case model.GENERATOR:
		return "Einspeisung"
	default:
		return ""
	}
}

// SendMeteringPointInactiveMail renders the inactive mail and sends it to the member, with the
// community in Cc. A member without e-mail address gets no mail.
// store holds the community's customised mails; nil sends the legacy template.
func SendMeteringPointInactiveMail(sendMail services.SendMailFunc, store mailtemplate.Store, tenant string, data InactiveMailData) error {
	mailData := memberMailData(data.Eeg, data.Participant)
	mailData.MeteringPoints = []string{data.MeteringPoint}
	mailData.Direction = data.Direction
	mailData.EndDate = data.EndDate
	mailData.Reason = data.Reason
	return sendTemplateMail(sendMail, store, tenant, InactiveMailSubject, InactiveMailTemplate, data.Eeg, data.Participant, data, mailData)
}
