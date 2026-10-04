package database

import (
	"at.ourproject/vfeeg-backend/model"
	"database/sql"
	"errors"
	"github.com/doug-martin/goqu/v9"
	_ "github.com/jackc/pgx/v5"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
	"reflect"
	"time"
)

type OpenDbXConnection func() (*sqlx.DB, error)

type TariffRepository interface {
	GetTariff(tenant string) ([]model.Tariff, error)
	GetTariffNameMap(tenant string) (map[string]string, error)
	AddTariff(tenant, user string, tariff *model.Tariff) error
	ArchiveTariff(tenant string, id string) error
	GetTariffHistory(tenant, id string) ([]model.Tariff, error)
}

func (db *sqlDatabase) GetTariff(tenant string) ([]model.Tariff, error) {
	return GetTariff(db.db, tenant)
}

func (db *sqlDatabase) GetTariffNameMap(tenant string) (map[string]string, error) {
	return GetTariffNameMap(db.db, tenant)
}

func (db *sqlDatabase) AddTariff(tenant, user string, tariff *model.Tariff) error {
	return AddTariff(db.db, tenant, user, tariff)
}

func (db *sqlDatabase) ArchiveTariff(tenant string, id string) error {
	return archiveTariff(db.db, tenant, id)
}

func (db *sqlDatabase) GetTariffHistory(tenant, id string) ([]model.Tariff, error) {
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
		log.Errorf("GetTariffHistory err: %v: %s", err, stmt)
		return []model.Tariff{}, model.ErrGetTariff(err)
	}

	return tariffHistory, nil
}

var (
	ErrTariffUtilized = errors.New("Tariff is currently used")
)

//var ConnectToDatabase = func() (*sqlx.DB, error) {
//	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
//		"password=%s dbname=%s sslmode=disable",
//		viper.GetString("database.host"), viper.GetInt("database.port"), viper.GetString("database.user"),
//		viper.GetString("database.password"), viper.GetString("database.dbname"))
//	return sqlx.Open("postgres", psqlInfo)
//}

var pgDialect = goqu.Dialect("postgres")

func GetTariff(db *sqlx.DB, tenant string) ([]model.Tariff, error) {
	var tariff []model.Tariff
	err := db.Select(&tariff, `SELECT id, name, "billingPeriod", "useVat", "vatInPercent", "accountNetAmount", "accountGrossAmount", "participantFee", "baseFee", "businessNr", version, type, "centPerKWh", discount, "freeKWh", "meteringPointFee", "meteringPointVat", "useMeteringPointFee", "vatSupplementaryText" `+
		`FROM base.activetariff WHERE tenant = $1`, tenant)
	if errors.Is(err, sql.ErrNoRows) || tariff == nil {
		return []model.Tariff{}, nil
	}

	if err != nil {
		log.WithField("tenant", tenant).Errorf("Error Query Tariff! %s", err.Error())
		return tariff, model.ErrGetTariff(err)
	}
	return tariff, nil
}

