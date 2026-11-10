package database

import (
	"context"
	dbsql "database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

type NotificationRepository interface {
	SaveNotification(tenant string, code model.EbMsMessageType, meters []string, errCodes []string, protocol model.EdaProtocol) error
	SaveNotificationFromMap(notificationValue map[string]interface{}, tenant string, notificationType model.NotificationType, process model.NotificationProcess, role string) error
	SaveHistory(tenant string, code model.EbMsMessageType, conversationId, role, dir string, protocol model.EdaProtocol, msg interface{}) error
	FindHistoryMessage(ctx context.Context, tenant, conversationId string, code model.EbMsMessageType) (*model.EbmsMessage, error)
	FetchEdaHistory(ctx context.Context, tenant, protocol string, start, end int64, pageSize uint) (interface{}, error)
	GetNotification(ctx context.Context, tenant string, start int64, isAdmin bool) ([]model.EegNotification, error)
}

func (db *sqlDatabase) SaveNotification(tenant string, code model.EbMsMessageType, meters []string, errCodes []string, protocol model.EdaProtocol) error {
	var err error
	notificationValue := map[string]interface{}{
		"type":           code,
		"meteringPoints": meters,
		"responseCodes":  errCodes,
	}

	if err = db.SaveNotificationFromMap(notificationValue, tenant, model.N_TYPE_MESSAGE, model.N_PROCESS_EDA_PROCESS, "ADMIN"); err != nil {
		logrus.WithField("PROTOCOL", protocol).Error(err)
	}
	return err
}

func (db *sqlDatabase) SaveNotificationFromMap(notificationValue map[string]interface{}, tenant string, notificationType model.NotificationType, process model.NotificationProcess, role string) error {
	var msgBytes []byte
	var err error
	if msgBytes, err = json.Marshal(notificationValue); err == nil {
		if err = createNotification(db.db, tenant, string(msgBytes), notificationType, process, role); err != nil {
			logrus.Error(err)
			return err
		}
	}
	return nil
}

// notificationPageSize is how many notifications the list returns.
const notificationPageSize = 30

// Batches of the admin list: the first one fills a page when nothing is left out; the next ones go further back past
// the requests and answers an energy request leaves out (one each per metering point, so a request for a whole
// community can hide a few thousand rows). collapseMaxBatches bounds the reads per call; reaching it is logged.
// Variables for the tests.
var (
	collapseFirstBatch uint = notificationPageSize
	collapseBatch      uint = 500
	collapseMaxBatches      = 40
)

// collapseTimeout bounds the reads of the list when the request context is not used (see GetNotification).
const collapseTimeout = 30 * time.Second

// GetNotification lists the newest notifications of tenant after id start. For admins, the requests and answers an
// energy request with a summary of this tenant stands for are left out (energyrequest.Collapser): while the run is
// open, a live entry (the current summary with live: true, at the run's row id and creation time) stands for them,
// placed with the newest of its messages; once closed, the stored summary. When the rows of the live entries, the
// runs or the summaries cannot be read, the plain list comes, nothing left out (logged: a cancelled or timed out
// request as warning, any other error as error).
func (db *sqlDatabase) GetNotification(ctx context.Context, tenant string, start int64, isAdmin bool) ([]model.EegNotification, error) {
	if !isAdmin {
		return getNotification(db.db, tenant, start)
	}
	rows, tenants, err := db.energyRequestCommunityCached(ctx, tenant)
	if err != nil {
		entry := logrus.WithContext(ctx).WithField("tenant", tenant).WithError(err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			entry.Warn("notifications: no live energy request entries")
		} else {
			entry.Error("notifications: no live energy request entries")
		}
		// as before this list did not depend on ctx: answer anyway, one bounded read
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), collapseTimeout)
		defer cancel()
		n, _, err := db.collapsedNotifications(detached, tenant, start, false)
		return n, err
	}
	live, _ := energyrequest.Live(rows, tenant, tenants, time.Now())
	n, latest, err := db.collapsedNotifications(ctx, tenant, start, true)
	if err != nil {
		// the runs or summaries could not be read: the plain list, without live entries
		logrus.WithContext(ctx).WithField("tenant", tenant).WithError(err).Error("notifications: energy request messages not collapsed")
		n, _, err = db.collapsedNotifications(ctx, tenant, start, false)
		return n, err
	}
	// a live entry is placed with its newest message; its id stays the run's row id
	place := map[int64]int64{}
	for _, e := range live {
		if e.RowId <= start {
			continue
		}
		msg, err := json.Marshal(e.Summary)
		if err != nil {
			return nil, err
		}
		n = append(n, model.EegNotification{Id: e.RowId, MsgType: string(model.N_TYPE_MESSAGE),
			Process: model.N_PROCESS_EDA_PROCESS, Message: msg, Date: time.UnixMilli(e.CreatedAt).UTC()})
		place[e.RowId] = max(e.RowId, latest[e.RowId])
	}
	key := func(x model.EegNotification) int64 {
		if p, ok := place[x.Id]; ok {
			return p
		}
		return x.Id
	}
	sort.SliceStable(n, func(i, j int) bool { return key(n[i]) > key(n[j]) })
	if len(n) > notificationPageSize {
		n = n[:notificationPageSize]
	}
	return n, nil
}

