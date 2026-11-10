package database

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

const TABLE_EEG = "base.eeg"
const TABLE_EEG_ADDRESS = "base.address"

// ErrEegExists: the tenant is already registered; nothing was written (261015-ca1).
var ErrEegExists = errors.New("eeg already exists")

// ErrInvalidEeg: the registration carries a value outside the known ones (e.g. the community type); nothing was written.
var ErrInvalidEeg = errors.New("invalid eeg registration")

type EegRepository interface {
	GetEegById(ctx context.Context, tenant string) (*model.Eeg, error)
	GetEegByIdForUser(ctx context.Context, tenant string) (*model.Eeg, error)
	GetEegByEcId(ctx context.Context, edId string) (*model.Eeg, error)
	GetEegByEcIdAndMeters(ctx context.Context, ecId string, meters []string) (*model.Eeg, error)
	GetBaseEegByEcId(ctx context.Context, ecId string) (*model.Eeg, error)
	GetTenantsByRcNumber(ctx context.Context, rcNumber string) ([]string, error)
	UpdateEegPartial(ctx context.Context, tenant string, fields map[string]interface{}) error
	GetGridOperators(ctx context.Context) (map[string]string, error)
	MeteringPointGridOperators(ctx context.Context, tenant string) ([]string, error)
	FetchTenantsName(ctx context.Context, tenants []string, isSuperUser bool) ([]tenantsNameStruct, error)
	InsertEeg(ctx context.Context, tenant string, eeg *model.Eeg) error
	UpdateOnlineState(ctx context.Context, tenant string, onlineState bool) error
	UpdateEcZoneLevel(ctx context.Context, ecId string, level int) error
	EcIdBelongsToRc(ctx context.Context, ecId, rcNumber string) (bool, error)
	UpdateEegAreaFromList(ctx context.Context, ecId string, area model.AreaType) ([]AreaChange, error)
	ChangeSettlementInterval(ctx context.Context, tenant, newInterval, changedBy string, state model.BillingState,
		dryRun bool) (model.SettlementIntervalDecision, error)
}

func (db *sqlDatabase) GetEegById(ctx context.Context, tenant string) (*model.Eeg, error) {
	return getEegById(ctx, db.db, tenant)
}
func (db *sqlDatabase) GetEegByIdForUser(ctx context.Context, tenant string) (*model.Eeg, error) {
	eeg, err := getEegById(ctx, db.db, tenant)
	if err != nil {
		return nil, err
	}
	eeg.BankName = null.String{}
	eeg.BusinessNr = null.String{}
	eeg.Contact = model.Contact{}
	eeg.Phone = null.String{}
	eeg.TaxNumber = null.String{}
	eeg.VatNumber = null.String{}

	return eeg, nil
}

func (db *sqlDatabase) GetEegByEcId(ctx context.Context, edId string) (*model.Eeg, error) {
	return getEegByEcId(ctx, db.db, edId)
}

// GetEegByEcIdAndMeters returns the community of ecId that holds the metering points of an inbound message. In a
// GEA several tenants share one community id (261028-mm1): the tenant holding one of the metering points wins — a
// live assigned row (flag 1, not INACTIVE, e.g. a registration running) before an ended one (revoked, still flag 1),
// before only a moved (0) or archived (2) row. When none of the tenants holds a metering point: the GEA's base tenant
// (GetBaseEegByEcId). With one tenant per community id: GetEegByEcId, as before.
func (db *sqlDatabase) GetEegByEcIdAndMeters(ctx context.Context, ecId string, meters []string) (*model.Eeg, error) {
	tenants, err := db.TenantsByEcId(ctx, ecId)
	if err != nil {
		return nil, model.ErrGetEeg(err)
	}
	if len(tenants) < 2 {
		return getEegByEcId(ctx, db.db, ecId)
	}
	owners := []string{}
	if len(meters) > 0 {
		query, args, err := sqlx.In(`SELECT tenant FROM base.meteringpoint WHERE tenant IN (?) AND metering_point_id IN (?)
			GROUP BY tenant ORDER BY bool_or(flag = 1 AND status <> 'INACTIVE') DESC, bool_or(flag = 1) DESC, tenant`, tenants, meters)
		if err != nil {
			return nil, model.ErrGetEeg(err)
		}
		if err = db.db.SelectContext(ctx, &owners, db.db.Rebind(query), args...); err != nil {
			return nil, model.ErrGetEeg(err)
		}
	}
	switch {
	case len(owners) == 0:
		log.WithContext(ctx).WithField("ecId", ecId).Warnf("none of the tenants %v holds the metering points %v; using the base tenant", tenants, meters)
		return db.GetBaseEegByEcId(ctx, ecId)
	case len(owners) > 1:
		log.WithContext(ctx).WithField("ecId", ecId).Warnf("the metering points %v are held by the tenants %v; applying to %s", meters, owners, owners[0])
	}
	return getEegById(ctx, db.db, owners[0])
}

