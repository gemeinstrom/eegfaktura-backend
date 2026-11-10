package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"at.ourproject/vfeeg-backend/gridoperator"
	"at.ourproject/vfeeg-backend/model"
	"github.com/doug-martin/goqu/v9"
	"github.com/jjeffery/civil"
	log "github.com/sirupsen/logrus"
	"github.com/xuri/excelize/v2"
	"gopkg.in/guregu/null.v4"
)

type ExcelRepository interface {
	ImportMasterdataFromExcel(ctx context.Context, r io.Reader, filename, sheet, tenant string) error
}

// Unzip limits for uploaded workbooks (excelize allows 16 GB unzipped by default).
// The limit is the sum of all parts, including images and styles. Uploads are capped at
// 15 MB by the ingress; the shipped test workbooks and typical templates unzip to a few MB,
// so 128 MiB leaves headroom. Worksheets above 16 MiB (the excelize default) are unzipped
// to a temp file instead of memory.
const (
	excelUnzipSizeLimit    = 128 << 20
	excelUnzipXMLSizeLimit = 16 << 20
)

// openReader opens an uploaded workbook with its own temp directory. The returned cleanup
// closes the file and removes the temp directory; on error or panic nothing is left behind.
func openReader(r io.Reader, filename string) (*excelize.File, func(), error) {
	tmpDir, err := os.MkdirTemp("", "excel-import-")
	if err != nil {
		return nil, nil, err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	f, err := excelize.OpenReader(r, excelize.Options{
		UnzipSizeLimit:    excelUnzipSizeLimit,
		UnzipXMLSizeLimit: excelUnzipXMLSizeLimit,
		TmpDir:            tmpDir,
	})
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, nil, err
	}
	f.Path = filename
	handedOver = true
	return f, func() {
		_ = f.Close()
		_ = os.RemoveAll(tmpDir)
	}, nil
}

func (db *sqlDatabase) ImportMasterdataFromExcel(ctx context.Context, r io.Reader, filename, sheet, tenant string) error {
	f, cleanup, err := openReader(r, filename)
	if err != nil {
		return err
	}
	defer cleanup()

	rows, err := f.Rows(sheet)
	if err != nil {
		log.WithContext(ctx).Error(err)
		var missing excelize.ErrSheetNotExist
		if errors.As(err, &missing) {
			return model.ErrImportSheetMissing(fmt.Errorf(model.ImportSheetMissingText, missing.SheetName))
		}
		return err
	}
	defer rows.Close()

	gridOperators, err := db.GetGridOperators(ctx)
	if err != nil {
		return err
	}

	gridOperatorName := func(id string) string {
		name, ok := gridOperators[id]
		if ok {
			return name
		}
		return ""
	}

	eeg, err := db.GetEegById(ctx, tenant)
	if err != nil {
		return err
	}

	importLog := &model.Log{Operation: "Excel Master Data Import", Messages: []*model.LogMessage{}}
	participants := transformExcelData(ctx, rows, gridOperatorName, eeg.Online, eeg.CommunityId, importLog)
	log.WithContext(ctx).Debugf("LEN _ Import participants: %v", len(participants))

	db.reportDuplicateParticipantNumbers(ctx, strings.ToUpper(tenant), participants, importLog)

	defaultDebit := 0
	for _, p := range participants {
		// Jeder Teilnehmer läuft in seiner eigenen Transaktion — ein Fehler wird
		// protokolliert, die restlichen Zeilen werden trotzdem importiert.
		if err := db.ImportParticipant(ctx, strings.ToUpper(tenant), "excel", p); err != nil {
			importLog.Messages = append(importLog.Messages, model.NewLogMessageFromVfeegError(
				fmt.Sprintf("%s %s", p.FirstName, p.LastName),
				err,
			))
			log.WithContext(ctx).Errorf("Error Import Participant from Excel: %s", err.Error())
			continue
		}
		// only a member the import created gets an id; for an existing one it only adds metering points
		if len(p.Id) > 0 && missesDebitType(p) {
			defaultDebit++
		}
	}
	if m := defaultDebitTypeMessage(defaultDebit); m != nil {
		importLog.Messages = append(importLog.Messages, m)
	}

	if len(importLog.Messages) > 0 {
		err = db.SaveNotificationFromMap(CreateNotificationMessageFromLog(importLog), tenant,
			model.N_TYPE_NOTIFICATION, model.N_PROCESS_IMPORT_EXCEL, "ADMIN")
		if err != nil {
			log.WithContext(ctx).Error(err)
		}
	}

	return err
}

// reportDuplicateParticipantNumbers warnt (ohne die Zeilen abzulehnen), wenn eine
// MitgliedsNr in der Datei mehrfach vorkommt oder im Bestand bereits an ein ANDERES
// Mitglied vergeben ist. Re-Import-Zeilen eines bestehenden Mitglieds (gleicher
// Name) lösen keine Warnung aus.
func (db *sqlDatabase) reportDuplicateParticipantNumbers(ctx context.Context, tenant string, participants []*model.EegParticipant, importLog *model.Log) {
	byNumber := map[string][]*model.EegParticipant{}
	for _, p := range participants {
		if nr := p.ParticipantNumber.String; nr != "" {
			byNumber[nr] = append(byNumber[nr], p)
		}
	}
	if len(byNumber) == 0 {
		return
	}

	for nr, ps := range byNumber {
		if len(ps) > 1 {
			names := make([]string, len(ps))
			for i, p := range ps {
				names[i] = fmt.Sprintf("%s %s", p.FirstName, p.LastName)
			}
			importLog.Messages = append(importLog.Messages, model.NewLogMessage(
				"WARNING",
				nr,
				"W_PARTICIPANT_NR_DUP",
				fmt.Sprintf("MitgliedsNr %s is used by several members in the file: %s", nr, strings.Join(names, ", ")),
			))
		}
	}

	type existingParticipant struct {
		Number    string `db:"participantNumber"`
		FirstName string `db:"firstname"`
		LastName  string `db:"lastname"`
	}
	stmt, _, err := pgDialect.From("base.participant").
		Select("participantNumber", "firstname", "lastname").
		Where(
			goqu.C("tenant").Eq(tenant),
			goqu.C("participantNumber").IsNotNull(),
			goqu.C("participantNumber").Neq("")).ToSQL()
	if err != nil {
		log.WithContext(ctx).WithError(err).Warn("duplicate participant number check skipped")
		return
	}
	var existing []existingParticipant
	if err := db.db.SelectContext(ctx, &existing, stmt); err != nil {
		log.WithContext(ctx).WithError(err).Warn("duplicate participant number check skipped")
		return
	}
	for _, e := range existing {
		for _, p := range byNumber[e.Number] {
			if p.FirstName != e.FirstName || p.LastName != e.LastName {
				importLog.Messages = append(importLog.Messages, model.NewLogMessage(
					"WARNING",
					e.Number,
					"W_PARTICIPANT_NR_DUP",
					fmt.Sprintf("MitgliedsNr %s is already assigned to existing member %s %s", e.Number, e.FirstName, e.LastName),
				))
			}
		}
	}
}

