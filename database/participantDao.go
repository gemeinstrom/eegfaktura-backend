package database

import (
	"context"
	dbsql "database/sql"
	"errors"
	"fmt"

	"strings"

	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"

	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
	"gopkg.in/guregu/null.v4"
)

type ParticipantRepository interface {
	GetParticipants(ctx context.Context, tenant string) ([]*model.EegParticipant, error)
	GetParticipant(ctx context.Context, tenant, participantId string) (*model.EegParticipant, error)
	GetParticipantByName(ctx context.Context, tenant string, email string) ([]*model.EegParticipant, error)
	ConfirmParticipant(ctx context.Context, tenant, username, participantId string) error
	RegisterParticipant(ctx context.Context, tenant, username string, participant *model.EegParticipant) error
	QueryParticipant(ctx context.Context, tenant, participantId string) (*model.EegParticipant, error)
	ImportParticipant(ctx context.Context, tenant, username string, participant *model.EegParticipant) error
	FindParticipantByMeteringPoint(ctx context.Context, tenant, meteringPoint string) (*model.EegParticipant, error)
	UpdateParticipant(ctx context.Context, tenant, user string, participant *model.EegParticipant) error
	UpdateParticipantPartial(ctx context.Context, tenant, participantId, name string, value interface{}) error
	UpdateParticipantValues(ctx context.Context, participantId, tenant string, values map[string]string) error
	DeleteParticipant(ctx context.Context, tenant, participantId string) error
}

func (db *sqlDatabase) GetParticipants(ctx context.Context, tenant string) ([]*model.EegParticipant, error) {
	return getParticipants(ctx, db.db, tenant)
}

func (db *sqlDatabase) GetParticipant(ctx context.Context, tenant, participantId string) (*model.EegParticipant, error) {
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return nil, err
	}
	return getParticipant(ctx, db.db, participantId)
}

func (db *sqlDatabase) GetParticipantByName(ctx context.Context, tenant, email string) ([]*model.EegParticipant, error) {
	return getParticipantByName(ctx, db.db, tenant, email)
}

func (db *sqlDatabase) RegisterParticipant(ctx context.Context, tenant, username string, participant *model.EegParticipant) error {
	return inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		return registerParticipant(ctx, tx, tenant, username, participant)
	})
}

func (db *sqlDatabase) ConfirmParticipant(ctx context.Context, tenant, username, participantId string) error {
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return err
	}
	return confirmParticipant(ctx, db.db, username, participantId)
}

func (db *sqlDatabase) QueryParticipant(ctx context.Context, tenant, participantId string) (*model.EegParticipant, error) {
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return nil, err
	}
	return queryParticipant(ctx, db.db, participantId)
}

func (db *sqlDatabase) ImportParticipant(ctx context.Context, tenant, username string, participant *model.EegParticipant) error {
	return inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		return importParticipant(ctx, tx, tenant, username, participant)
	})
}

func (db *sqlDatabase) FindParticipantByMeteringPoint(ctx context.Context, tenant, meteringPoint string) (*model.EegParticipant, error) {
	return findParticipantByMeteringPoint(ctx, db.db, tenant, meteringPoint)
}

func (db *sqlDatabase) UpdateParticipant(ctx context.Context, tenant, user string, participant *model.EegParticipant) error {
	return inTx(ctx, db.db, func(tx *sqlx.Tx) error {
		return updateParticipant(ctx, tx, tenant, user, participant)
	})
}

func (db *sqlDatabase) UpdateParticipantPartial(ctx context.Context, tenant, participantId, name string, value interface{}) error {
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return err
	}
	return updateParticipantPartial(ctx, db.db, participantId, name, value)
}

