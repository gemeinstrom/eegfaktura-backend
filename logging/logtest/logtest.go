// Package logtest captures the log output of one test and puts the global
// loggers back afterwards. Only tests import it. It does not import package
// logging, so the tests inside that package can use it too.
package logtest

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// Capture calls setup with a buffer (as a rule logging.Setup) and restores,
// when the test ends, the slog default, the logrus standard logger (level,
// output, formatter, caller reporting, hooks) and the log package's writer
// and flags, which slog.SetDefault changes. The buffer is emptied after setup:
// it holds what the test logs, not the start-up line of logging.Setup.
func Capture(t testing.TB, setup func(io.Writer)) *bytes.Buffer {
	t.Helper()
	oldSlog := slog.Default()
	oldLogOut, oldLogFlags := log.Writer(), log.Flags()
	std := logrus.StandardLogger()
	oldLevel, oldOut, oldFmt, oldCaller := std.Level, std.Out, std.Formatter, std.ReportCaller
	oldHooks := std.ReplaceHooks(logrus.LevelHooks{})
	t.Cleanup(func() {
		slog.SetDefault(oldSlog)
		log.SetOutput(oldLogOut)
		log.SetFlags(oldLogFlags)
		std.SetLevel(oldLevel)
		std.SetOutput(oldOut)
		std.SetFormatter(oldFmt)
		std.SetReportCaller(oldCaller)
		std.ReplaceHooks(oldHooks)
	})
	var buf bytes.Buffer
	setup(&buf)
	buf.Reset()
	return &buf
}

// Lines parses the captured JSON lines.
func Lines(t testing.TB, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m), l)
		out = append(out, m)
	}
	return out
}