// collapsedNotifications reads the newest admin notifications of tenant after id start in batches and, with
// collapse, leaves out what an energyrequest.Collapser leaves out, until a page is full. The runs and summaries come from the community's rows through the partial indexes
// notification_energy_request_idx and notification_summary_idx, read only for batches with requests or answers.
// latest is as in Collapser.Hide.
func (db *sqlDatabase) collapsedNotifications(ctx context.Context, tenant string, start int64,
	collapse bool) ([]model.EegNotification, map[int64]int64, error) {
	visible := []model.EegNotification{}
	latest := map[int64]int64{}
	collapser := energyrequest.NewCollapser(tenant, time.Now())
	summariesFrom := int64(math.MaxInt32) // summaries are read for the runs from this row id on (id is SERIAL)
	var community []string
	before := int64(math.MaxInt64)
	size := collapseFirstBatch
	for i := 0; len(visible) < notificationPageSize; i++ {
		if i == collapseMaxBatches {
			logrus.WithContext(ctx).WithField("tenant", tenant).Warnf("notifications: %d entries after reading %d batches", len(visible), i)
			break
		}
		batch := []model.EegNotification{}
		sql, _, err := pgDialect.From("base.notification").Select(&batch).
			Where(goqu.C("tenant").Eq(tenant), goqu.C("id").Gt(start), goqu.C("id").Lt(before),
				goqu.C("process").Neq(model.N_PROCESS_ENERGY_REQUEST)).
			Order(goqu.I("id").Desc()).Limit(size).ToSQL()
		if err != nil {
			return nil, nil, err
		}
		if err = db.db.SelectContext(ctx, &batch, sql); err != nil && !errors.Is(err, dbsql.ErrNoRows) {
			return nil, nil, err
		}
		if len(batch) == 0 {
			break
		}
		before = batch[len(batch)-1].Id
		messages := []energyrequest.Message{}
		var oldest time.Time
		for _, b := range batch {
			if !collapse || !energyrequest.Collapsible(json.RawMessage(b.Message)) {
				continue
			}
			messages = append(messages, energyrequest.Message{Id: b.Id, Date: b.Date, Content: json.RawMessage(b.Message)})
			if oldest.IsZero() || b.Date.Before(oldest) {
				oldest = b.Date
			}
		}
		left := map[int64]bool{}
		if len(messages) > 0 {
			if community == nil {
				if community, err = db.communityTenants(ctx, tenant); err != nil {
					return nil, nil, err
				}
			}
			runs, err := db.energyRequestRunsBefore(ctx, community, messages[0].Id, oldest.Add(-energyrequest.CollapseWindow))
			if err != nil {
				return nil, nil, err
			}
			collapser.AddRuns(runs)
			if len(runs) > 0 && runs[0].Id < summariesFrom {
				summaries, err := db.energyRequestSummariesFrom(ctx, community, runs[0].Id, summariesFrom)
				if err != nil {
					return nil, nil, err
				}
				collapser.AddSummaries(summaries)
				summariesFrom = runs[0].Id
			}
			var l map[int64]int64
			left, l = collapser.Hide(messages)
			for id, last := range l {
				latest[id] = max(latest[id], last)
			}
		}
		for _, b := range batch {
			if left[b.Id] {
				continue
			}
			visible = append(visible, b)
			if len(visible) == notificationPageSize {
				break
			}
		}
		if uint(len(batch)) < size {
			break
		}
		size = collapseBatch
	}
	return visible, latest, nil
}

func (db *sqlDatabase) SaveHistory(tenant string, code model.EbMsMessageType, conversationId, role, dir string, protocol model.EdaProtocol, msg interface{}) error {

	var err error
	var msgBytes []byte
	if msgBytes, err = json.Marshal(msg); err == nil {
		if err = saveEdaHistory(db.db, &model.EdaProcessHistory{
			Tenant:         tenant,
			ConversationId: conversationId,
			ProcessType:    code,
			Date:           civil.DateTime{},
			Protocol:       null.StringFrom(string(protocol)),
			Issuer:         role,
			MessageByte:    msgBytes,
			MessageMap:     nil,
			Direction:      dir,
		}); err != nil {
			logrus.Error(err)
			return err
		}
	}
	return nil
}