func CreateNotificationMessageFromLog(logMsg *model.Log) map[string]interface{} {
	return map[string]interface{}{
		"type":     logMsg.Operation,
		"messages": logMsg.Messages,
	}
}

// ExportMasterdataToExcel writes the master data workbook. joiningFees maps an EEG tariff id to its joining fee
// (> 0 only); feeStates is the optional list web fetched from billing (nil: column "Status" stays empty).
func ExportMasterdataToExcel(ctx context.Context, participants []*model.EegParticipant, eeg *model.Eeg, tariffMap map[string]string,
	joiningFees map[string]float64, feeStates []model.JoiningFeeState) (*bytes.Buffer, error) {
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			log.WithContext(ctx).WithField("tenant", eeg.Id).WithError(err).Error("Error while closing file")
		}
	}()

	err := generateEegMastersheet(f, eeg)
	if err != nil {
		return nil, err
	}
	err = generateParticipantMastersheet(ctx, f, byMemberNumber(participants), tariffMap, joiningFees, feeStates)
	if err != nil {
		return nil, err
	}

	_ = f.DeleteSheet("Sheet1")
	return f.WriteToBuffer()
}

// byMemberNumber orders the members of the export by member number (08.11.2026): numbers numerically ("2" before
// "10"), then numbers with other characters alphabetically, members without a number last; equal numbers keep their
// order. A copy: the caller's slice stays as it is.
func byMemberNumber(participants []*model.EegParticipant) []*model.EegParticipant {
	sorted := append([]*model.EegParticipant(nil), participants...)
	rank := func(p *model.EegParticipant) (int, int64, string) {
		nr := strings.TrimSpace(p.ParticipantNumber.String)
		if nr == "" {
			return 2, 0, ""
		}
		if n, err := strconv.ParseInt(nr, 10, 64); err == nil {
			return 0, n, nr
		}
		return 1, 0, strings.ToLower(nr)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		gi, ni, si := rank(sorted[i])
		gj, nj, sj := rank(sorted[j])
		if gi != gj {
			return gi < gj
		}
		if ni != nj {
			return ni < nj
		}
		return si < sj
	})
	return sorted
}

func generateEegMastersheet(f *excelize.File, eeg *model.Eeg) error {

	styleId, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}})
	styleIdHeader, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10.0},
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	styleIdHeaderTop, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 11.0},
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
		Fill: excelize.Fill{
			Type:    "pattern",
			Pattern: 1,
			Color:   []string{"#cccccc"},
			Shading: 0,
		},
	})

	line := 1
	sheet := eeg.RcNumber
	_, err = f.NewSheet(sheet)
	if err != nil {
		return err
	}

	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{"EEG"})
	_ = f.SetRowStyle(sheet, 1, 1, styleIdHeaderTop)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		"Kurzname", "Bezeichnung", "Gemeinschafts-ID", "Ponton",
	})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeader)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		eeg.Name, eeg.Description, eeg.CommunityId, eeg.Online,
	})
	_ = f.SetRowStyle(sheet, line, line, styleId)

	line += 2
	// Net Operator
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{"Netz"})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeaderTop)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		"Netzbetreiber", "Netzbetreiber Name", "Verteilung",
	})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeader)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		eeg.GridOperator, eeg.OperatorName, eeg.AllocationMode,
	})
	_ = f.SetRowStyle(sheet, line, line, styleId)

	line += 2
	// Contact
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{"Kontakt"})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeaderTop)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		"Kontaktperson", "E-Mail", "TelefonNr.", "PLZ", "Wohnort", "Straße", "StraßenNr.", "Web Seite",
	})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeader)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		eeg.ContactPerson.String, eeg.Email.String, eeg.Phone.String, eeg.Zip, eeg.City, eeg.Street, eeg.StreetNumber, eeg.Website.String,
	})
	_ = f.SetRowStyle(sheet, line, line, styleId)

	line += 2
	// Bank Account
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{"Bankdaten"})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeaderTop)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		"Kontoinhaber", "IBAN",
	})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeader)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		eeg.Owner.String, eeg.Iban.String,
	})
	_ = f.SetRowStyle(sheet, line, line, styleId)

	line += 2
	// Business
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{"Geschäftliches"})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeaderTop)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		"Rechtsform", "Geschäftsnummer", "Verrechnungsinterval", "Ust.", "SteuerNr.",
	})
	_ = f.SetRowStyle(sheet, line, line, styleIdHeader)
	line += 1
	_ = f.SetSheetRow(sheet, fmt.Sprintf("A%d", line), &[]interface{}{
		eeg.Legal, eeg.BusinessNr.String, eeg.SettlementInterval, eeg.VatNumber.String, eeg.TaxNumber.String,
	})
	_ = f.SetRowStyle(sheet, line, line, styleId)

	_ = f.SetColWidth(sheet, "A", "B", 25.0)
	_ = f.SetColWidth(sheet, "C", "C", 35.0)
	_ = f.SetColWidth(sheet, "D", "H", 20.0)

	return nil
}

// labels of the derived member status in the master data export (decision 20.10.2026, platform#165)
var memberActivityLabel = map[model.MemberActivityType]string{
	model.MemberActive:   "aktiv",
	model.MemberPending:  "in Anmeldung",
	model.MemberInactive: "inaktiv",
}

// germanDate shows "YYYY-MM-DD" as "TT.MM.JJJJ"; any other text as sent.
func germanDate(s string) string {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("02.01.2006")
	}
	return s
}

