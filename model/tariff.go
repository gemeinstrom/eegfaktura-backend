package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/jjeffery/civil"
	"github.com/pborman/uuid"
	"gopkg.in/guregu/null.v4"
)

type BillingPeriod string

const (
	ANNUAL     BillingPeriod = "annual"
	MONTHLY    BillingPeriod = "monthly"
	SEMIANNUAL BillingPeriod = "semiannual"
	QUARTERLY  BillingPeriod = "quarterly"
)

type TariffModelType string

const (
	EEG    TariffModelType = "EEG"
	VZP    TariffModelType = "VZP"
	EZP    TariffModelType = "EZP"
	AKONTO TariffModelType = "AKONTO"
)

// Workaround for int values which will be provided as string in the communication.
// Custom type that can handle string or int
type IntOrString int

func (i *IntOrString) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as int
	var intVal int
	if err := json.Unmarshal(data, &intVal); err == nil {
		*i = IntOrString(intVal)
		return nil
	}

	// Try to unmarshal as string
	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		intVal, err := strconv.Atoi(strVal)
		if err != nil {
			return err
		}
		*i = IntOrString(intVal)
		return nil
	}

	return fmt.Errorf("IntOrString: invalid data %s", string(data))
}

// FloatOrString is IntOrString for an amount with decimals (261026-mm1).
type FloatOrString float64

func (f *FloatOrString) UnmarshalJSON(data []byte) error {
	var floatVal float64
	if err := json.Unmarshal(data, &floatVal); err == nil {
		*f = FloatOrString(floatVal)
		return nil
	}

	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		floatVal, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			return err
		}
		if math.IsNaN(floatVal) || math.IsInf(floatVal, 0) {
			return fmt.Errorf("FloatOrString: invalid amount %s", strVal)
		}
		*f = FloatOrString(floatVal)
		return nil
	}

	return fmt.Errorf("FloatOrString: invalid data %s", string(data))
}

type Tariff struct {
	Id                   uuid.UUID       `json:"id" goqu:"defaultifempty"`
	Version              int             `json:"version" db:"version"`
	Type                 TariffModelType `json:"type"`
	Name                 string          `json:"name"`
	BillingPeriod        string          `json:"billingPeriod,omitempty" db:"billingPeriod" goqu:"omitempty"`
	UseVat               bool            `json:"useVat" db:"useVat"`
	VatSupplementaryText string          `json:"vatSupplementaryText,omitempty" db:"vatSupplementaryText" goqu:"omitempty"`
	VatInPercent         IntOrString     `json:"vatInPercent" db:"vatInPercent"`
	AccountNetAmount     IntOrString     `json:"accountNetAmount" db:"accountNetAmount"`
	AccountGrossAmount   IntOrString     `json:"accountGrossAmount"  db:"accountGrossAmount"`
	ParticipantFee       float32         `json:"participantFee" db:"participantFee"`
	BaseFee              FloatOrString   `json:"baseFee" db:"baseFee"`
	BusinessNr           null.Int        `json:"businessNr,string" db:"businessNr"`
	CentPerKWh           float32         `json:"centPerKWh" db:"centPerKWh"`
	FreeKWh              null.Int        `json:"freeKWh,omitempty,omitzero" db:"freeKWh"`
	Discount             null.Int        `json:"discount,omitempty,omitzero" db:"discount"`
	UseMeteringFee       bool            `json:"useMeteringPointFee"  db:"useMeteringPointFee"`
	MeteringFee          null.Float      `json:"meteringPointFee" db:"meteringPointFee"`
	MeteringVat          null.Int        `json:"meteringPointVat" db:"meteringPointVat"`
	CreatedAt            civil.NullDate  `json:"createdAt,omitempty" db:"createdDate" goqu:"omitempty,skipupdae,skipinsert"`
	InactiveSince        civil.NullDate  `json:"inactiveSince,omitempty" db:"inactiveSince" goqu:"omitempty,skipupdae,skipinsert"`

	// ZVT (zeitvariabler Tarif): CentPerKWh bleibt der Basispreis; bis zu zwei
	// Zeitfenster mit eigenem Preis. From/To als "HH:MM" (15-min-Raster,
	// From > To = Mitternachtsueberlauf; die Views liefern to_char 'HH24:MI').
	UseTimeTariff         bool        `json:"useTimeTariff" db:"useTimeTariff"`
	TimeTariff1Active     bool        `json:"timeTariff1Active" db:"timeTariff1Active"`
	TimeTariff1Name       null.String `json:"timeTariff1Name,omitempty" db:"timeTariff1Name"`
	TimeTariff1From       null.String `json:"timeTariff1From,omitempty" db:"timeTariff1From"`
	TimeTariff1To         null.String `json:"timeTariff1To,omitempty" db:"timeTariff1To"`
	TimeTariff1CentPerKWh null.Float  `json:"timeTariff1CentPerKWh,omitempty" db:"timeTariff1CentPerKWh"`
	TimeTariff2Active     bool        `json:"timeTariff2Active" db:"timeTariff2Active"`
	TimeTariff2Name       null.String `json:"timeTariff2Name,omitempty" db:"timeTariff2Name"`
	TimeTariff2From       null.String `json:"timeTariff2From,omitempty" db:"timeTariff2From"`
	TimeTariff2To         null.String `json:"timeTariff2To,omitempty" db:"timeTariff2To"`
	TimeTariff2CentPerKWh null.Float  `json:"timeTariff2CentPerKWh,omitempty" db:"timeTariff2CentPerKWh"`

	// Beitrittsgebuehr (konzept-beitrittsgebuehr.md): einmalig je Mitglied, nur am
	// EEG-Tarif, mit eigener USt-Einstellung. Die Spalte ist numeric(10,2), der Betrag
	// bleibt dort exakt; ValidateJoiningFee laesst hoechstens 2 Nachkommastellen zu.
	JoiningFee             float64     `json:"joiningFee" db:"joiningFee"`
	JoiningFeeUseVat       bool        `json:"joiningFeeUseVat" db:"joiningFeeUseVat"`
	JoiningFeeVatInPercent IntOrString `json:"joiningFeeVatInPercent" db:"joiningFeeVatInPercent"`
}

