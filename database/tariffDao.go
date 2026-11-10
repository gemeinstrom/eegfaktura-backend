package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"database/sql"
	"errors"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
	"reflect"
	"strings"
	"time"
)

type TariffRepository interface {
	GetTariff(ctx context.Context, tenant string) ([]model.Tariff, error)
	GetTariffNameMap(ctx context.Context, tenant string) (map[string]string, error)
	AddTariff(ctx context.Context, tenant, user string, tariff *model.Tariff) error
	ArchiveTariff(tenant string, id string) error
	GetTariffHistory(ctx context.Context, tenant, id string) ([]model.Tariff, error)
}

func (db *sqlDatabase) GetTariff(ctx context.Context, tenant string) ([]model.Tariff, error) {
	return GetTariff(ctx, db.db, tenant)
}

func (db *sqlDatabase) GetTariffNameMap(ctx context.Context, tenant string) (map[string]string, error) {
	return GetTariffNameMap(ctx, db.db, tenant)
}

func (db *sqlDatabase) AddTariff(ctx context.Context, tenant, user string, tariff *model.Tariff) error {
	return AddTariff(ctx, db.db, tenant, user, tariff)
}

func (db *sqlDatabase) ArchiveTariff(tenant string, id string) error {
	return archiveTariff(db.db, tenant, id)
}

func (db *sqlDatabase) GetTariffHistory(ctx context.Context, tenant, id string) ([]model.Tariff, error) {
	var tariff model.Tariff
	stmt, _, err := goqu.Dialect("postgres").From("base.tariff").Select(&tariff).
		Where(
			goqu.C("tenant").Eq(tenant),
			goqu.C("id").Eq(id)).
		Order(goqu.C("version").Desc()).
		ToSQL()
	if err != nil {
		return []model.Tariff{}, model.ErrGetTariff(err)
	}

	tariffHistory := []model.Tariff{}
	err = db.db.Select(&tariffHistory, stmt)
	if (err != nil) || len(tariffHistory) == 0 {
		log.WithContext(ctx).Errorf("GetTariffHistory err: %v", err)
		return []model.Tariff{}, model.ErrGetTariff(err)
	}

	return tariffHistory, nil
}

var (
	ErrTariffUtilized = errors.New("Tariff is currently used")
)

var pgDialect = goqu.Dialect("postgres")

func GetTariff(ctx context.Context, db *sqlx.DB, tenant string) ([]model.Tariff, error) {
	var tariff []model.Tariff
	err := db.Select(&tariff, `SELECT id, name, "billingPeriod", "useVat", "vatInPercent", "accountNetAmount", "accountGrossAmount", "participantFee", "baseFee", "businessNr", version, type, "centPerKWh", discount, "freeKWh", "meteringPointFee", "meteringPointVat", "useMeteringPointFee", "vatSupplementaryText", `+
		`"useTimeTariff", "timeTariff1Active", "timeTariff1Name", "timeTariff1From", "timeTariff1To", "timeTariff1CentPerKWh", "timeTariff2Active", "timeTariff2Name", "timeTariff2From", "timeTariff2To", "timeTariff2CentPerKWh", `+
		`"joiningFee", "joiningFeeUseVat", "joiningFeeVatInPercent" `+
		`FROM base.activetariff WHERE tenant = $1`, tenant)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.WithContext(ctx).WithField("tenant", tenant).Errorf("Error Query Tariff! %s", err.Error())
		return tariff, model.ErrGetTariff(err)
	}
	if tariff == nil {
		return []model.Tariff{}, nil
	}
	return tariff, nil
}

func GetTariffHistory(db *sqlx.DB, tenant string, id string) ([]model.Tariff, error) {

	stmt, _, err := goqu.Dialect("postgres").From("base.tariff").
		Where(
			goqu.C("tenant").Eq(tenant),
			goqu.C("id").Eq(id)).
		ToSQL()
	if err != nil {
		return []model.Tariff{}, model.ErrGetTariff(err)
	}

	tariffHistory := []model.Tariff{}
	err = db.Get(&tariffHistory, stmt)
	if (err != nil) || len(tariffHistory) == 0 {
		return []model.Tariff{}, model.ErrGetTariff(err)
	}

	return tariffHistory, nil
}

func GetTariffNameMap(ctx context.Context, db *sqlx.DB, tenant string) (map[string]string, error) {
	tariffs, err := GetTariff(ctx, db, tenant)
	if err != nil {
		return nil, err
	}
	tariffMap := map[string]string{}
	for _, t := range tariffs {
		tariffMap[t.Id.String()] = t.Name
	}
	return tariffMap, nil
}