func GetTariffHistory(db *sqlx.DB, tenant string, id string) ([]model.Tariff, error) {

	//ds := goqu.Dialect("postgres").From("base.tariff").
	//	Select(
	//		goqu.I("users.id"),
	//		goqu.I("users.name"),
	//		goqu.L("COALESCE(array_agg(DISTINCT roles.role), '{}')").As("roles"),
	//		goqu.L("COALESCE(array_agg(DISTINCT emails.email), '{}')").As("emails"),
	//		goqu.L(`COALESCE(json_agg(
	//                   DISTINCT jsonb_build_object(
	//                       'street', addresses.street,
	//                       'city', addresses.city
	//                   )
	//               ) FILTER (WHERE addresses.id IS NOT NULL), '[]')`).As("addresses"),
	//	).
	//	LeftJoin(goqu.T("roles"), goqu.On(goqu.I("roles.user_id").Eq(goqu.I("users.id")))).
	//	LeftJoin(goqu.T("emails"), goqu.On(goqu.I("emails.user_id").Eq(goqu.I("users.id")))).
	//	LeftJoin(goqu.T("addresses"), goqu.On(goqu.I("addresses.user_id").Eq(goqu.I("users.id")))).
	//	GroupBy(goqu.I("users.id"), goqu.I("users.name"))

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

func GetTariffNameMap(db *sqlx.DB, tenant string) (map[string]string, error) {
	tariffs, err := GetTariff(db, tenant)
	if err != nil {
		return nil, err
	}
	tariffMap := map[string]string{}
	for _, t := range tariffs {
		tariffMap[t.Id.String()] = t.Name
	}
	return tariffMap, nil
}

func archiveTariff(db *sqlx.DB, tenant string, id string) error {
	stmt, _, err := pgDialect.Select("id").From("base.participant").Where(goqu.Ex{"tariffId": id}).ToSQL()
	if err != nil {
		return model.ErrGetTariff(err)
	}
	rows, err := db.Query(stmt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.ErrTariffUtilized(ErrTariffUtilized)
	}
	if rows != nil {
		_ = rows.Close()
	}

	stmt, _, err = pgDialect.Select("metering_point_id").From("base.meteringpoint").Where(goqu.Ex{"tariff_id": id, "tenant": tenant}).ToSQL()
	if err != nil {
		return err
	}
	rows, err = db.Query(stmt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.ErrTariffUtilized(ErrTariffUtilized)
	}
	if rows != nil {
		_ = rows.Close()
	}

	// todo: use goqu for sql statement creation
	_, err = db.Exec("UPDATE base.tariff SET status = 'ARCHIVED', \"lastModifiedDate\" = 'now()' WHERE tenant = $1 AND id = $2", tenant, id)
	if err != nil {
		return model.ErrUpdateTariff(err)
	}
	return nil
}

func AddTariff(db *sqlx.DB, tenant, user string, tariff *model.Tariff) error {

	if len(tariff.Id.String()) == 0 {
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

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	//defer func() { _ = tx.Rollback() }()
	defer func() {
		switch err {
		case nil:
			err = tx.Commit()
		default:
			err = tx.Rollback()
		}
	}()
	var stmt string
	stmt, _, err = goqu.Insert("base.tariff").Rows(update).ToSQL()
	if err != nil {
		return model.ErrUpdateTariff(err)
	}
	_, err = tx.Exec(stmt)
	if err != nil {
		log.WithField("SQL", "INSERT").Errorf("Stmt: %v", stmt)
		return model.ErrUpdateTariff(err)
	}

	if tariff.Version > 0 {
		stmt, _, err = goqu.Update("base.tariff").Set(
			map[string]interface{}{"status": "INACTIVE", "inactiveSince": civil.Today(), "lastModifiedDate": civil.Today()}).Where(goqu.Ex{
			"version": tariff.Version - 1,
			"id":      tariff.Id.String(),
		}).ToSQL()
		if err != nil {
			log.WithField("tenant", tenant).Errorf("Update previous entry: %v", err)
			return model.ErrUpdateTariff(err)
		}
		_, err = tx.Exec(stmt)
	}
	if err != nil {
		return model.ErrUpdateTariff(err)
	}
	return err
}

func UpdateTariff(dbConn OpenDbXConnection, tenant string, tariff *model.Tariff) error {
	db, err := dbConn()
	if err != nil {
		return err
	}
	defer db.Close()

	if len(tariff.Id.NodeID()) == 0 {
		tariff.Id = uuid.NewUUID()
	} else {
		tariff.Version = tariff.Version + 1
	}

	type updateType struct {
		Tenant string
		model.Tariff
	}

	update := updateType{tenant, *tariff}

	log.Debugf("Tarrif: %+v\n", update)

	stmt, _, err := goqu.Update("base.tariff").Set(&update).Where(goqu.C("tenant").Eq(tenant)).ToSQL()
	_, err = db.Exec(stmt)
	if err != nil {
		log.WithField("SQL", "UPDATE").Errorf("Stmt: %v", stmt)
	}

	//_, err = db.NamedExec(
	//	"UPDATE base.tariff SET \"billingPeriod\"=:billingperiod, \"useVat\"=:usevat, \"vatInPercent\"=:vatinpercent, \"accountNetAmount\"=:accountnetamount, \"accountGrossAmount\"=:accountgrossamount, \"participantFee\"=:participantfee, \"baseFee\"=:basefee, discount=:discount, \"businessNr\"=:businessnr, \"centPerKWh\"=:centperkwh, \"freeKWh\" = :freekwh, \"createdBy\"=:createdby, version=:version WHERE id = :id", &update)

	return err
}

//func DateToCivilHookFunc(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
//
//
//
//}

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