// GetBaseEegByEcId returns the base tenant of a community id: the tenant named like its RC number (a GEA's
// GCxxxxxx beside GCxxxxxx-00n), else the first by name. Deterministic, unlike GetEegByEcId (261028-mm1).
func (db *sqlDatabase) GetBaseEegByEcId(ctx context.Context, ecId string) (*model.Eeg, error) {
	var tenant string
	if err := db.db.GetContext(ctx, &tenant, `SELECT tenant FROM base.eeg WHERE "communityId" = $1
		ORDER BY upper(tenant) = upper("rcNumber") DESC, tenant LIMIT 1`, ecId); err != nil {
		return nil, model.ErrGetEeg(err)
	}
	return getEegById(ctx, db.db, tenant)
}

// GetTenantsByRcNumber returns the tenants of an RC number (MQTT topic of incoming EDA messages).
// A GEA owns several tenants per RC number (GC100019-001, GC100019-002, …).
func (db *sqlDatabase) GetTenantsByRcNumber(ctx context.Context, rcNumber string) ([]string, error) {
	stmt, args, err := pgDialect.From(TABLE_EEG).Select("tenant").Where(goqu.Or(
		goqu.Func("upper", goqu.I("rcNumber")).Eq(strings.ToUpper(rcNumber)),
		goqu.Func("upper", goqu.C("tenant")).Eq(strings.ToUpper(rcNumber)),
	)).Order(goqu.C("tenant").Asc()).Prepared(true).ToSQL()
	if err != nil {
		return nil, model.ErrGetEeg(err)
	}
	var tenants []string
	if err = db.db.SelectContext(ctx, &tenants, stmt, args...); err != nil {
		return nil, model.ErrGetEeg(err)
	}
	return tenants, nil
}

func (db *sqlDatabase) UpdateEegPartial(ctx context.Context, tenant string, fields map[string]interface{}) error {
	return updateEegPartial(ctx, db.db, tenant, fields)
}

func (db *sqlDatabase) GetGridOperators(ctx context.Context) (map[string]string, error) {
	return getGridOperators(ctx, db.db)
}

func (db *sqlDatabase) FetchTenantsName(ctx context.Context, tenants []string, isSuperUser bool) ([]tenantsNameStruct, error) {
	return fetchTenantsName(ctx, db.db, tenants, isSuperUser)
}

func (db *sqlDatabase) InsertEeg(ctx context.Context, tenant string, eeg *model.Eeg) error {
	return insertEeg(ctx, db.db, tenant, eeg)
}

// EcIdBelongsToRc: one of the tenants of the community id belongs to the RC number, e.g. the RC number of the topic
// an inbound message came in on — by its RC number or its name, case-insensitive, the rule of GetTenantsByRcNumber.
func (db *sqlDatabase) EcIdBelongsToRc(ctx context.Context, ecId, rcNumber string) (bool, error) {
	var ok bool
	err := db.db.GetContext(ctx, &ok, `SELECT EXISTS (SELECT 1 FROM base.eeg WHERE "communityId" = $1
		AND (upper("rcNumber") = upper($2) OR upper(tenant) = upper($2)))`, ecId, rcNumber)
	return ok, err
}

// AreaChange is a community type changed by the grid operator's metering point list.
type AreaChange struct {
	Tenant string         `db:"tenant"`
	Old    model.AreaType `db:"old"`
}

