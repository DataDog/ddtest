package citransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
)

func TestSendCancellationWhileLeakCheckPausesTransport(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(202) }))
	defer server.Close()
	transport, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	resume := cidelivery.PrepareLeakCheck()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- transport.Send(ctx, []byte("fixture")) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Send error: %v", err)
		}
	case <-time.After(time.Second):
		resume()
		t.Fatal("Send ignored deadline")
	}
	resume()
	if requests.Load() != 0 {
		t.Fatal("canceled send reached HTTP server")
	}
	if err := transport.Send(context.Background(), []byte("next")); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("gate remained paused")
	}
	transport.CloseIdleConnections()
}
