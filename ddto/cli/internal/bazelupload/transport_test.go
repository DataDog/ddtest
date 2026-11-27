package bazelupload

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Like the rules uploader, an intake whose certificate fails verification is
// not retried: another attempt cannot pass.
func TestUntrustedCertificateIsNotRetried(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	send := newSender(Options{}, 1)
	result := send.post(context.Background(), outbound{url: server.URL, body: []byte("{}"), headers: make(http.Header)}, nil)
	if result.Attempts != 1 || result.Success || result.Err == nil || requests.Load() != 0 {
		t.Fatalf("unexpected delivery: %+v after %d requests", result, requests.Load())
	}
}