// joiningFeeStatus is the text of the column "Beitrittsgebühr Status" (konzept-beitrittsgebuehr.md, Erweiterung
// 27.10.2026, same texts and order as the member details in eegfaktura-web): a final document with the fee, then a
// payment outside eegfaktura, then the tariff (no fee: nothing will be charged, also not a request), then a request,
// then whether the member was billed with energy. Without a fee the cell stays empty (web: „kein Tarif mit
// Beitrittsgebühr“), so a community without joining fees gets no column full of hints. s is the member's entry of
// billing's list (nil: no entry, i.e. no final document yet), fee the joining fee of the member's EEG tariff.
func joiningFeeStatus(s *model.JoiningFeeState, fee float64) string {
	if s == nil {
		s = &model.JoiningFeeState{}
	}
	switch {
	case s.JoiningFee != nil:
		return fmt.Sprintf("verrechnet mit %s vom %s", s.JoiningFee.DocumentNumber, germanDate(s.JoiningFee.DocumentDate))
	case s.PaidExternally != nil:
		text := "außerhalb bezahlt am " + germanDate(s.PaidExternally.PaidOn)
		if s.PaidExternally.Note != "" {
			text += " (" + s.PaidExternally.Note + ")"
		}
		return text
	case fee <= 0:
		return ""
	case s.Requested:
		return "zur Verrechnung im nächsten Abrechnungslauf vorgemerkt"
	case !s.EnergyBilled:
		return "offen – wird mit der ersten Energieabrechnung verrechnet"
	default:
		return "nicht verrechnet – Mitglied wurde schon abgerechnet"
	}
}

func generateParticipantMastersheet(ctx context.Context, f *excelize.File, participants []*model.EegParticipant, tariffMap map[string]string,
	joiningFees map[string]float64, feeStates []model.JoiningFeeState) error {

	getTariffName := func(id string) string {
		name, ok := tariffMap[id]
		if !ok {
			return ""
		}
		return name
	}

	// Only the tenant's own members are looked up below, so entries for other participant ids are ignored.
	feeState := map[string]*model.JoiningFeeState{}
	for i := range feeStates {
		feeState[strings.ToLower(feeStates[i].ParticipantId)] = &feeStates[i]
	}
	getJoiningFee := func(tariffId string) interface{} {
		if fee, ok := joiningFees[tariffId]; ok && fee > 0 {
			return fee
		}
		return ""
	}
	// without a list from billing (older web, billing not reachable) the column stays empty
	getJoiningFeeStatus := func(c *model.EegParticipant) string {
		if feeStates == nil {
			return ""
		}
		return joiningFeeStatus(feeState[strings.ToLower(c.Id.String())], joiningFees[c.TariffId.String])
	}

	getNullDate := func(d civil.NullDate) string {
		if !d.Valid {
			return ""
		}
		return d.Date.String()
	}

	styleId, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}})
	styleDateId, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}, NumFmt: 14})
	styleIdHeader, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10.0},
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	styleIdDate, err := f.NewStyle(&excelize.Style{
		Font:   &excelize.Font{Size: 10.0},
		NumFmt: 14,
	})
	styleIdAmount, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}, NumFmt: 2})

	sheet := "Mitglieder"
	_, err = f.NewSheet(sheet)
	if err != nil {
		return err
	}

	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}

	err = sw.SetColWidth(1, 1, 5.0)
	err = sw.SetColWidth(2, 3, 30.0)
	colNr, _ := excelize.ColumnNameToNumber("F")
	err = sw.SetColWidth(colNr, colNr, 12.0)
	err = sw.SetColWidth(colNr+1, colNr+1, 25.0)
	err = sw.SetColWidth(colNr+2, colNr+7, 20.0)
	colNr, _ = excelize.ColumnNameToNumber("O")
	err = sw.SetColWidth(colNr, colNr, 20.0)
	err = sw.SetColWidth(colNr+1, colNr+1, 12.0)
	colNr, _ = excelize.ColumnNameToNumber("R")
	err = sw.SetColWidth(colNr, colNr+1, 20.0)
	colNr, _ = excelize.ColumnNameToNumber("Y")
	err = sw.SetColWidth(colNr, colNr, 32.0)
	err = sw.SetColWidth(colNr+1, colNr+1, 14.0) // Beitrittsgebühr (€)
	err = sw.SetColWidth(colNr+2, colNr+2, 45.0) // Beitrittsgebühr Status
	colNr, _ = excelize.ColumnNameToNumber("AB")
	err = sw.SetColWidth(colNr, colNr, 32.0)
	err = sw.SetColWidth(colNr+2, colNr+2, 8.0)
	err = sw.SetColWidth(colNr+3, colNr+3, 20.0)
	err = sw.SetColWidth(colNr+5, colNr+5, 18.0)
	colNr, _ = excelize.ColumnNameToNumber("AK")
	err = sw.SetColWidth(colNr, colNr+1, 20.0)
	err = sw.SetColWidth(colNr+3, colNr+4, 12.0)
	err = sw.SetColWidth(colNr+5, colNr+5, 30.0)

	line := 1
	header := []interface{}{
		excelize.Cell{Value: "Mit. Nr."},
		excelize.Cell{Value: "Name 1"},
		excelize.Cell{Value: "Name 2"},
		excelize.Cell{Value: "Titel"},
		excelize.Cell{Value: "Status"},
		excelize.Cell{Value: "Mitglied seit."},
		excelize.Cell{Value: "E-Mail"},
		excelize.Cell{Value: "Telefonnummer"},
		excelize.Cell{Value: "Ust."},
		excelize.Cell{Value: "IBAN."},
		excelize.Cell{Value: "Kontoinhaber"},
		excelize.Cell{Value: "Bankname"},
		excelize.Cell{Value: "DebitType"},
		excelize.Cell{Value: "Mandat-Ref."},
		excelize.Cell{Value: "Mandat-Dat."},
		excelize.Cell{Value: "PLZ"},
		excelize.Cell{Value: "Ort"},
		excelize.Cell{Value: "Straße"},
		excelize.Cell{Value: "HausNr."},
		excelize.Cell{Value: ""},
		excelize.Cell{Value: "EEG-Role"},
		excelize.Cell{Value: "teilnahme als"},
		excelize.Cell{Value: "Status"},
		excelize.Cell{Value: "Mitgliedstarif"},
		excelize.Cell{Value: "Beitrittsgebühr (€)"},
		excelize.Cell{Value: "Beitrittsgebühr Status"},
		excelize.Cell{Value: "Zählpunkt"},
		excelize.Cell{Value: "ZP-Status"},
		excelize.Cell{Value: "ZpNr."},
		excelize.Cell{Value: "Zählpunktname"},
		excelize.Cell{Value: "registriert"},
		excelize.Cell{Value: "Bezugsrichtung"},
		excelize.Cell{Value: "Teilnahme Fkt."},
		excelize.Cell{Value: "WechselrichterNr."},
		excelize.Cell{Value: "PLZ"},
		excelize.Cell{Value: "Ort"},
		excelize.Cell{Value: "Straße"},
		excelize.Cell{Value: "HausNr."},
		excelize.Cell{Value: "aktiviert"},
		excelize.Cell{Value: "deaktiviert"},
		excelize.Cell{Value: "Zp. Tarifname"},
		excelize.Cell{Value: "Umspannwerk"},
	}
	err = sw.SetRow(fmt.Sprintf("A%d", line), header, excelize.RowOpts{StyleID: styleIdHeader, Height: 0.42 * 72})
	for _, c := range participants {
		// platform#165: the member status from its metering points, the stored one stays ACTIVE after leaving
		status := memberActivityLabel[model.MemberActivity(c.MeteringPoint)]
		feeStatus := getJoiningFeeStatus(c)
		for _, m := range c.MeteringPoint {
			line = line + 1
			err = sw.SetRow(fmt.Sprintf("A%d", line),
				[]interface{}{
					excelize.Cell{Value: c.ParticipantNumber.String},
					excelize.Cell{Value: c.FirstName},
					excelize.Cell{Value: c.LastName},
					excelize.Cell{Value: func() string {
						titles := []string{}
						if c.TitleBefore.Valid && c.TitleBefore.String != "" {
							titles = append(titles, c.TitleBefore.String)
						}
						if c.TitleAfter.Valid && c.TitleAfter.String != "" {
							titles = append(titles, c.TitleAfter.String)
						}
						if len(titles) == 0 {
							return ""
						}
						if len(titles) == 1 {
							return titles[0]
						}
						return strings.Join(titles, ", ")
					}()},
					excelize.Cell{Value: status},
					excelize.Cell{Value: getNullDate(c.ParticipantSince), StyleID: styleIdDate},
					excelize.Cell{Value: c.Contact.Email.String},
					excelize.Cell{Value: c.Contact.Phone.String},
					excelize.Cell{Value: c.VatNumber.String},
					excelize.Cell{Value: c.BankAccount.Iban.String},
					excelize.Cell{Value: c.BankAccount.Owner.String},
					excelize.Cell{Value: c.BankAccount.BankName.String},
					excelize.Cell{Value: c.BankAccount.SepaDirectDebit.String},
					excelize.Cell{Value: c.BankAccount.MandateReference.String},
					excelize.Cell{Value: c.BankAccount.MandateDate.Date, StyleID: styleDateId},
					excelize.Cell{Value: c.BillingAddress.Zip.String},
					excelize.Cell{Value: c.BillingAddress.City.String},
					excelize.Cell{Value: c.BillingAddress.Street.String},
					excelize.Cell{Value: c.BillingAddress.StreetNumber.String},
					excelize.Cell{Value: c.CompanyRegisterNumber.String},
					excelize.Cell{Value: c.Role},
					excelize.Cell{Value: func() string {
						if c.BusinessRole == "EEG_PRIVATE" {
							return "Privat"
						} else {
							return "Business"
						}
					}()},
					excelize.Cell{Value: status},
					excelize.Cell{Value: getTariffName(c.TariffId.String), StyleID: styleDateId},
					excelize.Cell{Value: getJoiningFee(c.TariffId.String), StyleID: styleIdAmount},
					excelize.Cell{Value: feeStatus},
					excelize.Cell{Value: m.MeteringPoint},
					excelize.Cell{Value: m.ProcessState},
					excelize.Cell{Value: m.EquipmentNumber.String},
					excelize.Cell{Value: m.EquipmentName.String},
					excelize.Cell{Value: m.RegisteredSince, StyleID: styleDateId},
					excelize.Cell{Value: m.Direction},
					excelize.Cell{Value: fmt.Sprintf("%d %%", m.PartFact)},
					excelize.Cell{Value: m.InverterId.String},
					excelize.Cell{Value: m.Zip.String},
					excelize.Cell{Value: m.City.String},
					excelize.Cell{Value: m.Street.String},
					excelize.Cell{Value: m.StreetNumber.String},
					excelize.Cell{Value: getNullDate(m.State.ActiveSince), StyleID: styleDateId},
					excelize.Cell{Value: getNullDate(m.State.InactiveSince), StyleID: styleDateId},
					excelize.Cell{Value: getTariffName(m.TariffId.String), StyleID: styleDateId},
					excelize.Cell{Value: m.Transformer.String},
				}, excelize.RowOpts{StyleID: styleId})
		}
	}

	setAutoFilter(ctx, f, sheet, len(header), line)
	err = sw.Flush()
	return err
}