// maxJoiningFee is the largest amount the column "joiningFee" numeric(10,2) holds; above it the INSERT would fail
// with a database error instead of a message.
const maxJoiningFee = 99999999.99

// ValidateJoiningFee enforces the joining fee rules server-side (konzept-beitrittsgebuehr.md,
// Tech Design B): only EEG tariffs carry a fee (other types must send 0/false), the amount
// is >= 0 with at most 2 decimals and fits numeric(10,2), the VAT rate lies in 0..100.
func (t *Tariff) ValidateJoiningFee() error {
	if t.Type != EEG {
		if t.JoiningFee != 0 || t.JoiningFeeUseVat || t.JoiningFeeVatInPercent != 0 {
			return fmt.Errorf("Beitrittsgebühr ist nur für Mitgliedsbeitrags-Tarife zulässig")
		}
		return nil
	}
	if t.JoiningFee < 0 {
		return fmt.Errorf("Beitrittsgebühr darf nicht negativ sein")
	}
	if t.JoiningFee > maxJoiningFee {
		return fmt.Errorf("Beitrittsgebühr darf höchstens 99.999.999,99 € betragen")
	}
	if cents := t.JoiningFee * 100; math.Abs(cents-math.Round(cents)) > 1e-6 {
		return fmt.Errorf("Beitrittsgebühr: höchstens 2 Nachkommastellen")
	}
	if t.JoiningFeeVatInPercent < 0 || t.JoiningFeeVatInPercent > 100 {
		return fmt.Errorf("USt-Satz der Beitrittsgebühr muss zwischen 0 und 100 liegen")
	}
	return nil
}

// parseTimeTariffTime parses "HH:MM", enforcing the 15-min raster
// (00/15/30/45). Returns minutes since midnight.
func parseTimeTariffTime(s string) (int, error) {
	var hh, mm int
	if _, err := fmt.Sscanf(s, "%d:%d", &hh, &mm); err != nil {
		return 0, fmt.Errorf("ungültige Uhrzeit %q (erwartet HH:MM)", s)
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("ungültige Uhrzeit %q (erwartet HH:MM)", s)
	}
	if mm%15 != 0 {
		return 0, fmt.Errorf("Uhrzeit %q nicht im 15-Minuten-Raster (00/15/30/45)", s)
	}
	return hh*60 + mm, nil
}

