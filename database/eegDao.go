package database

import (
	"context"

	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

const TABLE_EEG = "base.eeg"
const TABLE_EEG_ADDRESS = "base.address"

type EegRepository interface {
	GetEegById(ctx context.Context, tenant string) (*model.Eeg, error)
	GetEegByIdForUser(ctx context.Context, tenant string) (*model.Eeg, error)
	GetEegByEcId(ctx context.Context, edId string) (*model.Eeg, error)
	UpdateEegPartial(ctx context.Context, tenant string, fields map[string]interface{}) error
	GetGridOperators(ctx context.Context) (map[string]string, error)
	FetchTenantsName(ctx context.Context, tenants []string, isSuperUser bool) ([]tenantsNameStruct, error)
	InsertEeg(ctx context.Context, tenant string, eeg *model.Eeg) error
	UpdateOnlineState(ctx context.Context, tenant string, onlineState bool) error
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
		log.WithField("SQL", "SELECT").Errorf("Stmt: %s", stmt)
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
		log.WithField("SQL", "SELECT").Errorf("Stmt: %s", stmt)
		return nil, model.ErrGetEeg(err)
	}
	return &eeg, nil
}

func insertEeg(ctx context.Context, db *sqlx.DB, tenant string, eeg *model.Eeg) error {

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
	_, err = db.ExecContext(ctx, sql)
	if err != nil {
		log.WithField("SQL", "INSERT").Errorf("Stmt: %s", sql)
		return err
	}

	return err
}

func updateEegPartial(ctx context.Context, db *sqlx.DB, tenant string, fields map[string]interface{}) error {
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

	var eeg model.Eeg
	updateRecord, err := buildRecordMap(&eeg, fields)
	if err != nil {
		return err
	}
	statement, _, err := pgDialect.Update(TABLE_EEG).Set(updateRecord).Where(goqu.Ex{"tenant": goqu.V(tenant)}).ToSQL()
	if err != nil {
		log.WithError(err).Errorf("Update EEG VALUES: %s", statement)
		return err
	}

	_, err = db.ExecContext(ctx, statement)
	return err
}

func getGridOperators(ctx context.Context, db *sqlx.DB) (map[string]string, error) {

	sql, _, err := pgDialect.From("base.gridoperators").ToSQL()

	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var id string
	var name string
	result := map[string]string{}
	for rows.Next() {
		err = rows.Scan(&id, &name)
		if err != nil {
			return nil, err
		}
		result[id] = name
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
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
		log.WithField("SQL", "SELECT").Errorf("Stmt: %s", stmt)
		return nil, err
	}
	return tenantsName, nil
}
