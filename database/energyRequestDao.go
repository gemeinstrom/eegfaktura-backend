package database

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/jmoiron/sqlx"
)

// EnergyRequestRepository keeps the energy request summary (platform#173) in base.notification: no own tables. The
// evaluation reads id ranges on the primary key; the Messages list finds runs and summaries of any age through the
// partial indexes notification_energy_request_idx and notification_summary_idx.
type EnergyRequestRepository interface {
	SaveEnergyRequestRow(ctx context.Context, tenant, rowType string, value interface{}) (int64, error)
	UpdateEnergyRequestRow(ctx context.Context, id int64, value interface{}) error
	DeleteEnergyRequestRow(ctx context.Context, id int64) error
	EnergyRequestRows(ctx context.Context, tenants []string) ([]energyrequest.Row, error)
	EnergyRequestCommunity(ctx context.Context, tenant string) ([]energyrequest.Row, map[string]bool, error)
	TenantsByEcId(ctx context.Context, ecId string) ([]string, error)
	CloseEnergyRequest(ctx context.Context, tenant string, runRowId int64, summary energyrequest.Summary) (bool, error)
}

// communityCacheTTL is how long the notification list keeps the energy request rows of a tenant's community.
const communityCacheTTL = 15 * time.Second

// communityCache keeps EnergyRequestCommunity for the notification list, per tenant and per instance: web polls the
// list while a live entry is shown. It is cleared when this instance writes or changes a run or writes a summary;
// answers arriving meanwhile show up after communityCacheTTL at the latest. The lock reads uncached.
// generation counts the clears: rows read before a clear are not stored after it.
var communityCache = struct {
	sync.Mutex
	entries    map[string]communityRows
	generation uint64
}{entries: map[string]communityRows{}}

type communityRows struct {
	at      time.Time
	rows    []energyrequest.Row
	tenants map[string]bool
}

