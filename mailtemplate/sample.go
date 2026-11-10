package mailtemplate

import (
	"time"

	"at.ourproject/vfeeg-backend/model"
)

// SampleData is the example member for preview and test mails ("Max
// Mustermann", an example metering point) with the real data of the community.
func SampleData(eeg *model.Eeg) *Data {
	gridOperator := "Netz Musterland GmbH"
	if eeg != nil && eeg.OperatorName != "" {
		gridOperator = eeg.OperatorName
	}
	return &Data{
		FirstName:      "Max",
		LastName:       "Mustermann",
		Number:         "1001",
		MeteringPoints: []string{"AT0030000000000000000000000012345"},
		GridOperator:   gridOperator,
		Direction:      "Bezug",
		EndDate:        time.Now().Format("02.01.2006"),
		Reason:         "Dein Netzbetreiber hat die Datenfreigabe für diesen Zählpunkt beendet, zum Beispiel wegen eines Zählertauschs oder Umzugs.",
		Eeg:            eeg,
	}
}
