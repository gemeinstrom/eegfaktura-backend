package main

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hanging request must not use up the time the pod has for the broker and the mail worker.
func TestShutdownHTTP_CutsOffAtTheLimit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	go func() {
		if resp, err := http.Get("http://" + lis.Addr().String()); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not arrive")
	}

	began := time.Now()
	shutdownHTTP(srv, 50*time.Millisecond)
	assert.Less(t, time.Since(began), 5*time.Second)
}
