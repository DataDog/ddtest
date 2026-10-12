// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

// ddto's Git worker owns the invocation's telemetry application. The test
// processes that use its state report their telemetry under that
// application, numbering their requests with the worker's in one sequence.
const (
	// CLITelemetryFile holds the invocation's telemetry.SharedApp.
	CLITelemetryFile = "telemetry.json"
	// cliTelemetrySeqDir holds one file for each request number taken.
	cliTelemetrySeqDir = "seq"
	// cliTelemetryFailurePrefix names a test process's undelivered request.
	cliTelemetryFailurePrefix = "telemetry-failure-"
)

// CLITelemetrySequence numbers the requests of the processes that report the
// invocation's telemetry application. A number is taken by creating its file
// exclusively, so no two requests share one. Each process creates the number
// after the highest that exists, so the numbers taken are 1 to n without
// gaps, and a request numbered after another, in any process, has a higher
// number.
type CLITelemetrySequence struct {
	dir      string
	mu       sync.Mutex
	last     int64 // a number that exists
	reserved int64 // the owner's first number, taken before any other
	failed   bool
	// report reports, once, that the sequence fell back to local numbers.
	report func()
}

// cliTelemetryMaxSteps bounds the search for a free number under contention.
const cliTelemetryMaxSteps = 1 << 10

// Next returns the next request number. If the sequence's directory cannot
// be used, it continues this process's numbers, which may then repeat
// another process's. Files are created and probed without entering Go's
// test log (see openUnlogged).
func (s *CLITelemetrySequence) Next() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved != 0 {
		n := s.reserved
		s.reserved = 0
		s.last = max(s.last, n)
		return n
	}
	return s.take()
}

// Reserve takes the number of the next request now. The owner reserves the
// number of its final request before that request takes its data, so that a
// fallback's log leaves with it.
func (s *CLITelemetrySequence) Reserve() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved == 0 {
		s.reserved = s.take()
	}
}

// take takes the next number; s.mu is held.
func (s *CLITelemetrySequence) take() int64 {
	var err error
	for range cliTelemetryMaxSteps {
		n := s.highest() + 1
		if err = createUnlogged(s.path(n)); err == nil {
			s.last = n
			return n
		}
		if !errors.Is(err, fs.ErrExist) {
			break
		}
		s.last = n
	}
	s.last++
	if !s.failed {
		s.failed = true
		if err != nil {
			log.Debug("civisibility: CLI telemetry sequence unavailable: %s", err.Error())
		}
		if s.report != nil {
			s.report()
		}
	}
	return s.last
}

// highest finds the highest number taken, at least s.last: it doubles its
// step from s.last while numbers exist, then bisects the last step.
func (s *CLITelemetrySequence) highest() int64 {
	low, step := s.last, int64(1)
	for step < 1<<40 && s.exists(low+step) {
		low += step
		step *= 2
	}
	high := low + step
	for high-low > 1 {
		middle := low + (high-low)/2
		if s.exists(middle) {
			low = middle
		} else {
			high = middle
		}
	}
	s.last = low
	return low
}

func (s *CLITelemetrySequence) exists(n int64) bool {
	return existsUnlogged(s.path(n))
}

func (s *CLITelemetrySequence) path(n int64) string {
	return filepath.Join(s.dir, strconv.FormatInt(n, 10))
}

// WriteCLITelemetry records app as the invocation's telemetry application in
// dir and returns its owner's sequence, whose first number, for app-started,
// precedes every test process's. The owner logs a fallback to local numbers.
func WriteCLITelemetry(dir string, app telemetry.SharedApp) (*CLITelemetrySequence, error) {
	seq := &CLITelemetrySequence{dir: filepath.Join(dir, cliTelemetrySeqDir), reserved: 1, report: func() {
		telemetry.Log(telemetry.NewRecord(telemetry.LogError, "civisibility: CLI telemetry sequence unavailable"))
	}}
	if err := os.Mkdir(seq.dir, 0o700); err != nil {
		return nil, err
	}
	if err := createUnlogged(seq.path(1)); err != nil {
		return nil, err
	}
	if err := WriteCLIGitStateFile(dir, CLITelemetryFile, app); err != nil {
		return nil, err
	}
	return seq, nil
}

// CLITelemetry is the invocation's telemetry application as a test process
// reports it.
type CLITelemetry struct {
	App      telemetry.SharedApp
	Sequence *CLITelemetrySequence
	// Failed records an undelivered request, with its last status code or 0
	// for a network error, for the owner to report.
	Failed func(statusCode int)
}

// Tags of the failures that test processes record for the owner to report.
const (
	CLITelemetrySequenceFailure = "reason:sequence"
	cliTelemetryNetworkFailure  = "type:network"
)

// HandedCLITelemetry returns the invocation's telemetry application when
// ddto handed its state to this process (see HandedCLIGitState). Otherwise
// the process reports its own application.
func HandedCLITelemetry() (CLITelemetry, bool) {
	dir, record := HandedCLIGitState()
	if record == nil {
		return CLITelemetry{}, false
	}
	var app telemetry.SharedApp
	if err := ReadCLIGitStateFile(dir, CLITelemetryFile, &app); err != nil || app.RuntimeID == "" {
		reason := "invalid"
		if err != nil {
			log.Debug("civisibility: CLI telemetry application unavailable: %s", err.Error())
			reason = "unreadable"
			if errors.Is(err, fs.ErrNotExist) {
				reason = "missing"
			}
		}
		telemetry.Log(telemetry.NewRecord(telemetry.LogError, "civisibility: CLI telemetry application unavailable"), telemetry.WithTags([]string{"reason:" + reason}))
		return CLITelemetry{}, false
	}
	// The owner reports a test process's failures, including one in its
	// final request, which no later request of its own could carry.
	return CLITelemetry{
		App: app,
		Sequence: &CLITelemetrySequence{dir: filepath.Join(dir, cliTelemetrySeqDir), report: func() {
			recordCLITelemetryFailure(dir, CLITelemetrySequenceFailure)
		}},
		Failed: func(statusCode int) {
			tag := cliTelemetryNetworkFailure
			if statusCode != 0 {
				tag = "status_code:" + strconv.Itoa(statusCode)
			}
			recordCLITelemetryFailure(dir, tag)
		},
	}, true
}

// recordCLITelemetryFailure records a test process's failure, identified by
// its telemetry tag, in a new file.
func recordCLITelemetryFailure(dir, tag string) {
	for range 8 {
		file, err := openUnlogged(filepath.Join(dir, cliTelemetryFailurePrefix+strconv.FormatUint(rand.Uint64(), 36)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err == nil {
			_, _ = file.WriteString(tag)
			_ = file.Close()
		}
		return
	}
}

// CLITelemetryFailures counts the failures that test processes recorded
// under the invocation's application, by tag: CLITelemetrySequenceFailure,
// or the status code of an undelivered request ("type:network" without one).
func CLITelemetryFailures(dir string) map[string]int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var failures map[string]int
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), cliTelemetryFailurePrefix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || len(data) == 0 {
			continue
		}
		if failures == nil {
			failures = map[string]int{}
		}
		failures[string(data)]++
	}
	return failures
}
