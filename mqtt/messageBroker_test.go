package mqttclient

import (
	"sync/atomic"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The caller stops the mail worker and closes the database after Stop: a handler still running in
// Listen must be finished by then.
func TestMessageBroker_StopWaitsForTheRunningHandler(t *testing.T) {
	m, err := NewMessageBroker()
	require.NoError(t, err)
	entered := make(chan struct{})
	var finished atomic.Bool
	m.Subscribe(model.Subscriptions{Protocol: model.CM_REV_IMP, Handler: func(model.SubscribeMessage) {
		close(entered)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	}})
	go m.Listen()
	m.Inbound <- InboundMessage{"TE000001", model.CM_REV_IMP, []byte(`{}`)}
	<-entered

	m.Stop()
	assert.True(t, finished.Load())
}

func TestMessageBroker_command(t *testing.T) {

	type args struct {
		cmd CommandMessage
	}
	tests := []struct {
		name string
		args args
	}{
		{name: "Sepp", args: args{cmd: CommandMessage{
			tenant: "TE100100",
			cmd:    "pontononlinestate",
			msg:    []byte(`{"online": true}`),
		}}},
	}
	//repository.InitRepositories()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &MessageBroker{}
			m.command(tt.args.cmd)
		})
	}
}

func TestMessageBroker_command_malformedOnlineState(t *testing.T) {
	payloads := map[string]string{
		"string": `{"online": "true"}`,
		"null":   `{"online": null}`,
		"number": `{"online": 1}`,
		"object": `{"online": {}}`,
		"array":  `[true]`,
		"noJson": `online`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			m := &MessageBroker{}
			require.NotPanics(t, func() {
				m.command(CommandMessage{tenant: "TE100100", cmd: "pontononlinestate", msg: []byte(payload)})
			})
		})
	}
}

func TestParseOnlineState(t *testing.T) {
	valid := []struct {
		payload       string
		online, found bool
	}{
		{payload: `{"online": true}`, online: true, found: true},
		{payload: `{"online": false}`, online: false, found: true},
		{payload: `{}`, online: false, found: false},
	}
	for _, tt := range valid {
		online, found, err := parseOnlineState([]byte(tt.payload))
		require.NoError(t, err, tt.payload)
		require.Equal(t, tt.online, online, tt.payload)
		require.Equal(t, tt.found, found, tt.payload)
	}
	for _, payload := range []string{`{"online": "true"}`, `{"online": null}`, `{"online": 1}`, `{"online": {}}`, `[true]`, `online`} {
		_, _, err := parseOnlineState([]byte(payload))
		require.Error(t, err, payload)
	}
}
