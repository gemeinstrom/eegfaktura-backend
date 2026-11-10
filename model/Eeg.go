package model

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/jjeffery/civil"
	"gopkg.in/guregu/null.v4"
)

type Eeg struct {
	Id                 string             `json:"id" db:"tenant" goqu:"skipupdate"`
	Name               string             `json:"name,omitempty" goqu:"omitempty"`
	Description        string             `json:"description" goqu:"omitempty"`
	BusinessNr         null.String        `json:"businessNr,omitempty" db:"businessNr" goqu:"omitempty"`
	Area               AreaType           `json:"area" goqu:"omitempty"` /* LOCAL | REGIONAL | BEG | GEA | P2P | EVA */
	Legal              string             `json:"legal,omitempty" goqu:"omitempty"`
	GridOperator       string             `json:"gridOperator,omitempty" db:"gridoperator_code" goqu:"omitempty"`
	OperatorName       string             `json:"operatorName,omitempty" db:"gridoperator_name" goqu:"omitempty"`
	CommunityId        string             `json:"communityId,omitempty" db:"communityId" goqu:"omitempty"`
	RcNumber           string             `json:"rcNumber" db:"rcNumber" goqu:"skipupdate"`
	AllocationMode     AllocationModeType `json:"allocationMode,omitempty" db:"allocationMode" goqu:"omitempty"`
	SettlementInterval string             `json:"settlementInterval,omitempty" db:"settlementInterval" goqu:"omitempty"`
	ProviderBusinessNr null.Int           `json:"providerBusinessNr,omitempty" db:"providerBusinessNr" goqu:"omitempty"`
	TaxNumber          null.String        `json:"taxNumber,omitempty" db:"taxNumber" goqu:"omitempty"`
	VatNumber          null.String        `json:"vatNumber" db:"vatNumber" goqu:"omitempty"`
	ContactPerson      null.String        `json:"contactPerson" db:"contactPerson" goqu:"omitempty"`
	Online             bool               `json:"online" goqu:"skipupdate"`
	CreatedAt          civil.NullDate     `json:"createdAt,omitempty" db:"createdat" goqu:"skipinsert,skipupdate"`
	EcZoneLevel        null.Int           `json:"ecZoneLevel" db:"ec_zone_level" goqu:"skipupdate"` /* Nahebereich 1–7 from the metering point list, empty = unknown */
	EegAddress         `json:"address,omitempty" mapstructure:",squash" goqu:"omitempty"`
	AccountInfo        `json:"accountInfo,omitempty" mapstructure:",squash" goqu:"omitempty"`
	Contact            `json:"contact,omitempty" mapstructure:",squash" goqu:"omitempty"`
	Optionals          `json:"optionals,omitempty" mapstructure:",squash" goqu:"omitempty"`
	//Periods            []int16 `json:"periods" goqu:"skipinsert,defaultifempty"`
}

type AllocationModeType string

const (
	STATIC  AllocationModeType = "STATIC"
	DYNAMIC AllocationModeType = "DYNAMIC"
)

type AreaType string

const (
	LOCAL    AreaType = "LOCAL"
	REGIONAL AreaType = "REGIONAL"
	BEG      AreaType = "BEG"
	GEA      AreaType = "GEA"
	P2P      AreaType = "P2P" // Peer-to-Peer, ECType PP (eda-xp); no billing (platform#229)
	EVA      AreaType = "EVA" // Eigenversorgung, ECType SC (eda-xp); no billing (platform#229)
)

// areaTypes are the community types a registration may set (platform#229: one place in the code).
var areaTypes = []AreaType{LOCAL, REGIONAL, BEG, GEA, P2P, EVA}

// AreaTypes returns a copy of the known community types.
func AreaTypes() []AreaType { return slices.Clone(areaTypes) }

// ValidateArea refuses a community type outside AreaTypes, e.g. a gRPC enum value this backend does not know yet
// (its String() is the number, "6").
func ValidateArea(area AreaType) error {
	if !slices.Contains(areaTypes, area) {
		return fmt.Errorf("unknown community type %q", string(area))
	}
	return nil
}