func (db *sqlDatabase) energyRequestCommunityCached(ctx context.Context, tenant string) ([]energyrequest.Row, map[string]bool, error) {
	communityCache.Lock()
	e, ok := communityCache.entries[tenant]
	generation := communityCache.generation
	communityCache.Unlock()
	if ok && time.Since(e.at) < communityCacheTTL {
		return e.rows, e.tenants, nil
	}
	// most tenants have no open run: without a run of the tenant younger than Deadline there is no live entry and
	// no answer to leave out, so the community rows are not read
	found, err := db.hasEnergyRequestRun(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		tenants := map[string]bool{tenant: true}
		storeCommunityRows(tenant, generation, communityRows{at: time.Now(), rows: []energyrequest.Row{}, tenants: tenants})
		return []energyrequest.Row{}, tenants, nil
	}
	rows, tenants, err := db.EnergyRequestCommunity(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	storeCommunityRows(tenant, generation, communityRows{at: time.Now(), rows: rows, tenants: tenants})
	return rows, tenants, nil
}

// storeCommunityRows keeps rows read at cache generation generation, unless the cache was cleared since.
func storeCommunityRows(tenant string, generation uint64, e communityRows) {
	communityCache.Lock()
	defer communityCache.Unlock()
	if communityCache.generation == generation {
		communityCache.entries[tenant] = e
	}
}

func clearCommunityCache() {
	communityCache.Lock()
	communityCache.entries = map[string]communityRows{}
	communityCache.generation++
	communityCache.Unlock()
}

// energyRequestLock serialises closing runs (pg_advisory_xact_lock), so a summary is written once.
const energyRequestLock = 173173

// SaveEnergyRequestRow writes a hidden row (process ENERGY_REQUEST, not listed in Messages) and returns its id.
func (db *sqlDatabase) SaveEnergyRequestRow(ctx context.Context, tenant, rowType string, value interface{}) (int64, error) {
	msg, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	defer clearCommunityCache()
	var id int64
	err = db.db.GetContext(ctx, &id, `INSERT INTO base.notification (tenant, notification, type, role, process, date)
		VALUES ($1, $2, $3, 'ADMIN', $4, now() AT TIME ZONE 'utc') RETURNING id`,
		tenant, string(msg), rowType, model.N_PROCESS_ENERGY_REQUEST)
	return id, err
}

// UpdateEnergyRequestRow replaces the content of a hidden row.
func (db *sqlDatabase) UpdateEnergyRequestRow(ctx context.Context, id int64, value interface{}) error {
	msg, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clearCommunityCache()
	_, err = db.db.ExecContext(ctx, `UPDATE base.notification SET notification = $1 WHERE id = $2 AND process = $3`,
		string(msg), id, model.N_PROCESS_ENERGY_REQUEST)
	return err
}

// DeleteEnergyRequestRow deletes a hidden row.
func (db *sqlDatabase) DeleteEnergyRequestRow(ctx context.Context, id int64) error {
	defer clearCommunityCache()
	_, err := db.db.ExecContext(ctx, `DELETE FROM base.notification WHERE id = $1 AND process = $2`,
		id, model.N_PROCESS_ENERGY_REQUEST)
	return err
}

// EnergyRequestRows returns the rows every evaluation needs (ascending id): the hidden run rows and the summaries of
// the last energyrequest.LookBack, and the answers (ANTWORT_PT, ABLEHNUNG_PT) from the oldest of these runs on; of the
// given tenants, or of all tenants for nil. The lower id bound is the newest row older than LookBack: the primary key
// is read backwards up to it, so only the rows of that window are scanned (date grows with id; both are set on
// insert). Without a run, no answers are read.
func (db *sqlDatabase) EnergyRequestRows(ctx context.Context, tenants []string) ([]energyrequest.Row, error) {
	if tenants != nil && len(tenants) == 0 {
		return []energyrequest.Row{}, nil
	}
	tenantFilter := ``
	if tenants != nil {
		tenantFilter = ` AND tenant IN (?)`
	}
	query := `WITH b AS (SELECT COALESCE((SELECT id FROM base.notification
			WHERE date <= (now() AT TIME ZONE 'utc') - make_interval(secs => ?) ORDER BY id DESC LIMIT 1), 0) AS id),
		r AS (SELECT min(id) AS id FROM base.notification
			WHERE id > (SELECT id FROM b) AND process = ? AND type = ?` + tenantFilter + `)
		SELECT id, tenant, process, type, notification::text AS notification
		FROM base.notification
		WHERE id > (SELECT id FROM b) AND ((process = ? AND type = ?)
		   OR (process = ? AND notification->>'type' = ?)
		   OR (id > (SELECT id FROM r) AND process = ? AND notification->>'type' IN ('ANTWORT_PT', 'ABLEHNUNG_PT')))` +
		tenantFilter + ` ORDER BY id`
	args := []interface{}{energyrequest.LookBack.Seconds(), model.N_PROCESS_ENERGY_REQUEST, energyrequest.RowTypeRun}
	if tenants != nil {
		args = append(args, tenants)
	}
	args = append(args, model.N_PROCESS_ENERGY_REQUEST, energyrequest.RowTypeRun,
		model.N_PROCESS_EDA_PROCESS, energyrequest.SummaryType, model.N_PROCESS_EDA_PROCESS)
	if tenants != nil {
		args = append(args, tenants)
	}
	query, args, err := sqlx.In(query, args...)
	if err != nil {
		return nil, err
	}
	rows := []energyrequest.Row{}
	err = db.db.SelectContext(ctx, &rows, db.db.Rebind(query), args...)
	return rows, err
}

// hasEnergyRequestRun tells whether tenant has a run row younger than energyrequest.Deadline (energyrequest.Live
// lists only those). As in EnergyRequestRows, the lower id bound is the newest row older than the window: the primary
// key is read backwards over the rows of the last Deadline of all tenants up to it, then forwards from it until the
// first run row of tenant. No JSON is read, no answers, no base.eeg lookup.
func (db *sqlDatabase) hasEnergyRequestRun(ctx context.Context, tenant string) (bool, error) {
	var found bool
	err := db.db.GetContext(ctx, &found, `SELECT EXISTS (SELECT 1 FROM base.notification
		WHERE id > COALESCE((SELECT id FROM base.notification
			WHERE date <= (now() AT TIME ZONE 'utc') - make_interval(secs => $1) ORDER BY id DESC LIMIT 1), 0)
		AND process = $2 AND type = $3 AND tenant = $4)`,
		energyrequest.Deadline.Seconds(), model.N_PROCESS_ENERGY_REQUEST, energyrequest.RowTypeRun, tenant)
	return found, err
}

// EnergyRequestCommunity returns the rows of EnergyRequestRows for the tenants of tenant's community id (several for
// a GEA), and these tenants (tenant included).
func (db *sqlDatabase) EnergyRequestCommunity(ctx context.Context, tenant string) ([]energyrequest.Row, map[string]bool, error) {
	list, err := db.communityTenants(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	tenants := map[string]bool{}
	for _, t := range list {
		tenants[t] = true
	}
	rows, err := db.EnergyRequestRows(ctx, list)
	return rows, tenants, err
}

// communityTenants returns tenant and the other tenants with its community id (GEA), each once.
func (db *sqlDatabase) communityTenants(ctx context.Context, tenant string) ([]string, error) {
	list := []string{}
	if err := db.db.SelectContext(ctx, &list, `SELECT tenant FROM base.eeg
		WHERE "communityId" = (SELECT "communityId" FROM base.eeg WHERE tenant = $1)`, tenant); err != nil {
		return nil, err
	}
	tenants := []string{tenant}
	for _, t := range list {
		if t != tenant {
			tenants = append(tenants, t)
		}
	}
	return tenants, nil
}

// energyRequestRunsBefore returns the RUN rows of tenants with an id below before, written at since or later, in
// ascending id, for the Messages list (energyrequest.Collapser). The literal process matches the partial index
// notification_energy_request_idx, so only it is read.
func (db *sqlDatabase) energyRequestRunsBefore(ctx context.Context, tenants []string, before int64,
	since time.Time) ([]energyrequest.Row, error) {
	query, args, err := sqlx.In(`SELECT id, tenant, process, type, notification::text AS notification
		FROM base.notification
		WHERE process = 'ENERGY_REQUEST' AND tenant IN (?) AND id < ? AND type = ?
		  AND date >= (to_timestamp(?) AT TIME ZONE 'utc')
		ORDER BY id`, tenants, before, energyrequest.RowTypeRun, float64(since.UnixMilli())/1000)
	if err != nil {
		return nil, err
	}
	rows := []energyrequest.Row{}
	err = db.db.SelectContext(ctx, &rows, db.db.Rebind(query), args...)
	return rows, err
}

// energyRequestSummariesFrom returns the stored summaries of tenants with a row id from from up to below to (the
// runs' summaries are written after them), for the Messages list. The literal condition matches the partial index
// notification_summary_idx.
func (db *sqlDatabase) energyRequestSummariesFrom(ctx context.Context, tenants []string, from, to int64) ([]energyrequest.Row, error) {
	query, args, err := sqlx.In(`SELECT id, tenant, process, type, notification::text AS notification
		FROM base.notification
		WHERE (notification->>'type') = 'ZUSAMMENFASSUNG_PT' AND tenant IN (?) AND id >= ? AND id < ?
		ORDER BY id`, tenants, from, to)
	if err != nil {
		return nil, err
	}
	rows := []energyrequest.Row{}
	err = db.db.SelectContext(ctx, &rows, db.db.Rebind(query), args...)
	return rows, err
}

// TenantsByEcId returns the tenants of a community id (several for a GEA).
func (db *sqlDatabase) TenantsByEcId(ctx context.Context, ecId string) ([]string, error) {
	tenants := []string{}
	err := db.db.SelectContext(ctx, &tenants, `SELECT tenant FROM base.eeg WHERE "communityId" = $1`, ecId)
	return tenants, err
}

// CloseEnergyRequest writes the summary of the run in row runRowId, unless one was written already. Check and insert
// run under a transaction-scoped advisory lock: no second summary from an overlapping check or a second instance.
func (db *sqlDatabase) CloseEnergyRequest(ctx context.Context, tenant string, runRowId int64, summary energyrequest.Summary) (bool, error) {
	msg, err := json.Marshal(summary)
	if err != nil {
		return false, err
	}
	inserted, clearCache := false, false
	err = inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, energyRequestLock); err != nil {
			return err
		}
		var exists bool
		if err := tx.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM base.notification
			WHERE id > $1 AND process = $2 AND notification->>'type' = $3 AND notification->>'runId' = $4)`,
			runRowId, model.N_PROCESS_EDA_PROCESS, energyrequest.SummaryType, summary.RunId); err != nil {
			return err
		}
		if exists {
			return nil
		}
		clearCache = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO base.notification (tenant, notification, type, role, process, date)
			VALUES ($1, $2, $3, 'ADMIN', $4, now() AT TIME ZONE 'utc')`,
			tenant, string(msg), model.N_TYPE_MESSAGE, model.N_PROCESS_EDA_PROCESS); err != nil {
			return err
		}
		inserted = true
		return nil
	})
	if clearCache {
		clearCommunityCache() // after the commit or rollback, as before
	}
	return inserted, err
}
