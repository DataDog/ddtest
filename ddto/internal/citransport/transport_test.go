package citransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeferredTransportOwnsItsConnections(t *testing.T) {
	callerTransport := http.DefaultTransport.(*http.Transport).Clone()
	caller := &http.Client{Transport: callerTransport}
	transport, err := New(Config{Endpoint: "http://fixture.invalid", HTTPClient: caller, CloseIdleAfterSend: true})
	if err != nil {
		t.Fatal(err)
	}
	if transport.client == caller || transport.client.Transport == callerTransport {
		t.Fatal("deferred delivery borrowed the caller's transport")
	}
	if caller.Transport != callerTransport {
		t.Fatal("changed the caller's client")
	}
	transport, err = New(Config{Endpoint: "http://fixture.invalid", CloseIdleAfterSend: true})
	if err != nil || transport.client.Transport == http.DefaultTransport || transport.client.Transport == nil {
		t.Fatal("deferred delivery borrowed the process default transport", err)
	}
}

// A test can replace http.DefaultTransport before Mini starts, with a mock
// that fails on unexpected requests or with settings of its own. Mini's
// delivery never goes through it, also with a client that has no transport.
func TestDeliveryNeverUsesTheDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	var replaced atomic.Int32
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		replaced.Add(1)
		return nil, errors.New("the replaced default transport")
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(202)
	}))
	defer server.Close()
	for _, client := range []*http.Client{nil, {Timeout: 5 * time.Second}} {
		transport, err := New(Config{Endpoint: server.URL, HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		if err := transport.Send(context.Background(), []byte("payload")); err != nil {
			t.Fatal(err)
		}
		transport.CloseIdleConnections()
	}
	if replaced.Load() != 0 {
		t.Fatal("delivery went through the replaced http.DefaultTransport")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLeakCheckpointLeavesCustomTransportOwnershipAlone(t *testing.T) {
	custom := &customConnectionTransport{}
	transport, err := New(Config{Endpoint: "http://fixture.invalid", HTTPClient: &http.Client{Transport: custom}})
	if err != nil {
		t.Fatal(err)
	}
	transport.CloseOwnedIdleConnections()
	if custom.closed.Load() != 0 {
		t.Fatal("goleak checkpoint closed a borrowed custom transport")
	}
	transport.CloseIdleConnections()
	if custom.closed.Load() != 1 {
		t.Fatal("explicit shutdown lost the custom transport's lifecycle")
	}
}

type customConnectionTransport struct{ closed atomic.Int32 }

func (*customConnectionTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 202, Body: http.NoBody}, nil
}
func (t *customConnectionTransport) CloseIdleConnections() { t.closed.Add(1) }

func TestAgentlessGzipRoundTripsPayloadShapes(t *testing.T) {
	random := make([]byte, 64<<10)
	if _, err := rand.NewChaCha8([32]byte{1}).Read(random); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("small payload"), bytes.Repeat([]byte("test.name:subtest,error.type:fixture;"), 8000), random, bytes.Repeat([]byte("x"), TestCycleMaxPayloadBytes)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Content-Encoding") != "gzip" {
				t.Error("lost standard gzip encoding")
			}
			reader, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer reader.Close()
			got, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(data, got) {
				t.Error("gzip changed payload bytes", err)
			}
			w.WriteHeader(202)
		}))
		transport, err := New(Config{Endpoint: server.URL, Agentless: true, APIKey: "fixture", CloseIdleAfterSend: true})
		if err != nil {
			t.Fatal(err)
		}
		err = transport.Send(context.Background(), data)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeliveryModesAndRetries(t *testing.T) {
	for _, agentless := range []bool{false, true} {
		t.Run(map[bool]string{false: "agent", true: "agentless"}[agentless], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader := io.Reader(r.Body)
				if agentless {
					if r.Header.Get("dd-api-key") != "fixture-key" || r.Header.Get("Content-Encoding") != "gzip" {
						t.Error("missing agentless headers")
					}
					gz, err := gzip.NewReader(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer gz.Close()
					reader = gz
				} else {
					if r.Header.Get("dd-api-key") != "" || r.Header.Get("X-Datadog-EVP-Subdomain") != "citestcycle-intake" {
						t.Error("invalid agent headers")
					}
				}
				data, err := io.ReadAll(reader)
				if err != nil || string(data) != "payload" {
					t.Error("retry changed body")
				}
				if calls.Add(1) == 1 {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(202)
				}
			}))
			defer server.Close()
			transport, err := New(Config{Endpoint: server.URL, Agentless: agentless, APIKey: "fixture-key", RetryDelay: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if err = transport.Send(context.Background(), []byte("payload")); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatal("wrong retry count")
			}
		})
	}
}
func TestPermanentFailureRedirectAndCancellation(t *testing.T) {
	for _, status := range []int{401, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
			}))
			defer server.Close()
			transport, err := New(Config{Endpoint: server.URL, Agentless: true, APIKey: "private-fixture-key", RetryDelay: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if err = transport.Send(context.Background(), []byte("payload")); err == nil {
				t.Fatal("accepted failed delivery")
			}
			if calls.Load() != 1 {
				t.Fatal("retried permanent failure or followed redirect")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	transport, err := New(Config{Endpoint: server.URL, RetryDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = transport.Send(ctx, []byte("payload")); err != context.DeadlineExceeded {
		t.Fatalf("lost cancellation: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("backoff ignored context")
	}
}
func TestInvalidConfiguration(t *testing.T) {
	for _, c := range []Config{{Endpoint: "file:///tmp/file"}, {Endpoint: "http://user:secret@localhost"}, {Endpoint: "http://localhost", Agentless: true}, {Endpoint: "http://localhost", Attempts: -1}} {
		if _, err := New(c); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

func TestDeliveryErrorsKeepTheirCause(t *testing.T) {
	const key = "private-fixture-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("payload rejected: missing field"))
	}))
	transport, err := New(Config{Endpoint: server.URL, Agentless: true, APIKey: key, RetryDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	err = transport.Send(context.Background(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 400: payload rejected: missing field (Status: Bad Request)") || strings.Contains(err.Error(), key) {
		t.Fatalf("status error: %v", err)
	}
	server.Close() // Later requests fail at the network layer.
	transport, err = New(Config{Endpoint: server.URL, Agentless: true, APIKey: key, Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = transport.Send(context.Background(), []byte("payload"))
	var urlErr *url.Error
	if err == nil || !errors.As(err, &urlErr) || !strings.Contains(err.Error(), "connect") || strings.Contains(err.Error(), key) {
		t.Fatalf("network error lost its cause: %v", err)
	}
}

// Concurrent senders keep one idle connection each on the cloned standard
// transport, never lowering a larger caller setting or changing the caller's
// transport or a custom RoundTripper.
func TestIdleConnectionLimitForConcurrentSenders(t *testing.T) {
	limit := func(c Config) (int, http.RoundTripper) {
		t.Helper()
		c.Endpoint = "http://intake.invalid/api/v2/citestcycle"
		transport, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		if standard, ok := transport.client.Transport.(*http.Transport); ok {
			return standard.MaxIdleConnsPerHost, standard
		}
		return -1, transport.client.Transport
	}
	if got, _ := limit(Config{}); got != 0 {
		t.Fatalf("limit changed without a request: %d", got)
	}
	if got, _ := limit(Config{MaxIdleConnsPerHost: 4}); got != 4 {
		t.Fatalf("concurrent sender limit: %d", got)
	}
	caller := &http.Transport{MaxIdleConnsPerHost: 10}
	got, clone := limit(Config{MaxIdleConnsPerHost: 4, HTTPClient: &http.Client{Transport: caller}})
	if got != 10 || clone == http.RoundTripper(caller) || caller.MaxIdleConnsPerHost != 10 {
		t.Fatalf("caller transport not preserved: clone=%d caller=%d", got, caller.MaxIdleConnsPerHost)
	}
	custom := roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	if got, rt := limit(Config{MaxIdleConnsPerHost: 4, HTTPClient: &http.Client{Transport: custom}}); got != -1 || rt == nil {
		t.Fatal("custom RoundTripper replaced")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Requests over TLS share one connection per host, so a dial that another
// request's connection made unneeded cannot finish after delivery returns;
// plain HTTP, as to a local Agent, keeps concurrent connections.
func TestTLSDeliveryUsesOneConnectionPerHost(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	for endpoint, want := range map[string]int{
		"https://intake.invalid/api/v2/citestcycle": 1,
		"http://127.0.0.1:8126/evp_proxy/v2":        0,
	} {
		transport, err := New(Config{Endpoint: endpoint, APIKey: "key", Agentless: strings.HasPrefix(endpoint, "https")})
		if err != nil {
			t.Fatal(err)
		}
		standard, ok := transport.client.Transport.(*http.Transport)
		if !ok || standard.MaxConnsPerHost != want {
			t.Fatalf("%s: %T with %d connections per host; want %d", endpoint, transport.client.Transport, standard.MaxConnsPerHost, want)
		}
	}
	proxied := http.DefaultTransport.(*http.Transport).Clone()
	proxied.Proxy = func(*http.Request) (*url.URL, error) { return url.Parse("https://proxy.invalid") }
	transport, err := New(Config{Endpoint: "http://127.0.0.1:8126/evp_proxy/v2", HTTPClient: &http.Client{Transport: proxied}})
	if err != nil {
		t.Fatal(err)
	}
	if standard := transport.client.Transport.(*http.Transport); standard.MaxConnsPerHost != 1 || proxied.MaxConnsPerHost != 0 {
		t.Fatalf("through an https proxy: %d connections per host, caller's %d", standard.MaxConnsPerHost, proxied.MaxConnsPerHost)
	}
}
