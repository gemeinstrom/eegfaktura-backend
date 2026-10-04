package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jjeffery/civil"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"gopkg.in/guregu/null.v4"
)

func Test_transformExcelData(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	reader, err := os.Open("../tests/TE100200-Muster-Stammdatenimport.xlsx")
	require.NoError(t, err)
	defer reader.Close()

	f, err := openReader(reader, "TE100200-Muster-Stammdatenimport.xlsx")
	require.NoError(t, err)
	defer f.Close()

	rows, err := f.Rows("EEG Stammdaten")
	require.NoError(t, err)

	importLog := &model.Log{Operation: "Excel Master Data Import", Messages: []*model.LogMessage{}}
	participants := transformExcelData(rows, func(id string) string { return id }, false, "AT00999900000TC100200000000000002", importLog)

	findParticipant := func(n string, p []*model.EegParticipant) *model.EegParticipant {
		for i := range p {
			if p[i].LastName == n {
				return p[i]
			}
		}
		return &model.EegParticipant{}
	}

	assert.Equal(t, 7, len(participants))
	assert.Equal(t, 2, len(findParticipant("Finnegan", participants).MeteringPoint))
	assert.Equal(t, null.StringFrom("001-3456"), findParticipant("Finnegan", participants).TaxNumber)

	assert.Equal(t, null.StringFrom("003"), findParticipant("Finnegan", participants).ParticipantNumber)
	assert.Equal(t, null.StringFrom("005"), findParticipant("Beckett", participants).ParticipantNumber)
	assert.Equal(t, null.StringFrom("Silvia.Beckett@eegfaktura.at"), findParticipant("Beckett", participants).Contact.Email)
	assert.Equal(t, null.StringFrom("AT009999"), findParticipant("Beckett", participants).MeteringPoint[0].GridOperatorId)
	assert.Equal(t, null.StringFrom("AT009999"), findParticipant("Beckett", participants).MeteringPoint[0].GridOperatorName)

}

func TestImportMasterdataFromExcel(t *testing.T) {
	log.SetLevel(log.DebugLevel)

	findParticipant := func(ps []*model.EegParticipant, firstname, lastname string) *model.EegParticipant {
		for _, p := range ps {
			if p.FirstName == firstname && p.LastName == lastname {
				return p
			}
		}
		return nil
	}

	reader, err := os.Open("../tests/TE100200-Muster-Stammdatenimport.xlsx")
	require.NoError(t, err)
	defer reader.Close()

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	type args struct {
		r        io.Reader
		filename string
		sheet    string
		tenant   string
	}
	tests := []struct {
		name    string
		args    args
		prepare func()
		test    func(t *testing.T, args args)
	}{
		{
			name: "import file",
			args: args{
				r:        reader,
				filename: "TE100200-Muster-Stammdatenimport.xlsx",
				sheet:    "EEG Stammdaten",
				tenant:   "TE100200",
			},
			prepare: func() {

			},
			test: func(t *testing.T, args args) {
				require.NoError(t, db.ImportMasterdataFromExcel(context.Background(), args.r, args.filename, args.sheet, args.tenant))
				ps, err := db.GetParticipants(context.Background(), args.tenant)
				require.NoError(t, err)
				assert.Equal(t, 7, len(ps))

				p := findParticipant(ps, "Max", "Mustermann")
				require.NotNil(t, p)
				require.Equal(t, 1, len(p.MeteringPoint))

				fmt.Printf("ACTIVE-DATE: %v %v\n", p.MeteringPoint[0].State.ActiveSince.Valid, p.MeteringPoint[0].State.ActiveSince.Date)

				assert.Equal(t, "Test Operator", p.MeteringPoint[0].GridOperatorName.String)
				assert.Equal(t, civil.DateOf(time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.Local)), p.MeteringPoint[0].State.ActiveSince.Date)
				assert.Equal(t, civil.DateOf(time.Date(2999, 12, 31, 0, 0, 0, 0, time.Local)), p.MeteringPoint[0].State.InactiveSince.Date)
				assert.Equal(t, model.F_ASSIGNED, p.MeteringPoint[0].State.Flag)
				assert.Equal(t, model.ACTIVE, p.MeteringPoint[0].ProcessState)
				assert.Equal(t, model.S_ACTIVE, p.MeteringPoint[0].Status)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t, tt.args)
		})
	}
}