// UpdateEegAreaFromList sets LOCAL or REGIONAL, as the grid operator's metering point list says (ECType RC_L/RC_R),
// at every tenant of the community id that has the other of the two; no other type is changed (platform#229,
// decided 05.11.2026). A switch clears the Nahebereich: the old one belongs to the old type; the caller sets the
// list's own level afterwards (eegfaktura-dev#56 #10). A system path: the generic updates still drop area.
func (db *sqlDatabase) UpdateEegAreaFromList(ctx context.Context, ecId string, area model.AreaType) ([]AreaChange, error) {
	if ecId == "" || (area != model.LOCAL && area != model.REGIONAL) {
		return nil, fmt.Errorf("no area change from the list to %q", string(area))
	}
	changes := []AreaChange{}
	err := db.db.SelectContext(ctx, &changes, `UPDATE base.eeg e SET area = $1, ec_zone_level = NULL
		FROM (SELECT tenant, area AS old FROM base.eeg WHERE "communityId" = $2 AND area IN ('LOCAL', 'REGIONAL')
		      AND area <> $1 FOR UPDATE) o
		WHERE e.tenant = o.tenant RETURNING e.tenant, o.old`, string(area), ecId)
	return changes, err
}

// UpdateEcZoneLevel sets the Nahebereich of every tenant of the community id (several for a GEA) from the grid
// operator's metering point list; an invalid level is refused (platform#229).
func (db *sqlDatabase) UpdateEcZoneLevel(ctx context.Context, ecId string, level int) error {
	if ecId == "" {
		return errors.New("no community id")
	}
	if !model.ValidEcZoneLevel(level) {
		return fmt.Errorf("invalid ECZoneLevel %d", level)
	}
	_, err := db.db.ExecContext(ctx, `UPDATE base.eeg SET ec_zone_level = $1 WHERE "communityId" = $2`, level, ecId)
	return err
}

func (db *sqlDatabase) UpdateOnlineState(ctx context.Context, tenant string, onlineState bool) error {

	stmt, _, err := goqu.Update(TABLE_EEG).
		Set(goqu.Record{"online": onlineState}).
		Where(goqu.Ex{"tenant": goqu.V(tenant)}).
		ToSQL()

	if err != nil {
		return err
	}

	_, err = db.db.ExecContext(ctx, stmt)
	if err != nil {
		return err
	}
	return nil
}

func getEegById(ctx context.Context, tx *sqlx.DB, tenant string) (*model.Eeg, error) {

	var eeg model.Eeg
	stmt, args, err := pgDialect.From(TABLE_EEG).Select(&eeg).Where(goqu.C("tenant").Eq(tenant)).Prepared(true).ToSQL()
	if err != nil {
		return nil, model.ErrGetEeg(err)
	}

	err = tx.GetContext(ctx, &eeg, stmt, args...)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, model.ErrGetEeg(err)
	}
	return &eeg, nil
}

func getEegByEcId(ctx context.Context, tx *sqlx.DB, edId string) (*model.Eeg, error) {

	var eeg model.Eeg
	stmt, args, err := pgDialect.From(TABLE_EEG).Select(&eeg).Where(goqu.C("communityId").Eq(edId)).Prepared(true).ToSQL()
	if err != nil {
		return nil, model.ErrGetEeg(err)
	}

	err = tx.GetContext(ctx, &eeg, stmt, args...)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, model.ErrGetEeg(err)
	}
	return &eeg, nil
}

