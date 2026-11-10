package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

type MeteringPointRepository interface {
	RestoreMeteringPointProcessState(ctx context.Context, tenant, meterId string) error
	MeteringPointsSetStatus(ctx context.Context, tenant string, processState model.ProcessStatusType, statusCode *int16, meterId []string, activeSince *civil.Date, consentId *string) error
	FindActiveMeteringByIds(ctx context.Context, tenant string, meterIds []string) ([]*model.MeteringPoint, error)
	FindMeteringPointsActivePeriod(ctx context.Context, tenant string, activeSince, inactiveSince int64) ([]*model.MeteringPoint, error)
	FindMeteringByStatus(ctx context.Context, tenant, meterId string, status model.StatusType) (*model.MeteringPoint, error)
	FindNewMeteringById(ctx context.Context, tenant string, meterId string) ([]*model.MeteringPoint, error)
	FindInactiveMeteringById(ctx context.Context, tenant string, meterId string) ([]*model.MeteringPoint, error)
	FindMeteringById(ctx context.Context, tenant string, meterId string) (*model.MeteringPoint, error)
	FindAssignedMeteringById(ctx context.Context, tenant string, meterId string) (*model.MeteringPoint, error)
	FindMeteringByIds(ctx context.Context, tenant string, meterIds []string) ([]*model.MeteringPoint, error)
	FindMeteringPointsForTenant(ctx context.Context, tenant string) ([]*model.MeteringPoint, error)
	FindAllMeteringByTenant(ctx context.Context, tenant, participantId string, meterIds []string) ([]*model.MeteringPoint, error)
	RegisterMeteringPoint(ctx context.Context, tenant, username, participantId string, point *model.MeteringPoint) error
	RemoveMeteringPoint(ctx context.Context, tenant, participantId, meterId string) error
	MeteringPointRevoke(ctx context.Context, tenant, meterId string, consentEnd civil.Date) error
	MeteringPointRevokeActive(ctx context.Context, tenant, meterId string, inactiveSince civil.Date) (bool, error)
	MeteringPointRevokeByConsentId(ctx context.Context, receiver string, consentId *string, meterId string, consentEnd civil.Date) ([]RevokedTenant, error)
	ImportMeteringPoints(ctx context.Context, tenant, username, participantId string, point []*model.MeteringPoint) error
	UpdateMeteringPoint(ctx context.Context, tenant, username, participantId, meterId string, meteringPoint *model.MeteringPoint) error
	UpdateMeteringPointPartial(ctx context.Context, tenant, username, participantId, meterId string, values map[string]interface{}) error
	UpdateActiveMeteringPoints(ctx context.Context, tenant string, ml []model.Meter) error
	MeteringPointChangePartFactor(ctx context.Context, tenant, participantId string, meters []model.Meter) error
	MoveMeteringPoint(ctx context.Context, tenant, username, sParticipantId, dParticipantId, meterId string) error
	ArchiveMeteringPoint(ctx context.Context, tenant, participantId, meterId string) error
	UpdateProcessStatus(
		ctx context.Context,
		tenant string, meters []string,
		processState model.ProcessStatusType, statusCode *int16, activeSince, inactiveSince *civil.Date, consentId *string) error
	UpdateActiveSinceDate(ctx context.Context, tenant, participantId, meter, username string, activeSince *civil.Date) error
	UpdateInActiveSinceDate(ctx context.Context, tenant, participantId, meter, username string, inactiveSince *civil.Date) error
}

func (db *sqlDatabase) RestoreMeteringPointProcessState(ctx context.Context, tenant, meterId string) error {
	return restoreMeteringPointProcessState(ctx, db.db, tenant, meterId)
}

func (db *sqlDatabase) MeteringPointsSetStatus(ctx context.Context, tenant string, processState model.ProcessStatusType, statusCode *int16, meterId []string, activeSince *civil.Date, consentId *string) error {
	return meteringPointsSetStatus(ctx, db.db, tenant, processState, statusCode, meterId, activeSince, consentId)
}

func (db *sqlDatabase) FindActiveMeteringByIds(ctx context.Context, tenant string, meterIds []string) ([]*model.MeteringPoint, error) {
	return findActiveMeteringByIds(ctx, db.db, tenant, meterIds)
}

func (db *sqlDatabase) FindMeteringPointsActivePeriod(ctx context.Context, tenant string, activeSince, inactiveSince int64) ([]*model.MeteringPoint, error) {
	return findMeteringPointsActivePeriod(ctx, db.db, tenant, activeSince, inactiveSince)
}

func (db *sqlDatabase) FindMeteringByStatus(ctx context.Context, tenant, meterId string, status model.StatusType) (*model.MeteringPoint, error) {
	return findMeteringByStatus(ctx, db.db, tenant, meterId, status)
}

func (db *sqlDatabase) FindNewMeteringById(ctx context.Context, tenant string, meterId string) ([]*model.MeteringPoint, error) {
	return findNewMeteringById(ctx, db.db, tenant, meterId)
}

func (db *sqlDatabase) FindInactiveMeteringById(ctx context.Context, tenant string, meterId string) ([]*model.MeteringPoint, error) {
	return findInactiveMeteringById(ctx, db.db, tenant, meterId)
}

func (db *sqlDatabase) FindMeteringById(ctx context.Context, tenant string, meterId string) (*model.MeteringPoint, error) {
	return findMeteringById(ctx, db.db, tenant, meterId)
}

func (db *sqlDatabase) FindAssignedMeteringById(ctx context.Context, tenant string, meterId string) (*model.MeteringPoint, error) {
	return findAssignedMetering(ctx, db.db, tenant, meterId)
}

func (db *sqlDatabase) FindMeteringByIds(ctx context.Context, tenant string, meterIds []string) ([]*model.MeteringPoint, error) {
	return findMeteringByIds(ctx, db.db, tenant, meterIds)
}

func (db *sqlDatabase) FindMeteringPointsForTenant(ctx context.Context, tenant string) ([]*model.MeteringPoint, error) {
	return findMeteringPointsForTenant(ctx, db.db, tenant)
}

func (db *sqlDatabase) FindAllMeteringByTenant(ctx context.Context, tenant, participantId string, meterIds []string) ([]*model.MeteringPoint, error) {
	return findAllMeteringByTenant(ctx, db.db, tenant, participantId, meterIds)
}

func (db *sqlDatabase) RegisterMeteringPoint(ctx context.Context, tenant, username, participantId string, point *model.MeteringPoint) error {
	return registerMeteringPoint(ctx, db.db, tenant, username, participantId, point)
}

func (db *sqlDatabase) RemoveMeteringPoint(ctx context.Context, tenant, participantId, meterId string) error {
	return removeMeteringPoint(ctx, db.db, tenant, participantId, meterId)
}

func (db *sqlDatabase) MeteringPointRevoke(ctx context.Context, tenant, meterId string, consentEnd civil.Date) error {
	return meteringPointRevoke(ctx, db.db, tenant, meterId, consentEnd)
}

func (db *sqlDatabase) MeteringPointRevokeActive(ctx context.Context, tenant, meterId string, inactiveSince civil.Date) (bool, error) {
	return meteringPointRevokeActive(ctx, db.db, tenant, meterId, inactiveSince)
}