func (db *sqlDatabase) UpdateParticipantValues(ctx context.Context, participantId, tenant string, values map[string]string) error {
	// Same rules as the REST partial update: the participant must belong to the
	// tenant, and every key must name an updatable field — it ends up as an SQL
	// identifier. All keys are checked before the first write.
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return err
	}
	for k := range values {
		if !model.IsAllowedParticipantUpdatePath(k) {
			return model.ErrUpdateParticipant(fmt.Errorf("field %q cannot be updated", k))
		}
	}
	var err error
	for k, v := range values {
		if err = updateParticipantPartial(ctx, db.db, participantId, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (db *sqlDatabase) DeleteParticipant(ctx context.Context, tenant, participantId string) error {
	if err := assertParticipantTenant(ctx, db.db, tenant, participantId); err != nil {
		return err
	}
	tx, err := db.db.BeginTxx(ctx, nil)
	if err != nil {
		return model.ErrDeleteParticipant(err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after the commit; a joined error would lose its code for the client
	if err = deleteParticipant(ctx, tx, participantId); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return model.ErrDeleteParticipant(err)
	}
	return nil
}

const TABLE_PARTICIPANT = "base.participant"

// Die Abfragen auf einen einzelnen Teilnehmer liefen frueher ausschliesslich ueber
// die ID, ohne den aus dem Token geprueften Mandanten. Wer eine fremde ID kannte,
// konnte den Datensatz lesen, aendern oder loeschen — samt Bankverbindung und
// Adressen. Die IDs sind zudem zeitbasiert (uuid.NewUUID), auf ihre Unkenntnis ist
// also kein Verlass. Der Fremdzugriff wird immer abgewiesen; der fruehere Schalter
// PARTICIPANT_TENANT_ENFORCE (nur protokollieren) ist entfernt, seit alle Umgebungen
// mit aktiver Pruefung laufen.
//
// assertParticipantTenant stellt sicher, dass der Teilnehmer zur Gemeinschaft des
// Aufrufers gehoert. Bewusst als Vorabpruefung statt als zusaetzliche WHERE-Bedingung:
// updateParticipantPartial schreibt auch in Kindtabellen (Adressen, Kontakt), dort
// waere ein durchgereichter Mandant leicht zu uebersehen.
func assertParticipantTenant(ctx context.Context, db sqlx.QueryerContext, tenant, participantId string) error {
	if tenant == "" {
		return model.ErrForeignTenantParticipant(errors.New("no tenant in request context"))
	}

	stmt, _, err := pgDialect.From(TABLE_PARTICIPANT).
		Select("tenant").
		Where(goqu.Ex{"id": participantId}).
		ToSQL()
	if err != nil {
		return model.ErrGetParticipant(err)
	}

	var owner string
	if err := sqlx.GetContext(ctx, db, &owner, stmt); err != nil {
		if errors.Is(err, dbsql.ErrNoRows) {
			// Nicht vorhanden: als "nicht gefunden" abweisen, nicht durchlassen (261005-ca4).
			return errParticipantNotFound()
		}
		return model.ErrGetParticipant(err)
	}

	if strings.EqualFold(owner, tenant) {
		return nil
	}

	log.WithContext(ctx).WithFields(log.Fields{
		"tenant":        tenant,
		"participantId": participantId,
	}).Warn("cross-tenant participant access refused")

	return errParticipantNotFound()
}

// errParticipantNotFound is the one answer for an unknown and a foreign participant:
// the caller must not learn that the id exists in another community. The log above
// keeps the two apart.
func errParticipantNotFound() error {
	return model.ErrFindParticipant(errors.New("participant not found"))
}

func getParticipants(ctx context.Context, db *sqlx.DB, tenant string) ([]*model.EegParticipant, error) {
	var participants []*model.EegParticipant = []*model.EegParticipant{}

	stmt, _, err := buildParticipantQueryStmt().
		Where(goqu.C("tenant").Eq(tenant)).
		ToSQL()
	if err != nil {
		return []*model.EegParticipant{}, model.ErrGetParticipant(err)
	}

	err = db.SelectContext(ctx, &participants, stmt)
	if err != nil {
		return []*model.EegParticipant{}, model.ErrGetParticipant(err)
	}

	err = completeParticipants(ctx, db, tenant, participants)
	if err != nil {
		return []*model.EegParticipant{}, model.ErrGetParticipant(err)
	}

	return participants, nil
}

func getParticipant(ctx context.Context, db *sqlx.DB, participantId string) (*model.EegParticipant, error) {
	participant := &model.EegParticipant{}

	stmt, _, err := buildParticipantQueryStmt().
		Where(goqu.C("id").Eq(participantId)).
		ToSQL()
	if err != nil {
		return participant, model.ErrGetParticipant(err)
	}

	err = db.GetContext(ctx, participant, stmt)
	if err != nil {
		return participant, model.ErrGetParticipant(err)
	}

	if participant != nil {
		err = completeParticipant(ctx, db, participant)
		if err != nil {
			log.WithContext(ctx).Errorf("Cannot fetch Participant correct: %s", err.Error())
		}
		if participant.MeteringPoint == nil {
			participant.MeteringPoint = make([]*model.MeteringPoint, 0)
		}
	}

	return participant, nil
}

func getParticipantByName(ctx context.Context, db *sqlx.DB, tenant, email string) ([]*model.EegParticipant, error) {
	var participants []*model.EegParticipant

	subquery := goqu.
		From("base.contactdetail").
		Select("participant_id").
		Where(goqu.L("LOWER(email) = ?", strings.ToLower(email)))

	stmt, _, err := buildParticipantQueryStmt().
		Where(goqu.C("tenant").Eq(tenant),
			goqu.C("id").In(subquery)).
		ToSQL()

	if err != nil {
		return nil, model.ErrGetParticipant(err)
	}

	err = db.SelectContext(ctx, &participants, stmt)
	if err != nil {
		return nil, model.ErrGetParticipant(err)
	}

	err = completeParticipants(ctx, db, tenant, participants)
	if err != nil {
		return nil, model.ErrGetParticipant(err)
	}

	return participants, nil
}

func queryParticipant(ctx context.Context, db *sqlx.DB, participantId string) (*model.EegParticipant, error) {
	var participant model.EegParticipant = model.EegParticipant{}

	sql, _, err := buildParticipantQueryStmt().
		Where(goqu.Ex{"base.participant.id": participantId}).
		ToSQL()

	if err != nil {
		return nil, model.ErrGetParticipant(err)
	}
	err = db.GetContext(ctx, &participant, sql)
	if err != nil {
		return nil, model.ErrGetParticipant(err)
	}

	err = completeParticipant(ctx, db, &participant)
	if err != nil {
		return nil, err
	}

	return &participant, nil
}

func buildParticipantQueryStmt() *goqu.SelectDataset {
	billingAddrStmt := pgDialect.From("base.address").Select(
		goqu.C("participant_id"), &model.Address{}).Where(goqu.Ex{"type": "BILLING"})
	residentAddStmt := pgDialect.From("base.address").
		Select(goqu.C("participant_id"), &model.Address{}).Where(goqu.Ex{"type": "RESIDENCE"})
	bankAccountStmt := pgDialect.From("base.bankaccount").
		Select(goqu.C("participant_id"), &model.BankInfo{})
	contactInfoStmt := pgDialect.From("base.contactdetail").Select(goqu.C("participant_id"), &model.ContactInfo{})

	return pgDialect.From(TABLE_PARTICIPANT).
		LeftJoin(billingAddrStmt.As("billingAddress"), goqu.On(goqu.Ex{"participant.id": goqu.I("billingAddress.participant_id")})).   /*.As("billingAddress")*/
		LeftJoin(residentAddStmt.As("residentAddress"), goqu.On(goqu.Ex{"participant.id": goqu.I("residentAddress.participant_id")})). /*.As("billingAddress")*/
		LeftJoin(bankAccountStmt.As("accountInfo"), goqu.On(goqu.Ex{"participant.id": goqu.I("accountInfo.participant_id")})).
		LeftJoin(contactInfoStmt.As("contact"), goqu.On(goqu.Ex{"participant.id": goqu.I("contact.participant_id")})).Select(&model.EegParticipant{})
}

// enforceContactEmail normalizes (trim per ';'-part) and validates the
// participant's e-mail against the shared address rule before any
// persist. Server-side enforcement — the web forms validate too, but
// the API must not rely on the frontend alone. An address that is
// empty after trimming is stored as NULL so the send-path guard
// (Contact.Email.Valid) keeps working.
func enforceContactEmail(participant *model.EegParticipant) error {
	if !participant.Contact.Email.Valid {
		return nil
	}
	normalized, err := model.ValidateEmailList(participant.Contact.Email.String)
	if err != nil {
		return err
	}
	if normalized == "" {
		participant.Contact.Email = null.String{}
	} else {
		participant.Contact.Email = null.StringFrom(normalized)
	}
	return nil
}

// enforceAddressTypes stamps the row discriminator server-side. base.address
// keeps both addresses of a member in one table and every read path joins on
// type = 'RESIDENCE' / 'BILLING'. The column default never applies because
// toRecord always writes the field, so a request without a residentAddress
// block inserted a row with an empty type: invisible to every join, and the
// update path matched nothing either. The discriminator is not user data.
func enforceAddressTypes(participant *model.EegParticipant) {
	participant.BillingAddress.Type = model.BILLING
	participant.ResidentAddress.Type = model.RESIDENCE
}

// updateParticipant runs inside the caller's transaction: a failure part-way must
// not leave the member half updated (261005-ca1).
func updateParticipant(ctx context.Context, tx *sqlx.Tx, tenant, user string, participant *model.EegParticipant) error {

	// The child-table updates below (contactdetail, address, bankaccount) are
	// scoped by participant_id only. Assert first that the participant belongs to
	// the caller's tenant — otherwise a foreign id in the body would overwrite
	// another tenant's contact, addresses and bank account. Mirrors
	// UpdateParticipantPartial / DeleteParticipant.
	if err := assertParticipantTenant(ctx, tx, tenant, participant.Id.String()); err != nil {
		return err
	}

	if err := enforceContactEmail(participant); err != nil {
		return model.ErrInvalidParticipantEmail(err)
	}
	trimBankName(&participant.BankAccount)

	updateValues := struct {
		model.EegParticipantBase
		LastModifiedBy string         `db:"lastModifiedBy"`
		LastModifiedAt civil.DateTime `db:"lastModifiedDate"`
	}{
		participant.EegParticipantBase,
		user,
		civil.Now(),
	}

	sql, _, _ := goqu.Update("base.participant").
		Set(updateValues).
		Where(goqu.Ex{
			"tenant": goqu.Op{"eq": tenant},
			"id":     goqu.Op{"eq": participant.Id.String()},
		}).
		ToSQL()
	_, err := tx.ExecContext(ctx, sql)
	if err != nil {
		return err
	}

	extra := map[string]interface{}{"participant_id": participant.Id.String()}
	resident, billing := participant.ResidentAddress, participant.BillingAddress
	resident.Type, billing.Type = model.RESIDENCE, model.BILLING

	sql, _, _ = goqu.Update("base.contactdetail").
		Set(participant.Contact).
		Where(goqu.Ex{
			"participant_id": participant.Id.String(),
		}).
		ToSQL()
	err = execOrInsertChild(ctx, tx, sql, "base.contactdetail", toRecord(participant.Contact, extra))
	if err != nil {
		return err
	}

	sql, _, _ = goqu.Update("base.address").
		Set(participant.ResidentAddress).
		Where(goqu.Ex{
			"type":           model.RESIDENCE,
			"participant_id": participant.Id.String(),
		}).
		ToSQL()
	err = execOrInsertChild(ctx, tx, sql, "base.address", toRecord(resident, extra))
	if err != nil {
		return err
	}

	sql, _, _ = goqu.Update("base.address").
		Set(participant.BillingAddress).
		Where(goqu.Ex{
			"type":           model.BILLING,
			"participant_id": participant.Id.String(),
		}).
		ToSQL()
	err = execOrInsertChild(ctx, tx, sql, "base.address", toRecord(billing, extra))
	if err != nil {
		return err
	}

	sql, _, _ = goqu.Update("base.bankaccount").
		Set(participant.BankAccount).
		Where(goqu.Ex{
			"participant_id": participant.Id.String(),
		}).
		ToSQL()
	err = execOrInsertChild(ctx, tx, sql, "base.bankaccount", bankAccountRecord(participant.BankAccount, extra))
	if err != nil {
		return err
	}
	return err
}

// execOrInsertChild runs the UPDATE of a child row (contact, address, bank account) and
// inserts the row when the member has none yet: such members are listed and editable,
// so an edit must not be lost (261029-mm1). An empty statement (nothing to set) inserts
// nothing.
func execOrInsertChild(ctx context.Context, tx *sqlx.Tx, updateSql, table string, record goqu.Record) error {
	res, err := tx.ExecContext(ctx, updateSql)
	if err != nil || updateSql == "" {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	sql, _, err := pgDialect.Insert(table).Rows(record).ToSQL()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, sql)
	return err
}

// enforceDebitIban: a new member with direct debit (CORE, B2B) needs an IBAN. Before the empty IBAN of
// bankAccountRecord, NOT NULL in production refused such a member by chance; web checks it in the form. Updates and
// the Excel import are not checked: web switches the debit type before the IBAN is entered (partial update).
func enforceDebitIban(participant *model.EegParticipant) error {
	switch strings.ToUpper(strings.TrimSpace(participant.BankAccount.SepaDirectDebit.String)) {
	case "CORE", "B2B":
		if strings.TrimSpace(participant.BankAccount.Iban.String) == "" {
			return model.ErrDebitWithoutIban(errors.New(model.DebitWithoutIbanText))
		}
	}
	return nil
}

// trimBankName removes the spaces around the bank name: the form required it, so a space was entered in its place,
// which the invoice prints as "Bankverbindung:   | …" (platform#249).
func trimBankName(b *model.BankInfo) {
	if b.BankName.Valid {
		b.BankName.String = strings.TrimSpace(b.BankName.String)
	}
}

// bankAccountRecord is the row of a new bank account. A member without IBAN ("Kein SEPA", platform#143) gets an empty
// IBAN instead of NULL: databases created before the migrations keep NOT NULL on base.bankaccount.iban (decision
// 2026-11-09, no schema change).
func bankAccountRecord(b model.BankInfo, extra map[string]interface{}) goqu.Record {
	rec := toRecord(b, extra)
	if rec["iban"] == nil {
		rec["iban"] = ""
	}
	return rec
}

func registerParticipant(ctx context.Context, tx *sqlx.Tx, tenant, username string, participant *model.EegParticipant) error {
	participant.Status = model.PENDING
	participant.Id = uuid.NewUUID()
	participant.CreatedBy = username
	// invalid input like the member update (updateParticipant): the code keeps the reason for the
	// client; the Excel import (importParticipant) logs the bare error as before
	if err := enforceContactEmail(participant); err != nil {
		return model.ErrInvalidParticipantEmail(err)
	}
	if err := enforceDebitIban(participant); err != nil {
		return err
	}
	return saveParticipant(ctx, tx, tenant, username, participant, ImportMeteringPoints)
}

func importParticipant(ctx context.Context, tx *sqlx.Tx, tenant, username string, participant *model.EegParticipant) error {

	// check if User already exists
	stmt, _, err := pgDialect.From("base.participant").
		Select("id").
		Where(
			goqu.C("firstname").Eq(participant.FirstName),
			goqu.C("lastname").Eq(participant.LastName),
			goqu.C("tenant").Eq(tenant)).ToSQL()
	if err != nil {
		return err
	}
	participantId := ""
	err = tx.GetContext(ctx, &participantId, stmt)
	if err == nil {
		return ImportMeteringPoints(ctx, tx, tenant, username, participantId, participant.MeteringPoint)
	}

	participant.Id = uuid.NewUUID()
	return saveParticipant(ctx, tx, tenant, username, participant, ImportMeteringPoints)
}

func confirmParticipant(ctx context.Context, db *sqlx.DB, username, participantId string) error {

	sql, _, err := pgDialect.Update(TABLE_PARTICIPANT).
		Set(goqu.Ex{"status": model.ACTIVE, "lastModifiedDate": civil.Now(), "lastModifiedBy": username}).
		Where(goqu.C("id").Eq(participantId)).
		ToSQL()
	if err != nil {
		log.WithContext(ctx).WithField("SQL", "CREATESTMT").WithError(err)
		return model.ErrCompleteParticipant(err)
	}

	_, err = db.ExecContext(ctx, sql)
	if err != nil {
		log.WithContext(ctx).WithField("SQL", "UPDATE").WithError(err).Error("SQL statement failed")
		return model.ErrCompleteParticipant(err)
	}
	return nil
}

// deleteParticipant deletes a member whose metering points never took part (261005-ca10): the rows cascade, also
// the history of active ones. Same rule as removeMeteringPoint, plus REJECTED: a rejected metering point could
// otherwise neither go alone nor with its member. PENDING stays: the member can still approve at the grid operator. The member's row is locked first: a metering point moved or imported
// onto it waits or is seen. The rows stay locked so an EDA answer cannot activate one in between.
func deleteParticipant(ctx context.Context, tx *sqlx.Tx, participantId string) error {
	var deletable []bool
	var id string
	err := tx.GetContext(ctx, &id, `SELECT id FROM `+TABLE_PARTICIPANT+` WHERE id = $1 FOR UPDATE`, participantId)
	if err != nil {
		return model.ErrDeleteParticipant(err)
	}
	err = tx.SelectContext(ctx, &deletable, `SELECT status = 'INIT' AND activesince IS NULL
			AND process_state IN ('INVALID', 'NEW', 'REJECTED')
		FROM `+TABLE_METERINGPOINT+` WHERE participant_id = $1 FOR UPDATE`, participantId)
	if err != nil {
		return model.ErrDeleteParticipant(err)
	}
	for _, ok := range deletable {
		if !ok {
			return model.ErrParticipantHasActivatedMeter(errors.New("participant has an activated metering point"))
		}
	}

	stmt, _, err := pgDialect.Delete(TABLE_PARTICIPANT).
		Where(goqu.Ex{"id": participantId}).ToSQL()
	if err != nil {
		return model.ErrDeleteParticipant(err)
	}
	_, err = tx.ExecContext(ctx, stmt)
	if err != nil {
		return model.ErrDeleteParticipant(err)
	}
	return nil
}

func saveParticipant(ctx context.Context, tx *sqlx.Tx, tenant, username string, participant *model.EegParticipant,
	registerMeteringPointsFunc func(context.Context, *sqlx.Tx, string, string, string, []*model.MeteringPoint) error) error {

	if err := enforceContactEmail(participant); err != nil {
		return err
	}
	enforceAddressTypes(participant)
	trimBankName(&participant.BankAccount)

	// "Mitglied seit" aus Import/Registrierung übernehmen; nur ohne Wert auf heute setzen.
	if !participant.ParticipantSince.Valid {
		participant.ParticipantSince = civil.NullDate{Date: civil.Today(), Valid: true}
	}
	registeringParticipant := struct {
		model.EegParticipantBase
		Tenant           string         `db:"tenant"`
		CreatedBy        string         `db:"createdBy"`
		LastmodifiedBy   string         `db:"lastModifiedBy"`
		LastmodifiedDate civil.DateTime `db:"lastModifiedDate"`
	}{
		participant.EegParticipantBase, tenant, username, username, civil.Now(),
	}

	participantId := ""
	sql, _, _ := pgDialect.Insert(TABLE_PARTICIPANT).Rows(registeringParticipant).Returning("id").ToSQL()
	err := tx.QueryRowContext(ctx, sql).Scan(&participantId)
	if err != nil {
		return model.ErrRegisterParticipant(err)
	}

	extra := map[string]interface{}{"participant_id": participantId}

	sql, _, _ = pgDialect.Insert("base.contactdetail").Rows(toRecord(participant.Contact, extra)).ToSQL()
	_, err = tx.Exec(sql)
	if err != nil {
		return model.ErrRegisterParticipant(err)
	}

	sql, _, _ = pgDialect.Insert("base.bankaccount").Rows(bankAccountRecord(participant.BankAccount, extra)).ToSQL()
	_, err = tx.Exec(sql)
	if err != nil {
		return model.ErrRegisterParticipant(err)
	}

	sql, _, err = pgDialect.Insert("base.address").Rows(
		toRecord(participant.BillingAddress, extra),
		toRecord(participant.ResidentAddress, extra),
	).ToSQL()

	if err != nil {
		log.WithContext(ctx).WithField("STMT", "INSERT").WithError(err).Error("SQL statement failed")
		return model.ErrRegisterParticipant(err)
	}
	_, err = tx.Exec(sql)
	if err != nil {
		return model.ErrRegisterParticipant(err)
	}

	err = registerMeteringPointsFunc(ctx, tx, tenant, username, participantId, participant.MeteringPoint)
	if err != nil {
		// a metering point active at another member keeps its code and text for the user (VfeegError has no Unwrap)
		var ve *model.VfeegError
		if errors.As(err, &ve) && ve.Code == model.ErrMeterActiveAtOtherParticipant(nil).Code {
			return err
		}
		return model.ErrRegisterParticipant(err)
	}
	return nil
}

func updateParticipantPartial(ctx context.Context, db *sqlx.DB, participantId, name string, value interface{}) error {

	var stmt *goqu.UpdateDataset
	var sql string
	var updateValues interface{}
	var err error

	// The generic path/value mechanism also carries the e-mail — enforce
	// the shared address rule here as well (normalize, reject invalid,
	// store NULL when empty after trimming).
	if name == "contact.email" {
		if s, ok := value.(string); ok {
			normalized, err := model.ValidateEmailList(s)
			if err != nil {
				// the caller gets the reason (code 1110), as the full update does
				return model.ErrInvalidParticipantEmail(err)
			}
			if normalized == "" {
				value = nil
			} else {
				value = normalized
			}
		}
	}

	// clearing the IBAN stores an empty one, as for a new bank account (bankAccountRecord)
	if name == "accountInfo.iban" && value == nil {
		value = ""
	}
	if s, ok := value.(string); ok && name == "accountInfo.bankName" {
		value = strings.TrimSpace(s)
	}

	fields := map[string]interface{}{}

	names := strings.Split(name, ".")
	if len(names) == 2 {

		fields[names[1]] = value
		switch names[0] {
		case "billingAddress":
			var result model.Address
			updateValues, err = buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Update("base.address").
				Where(goqu.Ex{"participant_id": goqu.V(participantId)}, goqu.Ex{"type": goqu.V("BILLING")})
		case "residentAddress":
			var result model.Address
			updateValues, err = buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Update("base.address").
				Where(goqu.Ex{"participant_id": goqu.V(participantId)}, goqu.Ex{"type": goqu.V("RESIDENCE")})
		case "contact":
			var result model.ContactInfo
			updateValues, err = buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Update("base.contactdetail").
				Where(goqu.Ex{"participant_id": goqu.V(participantId)})
		case "accountInfo":
			var result model.BankInfo
			updateValues, err = buildRecordMap(&result, fields)
			if err != nil {
				return err
			}

			stmt = pgDialect.Update("base.bankaccount").
				Where(goqu.Ex{"participant_id": goqu.V(participantId)})
		default:
			return model.ErrUpdateParticipant(errors.New(fmt.Sprintf("Can not update structure of %s", name)))
		}

		sql, _, err = stmt.Set(updateValues).ToSQL()

	} else if len(names) == 1 {
		var result model.EegParticipantBase
		fields[names[0]] = value
		if names[0] == "businessRole" && value == "EEG_BUSINESS" {
			fields["lastname"] = ""
			fields["titleBefore"] = ""
			fields["titleAfter"] = ""
		}
		updateValues, err = buildRecordMap(&result, fields)
		if err != nil {
			return err
		}

		sql, _, err = pgDialect.Update("base.participant").Set(updateValues).
			Where(goqu.Ex{"id": goqu.V(participantId)}).ToSQL()
	} else {
		return model.ErrUpdateParticipant(errors.New(fmt.Sprintf("Can not update structure of %s", name)))
	}

	res, err := db.ExecContext(ctx, sql)
	if err == nil {
		if rows, err := res.RowsAffected(); rows == 0 || err != nil {
			err = insertParticipantPartial(ctx, db, participantId, name, value)
			if err != nil {
				return err
			}
		}
		return nil
	} else {
		log.WithContext(ctx).WithError(err).Error("Update partial participant failed")
		return model.ErrUpdateParticipant(err)
	}
}

func insertParticipantPartial(ctx context.Context, db *sqlx.DB, participantId, name string, value interface{}) error {

	var stmt *goqu.InsertDataset
	var sql string
	fields := map[string]interface{}{}

	names := strings.Split(name, ".")
	if len(names) == 2 {
		fields["participant_id"] = participantId
		fields[names[1]] = value

		switch names[0] {
		case "billingAddress":
			var result model.Address
			fields["type"] = "BILLING"
			insertValues, err := buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Insert("base.address").Rows(insertValues)
		case "residentAddress":
			var result model.Address
			fields["type"] = "RESIDENCE"
			insertValues, err := buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Insert("base.address").Rows(insertValues)
		case "contact":
			var result model.ContactInfo
			insertValues, err := buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			stmt = pgDialect.Insert("base.contactdetail").Rows(insertValues)
		case "accountInfo":
			result := struct {
				model.BankInfo
				ParticipantId string `json:"participant_id" db:"participant_id"`
			}{}
			insertValues, err := buildRecordMap(&result, fields)
			if err != nil {
				return err
			}
			// a member without bank account row: the new row needs an IBAN, empty as in bankAccountRecord
			if insertValues["iban"] == nil {
				insertValues["iban"] = ""
			}
			stmt = pgDialect.Insert("base.bankaccount").Rows(insertValues)
		default:
			return model.ErrInsertParticipant(errors.New(fmt.Sprintf("Can not update structure of %s", name)))
		}
		sql, _, _ = stmt.ToSQL()
	} else {
		return model.ErrInsertParticipant(errors.New(fmt.Sprintf("Can not update structure of %s", name)))
	}

	_, err := db.Exec(sql)
	if err != nil {
		log.WithContext(ctx).WithError(err).Error("Insert partial participant failed")
		return model.ErrInsertParticipant(err)
	}
	return nil
}

// buildGetParticipantQuery: partFactFilter restricts the partition factor view, e.g. to the tenant, which the view
// can then apply below its window function (support ticket 6611200).
func buildGetParticipantQuery(partFactFilter ...goqu.Expression) *goqu.SelectDataset {
	stateStmt := pgDialect.From("base.meteringpoint").
		Select(
			goqu.C("activesince"),
			goqu.C("inactivesince"),
			goqu.C("flag"),
			goqu.C("metering_point_id").As("mid"),
			goqu.C("participant_id").As("pid"))

	partFactStmt := pgDialect.From(TABLE_PARTITION_FACT_VIEW).
		Select(
			goqu.C("partFact"),
			goqu.C("metering_point_id").As("mpfmid"),
			goqu.C("participant_id").As("mpfpid")).
		Where(partFactFilter...)

	stmt := pgDialect.From("base.meteringpoint", stateStmt.As("state"), partFactStmt.As("mpfpF1")).Select(&model.MeteringPoint{}).
		Where(
			goqu.C("mid").Eq(goqu.C("metering_point_id")),
			goqu.C("pid").Eq(goqu.C("participant_id")),
			goqu.C("mpfmid").Eq(goqu.C("metering_point_id")),
			goqu.C("mpfpid").Eq(goqu.C("participant_id")),
		)

	return stmt
}

// completeParticipantsQuery: the metering points of all members of a community.
func completeParticipantsQuery(tenant string) *goqu.SelectDataset {
	return buildGetParticipantQuery(goqu.C("tenant").Eq(tenant)).
		Where(goqu.C("tenant").Table("meteringpoint").Schema("base").Eq(tenant))
}

func completeParticipants(ctx context.Context, db *sqlx.DB, tenant string, participants []*model.EegParticipant) error {
	stmt, _, err := completeParticipantsQuery(tenant).ToSQL()

	if err != nil {
		return model.ErrCompleteParticipant(err)
	}

	meteringPoints := []model.MeteringPoint{}

	err = db.SelectContext(ctx, &meteringPoints, stmt)
	if err != nil && !errors.Is(err, dbsql.ErrNoRows) {
		return model.ErrCompleteParticipant(err)
	}

	meteringPointsMap := make(map[string][]*model.MeteringPoint)
	for i, meteringPoint := range meteringPoints {
		meteringPointsMap[meteringPoint.ParticipantId] = append(meteringPointsMap[meteringPoint.ParticipantId], &meteringPoints[i])
	}

	for i, participant := range participants {
		m, ok := meteringPointsMap[participant.Id.String()]
		if !ok {
			participants[i].MeteringPoint = []*model.MeteringPoint{}
		} else {
			participants[i].MeteringPoint = m
		}
	}
	return nil
}

func completeParticipant(ctx context.Context, db *sqlx.DB, participant *model.EegParticipant) error {
	participantId := participant.Id.String()
	stmt, _, err := buildGetParticipantQuery().Where(
		goqu.C("participant_id").Table("meteringpoint").Schema("base").Eq(participantId)).ToSQL()
	if err != nil {
		return model.ErrCompleteParticipant(err)
	}

	err = db.SelectContext(ctx, &participant.MeteringPoint, stmt)
	if err != nil && !errors.Is(err, dbsql.ErrNoRows) {
		return model.ErrCompleteParticipant(err)
	}

	if participant.MeteringPoint == nil {
		log.WithContext(ctx).Debugf("Participant (%+v) with zero Meteringpoints", participant.Id.String())
		participant.MeteringPoint = make([]*model.MeteringPoint, 0)
	}
	return nil
}

func findParticipantByMeteringPoint(ctx context.Context, db *sqlx.DB, tenant, meteringPoint string) (*model.EegParticipant, error) {

	participant := model.EegParticipant{}

	participantIdStmt := pgDialect.From("base.meteringpoint").Select("participant_id").
		Where(
			goqu.C("metering_point_id").Eq(meteringPoint),
			goqu.C("tenant").Eq(tenant),
			goqu.C("flag").Eq(model.F_ASSIGNED),
		)

	stmt, _, err := buildParticipantQueryStmt().Where(goqu.C("id").Eq(participantIdStmt)).ToSQL()
	if err != nil {
		log.WithContext(ctx).WithField("SQL", "SELECT").WithError(err).Info("SQL statement failed")
		return nil, err
	}

	err = db.GetContext(ctx, &participant, stmt)
	if err != nil {
		log.WithContext(ctx).WithError(err).WithField("SQL", "SELECT").Info("SQL statement failed")
		return nil, err
	}
	err = completeParticipant(ctx, db, &participant)
	if err != nil {
		return nil, err
	}
	return &participant, nil
}
