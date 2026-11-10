// Package logging is the one place where the backend's log output is set up
// (logging concept W-261008-ca2, point 0): format, levels, the common ECS
// fields and the access line. Every log line goes through one slog handler;
// the existing logrus calls are routed into it by a hook (logrus.go).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const ServiceName = "eegfaktura-backend"

// LevelTrace is below slog's debug; slog has no trace level of its own.
const LevelTrace = slog.Level(-8)

// Config is read from LOG_LEVEL, LOG_FORMAT, SERVICE_VERSION and
// DEPLOYMENT_ENVIRONMENT.
type Config struct {
	Level       slog.Level
	JSON        bool
	Version     string
	Environment string
}

// ConfigFromEnv reads LOG_LEVEL (default info), LOG_FORMAT (default json),
// SERVICE_VERSION (set by the image build, default dev) and
// DEPLOYMENT_ENVIRONMENT (no default, the field is left out when unset). An
// empty value is the default; an unknown level or format is an error: the
// backend refuses to start.
func ConfigFromEnv(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{Level: slog.LevelInfo, JSON: true, Version: "dev"}
	if v, ok := lookup("SERVICE_VERSION"); ok && v != "" {
		cfg.Version = v
	}
	if v, ok := lookup("DEPLOYMENT_ENVIRONMENT"); ok {
		cfg.Environment = strings.TrimSpace(v)
	}
	if v, ok := lookup("LOG_LEVEL"); ok && strings.TrimSpace(v) != "" {
		l, err := ParseLevel(v)
		if err != nil {
			return cfg, err
		}
		cfg.Level = l
	}
	if v, ok := lookup("LOG_FORMAT"); ok && strings.TrimSpace(v) != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "json":
			cfg.JSON = true
		case "text":
			cfg.JSON = false
		default:
			return cfg, fmt.Errorf("unknown LOG_FORMAT %q (json, text)", v)
		}
	}
	return cfg, nil
}

// ParseLevel accepts trace, debug, info, warn (or warning) and error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown LOG_LEVEL %q (trace, debug, info, warn, error)", s)
}

func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelDebug:
		return "trace"
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	}
	return "error"
}

// NewHandler builds the one handler: one JSON object (or one key=value line)
// per record, ECS field names, service.name, service.version and (when set)
// deployment.environment on every line.
func NewHandler(w io.Writer, cfg Config) slog.Handler {
	opts := &slog.HandlerOptions{Level: cfg.Level, ReplaceAttr: ecsKeys}
	var h slog.Handler
	if cfg.JSON {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	common := []slog.Attr{
		slog.String("service.name", ServiceName),
		slog.String("service.version", cfg.Version),
	}
	if cfg.Environment != "" {
		common = append(common, slog.String("deployment.environment", cfg.Environment))
	}
	return contextHandler{Handler: h.WithAttrs(common)}
}

func ecsKeys(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.String("@timestamp", a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
	case slog.LevelKey:
		return slog.String("log.level", levelName(a.Value.Any().(slog.Level)))
	case slog.MessageKey:
		return slog.Attr{Key: "message", Value: a.Value}
	}
	return a
}

// allowedFields are the only record fields written (never-log rule: a field
// is a decision, not a side effect); others are dropped. ECS fields first,
// then the field names the existing logrus calls use. request.id, trace.id,
// span.id and tenant.id are not on it: contextHandler writes them, once each.
var allowedFields = map[string]bool{
	"event.action": true, "event.outcome": true, "event.duration": true,
	"http.request.method": true, "http.route": true, "http.response.status_code": true,
	"client.id": true, "graphql.operation.name": true,
	"error.type": true, "error.message": true, "error.stack_trace": true,
	"log.effective_level": true, "log.origin.function": true, "log.origin.file.name": true, "log.origin.file.line": true,
	// logrus fields in use (2026-10-30)
	"SQL": true, "STMT": true, "MSG": true, "PROTOCOL": true, "protocol": true,
	"code": true, "command": true, "ecId": true, "participantId": true, "templates": true,
	"sepaActiv": true, "settlementInterval": true,
	// settlement interval change (api/settlementInterval.go)
	"event.reason": true, "settlementInterval.old": true, "settlementInterval.new": true, "settlementInterval.effectiveFrom": true,
	// migration result (database/migrate.go)
	"migration.versionBefore": true, "migration.versionAfter": true, "migration.applied": true, "migration.version": true,
	"eeg.area.old": true, "eeg.area.new": true, // eeg.area.updated_from_list (platform#229)
	// db pool stats (database.go)
	"open": true, "inUse": true, "idle": true, "maxOpen": true, "waitCount": true, "waitDuration": true,
	"maxIdleClosed": true, "maxLifetimeClosed": true,
	// gql-admin-check, temporary F1 log (api/middleware/gqlAdminCheck.go); no "sub"
	// (user id) — no user tracking, 2026-10-29
	"access_groups.class": true, "superuser": true, "operation_type": true,
}

// contextHandler drops fields outside allowedFields and adds request.id,
// tenant.id, trace.id and span.id when the record's context belongs to an HTTP
// request (see AccessLog). In a request tenant.id is only ever what passed the
// tenant check (SetTenant), a tenant field of the record or of With does not
// count. Outside a request the tenant field (the record's over With's) is
// written upper-cased, or dropped when it fails validTenant. A request.id,
// trace.id or span.id field of the record is dropped.
type contextHandler struct {
	slog.Handler
	tenant string // a tenant.id field of WithAttrs
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	tenant := h.tenant
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "tenant.id" {
			tenant = a.Value.String()
		} else if allowedFields[a.Key] {
			out.AddAttrs(a)
		}
		return true
	})
	if ai := fromContext(ctx); ai != nil {
		out.AddAttrs(ai.correlation()...)
	} else if upper, ok := validTenant(tenant); ok {
		out.AddAttrs(slog.String("tenant.id", upper))
	}
	return h.Handler.Handle(ctx, out)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	var kept []slog.Attr
	for _, a := range as {
		if a.Key == "tenant.id" {
			h.tenant = a.Value.String()
		} else if allowedFields[a.Key] {
			kept = append(kept, a)
		}
	}
	return contextHandler{h.Handler.WithAttrs(kept), h.tenant}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name), h.tenant}
}

// Setup makes the handler the default for slog and the standard log package,
// routes the global logrus logger into it and writes the one start-up line:
// version (service.version, as on every line) and effective log level. The
// line goes straight to the handler, a LOG_LEVEL above info must not hide it.
func Setup(w io.Writer, cfg Config) {
	h := NewHandler(w, cfg)
	slog.SetDefault(slog.New(h))

	logrus.SetLevel(logrusLevel(cfg.Level))
	logrus.SetOutput(io.Discard)
	logrus.SetFormatter(discardFormatter{})
	logrus.StandardLogger().ReplaceHooks(logrus.LevelHooks{})
	logrus.AddHook(&bridge{handler: h})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "service start", 0)
	r.AddAttrs(
		slog.String("event.action", "service.start"),
		slog.String("log.effective_level", levelName(cfg.Level)),
	)
	_ = h.Handle(context.Background(), r)
}
