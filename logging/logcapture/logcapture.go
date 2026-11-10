// Package logcapture captures the log of one test as the service writes it: JSON through logging.Setup at info level,
// so the field allow-list applies. For tests outside package logging (which cannot use it: logging's own tests use
// logtest, and this package imports logging). Only tests import it.
package logcapture

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/logging/logtest"
)

// JSON captures the log of the test; read it with logtest.Lines. The global loggers are restored when the test ends.
func JSON(t testing.TB) *bytes.Buffer {
	t.Helper()
	return logtest.Capture(t, func(w io.Writer) {
		logging.Setup(w, logging.Config{Level: slog.LevelInfo, JSON: true, Version: "test"})
	})
}