func (db *sqlDatabase) MeteringPointRevokeByConsentId(ctx context.Context, receiver string, consentId *string, meterId string, consentEnd civil.Date) ([]RevokedTenant, error) {
	return meteringPointRevokeByConsentId(ctx, db.db, receiver, consentId, meterId, consentEnd)
}

func (db *sqlDatabase) ImportMeteringPoints(ctx context.Context, tenant, username, participantId string, point []*model.MeteringPoint) error {
	return inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		return ImportMeteringPoints(ctx, tx, tenant, username, participantId, point)
	})
}

func (db *sqlDatabase) UpdateMeteringPoint(ctx context.Context, tenant, username, participantId, meterId string, point *model.MeteringPoint) error {
	return UpdateMeteringPoint(ctx, db.db, tenant, username, participantId, meterId, point)
}

func (db *sqlDatabase) UpdateMeteringPointPartial(ctx context.Context, tenant, username, participantId, meterId string, values map[string]interface{}) error {
	return UpdateMeteringPointPartial(ctx, db.db, tenant, username, participantId, meterId, values)
}

func (db *sqlDatabase) UpdateActiveMeteringPoints(ctx context.Context, tenant string, ml []model.Meter) error {
	return UpdateActiveMeteringPoints(ctx, db.db, tenant, ml)
}

func (db *sqlDatabase) MeteringPointChangePartFactor(ctx context.Context, tenant, participantId string, meters []model.Meter) error {
	return MeteringPointChangePartFactor(ctx, db.db, tenant, participantId, meters)
}

func (db *sqlDatabase) MoveMeteringPoint(ctx context.Context, tenant, username, sParticipantId, dParticipantId, meterId string) error {
	return moveMeteringPoint(ctx, db.db, tenant, username, sParticipantId, dParticipantId, meterId)
}

func (db *sqlDatabase) ArchiveMeteringPoint(ctx context.Context, tenant, participantId, meterId string) error {
	return archiveMeteringPoint(ctx, db.db, tenant, participantId, meterId)
}

func (db *sqlDatabase) UpdateProcessStatus(
	ctx context.Context,
	tenant string, meters []string,
	processState model.ProcessStatusType, statusCode *int16, activeSince, inactiveSince *civil.Date, consentId *string) error {

	var defaultStatusCode int16 = 0

	switch processState {
	case model.NEW:
		fallthrough
	case model.PENDING:
		fallthrough
	case model.INIT:
		return db.MeteringPointsSetStatus(ctx, tenant, processState, &defaultStatusCode, meters, nil, nil)
	case model.APPROVED:
		return db.MeteringPointsSetStatus(ctx, tenant, processState, &defaultStatusCode, meters, nil, consentId)
	case model.ACTIVE:
		return db.MeteringPointsSetStatus(ctx, tenant, processState, &defaultStatusCode, meters, activeSince, consentId)
	case model.REVOKED:
		return db.MeteringPointsSetStatus(ctx, tenant, processState, statusCode, meters, nil, nil)
	case model.INACTIVE:
		if inactiveSince == nil {
			today := civil.Today()
			inactiveSince = &today
		}
		return db.MeteringPointRevoke(ctx, tenant, meters[0], *inactiveSince)
	}
	return errors.New("invalid process state")
}

func (db *sqlDatabase) UpdateActiveSinceDate(ctx context.Context, tenant, participantId, meter, username string, activeSince *civil.Date) error {
	return UpdateMeteringPointPartial(ctx, db.db, tenant, username, participantId, meter, map[string]interface{}{"activesince": activeSince})
}

func (db *sqlDatabase) UpdateInActiveSinceDate(ctx context.Context, tenant, participantId, meter, username string, inactiveSince *civil.Date) error {
	return UpdateMeteringPointPartial(ctx, db.db, tenant, username, participantId, meter, map[string]interface{}{"inactivesince": inactiveSince})
}

const TABLE_METERINGPOINT = "base.meteringpoint"

// MIGRATION_CONSENT_ID is the placeholder consent id of metering points taken over from the old
// system; for revocations it counts as "no consent id".
const MIGRATION_CONSENT_ID = "Migration"
const TABLE_PARTITION_FACT = "base.metering_partition_factor"
const TABLE_PARTITION_FACT_VIEW = "base.activemeteringpartition"

type meteringEntryType struct {
	*model.MeteringPoint
	Participant_id string            `goqu:"skipupdate"`
	Tenant         string            `goqu:"skipupdate"`
	ActiveSince    civil.NullDate    `db:"activesince" goqu:"omitempty"`
	InactiveSince  civil.NullDate    `db:"inactivesince" goqu:"omitempty"`
	Active         int               `db:"active"`
	Flag           model.ProcessFlag `goqu:"skipupdate"`
}

type partitionFactorRecord struct {
	MeteringPoint  string `db:"metering_point_id"`
	Participant_id string `goqu:"skipupdate"`
	Tenant         string `goqu:"skipupdate"`
	PartFact       int    `db:"partFact"`
	CreatedBy      string `db:"createdBy"`
}

func createMeteringEntries(tenant, username, participantId string, points []*model.MeteringPoint, processState *model.ProcessStatusType) ([]*meteringEntryType, []*partitionFactorRecord) {
	var partFactEntries []*partitionFactorRecord
	var meteringEntries []*meteringEntryType

	getDateWithDefault := func(d civil.NullDate, defaultDate civil.NullDate) civil.NullDate {
		if d.Valid {
			return d
		}
		return defaultDate
	}

	for _, p := range points {
		var activeSince civil.NullDate
		var inactiveSince civil.NullDate

		if processState != nil {
			p.ProcessState = *processState
		}
		p.ModifiedBy = null.StringFrom(username)
		p.ModifiedAt = civil.Now()
		if p.RegisteredSince.IsZero() {
			p.RegisteredSince = civil.Today()
		}
		if len(p.ProcessState) == 0 {
			p.ProcessState = model.NEW
		} else if p.ProcessState == model.ACTIVE {
			if p.State != nil {
				activeSince = getDateWithDefault(p.State.ActiveSince, civil.NullDate{Date: p.RegisteredSince, Valid: true})
				inactiveSince = getDateWithDefault(p.State.InactiveSince, civil.NullDate{Date: civil.DateFor(2999, 12, 31), Valid: true})
			} else {
				activeSince = civil.NullDate{Date: p.RegisteredSince, Valid: true}
				inactiveSince = civil.NullDate{Date: civil.DateFor(2999, 12, 31), Valid: true}
			}
		}
		p.Status = calcState(p.ProcessState)

		meteringEntries = append(meteringEntries,
			&meteringEntryType{p, participantId, tenant,
				activeSince, inactiveSince, int(calcActive(p.Status)), model.F_ASSIGNED})

		partFactEntries = append(partFactEntries, &partitionFactorRecord{
			MeteringPoint:  p.MeteringPoint,
			Participant_id: participantId,
			Tenant:         tenant,
			PartFact:       p.PartFact,
			CreatedBy:      username,
		})
	}
	return meteringEntries, partFactEntries
}

func ImportMeteringPoints(ctx context.Context, tx *sqlx.Tx, tenant, username, participantId string, point []*model.MeteringPoint) error {
	if err := applyGridOperator(ctx, tx, tenant, point); err != nil {
		return err
	}
	meteringEntries, partFactEntries := createMeteringEntries(tenant, username, participantId, point, nil)
	return saveMeteringPoint(ctx, tx, tenant, meteringEntries, partFactEntries)
}