//func TestImportMasterdataFromExcel(t *testing.T) {
//	var mockDb, err = GetDatabaseMock()
//	require.NoError(t, err)
//
//	reader, err := os.Open("../tests/TE100200-Muster-Stammdatenimport.xlsx")
//	require.NoError(t, err)
//	defer reader.Close()
//
//	type args struct {
//		dbConn   OpenDbXConnection
//		r        io.Reader
//		filename string
//		sheet    string
//		tenant   string
//	}
//	tests := []struct {
//		name    string
//		args    args
//		prepare func()
//		test    func(t *testing.T, args args)
//	}{
//		{
//			name: "import file",
//			args: args{
//				dbConn:   mockDb.OpenMockDb,
//				r:        reader,
//				filename: "TE100200-Muster-Stammdatenimport.xlsx",
//				sheet:    "EEG Stammdaten",
//				tenant:   "TE100200",
//			},
//			prepare: func() {
//
//			},
//			test: func(t *testing.T, args args) {
//				require.NoError(t, err)
//				//mockDb.mock.ExpectExec("^INSERT INTO (.+) VALUES (.+)") //.WithArgs("firstname", "lastname")
//				//mockDb.Mock.ExpectQuery("^SELECT (.+)").WillReturnError(sql.ErrNoRows)
//				mockDb.Mock.ExpectBegin()
//				// , 'excel', 'Mustermann', '001', '2023-08-19T13:15:07.776003233Z', 'ACTIVE', '001-9876', 'TE100200', '', '', '', DEFAULT
//				mockDb.Mock.ExpectQuery("INSERT (.+)").WillReturnRows(sqlmock.NewRows([]string{"id"}).FromCSVString("1")) //.WillReturnResult(sqlmock.NewResult(1, 1)) //.WithArgs("firstname", "lastname")
//				mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//				mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//				mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//				mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//				//mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//				//mockDb.Mock.ExpectExec("INSERT (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
//
//				require.NoError(t, ImportMasterdataFromExcel(args.dbConn, args.r, args.filename, args.sheet, args.tenant))
//				//require.NoError(t, mockDb.Mock.ExpectationsWereMet())
//			},
//		},
//	}
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			tt.test(t, tt.args)
//		})
//	}
//}

func TestExportMasterdataToExcel(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	tenant := "TE000002"
	eeg, err := db.GetEegById(context.Background(), tenant)
	require.NoError(t, err)

	participants, err := db.GetParticipants(context.Background(), tenant)
	require.NoError(t, err)

	tariffMap, err := db.GetTariffNameMap(tenant)
	require.NoError(t, err)

	type args struct {
		participants []*model.EegParticipant
		eeg          *model.Eeg
		tariffMap    map[string]string
	}
	tests := []struct {
		name    string
		args    args
		check   func(t *testing.T, bytes *bytes.Buffer)
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Export Tenant",
			args: args{participants: participants, eeg: eeg, tariffMap: tariffMap},
			check: func(t *testing.T, buff *bytes.Buffer) {
				r := bytes.NewReader(buff.Bytes())
				f, err := openReader(r, "test")
				require.NoError(t, err)

				rows, err := f.Rows("Mitglieder")
				require.NoError(t, err)
				defer rows.Close()

				var cols [][]string
				for rows.Next() {
					c, err := rows.Columns(excelize.Options{RawCellValue: true})
					require.NoError(t, err)
					cols = append(cols, c)
					fmt.Printf("Col: %+v\n", c)
				}

				assert.Equal(t, 6, len(cols))
				assert.Equal(t, "6", cols[1][19])
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExportMasterdataToExcel(tt.args.participants, tt.args.eeg, tt.args.tariffMap)
			if !tt.wantErr(t, err, fmt.Sprintf("ExportMasterdataToExcel(%v, %v, %v)", tt.args.participants, tt.args.eeg, tt.args.tariffMap)) {
				return
			}
			tt.check(t, got)
		})
	}
}

