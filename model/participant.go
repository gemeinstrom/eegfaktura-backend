package model

import (
	"time"

	"github.com/jjeffery/civil"
	"github.com/pborman/uuid"
	"gopkg.in/guregu/null.v4"
)

//func (ts EegParticipantState) MarshalJSON() ([]byte, error) {
//	type Alias EegParticipantState
//	if ts.Since == nil {
//		return json.Marshal(&ts)
//	}
//
//	return json.Marshal(&struct {
//		*Alias
//		Since int64 `json:"since"`
//	}{
//		Alias: (*Alias)(&ts),
//		Since: (*ts.Since).UnixMilli(),
//	})
//}

type MeterState struct {
	ActiveSince   civil.NullDate `json:"activeSince" goqu:"skipinsert"`
	InactiveSince civil.NullDate `json:"inactiveSince" goqu:"skipinsert"`
	Active        ProcessStatus  `json:"-" db:"-" goqu:"skipinsert"`
	Flag          ProcessFlag    `json:"-" goqu:"skipinsert"`
}

type EegParticipantBase struct {
	Id                    uuid.UUID         `json:"id,omitempty" goqu:"skipupdate"`
	ParticipantNumber     null.String       `json:"participantNumber" db:"participantNumber" goqu:"omitempty"`
	BusinessRole          string            `json:"businessRole,omitempty" db:"businessRole" goqu:"omitempty"`
	Role                  string            `json:"role,omitempty" db:"role" goqu:"omitempty"`
	FirstName             string            `json:"firstname" goqu:"omitempty"`
	LastName              string            `json:"lastname" goqu:"omitempty"`
	TitleBefore           null.String       `json:"titleBefore" db:"titleBefore" goqu:"omitempty"`
	TitleAfter            null.String       `json:"titleAfter" db:"titleAfter" goqu:"omitempty"`
	ParticipantSince      civil.NullDate    `json:"participantSince" db:"participantSince" goqu:"omitempty,defaultifempty"`
	VatNumber             null.String       `json:"vatNumber,omitempty" db:"vatNumber" goqu:"omitempty"`
	TaxNumber             null.String       `json:"taxNumber,omitempty" db:"taxNumber" goqu:"omitempty"`
	CompanyRegisterNumber null.String       `json:"companyRegisterNumber,omitempty" db:"companyRegisterNumber" goqu:"omitempty"`
	MeteringPoint         []*MeteringPoint  `json:"meters" db:"-" goqu:"skipupdate,skipinsert"`
	TariffId              null.String       `json:"tariffId,omitempty" db:"tariffId" goqu:"omitempty,skipinsert"`
	Status                ProcessStatusType `json:"status,omitempty" goqu:"omitempty,defaultifempty"`
	Version               int               `json:"version,omitempty" goqu:"omitempty,defaultifempty"`
	CreatedBy             string            `json:"createdBy,omitempty" db:"createdBy" goqu:"skipupdate"`
}

type EegParticipant struct {
	EegParticipantBase
	Contact         ContactInfo `json:"contact" db:"contact"`
	BillingAddress  Address     `json:"billingAddress" db:"billingAddress"`
	ResidentAddress Address     `json:"residentAddress" db:"residentAddress"`
	BankAccount     BankInfo    `json:"accountInfo" db:"accountInfo"`
}

type ContactInfo struct {
	Phone null.String `json:"phone" db:"phone" goqu:"omitempty"`
	Email null.String `json:"email" db:"email" goqu:"omitempty"`
}

type BankInfo struct {
	Iban             null.String    `json:"iban" goqu:"omitempty"`
	Owner            null.String    `json:"owner" goqu:"omitempty"`
	BankName         null.String    `json:"bankName" db:"bankName" goqu:"omitempty"`
	MandateReference null.String    `json:"mandateReference" db:"mandate_reference" goqu:"omitempty"`
	MandateDate      civil.NullDate `json:"mandateDate,omitempty" db:"mandate_date" goqu:"omitempty"`
	SepaDirectDebit  null.String    `json:"sepaDirectDebit" db:"sepa_direct_debit" goqu:"omitempty"`
}

type DirectionType string