// saveMeteringPoint creates new metering point in the database.
// Accourding to the status of new metering point (ACTIVE when excel import; NEW otherwise) the flag of the meterstate will be adapted
func saveMeteringPoint(ctx context.Context, tx *sqlx.Tx, tenant string, meteringEntry []*meteringEntryType, partFactEntries []*partitionFactorRecord) error {
	if len(meteringEntry) == 0 || len(partFactEntries) == 0 {
		log.WithContext(ctx).Warn("Save Meteringpoints with empty list of metering points or partFact entries")
		return nil
	}

	// Check if a metering point already exists. Even when this metering point will be assigned to a new participant.
	// Because of dependencies in invoices it is not possible to delete existing relationships between participant and metering point
	// All existing metering points will be tagged to moved (flag = 0)
	meterIds := make([]string, 0)
	for _, entry := range meteringEntry {
		meterIds = append(meterIds, entry.MeteringPoint.MeteringPoint)
	}

	existingMeters := []string{}
	stmtExisting, _, err := pgDialect.From(TABLE_METERINGPOINT).Select("metering_point_id").
		Where(goqu.Ex{
			"metering_point_id": meterIds,
			"tenant":            tenant,
			"status":            model.S_INACTIVE},
		).ToSQL()

	if err := tx.Select(&existingMeters, stmtExisting); err == nil && len(existingMeters) > 0 {
		updateExistingStmt, _, err := pgDialect.Update(TABLE_METERINGPOINT).
			Set(goqu.Record{"flag": 0, "process_state": model.INACTIVE}).
			Where(goqu.Ex{
				"metering_point_id": existingMeters,
				"tenant":            tenant,
				"status":            model.S_INACTIVE,
			}).
			ToSQL()
		if err != nil {
			return model.ErrSaveMeteringPoint(err)
		}
		_, err = tx.Exec(updateExistingStmt)
		if err != nil {
			log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
			return model.ErrSaveMeteringPoint(err)
		}
	}
	// -------------------------------------------

	statement, _, err := pgDialect.Insert(TABLE_METERINGPOINT).Rows(meteringEntry). /*.OnConflict(goqu.DoNothing())*/ ToSQL()
	if err != nil {
		return model.ErrSaveMeteringPoint(err)
	}
	_, err = tx.Exec(statement)
	if err != nil {
		if e := activeAtOtherParticipantErr(err, meterIds); e != nil {
			log.WithContext(ctx).WithError(err).Warn("metering point active at another member")
			return e
		}
		log.WithContext(ctx).WithError(err).Error("SQL statement failed")
		return model.ErrSaveMeteringPoint(err)
	}

	statement, _, err = pgDialect.Insert(TABLE_PARTITION_FACT).Rows(partFactEntries). /*.OnConflict(goqu.DoNothing())*/ ToSQL()
	if err != nil {
		return model.ErrSaveMeteringPoint(err)
	}
	_, err = tx.Exec(statement)

	if err != nil {
		log.WithContext(ctx).WithError(err).Error("SQL statement failed")
		return model.ErrSaveMeteringPoint(err)
	}

	return err
}

func calcActive(status model.StatusType) model.ProcessStatus {
	switch status {
	case model.S_INACTIVE:
		return model.P_INACTIVE
	default:
		return model.P_ACTIVE
	}
}

func calcActivePtr(status *model.StatusType) *model.ProcessStatus {
	if status == nil {
		return nil
	}
	state := calcActive(*status)
	return &state
}

// existingMeterAtParticipant: the metering point's row at this member of the tenant, in any state (the primary key
// is metering point + tenant + member), as ErrMeterExistsAtParticipant; nil when there is none (platform#247). A row
// that is not assigned (moved or archived) is hidden from the member's list, so its text does not point to the process.
func existingMeterAtParticipant(tx *sqlx.Tx, tenant, participantId, meterId string) error {
	stmt, args, err := pgDialect.From(TABLE_METERINGPOINT).Prepared(true).Select("flag").
		Where(goqu.Ex{"metering_point_id": meterId, "tenant": tenant, "participant_id": participantId}).Limit(1).ToSQL()
	if err != nil {
		return model.ErrSaveMeteringPoint(err)
	}
	var flag model.ProcessFlag
	switch err := tx.Get(&flag, stmt, args...); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return model.ErrSaveMeteringPoint(err)
	case flag != model.F_ASSIGNED:
		return model.ErrMeterExistsAtParticipant(errors.New(model.MeterHiddenAtParticipantText))
	}
	return meterExistsErr()
}

func meterExistsErr() error {
	return model.ErrMeterExistsAtParticipant(errors.New(model.MeterExistsAtParticipantText))
}

// isMeterKeyViolation: the insert hit the primary key of base.meteringpoint (a second request at the same time
// passed the check before the first committed). VfeegError has no Unwrap, so the wrapped error is looked at directly.
func isMeterKeyViolation(err error) bool {
	var ve *model.VfeegError
	if errors.As(err, &ve) {
		err = ve.Err
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505" && strings.EqualFold(pqErr.Constraint, "meteringpointpk")
}

// activeKeyValueRe reads the metering point from the detail of the violated index:
// "Key (metering_point_id, tenant, flag)=(AT..., TE..., 1) already exists."
var activeKeyValueRe = regexp.MustCompile(`\)=\(([^,]+),`)

// activeAtOtherParticipantErr: the insert hit idx_unique_meteringpoint_active, so the metering point is assigned to
// another member of the tenant (this member's own row is found before, platform#247). nil for any other error.
func activeAtOtherParticipantErr(err error, meterIds []string) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23505" ||
		!strings.EqualFold(pqErr.Constraint, "idx_unique_meteringpoint_active") || len(meterIds) == 0 {
		return nil
	}
	meter := meterIds[0]
	if m := activeKeyValueRe.FindStringSubmatch(pqErr.Detail); m != nil {
		meter = m[1]
	}
	return model.ErrMeterActiveAtOtherParticipant(fmt.Errorf(model.MeterActiveAtOtherParticipantText, meter))
}

func registerMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, username, participantId string, point *model.MeteringPoint) error {
	// Assert the participant belongs to the caller's tenant before inserting the
	// metering point — the row was previously written before the participant was
	// ever checked, so a foreign participant id would attach the meter (and its
	// billing) to another tenant's member.
	if err := assertParticipantTenant(ctx, db, tenant, participantId); err != nil {
		return err
	}

	// without the cancel of the request: a client that goes away does not roll back the save (as before inTx)
	err := inTx(context.WithoutCancel(ctx), db, func(tx *sqlx.Tx) error {
		// the same member cannot get the meter a second time (platform#247)
		if err := existingMeterAtParticipant(tx, tenant, participantId, point.MeteringPoint); err != nil {
			return err
		}
		if err := applyGridOperator(ctx, tx, tenant, []*model.MeteringPoint{point}); err != nil {
			return err
		}
		meteringEntries, partFactEntries := createMeteringEntries(tenant, username, participantId, []*model.MeteringPoint{point}, &point.ProcessState)
		return saveMeteringPoint(ctx, tx, tenant, meteringEntries, partFactEntries)
	})
	// two requests at the same time: the second one hits the key instead of the check above
	if isMeterKeyViolation(err) {
		err = meterExistsErr()
	}
	// the new row stores no TechCode/FuelCode (only the metering point list sets them): the caller gets none back,
	// not what the client sent (eegfaktura-dev#56 #14, register)
	point.TechCode, point.FuelCode = null.String{}, null.String{}
	return err
}

func moveMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, username, sParticipantId, dParticipantId, meterId string) error {
	// The UPDATE below scopes the source by tenant, but the target participant
	// comes from the path unchecked — assert it belongs to the caller's tenant,
	// otherwise a metering point could be moved onto a foreign participant (and
	// into that tenant's billing).
	if err := assertParticipantTenant(ctx, db, tenant, dParticipantId); err != nil {
		return err
	}

	statement, _, err := goqu.Update(TABLE_METERINGPOINT).
		Set(goqu.Record{"participant_id": dParticipantId}).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"participant_id":    goqu.Op{"eq": sParticipantId},
		}).
		ToSQL()
	if err != nil {
		return model.ErrUpdateMeter(err)
	}
	// one statement: no transaction needed
	if _, err := db.Exec(statement); err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrUpdateMeter(err)
	}
	return nil
}

func UpdateMeteringPointPartial(ctx context.Context, db *sqlx.DB, tenant, username, participantId, meterId string, values map[string]interface{}) error {

	values["modifiedBy"] = username
	values["modifiedAt"] = civil.Now()

	// The grid operator follows the metering point number (platform#107): a new number sets it,
	// otherwise it is only filled in when nothing is stored yet (rows from before #107).
	newId, idChanged := values["metering_point_id"].(string)
	if !idChanged {
		newId = meterId
	}
	point := &model.MeteringPoint{MeteringPoint: newId}
	if err := applyGridOperator(ctx, db, tenant, []*model.MeteringPoint{point}); err != nil {
		if idChanged {
			return model.ErrUpdateMeter(err)
		}
		// Only the fill-in of an empty value is lost; the update itself (e.g. activesince from an
		// EDA answer) must still go through.
		log.WithContext(ctx).WithField("tenant", tenant).Warnf("Grid operator of %s not filled in: %v", meterId, err)
	}
	if point.GridOperatorId.Valid {
		var name interface{}
		if point.GridOperatorName.Valid {
			name = point.GridOperatorName.String
		}
		if idChanged {
			values["grid_operator_id"] = point.GridOperatorId.String
			values["grid_operator_name"] = name
		} else {
			empty := goqu.L(`COALESCE("grid_operator_id", '') = ''`)
			values["grid_operator_id"] = goqu.Case().When(empty, point.GridOperatorId.String).Else(goqu.C("grid_operator_id"))
			values["grid_operator_name"] = goqu.Case().When(empty, name).Else(goqu.C("grid_operator_name"))
		}
	}

	statement, _, err := pgDialect.Update(TABLE_METERINGPOINT).Set(values).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"participant_id":    goqu.Op{"eq": participantId},
		}).
		ToSQL()

	if err != nil {
		return model.ErrUpdateMeter(err)
	}

	_, err = db.Exec(statement)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrUpdateMeter(err)
	}
	return nil
}

func UpdateMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, username, participantId, meterId string, meteringPoint *model.MeteringPoint) error {
	// The number in the path is the row that is updated (metering_point_id is skipupdate); derive
	// the grid operator from it, not from the body (platform#107).
	meteringPoint.MeteringPoint = meterId
	if meteringPoint.State == nil {
		return model.ErrUpdateMeter(errors.New("participantState is missing"))
	}
	if err := applyGridOperator(ctx, db, tenant, []*model.MeteringPoint{meteringPoint}); err != nil {
		return model.ErrUpdateMeter(err)
	}
	updateObject := *meteringPoint
	updateObject.State = nil
	updateObject.ModifiedBy = null.StringFrom(username)
	updateObject.ModifiedAt = civil.Now()

	updateEntry := meteringEntryType{
		MeteringPoint: &updateObject, ActiveSince: meteringPoint.State.ActiveSince, Active: int(calcActive(updateObject.Status)),
	}
	statement, _, err := goqu.Update(TABLE_METERINGPOINT).
		Set(updateEntry).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"participant_id":    goqu.Op{"eq": participantId},
		}).
		Returning("tech_code", "fuel_code").
		ToSQL()
	if err != nil {
		return model.ErrUpdateMeter(err)
	}
	// TechCode/FuelCode are not written here (only the metering point list sets them): the caller gets the stored
	// ones back, not what the client sent (eegfaktura-dev#56 #14); no row updated, none stored
	meteringPoint.TechCode, meteringPoint.FuelCode = null.String{}, null.String{}
	err = db.QueryRowx(statement).Scan(&meteringPoint.TechCode, &meteringPoint.FuelCode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrUpdateMeter(err)
	}
	return nil
}

// RemoveMeteringPoint Deletes metering point in the database. All relations to any participants will be gone.
// A metering point can only be removed even no activation was ever be established. The status column must be in state INIT.
// Not PENDING: the grid operator answered and the member can still approve. No matching row is an error, not a success.
// This indicates a metering point waiting for to be part of a community.
// In case of the metering point is revoked by the net operator the status flag is switching to INACTIVE.
// !!Never set the status to INIT once it has been activated!
func removeMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, participantId, meterId string) error {
	statement, _, err := goqu.Delete(TABLE_METERINGPOINT).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"participant_id":    goqu.Op{"eq": participantId},
			"process_state":     goqu.Op{"in": []string{"INVALID", "NEW"}},
			"status":            goqu.Op{"eq": "INIT"},
		}).
		ToSQL()
	if err != nil {
		return model.ErrRemoveMeteringPoint(err)
	}

	res, err := db.Exec(statement)
	if err != nil {
		return model.ErrRemoveMeteringPoint(err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return model.ErrRemoveMeteringPoint(err)
	}
	if removed == 0 {
		return model.ErrRemoveMeteringPoint(errors.New("metering point cannot be removed in its state"))
	}
	return nil
}

func calcState(processState model.ProcessStatusType) model.StatusType {
	switch processState {
	case model.INACTIVE:
		return model.S_INACTIVE
	case model.ACTIVE:
		return model.S_ACTIVE
	default:
		return model.S_INIT
	}
}

func archiveMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, participantId, meterId string) error {
	statement, _, err := goqu.Update(TABLE_METERINGPOINT).
		Set(goqu.Record{"status": model.S_INACTIVE, "process_state": model.ARCHIVED, "flag": 2}).
		Where(goqu.Ex{
			"participant_id":    goqu.Op{"eq": participantId},
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
		}).ToSQL()
	if err != nil {
		return model.ErrStatusMeter(err)
	}

	result, err := db.Exec(statement)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrStatusMeter(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows == 0 {
		log.WithContext(ctx).WithField("SQL", "UPDATE").Info("No Rows Affected")
	}
	return nil
}

func restoreMeteringPointProcessState(ctx context.Context, db *sqlx.DB, tenant, meterId string) error {

	sql, _, err := pgDialect.Update(TABLE_METERINGPOINT).
		Set(goqu.Record{"process_state": goqu.Case().
			// an ended participation was INACTIVE before the competing attempt (INIT); everything else
			// stays (a registration in flight, an active metering point). last_process_state only holds
			// a rejection that was not applied (meteringPointsSetStatus), never a state to go back to.
			When(goqu.And(endedParticipation(), goqu.C("process_state").Eq(string(model.INIT))), string(model.INACTIVE)).
			Else(goqu.C("process_state"))}).
		Where(goqu.Ex{"tenant": tenant, "metering_point_id": meterId, "flag": model.F_ASSIGNED}).ToSQL()

	if err != nil {
		return model.ErrStatusMeter(err)
	}

	_, err = db.Exec(sql)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrStatusMeter(err)
	}
	return nil
}

