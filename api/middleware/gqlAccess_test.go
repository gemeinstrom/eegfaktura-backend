package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"at.ourproject/vfeeg-backend/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A resolver panic is one JSON line through the logging module (not a plain
// stack on stderr); the client gets a generic error.
func TestGQLRecover_LogsThroughTheModule(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(logging.NewHandler(&buf, logging.Config{Level: slog.LevelInfo, JSON: true, Version: "test"})))

	err := GQLRecover(t.Context(), "nil map")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal system error")
	var m map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m), buf.String())
	assert.Equal(t, "error", m["log.level"])
	assert.Equal(t, "graphql.panic", m["event.action"])
	assert.Equal(t, "string", m["error.type"])
	assert.Equal(t, "nil map", m["error.message"])
	assert.Contains(t, m["error.stack_trace"], "GQLRecover")
}
