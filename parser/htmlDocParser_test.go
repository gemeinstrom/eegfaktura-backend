package parser

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"at.ourproject/vfeeg-backend/services"
	"github.com/jjeffery/civil"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/guregu/null.v4"
)

func init() {
	viper.Set("services.mail-server", "localhost:9093")
	viper.Set("file-content.templates", "../public")
}

func trimString(s string) string {
	s = strings.Replace(s, " ", "", -1)
	s = strings.Replace(s, "\t", "", -1)
	s = strings.Replace(s, "\n", "", -1)
	s = strings.Replace(s, "\r", "", -1)
	return s
}

func TestParseTemplate(t *testing.T) {

	eeg := &model.Eeg{
		Id:                 "",
		Name:               "TE-EEG",
		Description:        "TEST EEG",
		BusinessNr:         null.String{},
		Area:               "",
		Legal:              "",
		OperatorName:       "",
		CommunityId:        "",
		GridOperator:       "",
		RcNumber:           "",
		AllocationMode:     "",
		SettlementInterval: "",
		ProviderBusinessNr: null.Int{},
		TaxNumber:          null.String{},
		VatNumber:          null.String{},
		ContactPerson:      null.StringFrom("Max Sonnenmann"),
		EegAddress:         model.EegAddress{},
		AccountInfo:        model.AccountInfo{},
		Contact: model.Contact{
			Phone: null.StringFrom("123456789"),
		},
		Optionals: model.Optionals{},
		//Periods:   nil,
		Online: false,
	}

	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			Id:                    nil,
			ParticipantNumber:     null.String{},
			BusinessRole:          "",
			FirstName:             "Max",
			LastName:              "Mustermann",
			TitleBefore:           null.String{},
			TitleAfter:            null.String{},
			ParticipantSince:      civil.NullDate{},
			VatNumber:             null.String{},
			TaxNumber:             null.String{},
			CompanyRegisterNumber: null.String{},
			MeteringPoint:         nil,
			TariffId:              null.String{},
			Status:                "",
			Version:               0,
		},
		Contact: model.ContactInfo{
			Phone: null.String{},
			Email: null.StringFrom("my@mail.com"),
		},
		BillingAddress:  model.Address{},
		ResidentAddress: model.Address{},
		BankAccount:     model.BankInfo{},
	}

	type args struct {
		templateFileName string
		data             interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    *bytes.Buffer
		wantErr bool
	}{
		{
			"Parse ACTIVATION Template",
			args{"../public/templates/AktivierungsEmail-template.html", struct {
				Eeg            *model.Eeg
				Participant    *model.EegParticipant
				Meteringpoints []string
			}{eeg, participant, []string{"AT0010000000000000000000000111"}}},
			bytes.NewBufferString(strings.Trim(`<!DOCTYPE html>
        <html lang="de">
        <head>
            <meta charset="UTF-8">
            <title>Aktivierung Zählpunkt</title>
        </head>
        <body>
        <p>Hallo Max,</p>
        <p>damit dein Zählpunkt tatsächlich Energie mit den anderen Mitgliedern austauschen kann, muss der EEG noch der Zugriff auf deine Energiedaten gewährt werden.</p>
        <p>Benötigt wird das für folgenden Zählpunkt:
        <ul> <li>AT0010000000000000000000000111</li> </ul>
        Welche Schritte dafür konkret ausgeführt werden müssen, findest du auf folgender Webseite:</p>
        <p>
            <a href="https://docs.eegfaktura.at/shelves/netz-betreiber-infos">https://docs.eegfaktura.at/shelves/netz-betreiber-infos</a>
        </p>
        <p>Wähle einfach deinen Netzbetreiber aus und folge der beschriebenen Vorgehensweise.</p>
        <p>Wir freuen uns, dass du bei uns mitmachst!</p>
        <p>Mit besten Grüßen</p>
        <div>TEST EEG</div>
        <div> ,  </div>
        
        <div>T: 123456789</div>
        
        
        
        </p>
        
        </body>
        </html>`, " ")),
			false,
		},
		{
			name: "Parse RC100915 Template",
			args: args{"../public/RC100915/templates/AktivierungsEmail-template.html", struct {
				Eeg            *model.Eeg
				Participant    *model.EegParticipant
				Meteringpoints []string
			}{eeg, participant, []string{"AT0010000000000000000000000111"}}},
			want: bytes.NewBufferString(strings.Trim(`<!DOCTYPE html>
        <html>
        <head>
          <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
          <style type="text/css" style="display:none;"> P {margin-top:0;margin-bottom:0;} </style>
        </head>
        <body dir="ltr">
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">Liebe Mitglieder der EEG St.Florian &amp; Nachbargemeinden,</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt"><br>
            </span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            Ich darf Ihnen hiermit mitteilen, dass wir Sie erfolgreich bei uns registrieren konnten.</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Ein letzter Schritt ist noch erforderlich:<br>
            Videoanleitung: <a href="https://youtu.be/zZdbxv-cPog?si=-yG5GNMZ5BsMLMmI" id="OWA5bd48a2b-81e0-0338-d18f-2f4c31144d23" data-auth="NotApplicable" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">
                https://youtu.be/zZdbxv-cPog?si=-yG5GNMZ5BsMLMmI
            </a>
            </span>
        </p>
        <br>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Sie haben dafür ab diesem Stichtag 14 Tage Zeit, die Freigabe zu bestaetigen! (Danach verfällt der Link beim Netzbetreiber)
        </span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            <br>
        </span></p>
        <p style="text-align: justify;"><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Bitte speichern Sie sich folgende Adressen in ihrem Mailprogramm ein:<br>
            <a href="mailto:online@eeg-stflorian.at" class="OWAAutoLink" title="online@eeg-stflorian.at" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">online@eeg-stflorian.at</a>&nbsp;(Kontakt zur EEG)<br>
            <a href="mailto:news@eeg-stflorian.at" class="OWAAutoLink" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">news@eeg-stflorian.at</a>&nbsp;(Allgemeine Infos, Newsletter)<br>
            <a href="mailto:no-reply@eegfaktura.at" class="OWAAutoLink" title="no-reply@eegfaktura.at" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">no-reply@eegfaktura.at</a> (Rechnungen & Gutschriften)<br>
            </span>
        </p>
        <p style="text-align: justify;"><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">&nbsp;</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            Abschließend bedanke ich mich für Ihr Vertrauen in unseren gemeinnützigen Verein, sowie unsere Vision und verbleibe mit sonnigen Grüßen,
            <br>
            <br>Gregor Hirscher, LLM<br>
            Geschäftsführung EEG St.Florian</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            gregor.hirscher@eeg-stflorian.at<br>
            +43 677 61458762<br>
            </span></p>
        <br>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            <strong>Erneuerbare Energiegemeinschaft St. Florian</strong><br>
            Gemeinnütziger Verein - ZVR 176739038<br>
            Linzerstraße 26<br>
            4490 St. Florian<br>
            <a href="mailto:online@eeg-stflorian.at" class="OWAAutoLink" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">online@eeg-stflorian.at</a><br>
            <br>
            Webseite - <a href="https://eeg-stflorian.jimdofree.com/" id="OWAf90744b3-c086-3851-3626-9ebcedc5ee90" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
               style="margin-top: 0px; margin-bottom: 0px;">
                https://eeg-stflorian.jimdofree.com/
            </a><br>
            Cities - <a href="https://citiesapps.com/pages/erneuerbare-energiegemeinschaft-stflorian/" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
                          style="margin-top: 0px; margin-bottom: 0px;">
                https://citiesapps.com/pages/erneuerbare-energiegemeinschaft-stflorian/
            </a><br>
            Facebook - <a href="https://www.facebook.com/profile.php?id=3D100092747569956" id="OWA65ff5825-9b1f-d3aa-06b5-f9b4610a0ff1" class="OWAAutoLink" data-auth="NotApplicable"
               data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">
                https://www.facebook.com/profile.php?id=3D100092747569956</a><br>
            Youtube - <a href="https://www.youtube.com/@SogehtEEG" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
                        style="margin-top: 0px; margin-bottom: 0px;">
                https://www.youtube.com/@SogehtEEG
            </a><br>
        </span></p>
        
        </body>
        </html>`, " ")),
			wantErr: false,
		},
		{
			name: "Parse RC101370 Template",
			args: args{"../public/RC101370/templates/AktivierungsEmail-template.html", struct {
				Eeg            *model.Eeg
				Participant    *model.EegParticipant
				Meteringpoints []string
			}{eeg, participant, []string{"AT0010000000000000000000000111"}}},
			want: bytes.NewBufferString(strings.Trim(`<!DOCTYPE html>
        <html>
        <head>
            <meta http-equiv="Content-Type" content="text/html; charset=utf-8">
            <style type="text/css" style="display:none;"> P {margin-top:0;margin-bottom:0;} </style>
        </head>
        <body dir="ltr">
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">Liebe Mitglieder der EEG St.Florian &amp; Nachbargemeinden,</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt"><br>
            </span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            Ich darf Ihnen hiermit mitteilen, dass wir Sie erfolgreich bei uns registrieren konnten.</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Ein letzter Schritt ist noch erforderlich:<br>
            Videoanleitung: <a href="https://youtu.be/zZdbxv-cPog?si=-yG5GNMZ5BsMLMmI" id="OWA5bd48a2b-81e0-0338-d18f-2f4c31144d23" data-auth="NotApplicable" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">
                https://youtu.be/zZdbxv-cPog?si=-yG5GNMZ5BsMLMmI
            </a>
            </span>
        </p>
        <br>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Sie haben dafür ab diesem Stichtag 14 Tage Zeit, die Freigabe zu bestaetigen! (Danach verfällt der Link beim Netzbetreiber)
        </span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            <br>
        </span></p>
        <p style="text-align: justify;"><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">
            Bitte speichern Sie sich folgende Adressen in ihrem Mailprogramm ein:<br>
            <a href="mailto:online@eeg-stflorian.at" class="OWAAutoLink" title="online@eeg-stflorian.at" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">online@eeg-stflorian.at</a>&nbsp;(Kontakt zur EEG)<br>
            <a href="mailto:news@eeg-stflorian.at" class="OWAAutoLink" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">news@eeg-stflorian.at</a>&nbsp;(Allgemeine Infos, Newsletter)<br>
            <a href="mailto:no-reply@eegfaktura.at" class="OWAAutoLink" title="no-reply@eegfaktura.at" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">no-reply@eegfaktura.at</a> (Rechnungen & Gutschriften)<br>
            </span>
        </p>
        <p style="text-align: justify;"><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt">&nbsp;</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            Abschließend bedanke ich mich für Ihr Vertrauen in unseren gemeinnützigen Verein, sowie unsere Vision und verbleibe mit sonnigen Grüßen,
            <br>
            <br>Gregor Hirscher, LLM<br>
            Geschäftsführung EEG St.Florian</span></p>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            gregor.hirscher@eeg-stflorian.at<br>
            +43 677 61458762<br>
            </span></p>
        <br>
        <p><span style="font-family: Verdana, Geneva, sans-serif; font-size: 10pt;">
            <strong>Erneuerbare Energiegemeinschaft St. Florian</strong><br>
            Gemeinnütziger Verein - ZVR 176739038<br>
            Linzerstraße 26<br>
            4490 St. Florian<br>
            <a href="mailto:online@eeg-stflorian.at" class="OWAAutoLink" data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">online@eeg-stflorian.at</a><br>
            <br>
            Webseite - <a href="https://eeg-stflorian.jimdofree.com/" id="OWAf90744b3-c086-3851-3626-9ebcedc5ee90" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
                          style="margin-top: 0px; margin-bottom: 0px;">
                https://eeg-stflorian.jimdofree.com/
            </a><br>
            Cities - <a href="https://citiesapps.com/pages/erneuerbare-energiegemeinschaft-stflorian/" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
                        style="margin-top: 0px; margin-bottom: 0px;">
                https://citiesapps.com/pages/erneuerbare-energiegemeinschaft-stflorian/
            </a><br>
            Facebook - <a href="https://www.facebook.com/profile.php?id=3D100092747569956" id="OWA65ff5825-9b1f-d3aa-06b5-f9b4610a0ff1" class="OWAAutoLink" data-auth="NotApplicable"
                          data-loopstyle="linkonly" style="margin-top: 0px; margin-bottom: 0px;">
                https://www.facebook.com/profile.php?id=3D100092747569956</a><br>
            Youtube - <a href="https://www.youtube.com/@SogehtEEG" class="OWAAutoLink" data-auth="NotApplicable" data-loopstyle="linkonly"
                         style="margin-top: 0px; margin-bottom: 0px;">
                https://www.youtube.com/@SogehtEEG
            </a><br>
        </span></p>
        
        </body>
        </html>`, " ")),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTemplate(os.DirFS(filepath.Dir(tt.args.templateFileName)), filepath.Base(tt.args.templateFileName), tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(bytes.NewBufferString(trimString(got.String())), bytes.NewBufferString(trimString(tt.want.String()))) {
				t.Errorf("ParseTemplate() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestParseTemplate2(t *testing.T) {
	eeg := &model.Eeg{
		Id:                 "",
		Name:               "TE-EEG",
		Description:        "TEST EEG",
		BusinessNr:         null.String{},
		Area:               "",
		Legal:              "",
		OperatorName:       "",
		CommunityId:        "",
		GridOperator:       "",
		RcNumber:           "",
		AllocationMode:     "",
		SettlementInterval: "",
		ProviderBusinessNr: null.Int{},
		TaxNumber:          null.String{},
		VatNumber:          null.String{},
		ContactPerson:      null.StringFrom("Max Sonnenmann"),
		EegAddress:         model.EegAddress{},
		AccountInfo:        model.AccountInfo{},
		Contact: model.Contact{
			Phone: null.StringFrom("123456789"),
		},
		Optionals: model.Optionals{},
		Online:    false,
	}

	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			Id:                    nil,
			ParticipantNumber:     null.String{},
			BusinessRole:          "",
			FirstName:             "Max",
			LastName:              "Mustermann",
			TitleBefore:           null.String{},
			TitleAfter:            null.String{},
			ParticipantSince:      civil.NullDate{},
			VatNumber:             null.String{},
			TaxNumber:             null.String{},
			CompanyRegisterNumber: null.String{},
			MeteringPoint:         nil,
			TariffId:              null.String{},
			Status:                "",
			Version:               0,
		},
		Contact: model.ContactInfo{
			Phone: null.String{},
			Email: null.StringFrom("my@mail.com"),
		},
		BillingAddress:  model.Address{},
		ResidentAddress: model.Address{},
		BankAccount:     model.BankInfo{},
	}

	sendMock := func(tenant, to, subject string, cc *string, body *bytes.Buffer, inlineContent []*services.Attachment, attachment *services.Attachment) error {
		fmt.Printf("Mail-Body: %s\n", body.String())
		return nil
	}

	err := SendActivationMailFromTemplate(sendMock, nil, "sepp", "test", eeg, participant, "activation-mail-template.toml")
	assert.NoError(t, err)
}

func TestManualSending(t *testing.T) {
	if os.Getenv("RUN_MANUAL_MAIL_TESTS") == "" {
		t.Skip("manual test: requires a reachable mail service; set RUN_MANUAL_MAIL_TESTS=1 to run")
	}
	eeg := &model.Eeg{
		Id:                 "TE100100",
		Name:               "TE-EEG",
		Description:        "TEST EEG",
		BusinessNr:         null.String{},
		Area:               "",
		Legal:              "",
		OperatorName:       "",
		CommunityId:        "",
		GridOperator:       "",
		RcNumber:           "",
		AllocationMode:     "",
		SettlementInterval: "",
		ProviderBusinessNr: null.Int{},
		TaxNumber:          null.String{},
		VatNumber:          null.String{},
		ContactPerson:      null.StringFrom("Max Sonnenmann"),
		EegAddress: model.EegAddress{
			Street:       "Solargasse",
			StreetNumber: "2",
			Zip:          "1111",
			City:         "Solarcity",
		},
		AccountInfo: model.AccountInfo{},
		Contact: model.Contact{
			Phone: null.StringFrom("++43 123456789"),
			Email: null.StringFrom("my@mail.com"),
		},
		Optionals: model.Optionals{Website: null.StringFrom("https://www.youtube.com")},
		Online:    false,
	}

	participant := &model.EegParticipant{
		EegParticipantBase: model.EegParticipantBase{
			Id:                    nil,
			ParticipantNumber:     null.String{},
			BusinessRole:          "",
			FirstName:             "Max",
			LastName:              "Mustermann",
			TitleBefore:           null.String{},
			TitleAfter:            null.String{},
			ParticipantSince:      civil.NullDate{},
			VatNumber:             null.String{},
			TaxNumber:             null.String{},
			CompanyRegisterNumber: null.String{},
			MeteringPoint: []*model.MeteringPoint{{
				MeteringPoint:    "AT00999900000000000000000000000333301",
				ConsentId:        null.String{},
				Transformer:      null.String{},
				Direction:        "CONSUMPTION",
				Status:           "",
				StatusCode:       null.Int{},
				TariffId:         null.String{},
				EquipmentNumber:  null.String{},
				EquipmentName:    null.String{},
				InverterId:       null.String{},
				Street:           null.String{},
				StreetNumber:     null.String{},
				City:             null.String{},
				Zip:              null.String{},
				RegisteredSince:  civil.Date{},
				ModifiedAt:       civil.DateTime{},
				ModifiedBy:       null.String{},
				GridOperatorId:   null.String{},
				GridOperatorName: null.String{},
				ProcessState:     "",
				State:            nil,
				PartFact:         0,
				ActivationMode:   "",
				AllocationFactor: null.Float{},
			}},
			TariffId: null.String{},
			Status:   "",
			Version:  0,
		},
		Contact: model.ContactInfo{
			Phone: null.String{},
			Email: null.StringFrom("obermueller.peter@gmail.com"),
		},
		BillingAddress:  model.Address{},
		ResidentAddress: model.Address{},
		BankAccount:     model.BankInfo{},
	}

	var err error
	if err = SendActivationMailFromTemplate(services.SendMail, nil,
		eeg.Id, "Aktivierung im Serviceportal", eeg, participant, "activation-mail-template.toml"); err != nil {
		logrus.WithField("tenant", eeg.Id).WithError(err).Error("Error Sending Mail")
	}

	assert.NoError(t, err)
}