// endedParticipation matches a metering point whose participation has ended (status INACTIVE with a
// start and an end date). A rejected re-registration leaves it INACTIVE (konzept-zaehlpunkt-ausblenden.md,
// part 2). Without activesince it never took part (e.g. revoked via CCMS before activation).
func endedParticipation() exp.Expression {
	return goqu.And(goqu.C("status").Eq(string(model.S_INACTIVE)), goqu.C("inactivesince").IsNotNull(),
		goqu.C("activesince").IsNotNull())
}

// activeParticipation matches a metering point that takes part (status ACTIVE). A rejection with a
// code of model.REJECTED_KEEP_ACTIVE_CODES leaves it ACTIVE (decision 24.10.2026).
func activeParticipation() exp.Expression {
	return goqu.C("status").Eq(string(model.S_ACTIVE))
}

// MeteringPointsSetStatus The MeteringPointSetStatus method handles only ECON or ECOF messages.
// It requires that only one metering point of a community is assigned (flag == 1).
func meteringPointsSetStatus(ctx context.Context, db *sqlx.DB, tenant string, processState model.ProcessStatusType, statusCode *int16, meterId []string, activeSince *civil.Date, consentId *string) error {

	// setStatus In case of the ABSCHLUSS message is received before the APPROVED message the processtatus should remain in ACTIVE mode.
	setStatus := func(status model.ProcessStatusType) goqu.Expression {
		switch status {
		case model.APPROVED:
			return goqu.Case().When(goqu.C("process_state").Eq(string(model.ACTIVE)), string(model.ACTIVE)).Else(goqu.V(status))
		default:
			return goqu.V(status)
		}
	}

	updateRecord := map[string]interface{}{
		"process_state": setStatus(processState),
		"modifiedAt":    civil.Now(),
		"modifiedBy":    "EVU",
	}

	// A rejection does not undo a participation: an ended metering point stays INACTIVE (any code), an
	// active one stays ACTIVE when the code concerns the request only (REJECTED_KEEP_ACTIVE_CODES;
	// INIT/PENDING/APPROVED of that request go back to ACTIVE, REVOKED and other states stay). The
	// rejected state goes to last_process_state, the code to statusCode, the notification is written
	// as before. Never-active (INIT) metering points, and active ones with other codes, take the
	// rejected state. Any other message starts or ends a registration: the saved rejection no longer applies.
	if processState == model.INVALID || processState == model.REJECTED {
		kept := endedParticipation()
		processCase := goqu.Case().When(endedParticipation(), string(model.INACTIVE))
		if statusCode != nil && slices.Contains(model.REJECTED_KEEP_ACTIVE_CODES, *statusCode) {
			kept = goqu.Or(endedParticipation(), activeParticipation())
			// the request just rejected left INIT/PENDING/APPROVED: back to ACTIVE; any other state stays
			processCase = processCase.
				When(goqu.And(activeParticipation(), goqu.C("process_state").In(
					string(model.INIT), string(model.PENDING), string(model.APPROVED))), string(model.ACTIVE)).
				When(activeParticipation(), goqu.C("process_state"))
		}
		updateRecord["process_state"] = processCase.Else(goqu.V(processState))
		// an applied rejection (the row takes INVALID/REJECTED) leaves no stale value behind
		updateRecord["last_process_state"] = goqu.Case().When(kept, string(processState)).Else(nil)
	} else {
		updateRecord["last_process_state"] = nil
	}

	if processState == model.INIT {
		updateRecord["activesince"] = goqu.COALESCE(goqu.C("activesince"), goqu.V(nil))
	}

	if consentId != nil {
		updateRecord["consent_id"] = *consentId
	}
	if statusCode != nil {
		updateRecord["statusCode"] = *statusCode
	}
	if processState == model.ACTIVE {
		updateRecord["status"] = model.S_ACTIVE
	}
	if activeSince != nil {
		updateRecord["activesince"] = goqu.COALESCE(goqu.C("activesince"), *activeSince)
		updateRecord["inactivesince"] = civil.DateFor(2999, 12, 31)
	}

	/**
	Consider in case reactivating the metering point for the same participant, the inactivesince time must be adjusted to the very end time period.
	The activesince time must be left alone as it controls the visibility of the time period in the user client.
	Therefore, the activesince time is only set at creation time.

	IMPROVE: Check the context of the meteringpoint while activating.
	*/
	log.WithContext(ctx).WithField("tenant", tenant).Infof("Change Status. Meters: %v activeSince: %v status: %v", meterId, activeSince, processState)

	flag := model.F_ASSIGNED
	statement, _, err := goqu.Update(TABLE_METERINGPOINT).
		Set(updateRecord).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"flag":              goqu.Op{"eq": flag},
		}).ToSQL()
	if err != nil {
		return model.ErrStatusMeter(err)
	}

	result, err := db.Exec(statement)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
		return model.ErrStatusMeter(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows == 0 {
		log.WithContext(ctx).WithField("SQL", "UPDATE").Info("No Rows Affected")
	}
	return nil
}

func meteringPointRevoke(ctx context.Context, db *sqlx.DB, tenant, meterId string, consentEnd civil.Date) error {

	log.WithContext(ctx).Debugf("Revoke Meter: %s at %v\n", meterId, consentEnd)

	statement, _, _ := goqu.Update(TABLE_METERINGPOINT).
		Set(goqu.Record{
			"process_state": model.INACTIVE,
			"status":        model.INACTIVE,
			"modifiedAt":    civil.Now(),
			"modifiedBy":    "EVU",
			"inactivesince": consentEnd}).
		Where(goqu.Ex{
			"tenant":            goqu.Op{"eq": tenant},
			"metering_point_id": goqu.Op{"eq": meterId},
			"flag":              model.F_ASSIGNED,
		}).
		ToSQL()

	// one statement: no transaction needed
	if _, err := db.Exec(statement); err != nil {
		return model.ErrUpdateMeter(err)
	}
	return nil
}

