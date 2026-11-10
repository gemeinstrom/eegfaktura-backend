package logging

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// bridge is a logrus hook that hands every logrus entry to the slog handler,
// so the existing logrus calls keep working and come out in the one format.
// logrus' own output is discarded (Setup).
type bridge struct{ handler slog.Handler }

func (b *bridge) Levels() []logrus.Level { return logrus.AllLevels }

func (b *bridge) Fire(e *logrus.Entry) error {
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	lvl := slogLevel(e.Level)
	if !b.handler.Enabled(ctx, lvl) {
		return nil
	}
	r := slog.NewRecord(e.Time, lvl, strings.TrimRight(e.Message, "\n"), 0)
	for k, v := range e.Data {
		r.AddAttrs(fieldAttrs(k, v)...)
	}
	if e.HasCaller() {
		r.AddAttrs(
			slog.String("log.origin.function", e.Caller.Function),
			slog.String("log.origin.file.name", filepath.Base(e.Caller.File)),
			slog.Int("log.origin.file.line", e.Caller.Line),
		)
	}
	return b.handler.Handle(ctx, r)
}

// fieldAttrs maps the logrus field names in use to ECS names: "tenant" is
// the tenant.id, "error" (WithError) becomes error.type and error.message.
func fieldAttrs(k string, v any) []slog.Attr {
	switch k {
	case "tenant":
		return []slog.Attr{slog.Any("tenant.id", v)}
	case logrus.ErrorKey:
		if err, ok := v.(error); ok {
			return []slog.Attr{
				slog.String("error.type", fmt.Sprintf("%T", err)),
				slog.String("error.message", err.Error()),
			}
		}
		return []slog.Attr{slog.String("error.message", fmt.Sprint(v))}
	}
	return []slog.Attr{slog.Any(k, v)}
}

func slogLevel(l logrus.Level) slog.Level {
	switch l {
	case logrus.TraceLevel:
		return LevelTrace
	case logrus.DebugLevel:
		return slog.LevelDebug
	case logrus.InfoLevel:
		return slog.LevelInfo
	case logrus.WarnLevel:
		return slog.LevelWarn
	}
	// error, fatal and panic
	return slog.LevelError
}

func logrusLevel(l slog.Level) logrus.Level {
	switch {
	case l < slog.LevelDebug:
		return logrus.TraceLevel
	case l < slog.LevelInfo:
		return logrus.DebugLevel
	case l < slog.LevelWarn:
		return logrus.InfoLevel
	case l < slog.LevelError:
		return logrus.WarnLevel
	}
	return logrus.ErrorLevel
}

// discardFormatter skips logrus' own formatting; its output is discarded.
type discardFormatter struct{}

func (discardFormatter) Format(*logrus.Entry) ([]byte, error) { return nil, nil }