// FindHistoryMessage returns the latest message of the given type in a conversation of the tenant, in either
// direction, or nil when there is none.
func (db *sqlDatabase) FindHistoryMessage(ctx context.Context, tenant, conversationId string, code model.EbMsMessageType) (*model.EbmsMessage, error) {
	var raw []byte
	err := db.db.GetContext(ctx, &raw, `SELECT message FROM base.processhistory
		WHERE tenant = $1 AND "conversationId" = $2 AND type = $3 ORDER BY date DESC LIMIT 1`, tenant, conversationId, code)
	if errors.Is(err, dbsql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	msg := &model.EbmsMessage{}
	if err := json.Unmarshal(raw, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func (db *sqlDatabase) FetchEdaHistory(ctx context.Context, tenant, protocol string, start, end int64, pageSize uint) (interface{}, error) {
	return fetchEdaHistory(ctx, db.db, tenant, protocol, start, end, pageSize)
}

func saveEdaHistory(db *sqlx.DB, history *model.EdaProcessHistory) error {
	sql, _, err := pgDialect.Insert("base.processhistory").Rows(history).ToSQL()
	_, err = db.Exec(sql)
	return err
}

func fetchEdaHistory(ctx context.Context, db *sqlx.DB, tenant, protocol string, start, end int64, pageSize uint) (interface{} /*map[string]map[string][]model.EdaProcessHistory*/, error) {
	startDate := civil.DateOf(time.UnixMilli(start))
	endDate := civil.DateOf(time.UnixMilli(end))

	protocolArray := strings.Split(protocol, ";")
	h := []model.EdaProcessHistory{}
	block := pgDialect.From("base.processhistory").Select(&h).
		Order(goqu.I("date").Asc()).
		Where(
			goqu.C("tenant").Eq(tenant), /*goqu.C("protocol").IsNotNull(),*/
			goqu.C("protocol").Eq(protocolArray),
			goqu.C("date").Between(goqu.Range(startDate, endDate)))

	if pageSize > 0 {
		block.Limit(pageSize + 1)
	}

	stmt, _, err := block.ToSQL()

	//fmt.Printf("STMT: %v\n", stmt)
	err = db.Select(&h, stmt)
	if err != nil && err != dbsql.ErrNoRows {
		logrus.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Error("Query History failed")
		return nil, err
	}

	resultSize := len(h)
	if pageSize > 0 {
		resultSize = int(math.Min(float64(len(h)), float64(pageSize)))
	}
	out := map[string]map[string][]model.EdaProcessHistory{}
	for i := 0; i < resultSize; i++ {
		e := h[i]
		err = json.Unmarshal(e.MessageByte, &e.MessageMap)
		if ci, ok := out[e.Protocol.String]; ok {
			ci[e.ConversationId] = append(ci[e.ConversationId], e)
		} else {
			ci := map[string][]model.EdaProcessHistory{}
			ci[e.ConversationId] = []model.EdaProcessHistory{e}
			out[e.Protocol.String] = ci
		}
	}

	type NextType struct {
		Start    int64  `json:"start,omitempty"`
		End      int64  `json:"end,omitempty"`
		Protocol string `json:"protocol,omitempty"`
		PageSize uint   `json:"page_size,omitempty"`
	}

	next := NextType{}
	if pageSize < uint(len(h)) {
		last := h[pageSize]
		next.Start = last.Date.Unix() * 1000
		next.End = end
		next.Protocol = last.Protocol.String
		next.PageSize = pageSize
	}

	result := struct {
		History struct {
			Next NextType `json:"next,omitempty"`
		}
		Data map[string]map[string][]model.EdaProcessHistory `json:"data"`
	}{
		History: struct {
			Next NextType `json:"next,omitempty"`
		}{
			Next: next,
		},
		Data: out,
	}

	return result, err
}

func createNotification(db sqlx.Execer, tenant, notification string,
	msgType model.NotificationType, process model.NotificationProcess, role string) error {
	stmt, _, err := pgDialect.Insert("base.notification").
		Rows(
			// date explizit als UTC setzen statt dem Spalten-Default `now()`: die Spalte ist
			// `timestamp without time zone`; `now()` schreibt lokale Wanduhrzeit (CEST), die als
			// `...Z` serialisiert wird → Frontend rechnet +Offset doppelt. `now() at time zone 'utc'`
			// liefert die UTC-Wanduhrzeit → korrekter Instant.
			goqu.Record{"tenant": tenant, "notification": notification, "type": msgType, "role": role, "process": process,
				"date": goqu.L("(now() AT TIME ZONE 'utc')")},
		).
		ToSQL()
	if err != nil {
		return err
	}

	_, err = db.Exec(stmt)
	return err
}

// getNotification lists the newest notifications for users (role USER) of tenant after id start.
func getNotification(db *sqlx.DB, tenant string, start int64) ([]model.EegNotification, error) {
	n := []model.EegNotification{}

	statement := pgDialect.From("base.notification").Select(&n).
		Where(goqu.C("tenant").Eq(tenant), goqu.C("id").Gt(start),
			goqu.C("process").Neq(model.N_PROCESS_ENERGY_REQUEST), goqu.C("role").Eq("USER"))

	sql, _, err := statement.Order(goqu.I("id").Desc()).Limit(notificationPageSize).ToSQL()
	if err != nil {
		return nil, err
	}
	err = db.Select(&n, sql)
	if err != nil && err != dbsql.ErrNoRows {
		return nil, err
	}

	return n, err
}