// meteringPointRevokeActive sets the assigned metering point INACTIVE since inactiveSince after the grid operator's
// answer to a deregistration (platform#238), but only while it is ACTIVE and its participation started on or before
// that date: an answer that arrives twice, after a revocation by grid operator or member (CCMI/CCMC), or after a new
// participation leaves the row and its date. It returns whether the row changed.
func meteringPointRevokeActive(ctx context.Context, db *sqlx.DB, tenant, meterId string, inactiveSince civil.Date) (bool, error) {
	statement, _, _ := goqu.Update(TABLE_METERINGPOINT).
		Set(goqu.Record{
			"process_state": model.INACTIVE,
			"status":        model.INACTIVE,
			"modifiedAt":    civil.Now(),
			"modifiedBy":    "EVU",
			"inactivesince": inactiveSince}).
		Where(
			goqu.C("tenant").Eq(tenant),
			goqu.C("metering_point_id").Eq(meterId),
			goqu.C("flag").Eq(model.F_ASSIGNED),
			goqu.C("status").Eq(model.S_ACTIVE),
			goqu.Or(goqu.C("activesince").IsNull(), goqu.C("activesince").Lte(inactiveSince))).
		ToSQL()

	res, err := db.ExecContext(ctx, statement)
	if err != nil {
		return false, model.ErrUpdateMeter(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, model.ErrUpdateMeter(err)
	}
	return n > 0, nil
}

// RevokedTenant is a tenant in which a revocation was applied. WasActive tells whether the
// metering point had status ACTIVE before, i.e. whether its participation really ended now
// (platform#116: only then the member gets a mail).
type RevokedTenant struct {
	Tenant    string `db:"tenant"`
	WasActive bool   `db:"was_active"`
}

// TenantNames returns the tenants of a revocation result.
func TenantNames(revoked []RevokedTenant) []string {
	names := make([]string, 0, len(revoked))
	for _, r := range revoked {
		names = append(names, r.Tenant)
	}
	return names
}

// meteringPointRevokeByConsentId revokes a metering point after AUFHEBUNG_CCMI/CCMC. These
// messages carry no community id, only the receiving RC number (MQTT topic). One RC number can
// own several tenants (GEA: GC100019-001, GC100019-002, …) and a metering point may be assigned in
// several communities (multiple participation, move), so the rows are searched step by step and
// the first step that matches wins:
//  1. tenants of the receiving RC number + exact consent id
//  2. tenants of the receiving RC number + no consent id (or the migration placeholder), assigned
//  3. any tenant + exact consent id — only without a receiving RC number
//  4. any tenant + no consent id (or placeholder), assigned — only without a receiving RC number
//
// With a receiving RC number only its own tenants are revoked (steps 3 and 4 never run, decided
// 06.11.2026); no match is an error, the caller records the message at the receiver.
//
// Without a consent id the "assigned" steps take any consent. A step may hit several tenants
// only if they all belong to the same RC number (the consent is given per RC number); otherwise
// nothing is changed and an error is returned. receiver must be upper case.
func meteringPointRevokeByConsentId(ctx context.Context, db *sqlx.DB, receiver string, consentId *string, meterId string, consentEnd civil.Date) ([]RevokedTenant, error) {
	execDB := goqu.New("postgres", db)

	tx, err := execDB.Begin()
	if err != nil {
		return nil, model.ErrOpenTx(err)
	}
	// Commit explicitly on success and check its error: a mail goes out on the result
	// (platform#116), so a revocation that was not persisted must not be reported as done.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	meter := goqu.C("metering_point_id").Eq(meterId)
	assignedWithoutConsent := goqu.And(
		goqu.Or(goqu.C("consent_id").Is(nil), goqu.C("consent_id").Eq(MIGRATION_CONSENT_ID)),
		goqu.C("flag").Eq(model.F_ASSIGNED))
	if consentId == nil {
		assignedWithoutConsent = goqu.And(goqu.C("flag").Eq(model.F_ASSIGNED))
	}

	var steps []exp.Expression
	if receiver != "" {
		inReceiver := goqu.C("tenant").In(tx.From(TABLE_EEG).Select("tenant").Where(goqu.Or(
			goqu.Func("upper", goqu.I("rcNumber")).Eq(receiver),
			goqu.Func("upper", goqu.C("tenant")).Eq(receiver))))
		if consentId != nil {
			steps = append(steps, goqu.And(meter, inReceiver, goqu.C("consent_id").Eq(*consentId)))
		}
		steps = append(steps, goqu.And(meter, inReceiver, assignedWithoutConsent))
	} else {
		if consentId != nil {
			steps = append(steps, goqu.And(meter, goqu.C("consent_id").Eq(*consentId)))
		}
		steps = append(steps, goqu.And(meter, assignedWithoutConsent))
	}

	for _, where := range steps {
		// Read and lock the rows before the update (platform#116). MQTT handlers run in parallel
		// (SetOrderMatters(false)): with FOR UPDATE a second message for the same metering point
		// waits for this transaction and then sees the row INACTIVE, so only one mail goes out.
		var rows []struct {
			Tenant string `db:"tenant"`
			Status string `db:"status"`
		}
		if err = tx.From(TABLE_METERINGPOINT).Select(goqu.C("tenant"), goqu.C("status")).
			Where(where).Order(goqu.C("tenant").Asc()).ForUpdate(exp.Wait).
			ScanStructsContext(ctx, &rows); err != nil {
			return nil, model.ErrUpdateMeter(err)
		}
		if len(rows) == 0 {
			continue
		}
		revoked := []RevokedTenant{}
		index := map[string]int{}
		for _, r := range rows {
			i, ok := index[r.Tenant]
			if !ok {
				i = len(revoked)
				index[r.Tenant] = i
				revoked = append(revoked, RevokedTenant{Tenant: r.Tenant})
			}
			if r.Status == string(model.S_ACTIVE) {
				revoked[i].WasActive = true
			}
		}
		tenants := TenantNames(revoked)
		var rcNumbers []string
		if err = tx.From(TABLE_EEG).Select(goqu.Func("upper", goqu.I("rcNumber"))).Distinct().
			Where(goqu.C("tenant").In(tenants)).ScanValsContext(ctx, &rcNumbers); err != nil {
			return nil, model.ErrUpdateMeter(err)
		}
		if len(rcNumbers) != 1 {
			log.WithContext(ctx).Warnf("Meteringpoint %s is not unique. %d-[%+v]", meterId, len(tenants), tenants)
			err = model.ErrUpdateMeter(fmt.Errorf("Meteringpoint %s is not unique", meterId))
			return nil, err
		}

		// An entry that ends here (status not INIT, start and end date set) while it carries a rejection goes to
		// INACTIVE like a rejected re-registration of an ended entry; the rejection goes to
		// last_process_state (konzept-zaehlpunkt-ausblenden.md, part 2).
		endsRejected := goqu.And(goqu.C("status").Neq("INIT"), goqu.C("inactivesince").IsNotNull(),
			goqu.C("activesince").IsNotNull(), goqu.C("process_state").In(string(model.INVALID), string(model.REJECTED)))
		_, err = tx.Update(TABLE_METERINGPOINT).
			Set(goqu.Record{
				"process_state": goqu.Case().
					When(goqu.C("process_state").Eq("ACTIVE"), model.INACTIVE).
					When(endsRejected, model.INACTIVE).Else(goqu.C("process_state")),
				"last_process_state": goqu.Case().
					When(endsRejected, goqu.C("process_state")).Else(goqu.C("last_process_state")),
				"status": goqu.Case().
					When(goqu.C("status").Eq("INIT"), model.S_INIT).Else(model.S_INACTIVE),
				"modifiedAt":    civil.Now(),
				"modifiedBy":    "EVU",
				"inactivesince": goqu.Case().When(goqu.C("inactivesince").IsNotNull(), consentEnd).Else(goqu.C("inactivesince")),
			}).
			Where(where).
			Executor().ExecContext(ctx)
		if err != nil {
			return nil, model.ErrUpdateMeter(err)
		}
		if err = tx.Commit(); err != nil {
			return nil, model.ErrUpdateMeter(err)
		}
		committed = true
		return revoked, nil
	}
	err = model.ErrUpdateMeter(fmt.Errorf("Meteringpoint %s not found", meterId))
	return nil, err
}

// MeteringPointChangePartFactor stores a new participation factor. After an owner change a metering point
// exists twice in a community: the old member's moved entry (flag 0) and the current one (flag 1)
// (support ticket 66110076). With a participant (member form) only that member's entry is changed;
// without one (ANTWORT_CPF, the EDA message names no member) only the current entry.
func MeteringPointChangePartFactor(ctx context.Context, db *sqlx.DB, tenant, participantId string, meters []model.Meter) error {

	log.WithContext(ctx).Debug("Change Partition Factor")

	metersJson, err := json.Marshal(meters)
	if err != nil {
		return model.ErrUpdateMeter(err)
	}

	entry := goqu.C("flag").Eq(model.F_ASSIGNED)
	if participantId != "" {
		entry = goqu.C("participant_id").Eq(participantId)
	}

	withClause := goqu.L(
		`(SELECT * FROM json_to_recordset(?::json) AS cols("meteringPoint" TEXT, direction TEXT, activation BIGINT, "partFact" INT))`, string(metersJson))
	insertQuery := goqu.From(TABLE_METERINGPOINT, withClause.As("ma")).
		Select(
			goqu.C("metering_point_id"),
			goqu.C("participant_id"),
			goqu.C("tenant"),
			goqu.C("partFact"),
			goqu.V("system").As("createdBy"),
		).Where(goqu.C("metering_point_id").Eq(goqu.I("ma.meteringPoint")), goqu.C("tenant").Eq(tenant), entry)
	stmt, _, err := goqu.Insert(TABLE_PARTITION_FACT).
		Cols("metering_point_id", "participant_id", "tenant", "partFact", "createdBy").
		FromQuery(insertQuery).ToSQL()

	// one statement: no transaction needed
	res, err := db.Exec(stmt)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return model.ErrUpdateMeter(err)
	}
	if n, err := res.RowsAffected(); err == nil && n < int64(len(meters)) {
		ids := make([]string, 0, len(meters))
		for _, m := range meters {
			ids = append(ids, m.MeteringPoint)
		}
		log.WithContext(ctx).WithField("tenant", tenant).Warnf("participation factor stored for %d of %d metering point(s) %v (no matching entry)", n, len(meters), ids)
	}
	return nil
}