// timeWindowContains reports whether minute m lies inside [from, to),
// cyclic over midnight when from > to.
func timeWindowContains(from, to, m int) bool {
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}

// ValidateTimeTariff enforces the ZVT rules server-side (konzept-zeitvariable-
// tarife.md): only VZP/EZP tariffs, no free kWh in time-based mode, active
// windows need from/to/price, From != To, 15-min raster, and the two active
// windows must not overlap (cyclic check).
func (t *Tariff) ValidateTimeTariff() error {
	if !t.UseTimeTariff {
		return nil
	}
	if t.Type != VZP && t.Type != EZP {
		return fmt.Errorf("zeitbasierter Tarif ist nur für Verbraucher- und Erzeuger-Tarife zulässig")
	}
	if t.FreeKWh.Valid && t.FreeKWh.Int64 != 0 {
		return fmt.Errorf("kostenlose Energie (freeKWh) ist im zeitbasierten Modus nicht zulässig")
	}

	type window struct {
		label    string
		active   bool
		from, to null.String
		price    null.Float
	}
	windows := []window{
		{"Zeitraum 1", t.TimeTariff1Active, t.TimeTariff1From, t.TimeTariff1To, t.TimeTariff1CentPerKWh},
		{"Zeitraum 2", t.TimeTariff2Active, t.TimeTariff2From, t.TimeTariff2To, t.TimeTariff2CentPerKWh},
	}

	type parsedWindow struct {
		label    string
		from, to int
	}
	var active []parsedWindow
	for _, w := range windows {
		if !w.active {
			continue
		}
		if !w.from.Valid || w.from.String == "" || !w.to.Valid || w.to.String == "" {
			return fmt.Errorf("%s: Von und Bis sind erforderlich", w.label)
		}
		from, err := parseTimeTariffTime(w.from.String)
		if err != nil {
			return fmt.Errorf("%s: %w", w.label, err)
		}
		to, err := parseTimeTariffTime(w.to.String)
		if err != nil {
			return fmt.Errorf("%s: %w", w.label, err)
		}
		if from == to {
			return fmt.Errorf("%s: Von und Bis dürfen nicht gleich sein", w.label)
		}
		if !w.price.Valid {
			return fmt.Errorf("%s: Preis (ct/kWh) ist erforderlich", w.label)
		}
		active = append(active, parsedWindow{w.label, from, to})
	}

	if len(active) == 2 {
		// Zwei zyklische Intervalle ueberlappen genau dann, wenn der Start des
		// einen im anderen liegt.
		if timeWindowContains(active[0].from, active[0].to, active[1].from) ||
			timeWindowContains(active[1].from, active[1].to, active[0].from) {
			return fmt.Errorf("die beiden Zeiträume dürfen sich nicht überschneiden")
		}
	}
	return nil
}

type TariffHistory struct {
}

// JoiningFeeState is one entry of billing's joiningFeeState list (GET
// /api/billingDocuments/tenant/{tenantId}/joiningFeeState). eegfaktura-web passes the list
// unchanged as the body of POST /eeg/export/masterdata; it only fills the export column
// "Beitrittsgebühr Status" (konzept-beitrittsgebuehr.md, Tech Design A and Erweiterung 27.10.2026).
type JoiningFeeState struct {
	ParticipantId  string                  `json:"participantId"`
	EnergyBilled   bool                    `json:"energyBilled"`
	JoiningFee     *JoiningFeeDocument     `json:"joiningFee"`
	Requested      bool                    `json:"requested"`
	PaidExternally *JoiningFeePaidExternal `json:"paidExternally"`
}

// JoiningFeeDocument is the final billing document that carries a member's joining fee.
type JoiningFeeDocument struct {
	DocumentNumber string `json:"documentNumber"`
	DocumentDate   string `json:"documentDate"` // YYYY-MM-DD
}

// JoiningFeePaidExternal is a payment of the fee outside eegfaktura, recorded in billing.
type JoiningFeePaidExternal struct {
	PaidOn string `json:"paidOn"` // YYYY-MM-DD
	Note   string `json:"note"`
}