// setAutoFilter sets the filter over every header column and all rows (both exports had a fixed A1:AH10: the
// master data columns from AI on and the rows after 10 had no filter). A failure only costs the filter buttons.
func setAutoFilter(ctx context.Context, f *excelize.File, sheet string, columns, lastRow int) {
	lastCell, err := excelize.CoordinatesToCellName(columns, lastRow)
	if err == nil {
		err = f.AutoFilter(sheet, "A1:"+lastCell, nil)
	}
	if err != nil {
		log.WithContext(ctx).WithError(err).Warnf("excel export: no auto filter on sheet %s", sheet)
	}
}

// findParticipant sammelt die Zeilen eines Mitglieds ein (eine Zeile je Zählpunkt).
// Der Abgleich läuft bewusst NUR über Vor-+Nachname: Bestandsdateien vergeben die
// MitgliedsNr teils fortlaufend pro Zeile (nicht pro Mitglied), sie taugt daher
// nicht als Schlüssel. Bekannte Limitation: namensgleiche Personen verschmelzen.
func findParticipant(participants []*model.EegParticipant, firstname, lastname string) (*model.EegParticipant, bool) {
	for _, p := range participants {
		if p.FirstName == firstname && p.LastName == lastname {
			return p, true
		}
	}
	return nil, false
}

