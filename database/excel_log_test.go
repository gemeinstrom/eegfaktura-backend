package database

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// L1 (never-log rule): a rejected import row is reported to the user with its
// names; the log lines carry neither names nor other cell values.
func Test_transformExcelData_logsNoCellValues(t *testing.T) {
	const (
		mp1 = "AT0099990000000000000000000000001"
		mp2 = "AT0099990000000000000000000000002"
		mp3 = "AT0099990000000000000000000000003"
	)
	f := buildImportSheet(t, [][]interface{}{
		{"AT009999", testCommunityId, "4020", "Linz", "Weg", "1",
			mp1, "CONSUMPTION", "", "Oneword", "privat", "", "", "001", "ACTIVE", "", ""},
		{"AT009999", testCommunityId, "4020", "Linz", "Weg", "2",
			mp2, "CONSUMPTION", "Erika", "Mustermann", "privat", "", "", "002", "WrongStatus", "", ""},
		{"AT009999", testCommunityId, "4020", "Linz", "Weg", "3",
			mp3, "Hildegard", "Max", "Muster", "privat", "", "", "003", "ACTIVE", "", ""},
		{"AT009999", "AT00888800000TC000008000000000001", "4020", "Linz", "Weg", "4",
			mp1, "CONSUMPTION", "Eva", "Sample", "privat", "", "", "004", "ACTIVE", "", ""},
	})

	prev := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(prev) })
	hook := logtest.NewLocal(logrus.StandardLogger())

	_, importLog := transformSheet(t, f, false)

	var shown strings.Builder
	for _, m := range importLog.Messages {
		shown.WriteString(m.Message + " " + m.Identifier + "\n")
	}
	for _, want := range []string{"Oneword", "Erika Mustermann", "WRONGSTATUS", "HILDEGARD"} {
		assert.Contains(t, shown.String(), want, "the import result keeps it")
	}

	require.NotEmpty(t, hook.AllEntries())
	var logged strings.Builder
	for _, e := range hook.AllEntries() {
		logged.WriteString(e.Message)
		for _, v := range e.Data {
			logged.WriteString(" ")
			if s, ok := v.(string); ok {
				logged.WriteString(s)
			}
		}
		logged.WriteString("\n")
	}
	for _, never := range []string{"Oneword", "Erika", "Mustermann", "WRONGSTATUS", "HILDEGARD", "AT00888800000TC000008000000000001"} {
		assert.NotContains(t, logged.String(), never)
	}
}
