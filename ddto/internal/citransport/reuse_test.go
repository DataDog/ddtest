package citransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestGzipReuseConcurrentPayloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compressed, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if int64(len(compressed)) != r.ContentLength {
			t.Error("content length changed")
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil || len(data) < 1 {
			t.Error("invalid compressed payload")
			return
		}
		if !bytes.Equal(data, bytes.Repeat(data[:1], len(data))) {
			t.Error("compressed payload crossed requests")
		}
		w.WriteHeader(202)
	}))
	defer server.Close()
	transport, err := New(Config{Endpoint: server.URL, Agentless: true, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 1; j <= 5; j++ {
				payload := bytes.Repeat([]byte{byte(i + 1)}, j*8192)
				if err := transport.Send(context.Background(), payload); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
}

type delayedBodyTransport struct{ request *http.Request }

func (transport *delayedBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request = request
	return nil, errors.New("synthetic network failure")
}
func TestSendSealsLateRequestReaders(t *testing.T) {
	for _, agentless := range []bool{false, true} {
		t.Run(map[bool]string{false: "agent", true: "gzip"}[agentless], func(t *testing.T) {
			rt := &delayedBodyTransport{}
			transport, err := New(Config{Endpoint: "http://fixture.invalid", Agentless: agentless, APIKey: "fixture", Attempts: 1, HTTPClient: &http.Client{Transport: rt}})
			if err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("original"), 1000)
			if err := transport.Send(context.Background(), payload); err == nil {
				t.Fatal("expected failure")
			}
			if rt.request == nil || rt.request.GetBody == nil {
				t.Fatal("lost replayable request body")
			}
			replay, err := rt.request.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				for i := 0; i < 100; i++ {
					clear(payload)
				}
				close(done)
			}()
			for _, reader := range []io.ReadCloser{rt.request.Body, replay} {
				data, err := io.ReadAll(reader)
				if err != nil || len(data) != 0 {
					t.Fatal("late reader accessed released buffer")
				}
				reader.Close()
			}
			<-done
		})
	}
}

func TestCompressorRetentionBound(t *testing.T) {
	compressor := &gzipCompressor{}
	compressor.writer = gzip.NewWriter(&compressor.buffer)
	compressor.buffer.Grow(TestCycleFlushBytes + 1)
	releaseCompressor(compressor)
	if compressor.buffer.Cap() != 0 {
		t.Fatal("oversized compressor buffer retained")
	}
}