func getColumValue(cols []string, values map[string]int, deName, enName string, defaultValue *string) string {
	idx := -1
	if _, ok := values[strings.ToLower(deName)]; ok {
		idx = values[strings.ToLower(deName)]
	} else if _, ok := values[strings.ToLower(enName)]; ok {
		idx = values[strings.ToLower(enName)]
	}

	if idx < 0 {
		if defaultValue != nil {
			return *defaultValue
		}
		return ""
	}
	if idx >= len(cols) {
		if defaultValue != nil {
			return *defaultValue
		}
		return ""
	}
	return cols[idx]
}

var numberPattern = regexp.MustCompile(`^[0-9\\.,]+$`)
var dateStringPattern = regexp.MustCompile(`^\d{1,2}\.\d{1,2}\.\d{4}$`)

func isDate(cell string) bool {
	if len(cell) > 0 && numberPattern.MatchString(cell) {
		return true
	}
	return false
}

func isDateString(cell string) bool {
	if len(cell) > 0 && dateStringPattern.MatchString(cell) {
		return true
	}
	return false
}

func parseExcelDate(cell string) time.Time {
	if isDateString(cell) {
		var d, m, y int
		if _, err := fmt.Sscanf(cell, "%d.%d.%d", &d, &m, &y); err != nil {
			return time.Now()
		}
		return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	} else if isDate(cell) {
		var excelEpoch = time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)
		var days, _ = strconv.ParseFloat(cell, 64)
		return excelEpoch.Add(time.Second * time.Duration(days*86400))
	}
	return time.Now()
}

// missesDebitType: the member has an IBAN but no direct debit type (column "Einzugsart" missing,
// empty or with an unknown value). Without a type the member is not debited (platform#143).
func missesDebitType(p *model.EegParticipant) bool {
	return strings.TrimSpace(p.BankAccount.Iban.String) != "" && !p.BankAccount.SepaDirectDebit.Valid
}

// defaultDebitTypeMessage is the one import message about members created without a direct debit type
// (platform#143); nil when there are none.
func defaultDebitTypeMessage(n int) *model.LogMessage {
	if n == 0 {
		return nil
	}
	return model.NewLogMessage(
		"WARNING",
		"Einzugsart",
		"W_DEBIT_TYPE_DEFAULT",
		fmt.Sprintf("%d new member(s) with an IBAN have no direct debit type (column 'Einzugsart' missing, empty or unknown): they are not debited until CORE, B2B or 'Kein SEPA' is set for them.", n))
}

// importDebitType reads the optional column "Einzugsart" ("DebitType" works too): CORE, B2B, or
// NONE / "Kein SEPA". Case, blanks and hyphens are ignored ("kein-sepa", "Kein  SEPA"). Missing or
// empty stays empty: such a member is not debited (platform#143). Any other value is reported and left
// empty. The value only applies to members the import creates; for an existing member the import
// adds metering points and leaves the master data as it is.
func importDebitType(raw, firstname, lastname string, importLog *model.Log) null.String {
	v := strings.ToUpper(strings.ReplaceAll(strings.Join(strings.Fields(raw), ""), "-", ""))
	switch v {
	case "":
		return null.String{}
	case "NONE", "KEINSEPA":
		return null.StringFrom("NONE")
	case "CORE", "B2B":
		return null.StringFrom(v)
	}
	importLog.Messages = append(importLog.Messages, model.NewLogMessage(
		"WARNING",
		fmt.Sprintf("%s %s", firstname, lastname),
		"W_DEBIT_TYPE_UNKNOWN",
		fmt.Sprintf("unknown direct debit type %q (allowed: CORE, B2B, NONE, Kein SEPA); a new member gets none and is not debited", raw)))
	return null.String{}
}

// importEmail normalizes an imported e-mail (trim per ';'-part) and
// validates it against the shared address rule. Invalid addresses are
// not taken over silently: the member is imported without e-mail and
// the row is reported in the import log (admin notification), so the
// tenant admin can correct it.
func importEmail(raw, firstname, lastname string, importLog *model.Log) null.String {
	normalized, err := model.ValidateEmailList(raw)
	if err != nil {
		importLog.Messages = append(importLog.Messages, model.NewLogMessageFromVfeegError(
			fmt.Sprintf("%s %s", firstname, lastname), err))
		return null.String{}
	}
	if normalized == "" {
		return null.String{}
	}
	return null.StringFrom(normalized)
}