// HasMultipleGridOperators: the community may span several grid operators, so a message goes to the grid operator of
// the metering point and the metering point list is requested per grid operator (BEG; P2P and EVA run centrally over
// ebUtilities like the BEG, platform#229).
func HasMultipleGridOperators(area AreaType) bool {
	return area == BEG || area == P2P || area == EVA
}

// edaEcTypes maps the community type to the EDA ECType the grid operator uses (the mapping of eda-xp).
var edaEcTypes = map[AreaType]string{GEA: "GC", LOCAL: "RC_L", REGIONAL: "RC_R", BEG: "CC", P2P: "PP", EVA: "SC"}

// EdaEcType returns the EDA ECType of a community type, false for an unknown type.
func EdaEcType(area AreaType) (string, bool) {
	t, ok := edaEcTypes[area]
	return t, ok
}

// DefaultEcZoneLevel is the Nahebereich of a new registration until the first metering point list: GEA 1, LOCAL 3,
// REGIONAL 4, BEG 7; P2P and EVA are unknown (the migration 20261106100000 fills existing rows the same way).
func DefaultEcZoneLevel(area AreaType) null.Int {
	switch area {
	case GEA:
		return null.IntFrom(1)
	case LOCAL:
		return null.IntFrom(3)
	case REGIONAL:
		return null.IntFrom(4)
	case BEG:
		return null.IntFrom(7)
	}
	return null.Int{}
}

// validCode is a TechCode/FuelCode that may be stored: 1–10 letters, digits, ".", "_" or "-".
var validCode = regexp.MustCompile(`^[A-Za-z0-9._-]{1,10}$`)

// ValidCode: a TechCode/FuelCode of the metering point list that may be stored; another one is dropped, never
// stored truncated.
func ValidCode(code string) bool {
	return validCode.MatchString(code)
}

// ValidEcZoneLevel: ECZoneLevel (Nahebereich) is 1–7.
func ValidEcZoneLevel(level int) bool {
	return level >= 1 && level <= 7
}

type AddressType string

const (
	BILLING   AddressType = "BILLING"
	RESIDENCE AddressType = "RESIDENCE"
)

// Scan reads NULL as an empty type. The member list LEFT JOINs both addresses, so
// a member without one of them yields NULL here; that must not fail the whole list
// (261029-mm1).
func (t *AddressType) Scan(src any) error {
	var s null.String
	if err := s.Scan(src); err != nil {
		return err
	}
	*t = AddressType(s.String)
	return nil
}

type Address struct {
	Type         AddressType `json:"type,omitempty" goqu:"skipupdate"`
	Street       null.String `json:"street,omitempty"  goqu:"omitempty,omitnil"`
	StreetNumber null.String `json:"streetNumber,omitempty" db:"streetNumber" goqu:"omitempty,omitnil"`
	Zip          null.String `json:"zip,omitempty" goqu:"omitempty,omitnil"`
	City         null.String `json:"city,omitempty" goqu:"omitempty,omitnil"`
}

type EegAddress struct {
	Street       string `json:"street,omitempty" goqu:"omitempty"`
	StreetNumber string `json:"streetNumber,omitempty" db:"streetNumber" goqu:"omitempty"`
	Zip          string `json:"zip,omitempty" goqu:"omitempty"`
	City         string `json:"city,omitempty" goqu:"omitempty"`
}

type Contact struct {
	Phone null.String `json:"phone,omitempty" goqu:"omitempty"`
	Email null.String `json:"email,omitempty" goqu:"omitempty"`
}

type AccountInfo struct {
	Iban        null.String `json:"iban" goqu:"omitempty"`
	Owner       null.String `json:"owner" goqu:"omitempty"`
	BankName    null.String `json:"bankName" db:"bankName" goqu:"omitempty"`
	CreditorId  null.String `json:"creditorId" db:"creditor_id" goqu:"omitempty"`
	Bic         null.String `json:"bic" db:"bic" goqu:"omitempty"`
	Sepa        bool        `json:"sepa" db:"sepa" goqu:"omitempty"`
	BankPurpose null.String `json:"bankPurpose" db:"bankPurpose" goqu:"omitempty"`
}

type Optionals struct {
	Website null.String `json:"website,omitempty" goqu:"omitempty"`
}
