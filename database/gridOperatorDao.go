package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"at.ourproject/vfeeg-backend/gridoperator"
	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

// gridOperatorAlias is a variable so tests can supply their own alias list.
var gridOperatorAlias = gridoperator.Alias

// applyGridOperator sets the grid operator id and name of the given metering points. The id is
// always derived from the metering point number (plus alias list) — whatever the client sent is
// replaced, because the id is the receiver of the EDA messages (platform#107).
func applyGridOperator(ctx context.Context, q sqlx.QueryerContext, tenant string, points []*model.MeteringPoint) error {
	if len(points) == 0 {
		return nil
	}
	names, err := queryGridOperatorNames(ctx, q)
	if err != nil {
		return err
	}
	eegCode, eegName, err := queryEegGridOperator(ctx, q, tenant)
	if err != nil {
		return err
	}

	alias := gridOperatorAlias()
	for _, p := range points {
		r, ok := gridoperator.FromMeteringPoint(p.MeteringPoint, alias)
		if !ok {
			// Not derivable (no AT + 6 digits): never take the client's value. On insert the field
			// stays empty, on update it is omitted, so the stored value is kept.
			p.GridOperatorId = null.String{}
			p.GridOperatorName = null.String{}
			continue
		}
		if r.Aliased {
			log.WithContext(ctx).WithField("tenant", tenant).Infof("Grid operator of %s: %s -> %s (alias)", p.MeteringPoint, r.Prefix, r.Id)
		}
		name := gridOperatorName(r.Id, names, eegCode, eegName)
		p.GridOperatorId = null.StringFrom(r.Id)
		p.GridOperatorName = null.NewString(name, name != "")
	}
	return nil
}

// gridOperatorName looks the name up in base.gridoperators and falls back to the name stored with
// the EEG when the id is the EEG's own grid operator.
func gridOperatorName(id string, names map[string]string, eegCode, eegName string) string {
	if name, ok := names[id]; ok {
		return name
	}
	if strings.EqualFold(id, eegCode) {
		return eegName
	}
	return ""
}

func queryGridOperatorNames(ctx context.Context, q sqlx.QueryerContext) (map[string]string, error) {
	// An id can have several names (PK id+name): take the lowest, like fix.sql (min(name)).
	stmt, _, err := pgDialect.From("base.gridoperators").Select("id", "name").
		Order(goqu.C("id").Asc(), goqu.C("name").Desc()).ToSQL()
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]string{}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		result[id] = name
	}
	return result, rows.Err()
}

func queryEegGridOperator(ctx context.Context, q sqlx.QueryerContext, tenant string) (string, string, error) {
	stmt, args, err := pgDialect.From(TABLE_EEG).
		Select(goqu.COALESCE(goqu.C("gridoperator_code"), ""), goqu.COALESCE(goqu.C("gridoperator_name"), "")).
		Where(goqu.C("tenant").Eq(tenant)).Prepared(true).ToSQL()
	if err != nil {
		return "", "", err
	}
	var code, name string
	err = q.QueryRowxContext(ctx, stmt, args...).Scan(&code, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return code, name, err
}

// MeteringPointGridOperators returns the grid operators of the tenant's metering points, upper case, each once.
func (db *sqlDatabase) MeteringPointGridOperators(ctx context.Context, tenant string) ([]string, error) {
	operators := []string{}
	err := db.db.SelectContext(ctx, &operators, `SELECT DISTINCT upper(trim(grid_operator_id)) FROM base.meteringpoint
		WHERE tenant = $1 AND trim(COALESCE(grid_operator_id, '')) <> ''`, tenant)
	return operators, err
}
