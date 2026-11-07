// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package usagetelemetry reports onboarding command usage without customer
// credentials. It is independent of instrumentation telemetry and test intake.
package usagetelemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/httptransport"
)

// ClientToken and IntakeSite may be set by release builds with -ldflags -X.
// The token belongs to the Datadog organization receiving usage telemetry.
// An empty token disables reporting, including all network requests.
var (
	ClientToken string
	IntakeSite  = "datadoghq.com"
)

const (
	requestTimeout = 2 * time.Second
	flushTimeout   = 3 * time.Second
)

// Config contains only release metadata and the independent usage destination.
// Transport permits testing delivery without contacting a real intake.
type Config struct {
	Version     string
	ClientToken string
	IntakeSite  string
	Transport   http.RoundTripper
}

type event struct {
	SchemaVersion int       `json:"schema_version"`
	Service       string    `json:"service"`
	Source        string    `json:"ddsource"`
	Message       string    `json:"message"`
	EventName     string    `json:"event_name"`
	AttemptID     string    `json:"attempt_id"`
	Command       string    `json:"command"`
	Mode          string    `json:"mode"`
	Version       string    `json:"version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Date          time.Time `json:"date"`
	Outcome       string    `json:"outcome,omitempty"`
	Phase         string    `json:"phase,omitempty"`
	Code          string    `json:"code,omitempty"`
	ExitCode      *int      `json:"exit_code,omitempty"`
	DurationMS    *int64    `json:"duration_ms,omitempty"`
}

// Attempt owns one top-level command's events. ObserveResult and Finish are
// called by command execution; background delivery never reads mutable state.
type Attempt struct {
	event    event
	started  time.Time
	endpoint string
	client   *http.Client
	pending  sync.WaitGroup
	finished bool
}

// Start emits a start event, or returns nil when disabled or unconfigured.
// It never uses DD_API_KEY, an Agent, or a customer's intake override.
func Start(command, mode string, config Config) *Attempt {
	if config.ClientToken == "" || disabled() || (command != "onboard" && command != "testdrive") {
		return nil
	}
	switch mode {
	case "discovery", "apply", "instructions", "validation", "configuration_check", "report":
	default:
		return nil
	}
	switch config.IntakeSite {
	case "datadoghq.com", "us3.datadoghq.com", "us5.datadoghq.com", "datadoghq.eu",
		"ap1.datadoghq.com", "ap2.datadoghq.com", "uk1.datadoghq.com":
	default:
		return nil
	}
	now := time.Now()
	client := httptransport.NewHTTPClient(requestTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.Transport != nil {
		client.Transport = config.Transport
	}
	endpoint := url.URL{
		Scheme: "https", Host: "browser-http-intake.logs." + config.IntakeSite,
		Path: "/api/v2/logs", RawQuery: url.Values{"dd-api-key": {config.ClientToken}}.Encode(),
	}
	a := &Attempt{
		event: event{
			SchemaVersion: 1, Service: "ddtest-cli", Source: "ddtest",
			AttemptID: rand.Text(), Command: command, Mode: mode,
			Version: config.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		},
		started:  now,
		endpoint: endpoint.String(),
		client:   client,
	}
	a.send("ddtest.command.started", now)
	a.event.Phase = "preparation"
	return a
}

func disabled() bool {
	for _, name := range []string{"DO_NOT_TRACK", "DDTEST_TELEMETRY_DISABLED"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED")))
	return err == nil && !value
}

// Finish sends the terminal event and waits a bounded time for both requests.
// Neither delivery failures nor cancellation change the command's exit status.
func (a *Attempt) Finish(exitCode int, cancelled bool) {
	if a == nil || a.finished {
		return
	}
	a.finished = true
	if cancelled {
		a.event.Outcome = "cancelled"
	} else if a.event.Outcome == "" || (exitCode != 0 && (a.event.Outcome == "succeeded" || a.event.Outcome == "handoff")) {
		a.event.Outcome = "failed"
		if exitCode == 0 {
			a.event.Outcome = "succeeded"
			if a.event.Mode == "discovery" || a.event.Mode == "apply" {
				a.event.Outcome = "handoff"
			}
		}
	}
	if exitCode == 0 && a.event.Mode == "configuration_check" && a.event.Outcome == "handoff" {
		a.event.Outcome = "succeeded"
	}
	duration := time.Since(a.started).Milliseconds()
	a.event.ExitCode, a.event.DurationMS = &exitCode, &duration
	a.send("ddtest.command.finished", time.Now())
	done := make(chan struct{})
	go func() {
		a.pending.Wait()
		close(done)
	}()
	timer := time.NewTimer(flushTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (a *Attempt) send(name string, date time.Time) {
	e := a.event
	e.Message, e.EventName, e.Date = name, name, date
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.pending.Add(1)
	go func() {
		defer a.pending.Done()
		request, err := http.NewRequest(http.MethodPost, a.endpoint, bytes.NewReader(body))
		if err != nil {
			return
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := a.client.Do(request)
		if err == nil {
			// Bound reads even when an intake or proxy returns an unexpected body.
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
		}
	}()
}