func transformExcelData(ctx context.Context, rows *excelize.Rows, gridOperatorName func(id string) string, online bool, communityId string, importLog *model.Log) []*model.EegParticipant {
	colMap := map[string]int{}
	participants := []*model.EegParticipant{}
	defaultPartFact := "100"
	// je falscher (bzw. fehlender) Gemeinschafts-ID nur EINE Meldung —
	// sonst N Meldungen bei komplett falscher Datei
	rejectedCommunityIds := map[string]bool{}

	businessRole := func(cols []string, values map[string]int) string {
		val := getColumValue(cols, colMap, "BusinessRole", "BusinessRole", nil)
		if strings.ToLower(val) == "business" {
			return "EEG_BUSINESS"
		}
		return "EEG_PRIVATE"
	}

	equipmentName := func(cols []string, values map[string]int) null.String {
		val := getColumValue(cols, colMap, "ObjektName", "ObjectName", nil)
		if len(val) > 0 {
			return null.StringFrom(val)
		}
		return null.String{}
	}

	equipmentNumber := func(cols []string, values map[string]int) null.String {
		val := getColumValue(cols, colMap, "EquipmentNr", "EquipmentNr", nil)
		if len(val) > 0 {
			return null.StringFrom(val)
		}
		return null.String{}
	}

	partFact := func(cols []string, values map[string]int) int {
		// Vorlagen-Spalte heißt "Zugeteilte Menge in Prozent"; ältere Dateien
		// verwenden "Teilnehmerfaktor"/"PartFact".
		val := getColumValue(cols, colMap, "Zugeteilte Menge in Prozent", "Allocated Quantity in Percent", nil)
		if len(val) == 0 {
			val = getColumValue(cols, colMap, "Teilnehmerfaktor", "PartFact", &defaultPartFact)
		}
		val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), "%"))
		f, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", "."), 64)
		if err != nil || f <= 0 {
			return 100
		}
		// Prozent-formatierte Zellen liefern im Raw-Modus den Bruchwert (50 % -> "0.5").
		if f < 1 {
			f = f * 100
		}
		return int(math.Round(f))
	}

	getCivilDatePtr := func(date civil.Date) *civil.Date {
		return &date
	}

	getColumDate := func(cols []string, values map[string]int, deName, enName string, defaultValue *civil.Date) civil.NullDate {
		// Zellen werden mit RawCellValue gelesen: eine als Datum formatierte Zelle
		// liefert die Excel-Serialzahl, nicht "t.m.jjjj" — parseExcelDate kann beides.
		v := getColumValue(cols, colMap, deName, enName, nil)
		if isDateString(v) || isDate(v) {
			return civil.NullDate{
				Date:  civil.DateOf(parseExcelDate(v)),
				Valid: true,
			}
		}
		if defaultValue != nil {
			return civil.NullDate{
				Date:  *defaultValue,
				Valid: true,
			}
		}
		return civil.NullDate{}
	}

	getParticipantStatus := func(state string) model.ProcessStatusType {
		if state == "NEW" {
			return model.NEW
		}
		return model.ACTIVE
	}

	getMeteringPointProcessState := func(state string) model.ProcessStatusType {
		if state == "NEW" {
			return model.NEW
		}
		return model.ACTIVE
	}

	getMeteringPointStatus := func(state string) model.StatusType {
		if state == "NEW" {
			return model.S_INIT
		}
		return model.S_ACTIVE
	}

	for rows.Next() {
		if cols, err := rows.Columns(excelize.Options{RawCellValue: true}); err == nil && len(cols) > 0 {
			switch {
			case cols[0] == "[### Leerzeile für Importer ###]":
				continue
			case isImportHeaderRow(cols):
				for i, c := range cols {
					colMap[strings.ToLower(c)] = i
				}
				continue
			default:
				switch {
				// Datenzeile = Zählpunkt oder Name vorhanden. Die Spalte "Netzbetreiber" (A) ist
				// optional und wird nur noch verglichen (platform#107).
				case isImportDataRow(cols, colMap):
					givenGridOperator := strings.ToUpper(strings.TrimSpace(getColumValue(cols, colMap, "Netzbetreiber", "Grid Operator", nil)))

					// "Gemeinschafts-ID" ist Pflicht und muss zur Ziel-EEG passen — schützt
					// davor, die Datei einer anderen EEG (oder im falschen Tenant
					// eingeloggt) kommentarlos zu importieren.
					rowCommunityId := strings.TrimSpace(getColumValue(cols, colMap, "Gemeinschafts-ID", "Community Id", nil))
					if communityId != "" && !strings.EqualFold(rowCommunityId, communityId) {
						if !rejectedCommunityIds[rowCommunityId] {
							rejectedCommunityIds[rowCommunityId] = true
							msg := fmt.Sprintf("Rows skipped: 'Gemeinschafts-ID' %s does not match this community (%s) — wrong file or wrong community selected?", rowCommunityId, communityId)
							if rowCommunityId == "" {
								msg = fmt.Sprintf("Rows skipped: 'Gemeinschafts-ID' is empty — the column is required and must match this community (%s)", communityId)
							}
							importLog.Messages = append(importLog.Messages, model.NewLogMessage(
								"ERROR",
								rowCommunityId,
								"E_COMMUNITY_1000",
								msg,
							))
							log.WithContext(ctx).Warnf("Import rows skipped: community id does not match target %q", communityId)
						}
						continue
					}

					var firstname string
					var lastname string

					excelName1 := getColumValue(cols, colMap, "Name 2", "Name2", nil)
					excelName2 := getColumValue(cols, colMap, "Name 1", "Name1", nil)

					if len(excelName2) == 0 || len(excelName2) < 2 {
						if _, err := fmt.Sscanf(getColumValue(cols, colMap, "Name 2", "Name2", nil), "%s %s", &lastname, &firstname); err != nil {
							importLog.Messages = append(importLog.Messages, model.NewLogMessage(
								"ERROR",
								strings.Trim(getColumValue(cols, colMap, "Zählpunkt", "MeteringPoint Id", nil), " "),
								"E_PARTICIPANT_1001",
								fmt.Sprintf("Row skipped: 'Name 1' is missing and 'Name 2' (%s) cannot be split into last and first name", excelName1),
							))
							log.WithContext(ctx).Warn("Import row skipped: cannot extract name")
							continue
						}
					} else {
						firstname = excelName2
						lastname = excelName1
					}

					directionVal := strings.ToUpper(strings.TrimSpace(getColumValue(cols, colMap, "Energierichtung", "Energy Direction", nil)))
					role := model.CONSUMPTION
					switch directionVal {
					case "GENERATION":
						role = model.GENERATOR
					case "CONSUMPTION", "":
						// leer = dokumentierter Default (Verbraucher)
						role = model.CONSUMPTION
					default:
						// Tippfehler würde einen Erzeuger-ZP still als Verbraucher importieren
						// -> Zeile ablehnen und melden statt raten.
						importLog.Messages = append(importLog.Messages, model.NewLogMessage(
							"ERROR",
							strings.Trim(getColumValue(cols, colMap, "Zählpunkt", "MeteringPoint Id", nil), " "),
							"E_COUNTERPOINT_1001",
							fmt.Sprintf("Row skipped: unknown 'Energierichtung' %q (expected CONSUMPTION or GENERATION)", directionVal),
						))
						log.WithContext(ctx).Warn("Import row skipped: unknown energy direction")
						continue
					}

					streetNumber := getColumValue(cols, colMap, "Hausnummer", "Street Number", nil)
					var participantSince civil.NullDate
					memberSince := getColumValue(cols, colMap, "Mitglied seit", "member since", nil)
					if len(memberSince) == 0 {
						// ältere Vorlagen-Varianten
						memberSince = getColumValue(cols, colMap, "Dokument unterschrieben", "Document Signature Date", nil)
					}
					if isDateString(memberSince) || isDate(memberSince) {
						excelDate := civil.DateOf(parseExcelDate(memberSince))
						participantSince = civil.NullDateFrom(&excelDate)
					} else {
						today := civil.Today()
						participantSince = civil.NullDateFrom(&today)
					}

					var registeredSince civil.Date
					if online {
						registeredSince = civil.Today()
					} else {
						// Vorlagen-Spalte "registriert seit" = Zählpunkt registriert seit
						// ("Mitglied seit" gehört zum Mitglied, s. participantSince oben).
						regDateAt := getColumValue(cols, colMap, "registriert seit", "registriert since", nil)
						if isDateString(regDateAt) || isDate(regDateAt) {
							registeredSince = civil.DateOf(parseExcelDate(regDateAt))
						} else {
							registeredSince = civil.DateFor(time.Now().Year(), 1, 1)
						}
					}

					// tolerant gegenüber Gross-/Kleinschreibung und umgebenden Leerzeichen
					cpStatus := strings.ToUpper(strings.TrimSpace(getColumValue(cols, colMap, "Zählpunktstatus", "Metering Point State", nil)))
					if cpStatus == "ACTIVE" || cpStatus == "ACTIVATED" || cpStatus == "REGISTERED" || cpStatus == "NEW" {
						participantNumber := getColumValue(cols, colMap, "MitgliedsNr", "ParticipantNr", nil)
						var participant *model.EegParticipant
						if p, ok := findParticipant(participants, firstname, lastname); ok {
							participant = p
							// member on several rows: the first non-empty direct debit type counts,
							// a different one on a later row is reported
							debit := importDebitType(getColumValue(cols, colMap, "Einzugsart", "DebitType", nil), firstname, lastname, importLog)
							switch {
							case !debit.Valid:
							case !p.BankAccount.SepaDirectDebit.Valid:
								p.BankAccount.SepaDirectDebit = debit
							case p.BankAccount.SepaDirectDebit.String != debit.String:
								importLog.Messages = append(importLog.Messages, model.NewLogMessage(
									"WARNING",
									fmt.Sprintf("%s %s", firstname, lastname),
									"W_DEBIT_TYPE_CONFLICT",
									fmt.Sprintf("different direct debit types on several rows (%s, %s); a new member gets the first one",
										p.BankAccount.SepaDirectDebit.String, debit.String)))
							}
						} else {
							participant = &model.EegParticipant{
								EegParticipantBase: model.EegParticipantBase{
									ParticipantNumber: null.StringFrom(participantNumber),
									FirstName:         firstname,
									LastName:          lastname,
									TitleBefore:       null.StringFrom(getColumValue(cols, colMap, "TitelVor", "TitleBefor", nil)),
									TitleAfter:        null.StringFrom(getColumValue(cols, colMap, "TitelNach", "TitleAfter", nil)),
									BusinessRole:      businessRole(cols, colMap),
									Status:            getParticipantStatus(cpStatus),
									ParticipantSince:  participantSince,
									MeteringPoint:     []*model.MeteringPoint{},
									VatNumber:         null.StringFrom(getColumValue(cols, colMap, "UmsatzsteuerNr", "vatNumber", nil)),
									Version:           0,
								},
								ResidentAddress: model.Address{
									Type:         model.RESIDENCE,
									Street:       null.StringFrom(getColumValue(cols, colMap, "Straße", "Street", nil)),
									StreetNumber: null.StringFrom(streetNumber),
									Zip:          null.StringFrom(getColumValue(cols, colMap, "PLZ", "ZIP", nil)),
									City:         null.StringFrom(getColumValue(cols, colMap, "Ort", "City", nil)),
								},
								BillingAddress: model.Address{
									Type:         model.BILLING,
									Street:       null.StringFrom(getColumValue(cols, colMap, "Straße", "Street", nil)),
									StreetNumber: null.StringFrom(streetNumber),
									Zip:          null.StringFrom(getColumValue(cols, colMap, "PLZ", "ZIP", nil)),
									City:         null.StringFrom(getColumValue(cols, colMap, "Ort", "City", nil)),
								},
								BankAccount: model.BankInfo{
									Iban:             null.StringFrom(getColumValue(cols, colMap, "IBAN", "IBAN", nil)),
									Owner:            null.StringFrom(getColumValue(cols, colMap, "Kontoinhaber", "Accountname", nil)),
									BankName:         null.StringFrom(getColumValue(cols, colMap, "Bankname", "Bankname", nil)),
									MandateReference: null.StringFrom(getColumValue(cols, colMap, "MandatRef", "MandateRef", nil)),
									MandateDate:      getColumDate(cols, colMap, "MandatDat", "MandateDat", nil),
									SepaDirectDebit:  importDebitType(getColumValue(cols, colMap, "Einzugsart", "DebitType", nil), firstname, lastname, importLog),
								},
								Contact: model.ContactInfo{
									Email: importEmail(getColumValue(cols, colMap, "email", "email", nil), firstname, lastname, importLog),
									Phone: null.StringFrom(getColumValue(cols, colMap, "TelefonNr", "phonenr", nil)),
								},
							}
							participants = append(participants, participant)
						}
						meteringPointId := strings.Trim(getColumValue(cols, colMap, "Zählpunkt", "MeteringPoint Id", nil), " ")
						if len(meteringPointId) == 33 {
							gridOperatorId, gridOperatorNameValue := importGridOperator(meteringPointId, givenGridOperator, gridOperatorName, importLog)
							participant.MeteringPoint = append(participant.MeteringPoint, &model.MeteringPoint{
								GridOperatorId:   gridOperatorId,
								GridOperatorName: gridOperatorNameValue,
								MeteringPoint:    meteringPointId,
								Transformer:      null.String{},
								Direction:        role,
								Status:           getMeteringPointStatus(cpStatus),
								ProcessState:     getMeteringPointProcessState(cpStatus),
								TariffId:         null.String{},
								EquipmentNumber:  equipmentNumber(cols, colMap),
								EquipmentName:    equipmentName(cols, colMap),
								RegisteredSince:  registeredSince,
								InverterId:       null.String{},
								PartFact:         partFact(cols, colMap),
								Street:           null.StringFrom(getColumValue(cols, colMap, "Straße", "Street", nil)),
								StreetNumber:     null.StringFrom(getColumValue(cols, colMap, "Hausnummer", "Street Number", nil)),
								City:             null.StringFrom(getColumValue(cols, colMap, "Ort", "City", nil)),
								Zip:              null.StringFrom(getColumValue(cols, colMap, "PLZ", "ZIP", nil)),
								State: &model.MeterState{
									ActiveSince:   getColumDate(cols, colMap, "registriert seit", "registriert since", getCivilDatePtr(civil.DateFor(time.Now().Year(), 1, 1))),
									InactiveSince: civil.NullDate{Date: civil.DateFor(2999, 12, 31), Valid: true},
									Active:        1,
									Flag:          1,
								},
							})
						} else {
							importLog.Messages = append(importLog.Messages, model.NewLogMessage(
								"ERROR",
								meteringPointId,
								"E_COUNTERPOINT_1000",
								"Does not fulfill requirements! Len not equal 33. -> Not Included",
							))
							log.WithContext(ctx).Warnf("Metering Point -%s- does not fulfill requirements! Len not equal 33. -> Not Included", meteringPointId)
						}
					} else {
						importLog.Messages = append(importLog.Messages,
							model.NewLogMessage(
								"ERROR",
								fmt.Sprintf("%s %s", firstname, lastname),
								"E_PARTICIPANT_1000",
								fmt.Sprintf("Does not fulfill requirements! Participant has wrong status: %s", cpStatus)))
						log.WithContext(ctx).Warn("Import row skipped: participant has wrong status")
					}
				}
			}
		}
	}
	return participants
}

