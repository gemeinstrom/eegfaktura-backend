package database

import (
	"context"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jmoiron/sqlx"
)

// inTx runs fn in one transaction: commit when fn returns nil, otherwise roll back. The error of
// fn is returned unchanged (not joined with the rollback's), so a *model.VfeegError keeps its code
// for the client; a failed commit is returned as the result.
func inTx(ctx context.Context, db *sqlx.DB, fn func(tx *sqlx.Tx) error) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return model.ErrOpenTx(err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after the commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