func TestExportZPListToExcel(t *testing.T) {
	testMsg := `{"conversationId":"RC100417202404151713192460000008393","messageId":"AT002000202404151647464796647733092","sender":"AT002000","receiver":"RC100417","messageCode":"SENDEN_ECP","messageCodeVersion":"04.10","meterList":[{"meteringPoint":"AT0020000000000000000000100326383","direction":"GENERATION","activation":1708383600000,"partFact":100,"from":1708383600000,"to":253402210800000,"plantCategory":"SONNE"},{"meteringPoint":"AT0020000000000000000000100341356","direction":"GENERATION","activation":1673910000000,"partFact":100,"from":1673910000000,"to":253402210800000,"plantCategory":"SONNE"},{"meteringPoint":"AT0020000000000000000000100346220","direction":"GENERATION","activation":1673910000000,"partFact":100,"from":1673910000000,"to":253402210800000,"plantCategory":"SONNE"},{"meteringPoint":"AT0020000000000000000000100356673","direction":"GENERATION","activation":1700780400000,"partFact":100,"from":1700780400000,"to":253402210800000,"plantCategory":"SONNE"},{"meteringPoint":"AT0020000000000000000000100369058","direction":"GENERATION","activation":1700780400000,"partFact":100,"from":1700780400000,"to":253402210800000,"plantCategory":"SONNE"},{"meteringPoint":"AT0020000000000000000000020363290","direction":"CONSUMPTION","activation":1713132000000,"partFact":100,"from":1713132000000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020368382","direction":"CONSUMPTION","activation":1710457200000,"partFact":100,"from":1710457200000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020368542","direction":"CONSUMPTION","activation":1700780400000,"partFact":100,"from":1700780400000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020370978","direction":"CONSUMPTION","activation":1687384800000,"partFact":100,"from":1687384800000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020371090","direction":"CONSUMPTION","activation":1708038000000,"partFact":100,"from":1708038000000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020371275","direction":"CONSUMPTION","activation":1690149600000,"partFact":100,"from":1690149600000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020396712","direction":"CONSUMPTION","activation":1712527200000,"partFact":100,"from":1712527200000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000020397012","direction":"CONSUMPTION","activation":1690149600000,"partFact":100,"from":1690149600000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000021078996","direction":"CONSUMPTION","activation":1674687600000,"partFact":100,"from":1674687600000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000021124147","direction":"CONSUMPTION","activation":1713132000000,"partFact":100,"from":1713132000000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000021296887","direction":"CONSUMPTION","activation":1700780400000,"partFact":100,"from":1700780400000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100058953","direction":"CONSUMPTION","activation":1712008800000,"partFact":100,"from":1712008800000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100078028","direction":"CONSUMPTION","activation":1710457200000,"partFact":100,"from":1710457200000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100097123","direction":"CONSUMPTION","activation":1708383600000,"partFact":100,"from":1708383600000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100111690","direction":"CONSUMPTION","activation":1712527200000,"partFact":100,"from":1712527200000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100125540","direction":"CONSUMPTION","activation":1689026400000,"partFact":100,"from":1689026400000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100138318","direction":"CONSUMPTION","activation":1689026400000,"partFact":100,"from":1689026400000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100251848","direction":"CONSUMPTION","activation":1713132000000,"partFact":100,"from":1713132000000,"to":253402210800000},{"meteringPoint":"AT0020000000000000000000100251849","direction":"CONSUMPTION","activation":1673910000000,"partFact":100,"from":1673910000000,"to":253402210800000}]}`
	var ebmsMsgTest model.EbmsMessage

	err := json.Unmarshal([]byte(testMsg), &ebmsMsgTest)
	require.NoError(t, err)

	buf, err := ExportZPListToExcel(&ebmsMsgTest)
	err = os.WriteFile("./test.xlsx", buf.Bytes(), 0644)
	require.NoError(t, err)
}