// isImportHeaderRow erkennt die Kopfzeile: früher an "Netzbetreiber" in Spalte A, jetzt (Spalte A
// ist optional) auch an der Überschrift der Zählpunkt-Spalte.
func isImportHeaderRow(cols []string) bool {
	if first := strings.TrimSpace(cols[0]); first == "Netzbetreiber" || first == "Grid Operator" {
		return true
	}
	for _, c := range cols {
		if c = strings.TrimSpace(c); strings.EqualFold(c, "Zählpunkt") || strings.EqualFold(c, "MeteringPoint Id") {
			return true
		}
	}
	return false
}

// isImportDataRow: nach der Kopfzeile ist jede Zeile mit Zählpunkt oder Name eine Datenzeile.
func isImportDataRow(cols []string, colMap map[string]int) bool {
	if len(colMap) == 0 {
		return false
	}
	return len(strings.TrimSpace(getColumValue(cols, colMap, "Zählpunkt", "MeteringPoint Id", nil))) > 0 ||
		len(strings.TrimSpace(getColumValue(cols, colMap, "Name 1", "Name1", nil))) > 0 ||
		len(strings.TrimSpace(getColumValue(cols, colMap, "Name 2", "Name2", nil))) > 0
}

// importGridOperator leitet den Netzbetreiber aus der Zählpunktnummer ab (platform#107). Der Wert
// aus Spalte A wird nur verglichen; eine Abweichung oder Alias-Übersetzung landet als Hinweis im
// Import-Protokoll. Verbindlich setzt ihn die Datenbankschicht (applyGridOperator).
func importGridOperator(meteringPointId, given string, gridOperatorName func(id string) string, importLog *model.Log) (null.String, null.String) {
	r, ok := gridoperator.FromMeteringPoint(meteringPointId, gridOperatorAlias())
	if !ok {
		return null.String{}, null.String{}
	}
	switch {
	case given != "" && given != r.Id:
		importLog.Messages = append(importLog.Messages, model.NewLogMessage(
			"WARNING",
			meteringPointId,
			"W_GRID_OPERATOR_IGNORED",
			fmt.Sprintf("'Netzbetreiber' %s in column A ignored, %s is used (derived from the metering point number)", given, r.Id),
		))
	case r.Aliased && given != r.Id:
		importLog.Messages = append(importLog.Messages, model.NewLogMessage(
			"WARNING",
			meteringPointId,
			"W_GRID_OPERATOR_IGNORED",
			fmt.Sprintf("Grid operator %s of the metering point number is replaced by %s", r.Prefix, r.Id),
		))
	}
	name := gridOperatorName(r.Id)
	return null.StringFrom(r.Id), null.NewString(name, name != "")
}