// archiveTariff archives all versions of a tariff of the tenant, unless an entry whose tariff can still be
// changed references it: then ErrTariffUtilized (4002). Decision 24.10.2026 (#106): members of the tenant in
// every status (every member row can be edited, PUT /participant has no status guard), metering points only as
// the current entry (flag = F_ASSIGNED, any status); moved (0) and archived (2) history rows do not block, as
// they would block forever. Check and archive are one UPDATE; an assignment committed at the same moment is
// not prevented, and the paths that assign a tariff to a member or metering point do not check its status
// (known gap). 0 updated rows are told apart by a follow-up read: the tariff exists → in use, otherwise
// ErrUpdateTariff "unknown tariff".
func archiveTariff(db *sqlx.DB, tenant string, id string) error {
	// todo: use goqu for sql statement creation
	res, err := db.Exec(`UPDATE base.tariff SET status = 'ARCHIVED', "lastModifiedDate" = 'now()'
		WHERE tenant = $1 AND id = $2
		AND NOT EXISTS (SELECT 1 FROM base.participant WHERE tenant = $1 AND "tariffId" = $2)
		AND NOT EXISTS (SELECT 1 FROM base.meteringpoint WHERE tenant = $1 AND tariff_id = $2 AND flag = $3)`,
		tenant, id, model.F_ASSIGNED)
	if err != nil {
		return model.ErrUpdateTariff(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return model.ErrUpdateTariff(err)
	}
	if n > 0 {
		return nil
	}

	var exists bool
	if err = db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM base.tariff WHERE tenant = $1 AND id = $2)`, tenant, id); err != nil {
		return model.ErrGetTariff(err)
	}
	if exists {
		return model.ErrTariffUtilized(ErrTariffUtilized)
	}
	return model.ErrUpdateTariff(errors.New("unknown tariff"))
}

func AddTariff(ctx context.Context, db *sqlx.DB, tenant, user string, tariff *model.Tariff) error {

	isNew := len(tariff.Id.String()) == 0
	if isNew {
		tariff.Id = uuid.NewUUID()
	} else {
		tariff.Version = tariff.Version + 1
	}

	type updateType struct {
		Tenant           string    `json:"tenant" db:"tenant"`
		CreatedDate      time.Time `goqu:"omitempty" db:"createdDate"`
		LastModifiedDate time.Time `goqu:"omitempty" db:"lastModifiedDate"`
		CreatedBy        string    `db:"createdBy"`
		*model.Tariff
	}
	update := updateType{tenant, time.Now(), time.Now(), user, tariff}

	// without the cancel of the request: a client that goes away does not roll back the save (as before inTx)
	return inTx(context.WithoutCancel(ctx), db, func(tx *sqlx.Tx) error {
		var stmt string
		var err error

		// A new version may only be added to a tariff of the caller's own tenant.
		// The id comes from the client; without this check a known id of another
		// community would add a row under that id and deactivate its tariff.
		if !isNew {
			var args []interface{}
			stmt, args, err = pgDialect.From("base.tariff").Select("tenant").
				Where(goqu.Ex{"id": tariff.Id.String()}).Limit(1).Prepared(true).ToSQL()
			if err != nil {
				return model.ErrUpdateTariff(err)
			}
			var owner string
			if err = tx.Get(&owner, stmt, args...); err != nil || !strings.EqualFold(owner, tenant) {
				log.WithContext(ctx).WithField("tenant", tenant).Warnf("AddTariff: tariff %s does not belong to the tenant", tariff.Id.String())
				err = errors.New("unknown tariff")
				return model.ErrUpdateTariff(err)
			}
		}

		stmt, _, err = goqu.Insert("base.tariff").Rows(update).ToSQL()
		if err != nil {
			return model.ErrUpdateTariff(err)
		}
		_, err = tx.Exec(stmt)
		if err != nil {
			log.WithContext(ctx).WithError(err).WithField("SQL", "INSERT").Error("SQL statement failed")
			return model.ErrUpdateTariff(err)
		}

		if tariff.Version > 0 {
			stmt, _, err = goqu.Update("base.tariff").Set(
				map[string]interface{}{"status": "INACTIVE", "inactiveSince": civil.Today(), "lastModifiedDate": civil.Today()}).Where(goqu.Ex{
				"version": tariff.Version - 1,
				"id":      tariff.Id.String(),
				"tenant":  tenant,
			}).ToSQL()
			if err != nil {
				log.WithContext(ctx).WithField("tenant", tenant).Errorf("Update previous entry: %v", err)
				return model.ErrUpdateTariff(err)
			}
			_, err = tx.Exec(stmt)
		}
		if err != nil {
			return model.ErrUpdateTariff(err)
		}
		return nil
	})
}

func StringToNullStringHookFunc(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
	if f.Kind() == reflect.String {
		var s null.String
		if t == reflect.TypeOf(s) {
			s = null.StringFrom(data.(string))
			return s, nil
		}

		var d civil.NullDate
		if t == reflect.TypeOf(d) {
			date, err := civil.ParseDate(data.(string))
			if err == nil {
				d = civil.NullDateFrom(&date)
				return d, nil
			}
		}

		var dt civil.NullDateTime
		if t == reflect.TypeOf(dt) {
			date, err := civil.ParseDateTime(data.(string))
			if err == nil {
				dt = civil.NullDateTimeFrom(&date)
				return dt, nil
			}
		}
	}

	if f.Kind() == reflect.Int {
		var i null.Int
		if t == reflect.TypeOf(i) {
			switch data.(type) {
			case int:
				i = null.IntFrom(int64(data.(int)))
			case int16:
				i = null.IntFrom(int64(data.(int16)))
			case int32:
				i = null.IntFrom(int64(data.(int32)))
			case int64:
				i = null.IntFrom(data.(int64))
			default:
				return data, nil
			}
			return i, nil
		}
	}

	return data, nil
}