const (
	CONSUMPTION DirectionType = "CONSUMPTION"
	GENERATOR   DirectionType = "GENERATION"
	UNKNOWN     DirectionType = "UNKNOWN"
)

type RegistrationMode string

const (
	ONLINE  RegistrationMode = "ONLINE"
	OFFLINE RegistrationMode = "OFFLINE"
)

type ProcessStatusType string

const (
	NEW      ProcessStatusType = "NEW"
	INIT     ProcessStatusType = "INIT"
	PENDING  ProcessStatusType = "PENDING"  // Answer Message from grid-provider was received
	APPROVED ProcessStatusType = "APPROVED" // Participant has accepted in the grid-operator portal
	ACTIVE   ProcessStatusType = "ACTIVE"
	INACTIVE ProcessStatusType = "INACTIVE"
	REJECTED ProcessStatusType = "REJECTED"
	REVOKED  ProcessStatusType = "REVOKED"
	INVALID  ProcessStatusType = "INVALID"
	ARCHIVED ProcessStatusType = "ARCHIVED"
	ABORTED  ProcessStatusType = "ABORTED"
	RESTORE  ProcessStatusType = "RESTORE"
	WARN     ProcessStatusType = "WARN"
)

type StatusType string

const (
	S_INIT     StatusType = "INIT"
	S_ACTIVE   StatusType = "ACTIVE"
	S_INACTIVE StatusType = "INACTIVE"
)

type ProcessStatus int

const (
	P_INACTIVE ProcessStatus = iota
	P_ACTIVE
	P_ERROR
)

type ProcessFlag int

const (
	F_MOVED ProcessFlag = iota
	F_ASSIGNED
	F_DELETED
)

// MemberActivityType is the status of a member derived from its metering points (platform#165).
type MemberActivityType string

const (
	MemberActive   MemberActivityType = "ACTIVE"
	MemberPending  MemberActivityType = "PENDING"
	MemberInactive MemberActivityType = "INACTIVE"
)

// MemberActivity derives the member status from its metering points: ACTIVE with at least one active metering
// point; PENDING without one but with one not yet active (status INIT) whose registration is running, or with no
// metering point at all; INACTIVE when all of them have left, were archived or rejected. A metering point with status
// INACTIVE never counts as registering: in prod (20.10.2026) 36 members have only such rows with a stale registration
// state and could not be told apart from a re-registration; a re-registered one counts once it is active again.
// The stored participant status is not changed.
func MemberActivity(meters []*MeteringPoint) MemberActivityType {
	if len(meters) == 0 {
		return MemberPending
	}
	pending := false
	for _, m := range meters {
		if m.Status == S_ACTIVE {
			return MemberActive
		}
		if m.Status != S_INACTIVE && registering(m.ProcessState) {
			pending = true
		}
	}
	if pending {
		return MemberPending
	}
	return MemberInactive
}

// registering: the registration of the metering point is running (by its process state)
func registering(p ProcessStatusType) bool {
	switch p {
	case NEW, INIT, PENDING, APPROVED:
		return true
	}
	return false
}

