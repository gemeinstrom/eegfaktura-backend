package api

import (
	"context"
	"errors"
	"fmt"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
)

// assertPartitionFactorMeters checks that every metering point of a partition factor
// change belongs to the caller's community before anything is sent, and takes direction
// and grid operator from the current entry of the stored metering point, never from the
// request (261005-ca4). An owner change leaves a moved entry of the same metering point.
// Used by POST /meteringpoint/changepartitionfactor and POST /master/updatepartfact.
func assertPartitionFactorMeters(ctx context.Context, db database.Database, tenant string, meters []*model.ChangePartitionFactorRequest) error {
	if len(meters) == 0 {
		return nil
	}
	ids := make([]string, 0, len(meters))
	for _, m := range meters {
		if m == nil { // {"meteringPoints":[null]} (261005-ca6)
			return model.ErrParseJson(errors.New("empty metering point entry"))
		}
		ids = append(ids, m.MeteringPoint)
	}
	own, err := db.FindMeteringByIds(ctx, tenant, ids)
	if err != nil {
		return model.ErrFindMeter(err)
	}
	stored := map[string]*model.MeteringPoint{}
	for _, m := range own {
		if m.State != nil && m.State.Flag == model.F_ASSIGNED {
			stored[m.MeteringPoint] = m
		}
	}
	for _, id := range ids {
		if stored[id] == nil {
			return model.ErrFindMeter(fmt.Errorf("metering point %s not found", id))
		}
	}
	for _, m := range meters {
		m.Direction = stored[m.MeteringPoint].Direction
		m.GridOperatorId = stored[m.MeteringPoint].GridOperatorId
	}
	return nil
}
