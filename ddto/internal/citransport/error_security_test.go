package citransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type failingRequestTransport struct{ err error }

func (t failingRequestTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestRequestErrorsHideEndpointCredentials(t *testing.T) {
	for _, cause := range []error{errors.New("connection refused"), context.Canceled, context.DeadlineExceeded} {
		tr, err := New(Config{Endpoint: "http://fixture.invalid/events?token=SYNTHETIC_SECRET#PRIVATE_FRAGMENT", Attempts: 1, HTTPClient: &http.Client{Transport: failingRequestTransport{cause}}})
		if err != nil {
			t.Fatal(err)
		}
		err = tr.Send(context.Background(), []byte("payload"))
		if err == nil {
			t.Fatal("request failure was lost")
		}
		for _, secret := range []string{"SYNTHETIC_SECRET", "PRIVATE_FRAGMENT", "token="} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("credential escaped into error: %v", err)
			}
		}
		if !errors.Is(err, cause) {
			t.Fatal("sanitization lost the original error identity")
		}
		var requestError *url.Error
		if !errors.As(err, &requestError) {
			t.Fatal("sanitization lost the HTTP error type")
		}
	}
}

func TestResponseErrorsRedactEchoedCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, r.URL.String()+" api="+r.Header.Get("DD-API-KEY"), http.StatusForbidden)
	}))
	defer server.Close()
	tr, err := New(Config{Endpoint: server.URL + "?token=QUERY_SECRET", Agentless: true, APIKey: "API_SECRET", Attempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Send(context.Background(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("lost status: %v", err)
	}
	for _, secret := range []string{"QUERY_SECRET", "API_SECRET"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret escaped: %v", err)
		}
	}
}
