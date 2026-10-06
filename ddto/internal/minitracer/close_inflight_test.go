package minitracer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
)

func TestCloseReportsFailedBackgroundDelivery(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	c, err := New(Config{MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.StartSpan(context.Background(), "test", SpanType("test"))
	s.Finish()
	<-entered
	result := make(chan error, 1)
	go func() { result <- c.Close(context.Background()) }()
	waitFor(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed })
	close(release)
	if err := <-result; err == nil {
		t.Fatal("Close hid a failed background delivery")
	}
	if c.DroppedEvents() != 1 {
		t.Fatal("wrong dropped count", c.DroppedEvents())
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal("a later empty close reported an old failure", err)
	}
}
