package model

import (
	"time"

	"github.com/jjeffery/civil"
	"gopkg.in/guregu/null.v4"
)

type MeteringPointDBModel struct {
	MeteringPoint    string             `json:"meteringPoint" db:"metering_point_id" goqu:"skipupdate"`
	ConsentId        null.String        `json:"consentId" db:"consent_id" goqu:"omitempty"`
	Transformer      null.String        `json:"transformer,omitempty" goqu:"omitempty"`
	Direction        DirectionType      `json:"direction,omitempty" goqu:"omitnil"`
	Status           *StatusType        `json:"status,omitempty" goqu:"omitnil"`
	ProcessState     *ProcessStatusType `json:"processState" db:"process_state" goqu:"omitnil"`
	StatusCode       null.Int           `json:"statusCode,omitempty" db:"statusCode" goqu:"omitempty"`
	TariffId         null.String        `json:"tariff_id,omitempty" db:"tariff_id" goqu:"omitempty"`
	EquipmentNumber  null.String        `json:"equipmentNumber,omitempty" db:"equipmentNumber" goqu:"omitempty"`
	EquipmentName    null.String        `json:"equipmentName,omitempty" db:"equipmentName" goqu:"omitempty"`
	InverterId       null.String        `json:"inverterid,omitempty" db:"inverterid" goqu:"omitempty"`
	Street           null.String        `json:"street,omitempty" goqu:"omitempty"`
	StreetNumber     null.String        `json:"streetNumber,omitempty" db:"streetNumber" goqu:"omitempty"`
	City             null.String        `json:"city,omitempty" goqu:"omitempty"`
	Zip              null.String        `json:"zip,omitempty" goqu:"omitempty"`
	RegisteredSince  civil.NullDate     `json:"registeredSince" db:"registeredSince" goqu:"omitempty"`
	ModifiedAt       civil.NullDateTime `json:"modifiedAt" db:"modifiedAt" goqu:"omitempty"`
	ModifiedBy       null.String        `json:"modifiedBy" db:"modifiedBy" goqu:"omitempty"`
	GridOperatorId   null.String        `json:"gridOperatorId,omitempty" db:"grid_operator_id" goqu:"omitempty"`
	GridOperatorName null.String        `json:"gridOperatorName,omitempty" db:"grid_operator_name" goqu:"omitempty"`
	ActiveSince      civil.NullDate     `json:"activesince" goqu:"omitempty"`
	InactiveSince    civil.NullDate     `json:"inactivesince" goqu:"omitempty"`
	Active           *ProcessStatus     `json:"active" goqu:"omitnil"`
	Flag             *ProcessFlag       `json:"flat" goqu:"omitnil"`
	// from the grid operator's metering point list; empty is never written (platform#229)
	TechCode null.String `json:"techCode,omitempty" db:"tech_code" goqu:"omitempty"`
	FuelCode null.String `json:"fuelCode,omitempty" db:"fuel_code" goqu:"omitempty"`
}
type MeteringPartFactDBModel struct {
	MeteringPoint string             `json:"meteringPoint" db:"metering_point_id" goqu:"skipupdate"`
	ParticipantId *string            `json:"participant_id" db:"participant_id" goqu:"omitnil"`
	Tenant        *string            `json:"tenant" db:"tenant" goqu:"omitnil"`
	PartFact      int                `json:"partFact" db:"partFact"`
	CreatedAt     civil.NullDateTime `json:"createdAt,omitempty" goqu:"omitempty"`
	CreatedBy     null.String        `json:"createdBy,omitempty" db:"createdBy" goqu:"omitempty"`
}

func ConvertToDbMeterList(ml []Meter) []*MeteringPointDBModel {
	converted := make([]*MeteringPointDBModel, len(ml))
	for i := range ml {
		converted[i] = ConvertToDbMeter(ml[i])
	}
	return converted
}

func ConvertToDbMeter(m Meter) *MeteringPointDBModel {

	optionalString := func(v string) null.String {
		if len(v) > 0 {
			return null.StringFrom(v)
		}
		return null.String{}
	}

	getCivilDate := func(a int64) civil.NullDate {
		t := time.UnixMilli(a)
		if t.IsZero() == false {
			d := civil.DateOf(t)
			return civil.NullDateFrom(&d)
		}
		return civil.NullDate{}
	}

	getCivilDateTime := func(a int64) civil.NullDateTime {
		t := time.UnixMilli(a)
		if t.IsZero() == false {
			d := civil.DateTimeOf(t)
			return civil.NullDateTimeFrom(&d)
		}
		return civil.NullDateTime{}
	}

	return &MeteringPointDBModel{
		MeteringPoint: m.MeteringPoint,
		ConsentId:     optionalString(m.ConsentID),
		TechCode:      optionalString(m.TechCode),
		FuelCode:      optionalString(m.FuelCode),
		//Transformer:      null.String{},
		Direction:        m.Direction,
		Status:           nil,
		ProcessState:     nil,
		StatusCode:       null.Int{},
		TariffId:         null.String{},
		EquipmentNumber:  null.String{},
		EquipmentName:    null.String{},
		InverterId:       null.String{},
		Street:           null.String{},
		StreetNumber:     null.String{},
		City:             null.String{},
		Zip:              null.String{},
		RegisteredSince:  civil.NullDate{},
		ModifiedAt:       getCivilDateTime(civil.Now().Unix() * 1000),
		ModifiedBy:       null.StringFrom("ec_podlist"),
		GridOperatorId:   null.String{},
		GridOperatorName: null.String{},
		ActiveSince:      getCivilDate(m.From), //civil.NullDate{},
		InactiveSince:    civil.NullDate{Date: civil.DateFor(2999, 12, 31), Valid: true},
		Active:           nil,
		Flag:             nil,
	}
}
func ConvertToDbMeterPartFactList(ml []Meter) []*MeteringPartFactDBModel {
	converted := make([]*MeteringPartFactDBModel, len(ml))
	for i := range ml {
		converted[i] = ConvertToDbMeterPartFact(ml[i])
	}
	return converted
}

func ConvertToDbMeterPartFact(m Meter) *MeteringPartFactDBModel {
	return &MeteringPartFactDBModel{
		MeteringPoint: m.MeteringPoint,
		ParticipantId: nil,
		Tenant:        nil,
		PartFact:      m.PartFact,
		CreatedAt:     civil.NullDateTime{},
		CreatedBy:     null.String{},
	}
}

func StandardizeMeteringPointList(ml []Meter) []Meter {
	findEarliestActivation := func(ml []Meter) Meter {
		if len(ml) == 1 {
			return ml[0] // only one meteringpoint in meter list
		}

		//at := time.Date(2999, 12, 31, 23, 59, 59, 999, time.UTC).UnixMilli()
		at := int64(0)
		idx := 0
		for i, m := range ml {
			if m.From > at {
				at = m.From
				idx = i
			}
		}
		return ml[idx]
	}
	converted := map[string][]Meter{}
	for _, ml := range ml {
		if _, ok := converted[ml.MeteringPoint]; !ok {
			converted[ml.MeteringPoint] = []Meter{}
		}
		converted[ml.MeteringPoint] = append(converted[ml.MeteringPoint], ml)
	}

	cml := make([]Meter, 0, len(converted))
	for _, ml := range converted {
		cml = append(cml, findEarliestActivation(ml))
	}
	return cml
}