func ExportZPListToExcel(ctx context.Context, ebmsMsg *model.EbmsMessage) (*bytes.Buffer, error) {
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			log.WithContext(ctx).WithError(err).Error("Error closing file")
		}
	}()

	err := generateZPListMastersheet(ctx, f, ebmsMsg)
	if err != nil {
		return nil, err
	}

	_ = f.DeleteSheet("Sheet1")
	return f.WriteToBuffer()
}

func generateZPListMastersheet(ctx context.Context, f *excelize.File, ebmsMsg *model.EbmsMessage) error {
	styleId, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}})
	//styleDateId, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 10.0}, NumFmt: 14})
	styleIdHeader, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10.0},
		Alignment: &excelize.Alignment{Vertical: "top", WrapText: true},
	})
	styleIdDate, err := f.NewStyle(&excelize.Style{
		Font:   &excelize.Font{Size: 10.0},
		NumFmt: 14,
	})

	sheet := "ZP-List"
	_, err = f.NewSheet(sheet)
	if err != nil {
		return err
	}

	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}

	err = sw.SetColWidth(1, 1, 5.0)
	err = sw.SetColWidth(2, 3, 30.0)
	err = sw.SetColWidth(4, 4, 20.0)
	err = sw.SetColWidth(5, 5, 9.5)
	colNr, _ := excelize.ColumnNameToNumber("G")
	err = sw.SetColWidth(colNr, colNr+3, 12.0)

	line := 1
	zpHeader := []interface{}{
		excelize.Cell{Value: "Nr."},
		excelize.Cell{Value: "Zählpunktname"},
		excelize.Cell{Value: "ConsentID"},
		excelize.Cell{Value: "Bezugsrichtung"},
		excelize.Cell{Value: "Teilnahme-faktor"},
		excelize.Cell{Value: "statische Aufteilung"},
		excelize.Cell{Value: "aktiviert"},
		excelize.Cell{Value: "aktiv seit"},
		excelize.Cell{Value: "aktiv bis"},
	}
	err = sw.SetRow(fmt.Sprintf("A%d", line), zpHeader, excelize.RowOpts{StyleID: styleIdHeader, Height: 0.42 * 72})
	idx := 0
	for _, m := range ebmsMsg.MeterList {
		line = line + 1
		idx += 1
		err = sw.SetRow(fmt.Sprintf("A%d", line),
			[]interface{}{
				excelize.Cell{Value: idx},
				excelize.Cell{Value: m.MeteringPoint},
				excelize.Cell{Value: m.ConsentID},
				excelize.Cell{Value: m.Direction},
				excelize.Cell{Value: m.PartFact},
				excelize.Cell{Value: m.Share},
				excelize.Cell{Value: time.UnixMilli(m.Activation), StyleID: styleIdDate},
				excelize.Cell{Value: time.UnixMilli(m.From), StyleID: styleIdDate},
				excelize.Cell{Value: time.UnixMilli(m.To), StyleID: styleIdDate},
			}, excelize.RowOpts{StyleID: styleId})
	}

	setAutoFilter(ctx, f, sheet, len(zpHeader), line)
	err = sw.Flush()
	return err
}
