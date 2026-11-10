package database

import (
	"context"
	"database/sql"
	"errors"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jmoiron/sqlx"
)

// ErrSettlementIntervalUnchanged: the new interval is the current one; nothing was written.
var ErrSettlementIntervalUnchanged = errors.New("settlement interval unchanged")

// ErrSettlementIntervalNoEeg: the tenant has no base.eeg row; nothing was written.
var ErrSettlementIntervalNoEeg = errors.New("eeg not found")

// ChangeSettlementInterval applies the change rule (model.DecideSettlementInterval) to the tenant's current interval
// in one transaction: the base.eeg row is locked (FOR UPDATE), so two changes at the same time decide one after the
// other. With dryRun or when the rule blocks nothing is written; otherwise the interval is set and the change is
// recorded in base.settlement_interval_change (R6). newInterval must be valid (model.IsSettlementInterval).
func (db *sqlDatabase) ChangeSettlementInterval(ctx context.Context, tenant, newInterval, changedBy string,
	state model.BillingState, dryRun bool) (model.SettlementIntervalDecision, error) {
	var decision model.SettlementIntervalDecision
	err := inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		var current string
		if err := tx.GetContext(ctx, &current, `SELECT "settlementInterval" FROM base.eeg WHERE tenant = $1 FOR UPDATE`,
			tenant); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSettlementIntervalNoEeg
			}
			return err
		}
		if current == newInterval {
			decision.OldInterval = current
			return ErrSettlementIntervalUnchanged
		}
		decision = model.DecideSettlementInterval(newInterval, state)
		decision.OldInterval = current
		if dryRun || !decision.Allowed {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE base.eeg SET "settlementInterval" = $1 WHERE tenant = $2`,
			newInterval, tenant); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO base.settlement_interval_change
			(tenant, old_value, new_value, effective_from, last_final_period, changed_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			tenant, current, newInterval, decision.EffectiveFrom, decision.LastFinalPeriod, changedBy)
		return err
	})
	return decision, err
}