func findInactiveMeteringById(ctx context.Context, db *sqlx.DB, tenant string, meterId string) ([]*model.MeteringPoint, error) {
	mode := model.S_INACTIVE
	return findMeteringByIdAndState(ctx, db, tenant, []string{meterId}, &mode)
}

func findNewMeteringById(ctx context.Context, db *sqlx.DB, tenant string, meterId string) ([]*model.MeteringPoint, error) {
	mode := model.S_INIT
	return findMeteringByIdAndState(ctx, db, tenant, []string{meterId}, &mode)
}

func findActiveMeteringByIds(ctx context.Context, db *sqlx.DB, tenant string, meterIds []string) ([]*model.MeteringPoint, error) {
	mode := model.S_ACTIVE
	return findMeteringByIdAndState(ctx, db, tenant, meterIds, &mode)
}

func findMeteringByIds(ctx context.Context, db *sqlx.DB, tenant string, meterIds []string) ([]*model.MeteringPoint, error) {
	return findMeteringByIdAndState(ctx, db, tenant, meterIds, nil)
}

func findAllMeteringByTenant(ctx context.Context, tx *sqlx.DB, tenant, participantId string, meterIds []string) ([]*model.MeteringPoint, error) {
	var m []*model.MeteringPoint

	stateStmt := pgDialect.From(TABLE_METERINGPOINT).
		Select(
			goqu.C("activesince"),
			goqu.C("inactivesince"),
			goqu.C("flag"),
			goqu.C("metering_point_id").As("mid"),
			goqu.C("tenant").As("mid_tenant"),
			goqu.C("participant_id").As("mid_pid"))

	partFactStmt := pgDialect.From(TABLE_PARTITION_FACT_VIEW).
		Select(
			goqu.C("partFact"),
			goqu.C("metering_point_id").As("mpfmid"),
			goqu.C("participant_id").As("mpfpid"),
			goqu.C("tenant").As("mpf_tenant"))

	stmt, _, err := pgDialect.From(TABLE_METERINGPOINT, stateStmt.As("state"), partFactStmt.As("mpfpF2")).Select(&model.MeteringPoint{}).
		Where(
			goqu.C("metering_point_id").In(meterIds),
			goqu.C("tenant").Eq(tenant),
			goqu.C("mid_tenant").Eq(tenant),
			goqu.C("mpf_tenant").Eq(tenant),
			goqu.C("participant_id").Eq(participantId),
			goqu.C("mid").Eq(goqu.C("metering_point_id")),
			// per member: after an owner change the number has a second entry (support ticket 66110076)
			goqu.C("mid_pid").Eq(goqu.C("participant_id")),
			goqu.C("mpfmid").Eq(goqu.C("metering_point_id")),
			goqu.C("mpfpid").Eq(goqu.C("participant_id")),
		).ToSQL()

	if err != nil {
		return nil, model.ErrFindMeter(err)
	}
	err = tx.SelectContext(ctx, &m, stmt)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, model.ErrFindMeter(err)
	}
	return m, nil
}

func findMeteringById(ctx context.Context, tx *sqlx.DB, tenant string, meterId string) (*model.MeteringPoint, error) {
	mode := model.S_ACTIVE
	m, err := findMeteringByIdAndState(ctx, tx, tenant, []string{meterId}, &mode)
	if err != nil {
		return nil, err
	}
	if len(m) == 1 {
		return m[0], nil
	}
	return nil, model.ErrFindMeter(errors.New("More as one active Meteringpoint was found"))
}

func findMeteringByStatus(ctx context.Context, tx *sqlx.DB, tenant, meterId string, status model.StatusType) (*model.MeteringPoint, error) {
	m, err := findMeteringByIdAndState(ctx, tx, tenant, []string{meterId}, &status)
	if err != nil {
		return nil, err
	}
	if len(m) == 1 {
		return m[0], nil
	}
	return nil, model.ErrFindMeter(errors.New("More as one or zero Meteringpoint are found!"))
}

func findAssignedMetering(ctx context.Context, db *sqlx.DB, tenant string, meterId string) (*model.MeteringPoint, error) {
	m, err := findAssignedMeteringById(ctx, db, tenant, []string{meterId})
	if err != nil {
		return nil, err
	}
	if len(m) == 1 {
		return m[0], nil
	}
	return nil, model.ErrFindMeter(errors.New("More as one or zero Meteringpoint are found!"))
}

func findMeteringByIdAndState(ctx context.Context, db *sqlx.DB, tenant string, meterIds []string, status *model.StatusType) ([]*model.MeteringPoint, error) {
	var m []*model.MeteringPoint

	if len(meterIds) == 0 {
		return m, fmt.Errorf("no metering points found for tenant %s", tenant)
	}

	stateStmt := pgDialect.From(TABLE_METERINGPOINT).
		Select(
			goqu.C("activesince"),
			goqu.C("inactivesince"),
			goqu.C("flag"),
			goqu.C("metering_point_id").As("mid"),
			goqu.C("participant_id").As("mid_pid")).
		Where(goqu.C("tenant").Eq(tenant))

	partFactStmt := pgDialect.From(TABLE_PARTITION_FACT_VIEW).
		Select(
			goqu.C("partFact"),
			goqu.C("metering_point_id").As("mpfmid"),
			goqu.C("participant_id").As("mpfpid")).
		Where(goqu.C("tenant").Eq(tenant))

	whereClause := goqu.Ex{
		"metering_point_id": goqu.Op{"in": meterIds},
		"tenant":            tenant,
		"mid":               goqu.C("metering_point_id"),
		"mid_pid":           goqu.C("participant_id"), // the entry's own period, not the other owner's (owner change)
		"mpfmid":            goqu.C("metering_point_id"),
		"mpfpid":            goqu.C("participant_id"),
	}
	if status != nil {
		whereClause["status"] = *status
		whereClause["state.flag"] = model.F_ASSIGNED
	}

	stmt, _, err := pgDialect.From(TABLE_METERINGPOINT, stateStmt.As("state"), partFactStmt.As("mpfpF3")).Select(&model.MeteringPoint{}).
		Where(whereClause).
		ToSQL()

	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("tenant", tenant).Error("SQL statement failed")
		return nil, model.ErrFindMeter(err)
	}

	err = db.SelectContext(ctx, &m, stmt)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, model.ErrFindMeter(err)
	}
	return m, nil
}

