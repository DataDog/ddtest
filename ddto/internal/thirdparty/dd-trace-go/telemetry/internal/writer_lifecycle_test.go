package internal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

type tracedRoundTrip func(*http.Request) (*http.Response, error)

func (f tracedRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The transport's EOF handshake must finish before Flush returns. Closing an
// unread body can otherwise leave Go's response draining in a background worker.
func TestWriterFlushWaitsForResponseCompletion(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, strings.Repeat("response", 512))
			}))
			defer server.Close()
			standard := http.DefaultTransport.(*http.Transport).Clone()
			defer standard.CloseIdleConnections()
			idle, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			client := &http.Client{Timeout: 2 * time.Second, Transport: tracedRoundTrip(func(r *http.Request) (*http.Response, error) {
				trace := &httptrace.ClientTrace{PutIdleConn: func(error) {
					close(idle)
					<-release
				}}
				return standard.RoundTrip(r.WithContext(httptrace.WithClientTrace(r.Context(), trace)))
			})}
			endpoint, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewWriter(WriterConfig{HTTPClient: client, Endpoints: []*http.Request{endpoint}})
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				requests []EndpointRequestResult
				err      error
			}
			done := make(chan result, 1)
			go func() { requests, err := writer.Flush(transport.AppClosing{}); done <- result{requests, err} }()
			select {
			case <-idle:
			case <-time.After(3 * time.Second):
				t.Fatal("response never reached the idle pool")
			}
			select {
			case <-done:
				t.Fatal("Flush returned while the response transport was still processing")
			case <-time.After(20 * time.Millisecond):
			}
			unblock()
			select {
			case result := <-done:
				if (result.err != nil) != (status >= 300) || len(result.requests) != 1 || result.requests[0].StatusCode != status {
					t.Fatalf("response result changed: %+v, %v", result.requests, result.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Flush did not complete after the response was released")
			}
		})
	}
}