type MeteringPoint struct {
	MeteringPoint    string            `json:"meteringPoint" db:"metering_point_id" goqu:"skipupdate"`
	ParticipantId    string            `json:"participantId,omitempty" db:"participant_id" goqu:"skipupdate,skipinsert"`
	ConsentId        null.String       `json:"consentId" db:"consent_id" goqu:"skipupdate,omitnil"`
	Transformer      null.String       `json:"transformer,omitempty"`
	Direction        DirectionType     `json:"direction,omitempty"`
	Status           StatusType        `json:"status,omitempty"`
	StatusCode       null.Int          `json:"statusCode,omitempty" db:"statusCode" goqu:"omitempty"`
	TariffId         null.String       `json:"tariff_id,omitempty" db:"tariff_id"`
	EquipmentNumber  null.String       `json:"equipmentNumber,omitempty" db:"equipmentNumber"`
	EquipmentName    null.String       `json:"equipmentName,omitempty" db:"equipmentName"`
	InverterId       null.String       `json:"inverterid,omitempty" db:"inverterid"`
	Street           null.String       `json:"street,omitempty"`
	StreetNumber     null.String       `json:"streetNumber,omitempty" db:"streetNumber"`
	City             null.String       `json:"city,omitempty"`
	Zip              null.String       `json:"zip,omitempty"`
	RegisteredSince  civil.Date        `json:"registeredSince" db:"registeredSince"`
	ModifiedAt       civil.DateTime    `json:"modifiedAt,omitempty" db:"modifiedAt"`
	ModifiedBy       null.String       `json:"modifiedBy,omitempty" db:"modifiedBy"`
	GridOperatorId   null.String       `json:"gridOperatorId,omitempty" db:"grid_operator_id"`
	GridOperatorName null.String       `json:"gridOperatorName,omitempty" db:"grid_operator_name"`
	ProcessState     ProcessStatusType `json:"processState,omitempty" db:"process_state"`
	State            *MeterState       `json:"participantState" goqu:"skipupdate"`
	PartFact         int               `json:"partFact,omitempty" db:"partFact" goqu:"skipupdate,skipinsert"`
	ActivationMode   RegistrationMode  `json:"activationMode,omitempty" goqu:"skipupdate,skipinsert" db:"-"`
	AllocationFactor null.Float        `json:"allocationFactor,omitempty" db:"allocation_factor" goqu:"omitempty"`
	// from the grid operator's metering point list (platform#229); read only
	TechCode null.String `json:"techCode,omitempty" db:"tech_code" goqu:"skipinsert,skipupdate"`
	FuelCode null.String `json:"fuelCode,omitempty" db:"fuel_code" goqu:"skipinsert,skipupdate"`
}

//type MeteringPointOffline struct {
//	*MeteringPoint
//	Enabled        bool             `json:"enabled,omitempty"`
//	ActivationMode RegistrationMode `json:"activationMode"`
//	ActivationCode string           `json:"activationCode"`
//}

type ChangePartitionFactorRequest struct {
	MeteringPoint  string        `json:"meter"`
	Direction      DirectionType `json:"direction"`
	GridOperatorId null.String   `json:"gridOperatorId,omitempty"`
	Activation     civil.Date    `json:"activation"`
	PartFact       int           `json:"partFact"`
}

type MasterDataParticipant struct {
	ParticipantNumber string            `json:"participantNumber" db:"participantNumber" goqu:"omitempty"`
	BusinessRole      string            `json:"businessRole,omitempty" db:"businessRole" goqu:"omitempty"`
	Role              string            `json:"role,omitempty" db:"role" goqu:"omitempty"`
	FirstName         string            `json:"firstname" goqu:"omitempty"`
	LastName          string            `json:"lastname" goqu:"omitempty"`
	TitleBefore       string            `json:"titleBefore" db:"titleBefore" goqu:"omitempty"`
	TitleAfter        string            `json:"titleAfter" db:"titleAfter" goqu:"omitempty"`
	ParticipantSince  time.Time         `json:"participantSince" db:"participantSince" goqu:"omitempty,defaultifempty"`
	MeteringPoint     []MasterDataMeter `json:"meters" db:"-" goqu:"skipupdate,skipinsert"`
	Status            ProcessStatusType `json:"status,omitempty" goqu:"omitempty,defaultifempty"`
}

type MasterDataMeter struct {
	MeteringPoint    string            `json:"meteringPoint"`
	ConsentId        string            `json:"consentId"`
	Transformer      string            `json:"transformer"`
	Direction        DirectionType     `json:"direction"`
	Status           StatusType        `json:"status"`
	EquipmentNumber  string            `json:"equipmentNumber"`
	EquipmentName    string            `json:"equipmentName"`
	InverterId       string            `json:"inverterid"`
	RegisteredSince  string            `json:"registeredSince"`
	GridOperatorId   string            `json:"gridOperatorId"`
	GridOperatorName string            `json:"gridOperatorName"`
	ProcessState     ProcessStatusType `json:"processState"`
	PartFact         int               `json:"partFact"`
	ActivationMode   RegistrationMode  `json:"activationMode"`
	AllocationFactor float64           `json:"allocationFactor"`
	ActiveSince      time.Time         `json:"activeSince"`
	InactiveSince    time.Time         `json:"inactiveSince"`
}