func findAssignedMeteringById(ctx context.Context, db *sqlx.DB, tenant string, meterIds []string) ([]*model.MeteringPoint, error) {
	var m []*model.MeteringPoint

	stateStmt := pgDialect.From(TABLE_METERINGPOINT).
		Select(
			goqu.C("activesince"),
			goqu.C("inactivesince"),
			goqu.C("flag"),
			goqu.C("metering_point_id").As("mid"),
			goqu.C("participant_id").As("mid_pid")).
		Where(goqu.C("tenant").Eq(tenant))

	partFactStmt := pgDialect.From(TABLE_PARTITION_FACT_VIEW).
		Select(
			goqu.C("partFact"),
			goqu.C("metering_point_id").As("mpfmid"),
			goqu.C("participant_id").As("mpfpid")).
		Where(goqu.C("tenant").Eq(tenant))

	whereClause := goqu.Ex{
		"metering_point_id": goqu.Op{"in": meterIds},
		"tenant":            tenant,
		"mid":               goqu.C("metering_point_id"),
		// the entry's own state: after an owner change the moved entry (flag 0) would otherwise match the
		// current entry's state and the lookup returned two rows (support ticket 66110076)
		"mid_pid":    goqu.C("participant_id"),
		"mpfmid":     goqu.C("metering_point_id"),
		"mpfpid":     goqu.C("participant_id"),
		"state.flag": model.F_ASSIGNED,
	}

	stmt, _, err := pgDialect.From(TABLE_METERINGPOINT, stateStmt.As("state"), partFactStmt.As("mpfpF3")).Select(&model.MeteringPoint{}).
		Where(whereClause).
		ToSQL()

	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("tenant", tenant).Error("SQL statement failed")
		return nil, model.ErrFindMeter(err)
	}

	err = db.SelectContext(ctx, &m, stmt)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("SQL statement failed")
		return nil, model.ErrFindMeter(err)
	}
	return m, nil
}

func UpdateActiveMeteringPoints(ctx context.Context, db *sqlx.DB, tenant string, ml []model.Meter) error {
	tml := model.StandardizeMeteringPointList(ml)

	status := model.S_ACTIVE
	processState := model.ACTIVE

	// without the cancel of ctx, as before inTx: SyncMeteringPoints gives 10 s, a long list must not roll back
	return inTx(context.WithoutCancel(ctx), db, func(sqlTx *sqlx.Tx) error {
		tx := goqu.NewTx("postgres", sqlTx)
		for _, m := range tml {
			dbMeter := model.ConvertToDbMeter(m)
			dbMeter.Status = &status
			dbMeter.ProcessState = &processState
			record, err := exp.NewRecordFromStruct(*dbMeter, false, true)
			if err != nil {
				return model.ErrUpdateMeter(err)
			}
			if _, ok := record["activesince"]; ok {
				record["activesince"] = goqu.Case().
					When(goqu.COALESCE(goqu.C("activesince"), dbMeter.ActiveSince).Gte(dbMeter.ActiveSince), dbMeter.ActiveSince).Else(goqu.C("activesince"))
			}
			updateMeter := tx.Update(TABLE_METERINGPOINT).
				Set(record).
				Where(goqu.Ex{
					"tenant":            goqu.Op{"eq": tenant},
					"metering_point_id": goqu.Op{"eq": m.MeteringPoint},
					"flag":              goqu.Op{"eq": model.F_ASSIGNED},
				}).
				Returning("participant_id").
				Executor()

			var participantId string
			found, err := updateMeter.ScanVal(&participantId)

			if err != nil {
				log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
				return model.ErrUpdateMeter(err)
			}

			if found {
				partFact := model.ConvertToDbMeterPartFact(m)
				versionStmt := goqu.From(TABLE_PARTITION_FACT).Select(goqu.MAX("version")).Where(goqu.Ex{
					"tenant":            goqu.Op{"eq": tenant},
					"participant_id":    goqu.Op{"eq": participantId},
					"metering_point_id": goqu.Op{"eq": m.MeteringPoint},
				})
				updatePartFact := tx.Update(TABLE_PARTITION_FACT).
					Set(partFact).
					Where(goqu.Ex{
						"tenant":            goqu.Op{"eq": tenant},
						"metering_point_id": goqu.Op{"eq": m.MeteringPoint},
						"participant_id":    goqu.Op{"eq": participantId},
						"version":           goqu.Op{"eq": versionStmt},
					}).
					Executor()

				_, err = updatePartFact.Exec()
				if err != nil {
					log.WithContext(ctx).WithError(err).WithField("SQL", "UPDATE").Error("SQL statement failed")
					return model.ErrUpdateMeter(err)
				}
			}

			// Todo:
			// Redesign table schema. It is not possible to update partition factor if the metering point exist multiple times in one eeg.
			// Exactly this behaviour exist when participant's mapping is changed or a new participant take over a metering point.
		}
		return nil
	})
}

func findMeteringPointsForTenant(ctx context.Context, db *sqlx.DB, tenant string) ([]*model.MeteringPoint, error) {
	stmt, _, _ := pgDialect.From(TABLE_METERINGPOINT).
		Select("metering_point_id").
		Where(goqu.C("tenant").Eq(tenant)).Order(goqu.I("direction").Asc()).ToSQL()

	mIds := []string{}
	err := db.SelectContext(ctx, &mIds, stmt)
	if err != nil {
		log.WithContext(ctx).WithField("tenant", tenant).WithError(err).Error("SQL statement failed")
		return nil, err
	}
	return findMeteringByIdAndState(ctx, db, tenant, mIds, nil)
}

func findMeteringPointsActivePeriod(ctx context.Context, db *sqlx.DB, tenant string, activeSince, inactiveSince int64) ([]*model.MeteringPoint, error) {
	subStmt := pgDialect.From(TABLE_METERINGPOINT).Where(
		goqu.C("activesince").Lte(civil.DateOf(time.UnixMilli(inactiveSince))),
		goqu.C("tenant").Eq(tenant))

	stmt, _, _ := pgDialect.From(subStmt.As("a")).Select("metering_point_id").
		Where(goqu.I("inactivesince").Gte(civil.DateOf(time.UnixMilli(activeSince)))).
		ToSQL()

	mIds := []string{}
	err := db.Select(&mIds, stmt)
	if err != nil {
		log.WithContext(ctx).WithField("tenant", tenant).WithError(err).Error("SQL statement failed")
		return nil, err
	}
	return findMeteringByIdAndState(ctx, db, tenant, mIds, nil)
}
