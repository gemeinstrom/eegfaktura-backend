package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type respondCtxKey struct{}

// L12: the central error line is written with the request's context, that is
// where request.id and tenant.id come from.
func TestRespondWith_ErrorLineHasTheRequestContext(t *testing.T) {
	prev := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(prev) })
	hook := logtest.NewLocal(logrus.StandardLogger())

	ctx := context.WithValue(context.Background(), respondCtxKey{}, "request")
	respondWith(ctx, httptest.NewRecorder(), http.StatusBadRequest, "TE100100", model.ErrParseJson(errors.New("boom")))

	require.NotNil(t, hook.LastEntry())
	assert.Equal(t, ctx, hook.LastEntry().Context)
}
