// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

type sharedRequest struct {
	Type        string                `json:"request_type"`
	RuntimeID   string                `json:"runtime_id"`
	SeqID       int64                 `json:"seq_id"`
	Application transport.Application `json:"application"`
	Host        transport.Host        `json:"host"`
	Payload     json.RawMessage       `json:"payload"`
	session     string
	rootSession string
}

// A process that reports another process's application sends its data under
// that application's identity, numbered in the shared sequence, without
// app-started or app-closing, which only the owner sends.
func TestSharedAppReportsOwnerIdentity(t *testing.T) {
	t.Setenv("DD_API_KEY", "")
	var mu sync.Mutex
	var requests []sharedRequest
	failChild := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request sharedRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		request.session, request.rootSession = r.Header.Get("DD-Session-ID"), r.Header.Get("DD-Root-Session-ID")
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		if failChild.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	taken := func() []sharedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]sharedRequest(nil), requests...)
	}

	app := NewSharedApp("cli-runtime", "cli-service", "cli-env", "1.2.3")
	app.RootSessionID = "root-session"
	app.Application.LanguageVersion = "go1.99.0"
	var seq atomic.Int64
	newShared := func(owner bool, failed func(int)) *client {
		t.Helper()
		shared := app
		if !owner {
			shared.Application.LanguageVersion = ""
		}
		c, err := NewClient("process-service", "process-env", "", ClientConfig{
			HTTPClient: server.Client(), AgentURL: server.URL, MetricsEnabled: true,
			Shared: &shared, SharedOwner: owner, SharedSeqID: func() int64 { return seq.Add(1) }, SendFailed: failed,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c.(*client)
	}

	owner := newShared(true, nil)
	// The owner's data waits for app-closing, and leaves with it.
	owner.Count(NamespaceCIVisibility, "owner.metric", nil).Submit(1)
	owner.AppStart()
	owner.flushStartup()
	var failures []int
	child := newShared(false, func(code int) { failures = append(failures, code) })
	child.ProductStarted(NamespaceCIVisibility)
	// Data recorded before the start waits for a regular flush.
	child.Count(NamespaceCIVisibility, "shared.metric", nil).Submit(1)
	child.AppStart()
	child.flushStartup()
	if got := taken(); len(got) != 1 || got[0].Type != "app-started" {
		t.Fatalf("startup requests: %+v", got)
	}

	child.AppStop()
	child.Flush()
	failChild.Store(true)
	child.Count(NamespaceCIVisibility, "shared.metric", nil).Submit(1)
	child.Flush()
	failChild.Store(false)
	owner.AppStop()
	owner.Flush()

	got := taken()
	if len(got) != 4 {
		t.Fatalf("requests: %+v", got)
	}
	for i, request := range got {
		if request.SeqID != int64(i+1) || request.RuntimeID != "cli-runtime" || request.session != "cli-runtime" || request.rootSession != "root-session" {
			t.Errorf("request %d identity: %+v", i, request)
		}
		if request.Application.ServiceName != "cli-service" || request.Application.Env != "cli-env" || request.Application.ServiceVersion != "1.2.3" || request.Host.Hostname != app.Host.Hostname {
			t.Errorf("request %d application: %+v %+v", i, request.Application, request.Host)
		}
		closing := strings.Contains(string(request.Payload), "app-closing") || request.Type == "app-closing"
		if (i == 3) != closing || (i == 3) != strings.Contains(string(request.Payload), "owner.metric") {
			t.Errorf("request %d (%s) closing=%t: %s", i, request.Type, closing, request.Payload)
		}
	}
	if got[0].Application.LanguageVersion != "go1.99.0" || got[1].Application.LanguageVersion != runtime.Version() {
		t.Errorf("language versions: owner %q, child %q", got[0].Application.LanguageVersion, got[1].Application.LanguageVersion)
	}
	if strings.Contains(string(got[1].Payload), "app-product-change") {
		t.Errorf("the child reported a product change: %s", got[1].Payload)
	}
	if len(failures) != 1 || failures[0] != http.StatusForbidden {
		t.Errorf("undelivered requests: %v", failures)
	}
}

func TestSharedAppRequiresSequence(t *testing.T) {
	app := NewSharedApp("cli-runtime", "service", "", "")
	if _, err := NewClient("service", "", "", ClientConfig{AgentURL: "http://localhost:8126", Shared: &app}); err == nil {
		t.Fatal("a shared application without a sequence was accepted")
	}
}