func insertEeg(ctx context.Context, db *sqlx.DB, tenant string, eeg *model.Eeg) error {

	// Both registrations (gRPC, MQTT register-eeg) come through here: only a known community type (platform#229).
	if err := model.ValidateArea(eeg.Area); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEeg, err)
	}
	eeg.EcZoneLevel = model.DefaultEcZoneLevel(eeg.Area)

	// Tenants compare case-insensitively, the primary key only sees the exact spelling. Outside GEA an RC number
	// belongs to one community: a new tenant on it would reach that community's login and Ponton account.
	var exists bool
	if err := db.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM base.eeg WHERE upper(tenant) = upper($1)
		OR ($2 AND upper("rcNumber") = upper($3)))`, eeg.Id, eeg.Area != model.GEA, eeg.RcNumber); err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return err
	}
	if exists {
		return ErrEegExists
	}

	// Same shared address rule as updateEegPartial — the create path
	// must not be the one write that skips enforcement.
	if eeg.Contact.Email.Valid {
		normalized, err := model.ValidateEmailList(eeg.Contact.Email.String)
		if err != nil {
			return err
		}
		if normalized == "" {
			eeg.Contact.Email = null.String{}
		} else {
			eeg.Contact.Email = null.StringFrom(normalized)
		}
	}

	sql, _, err := pgDialect.Insert(TABLE_EEG).Rows(eeg).OnConflict(goqu.DoNothing()).ToSQL()
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, sql)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "INSERT").Error("SQL statement failed")
		return err
	}
	// a parallel registration in the same spelling was faster
	inserted, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return ErrEegExists
	}

	return nil
}

// eegProtectedUpdateFields must never be set through the generic EEG update:
// the identity/tenant key and rcNumber/online/createdAt (model: skipupdate),
// plus communityId and tenant, which route incoming EDA messages and identify
// the community, and settlementInterval, which changes only through
// ChangeSettlementInterval with its rule (konzept-abrechnungsintervall.md), and
// area and allocationMode, which are set at registration and otherwise only by
// the operator (platform#229, E5: a P2P/EVA community must not switch itself to
// an EEG type and around the billing block). The web round-trips the whole EEG
// object on save, so these are dropped silently rather than rejected.
var eegProtectedUpdateFields = func() map[string]struct{} {
	m := map[string]struct{}{"tenant": {}, "communityId": {}, "settlementInterval": {}, "area": {}, "allocationMode": {}}
	for _, k := range model.SkipUpdateJSONKeys(model.Eeg{}) {
		m[k] = struct{}{}
	}
	return m
}()

func updateEegPartial(ctx context.Context, db *sqlx.DB, tenant string, fields map[string]interface{}) error {
	// Drop write-protected fields so a client cannot set tenant, rcNumber,
	// communityId etc. through this generic update (mass assignment).
	for k := range eegProtectedUpdateFields {
		delete(fields, k)
	}

	// eeg.Email is the recipient of the ZP list mail and the billing CC —
	// enforce the shared address rule before persisting (normalize,
	// reject invalid).
	if raw, ok := fields["email"].(string); ok {
		normalized, err := model.ValidateEmailList(raw)
		if err != nil {
			return err
		}
		if normalized == "" {
			fields["email"] = nil
		} else {
			fields["email"] = normalized
		}
	}

	// Map every key to a known, updatable column. The key ends up as an SQL
	// identifier, which goqu quotes but does not escape, so an unknown key is
	// rejected instead of being passed through verbatim.
	updateRecord := goqu.Record{}
	for k, v := range fields {
		col, ok := model.ResolveFlatUpdateColumn(model.Eeg{}, k)
		if !ok {
			return fmt.Errorf("field %q cannot be updated", k)
		}
		updateRecord[col] = v
	}
	if len(updateRecord) == 0 {
		return nil
	}

	statement, _, err := pgDialect.Update(TABLE_EEG).Set(updateRecord).Where(goqu.Ex{"tenant": goqu.V(tenant)}).ToSQL()
	if err != nil {
		log.WithContext(ctx).WithError(err).Error("Update EEG VALUES failed")
		return err
	}

	_, err = db.ExecContext(ctx, statement)
	return err
}

func getGridOperators(ctx context.Context, db *sqlx.DB) (map[string]string, error) {
	return queryGridOperatorNames(ctx, db)
}

type tenantsNameStruct struct {
	Tenant string `json:"tenant" db:"tenant"`
	Name   string `json:"name" db:"name"`
}

func fetchTenantsName(ctx context.Context, db *sqlx.DB, tenants []string, isSuperUser bool) ([]tenantsNameStruct, error) {
	tenantsName := []tenantsNameStruct{}
	selectStmt := pgDialect.From(TABLE_EEG).Select(&tenantsName)
	if !isSuperUser {
		selectStmt = selectStmt.Where(goqu.C("tenant").In(tenants))
	}
	stmt, _, err := selectStmt.ToSQL()
	if err != nil {
		return nil, err
	}
	if err := db.SelectContext(ctx, &tenantsName, stmt); err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, err
	}
	return tenantsName, nil
}
