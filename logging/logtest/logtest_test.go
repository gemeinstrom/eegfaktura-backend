package logtest_test

import (
	"io"
	"log"
	"log/slog"
	"testing"

	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// After the test, every global logger is as before, also the log package
// that slog.SetDefault redirects.
func TestCapture_RestoresGlobalLoggers(t *testing.T) {
	std := logrus.StandardLogger()
	slogBefore, logOut, logFlags := slog.Default(), log.Writer(), log.Flags()
	level, out, formatter, caller, hooks := std.Level, std.Out, std.Formatter, std.ReportCaller, len(std.Hooks[logrus.InfoLevel])

	t.Run("capture", func(t *testing.T) {
		buf := logtest.Capture(t, func(w io.Writer) {
			logging.Setup(w, logging.Config{Level: slog.LevelInfo, JSON: true, Version: "test"})
		})
		logrus.SetReportCaller(true)
		log.Print("via log")
		logrus.Error("via logrus")
		ls := logtest.Lines(t, buf)
		require.Len(t, ls, 2)
		assert.Equal(t, "via log", ls[0]["message"])
		assert.Equal(t, "via logrus", ls[1]["message"])
	})

	assert.Same(t, slogBefore, slog.Default())
	assert.Equal(t, logOut, log.Writer())
	assert.Equal(t, logFlags, log.Flags())
	assert.Equal(t, level, std.Level)
	assert.Equal(t, out, std.Out)
	assert.Equal(t, formatter, std.Formatter)
	assert.Equal(t, caller, std.ReportCaller)
	assert.Len(t, std.Hooks[logrus.InfoLevel], hooks)
}
