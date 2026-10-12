// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

// Package cligit is the worker that ddto's CLI starts once per invocation to do
// the Git work that every test process of a checkout would otherwise repeat.
// It runs in the invocation's directory, with the test processes' environment,
// so the SDK code it calls sees what a test process would see. It writes its
// results to a state directory that the CLI hands to the test processes; see
// utils.HandedCLIGitState. It also owns the invocation's telemetry
// application, which those test processes report under; see
// utils.HandedCLITelemetry.
package cligit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/gitupload"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

// The CLI writes lines to the worker's input:
//   - Proceed, a space and the test processes' Go version, once test
//     processes will receive the state; end of input without it stops the
//     worker;
//   - Exited, once go test has exited;
//   - Stop, a space and a reason, to stop the worker's work: it stops its git
//     commands and requests, removes its pack files, records what it did not
//     finish and closes the telemetry application.
//
// End of input before Exited also stops the worker: no one waits for it.
const (
	Proceed = "proceed"
	Exited  = "exited"
	Stop    = "stop"
)

// client is the part of the CI Visibility client that the worker uses.
type client interface {
	gitupload.Client
	GetSettings() (*net.SettingsResponseData, error)
}

// appTelemetry is the invocation's telemetry application, which the worker
// owns: see net.CLITelemetry.
type appTelemetry interface {
	Start(languageVersion string)
	Stop()
}

// newClient creates the CI Visibility client, whose requests stop when ctx is
// done, and the invocation's telemetry application in the state directory
// dir; tests replace it.
var newClient = func(ctx context.Context, dir, runtimeID string) (client, appTelemetry) {
	c, t := net.NewCLIClient(ctx, dir, runtimeID)
	if c == nil {
		return nil, nil
	}
	if t == nil {
		return c, nil
	}
	return c, t
}

// Run writes the checkout's Git record to dir, then waits for the CLI. Once
// told to proceed, it compares the local commits with the backend's while go
// test builds, a read-only request. Once a test process uses the state, it
// starts the invocation's telemetry application, which the test processes
// that use the state report under, and uploads the missing commits, as each
// test process would, and records the end of the upload; meanwhile it
// requests the settings, and if they enable impacted tests it records the
// base commit and modified files. It closes results once it has written them,
// or failed to, so that ddto records any missing result for the test
// processes waiting for it. It closes the telemetry application after go test
// exits. When go test exits without running a test process that uses the
// state, as when every result comes from its cache, the worker exits without
// changing the repository, uploading or sending telemetry. uploadWait, which
// the record hands to test processes, bounds their wait for the upload.
func Run(dir, runtimeID string, uploadWait time.Duration, input io.Reader, results io.Closer, interrupts <-chan os.Signal) error {
	defer results.Close()
	if infra.BoolEnv("DD_TRACE_DEBUG", false) {
		log.SetLevel(log.LevelDebug)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	utils.SetGitCommandContext(ctx)
	defer utils.SetGitCommandContext(context.Background())
	// The CLI's lines, and a signal such as the terminal's interrupt, stop
	// the worker from its first git command on.
	cli := listen(bufio.NewReader(input), cancel)
	go cli.interrupt(ctx, interrupts, cancel)
	record := collectRecord(ctx)
	if ctx.Err() != nil {
		return nil
	}
	record.UploadWaitMs = uploadWait.Milliseconds()
	utils.UseCLIGitState(dir, record)
	// The telemetry application exists before the record, which ddto waits
	// for before it hands the state over.
	c, app := newClient(ctx, dir, runtimeID)
	if err := utils.WriteCLIGitStateFile(dir, utils.CLIGitRecordFile, record); err != nil {
		return err
	}
	goVersion, ok := cli.goAhead(ctx)
	if !ok {
		return nil
	}
	uploadEnabled := infra.BoolEnv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, true)
	search := searchAhead(c, uploadEnabled)
	if !waitUsed(ctx, dir, cli.exited) {
		// No test process needs the search any more. Its git commands end,
		// interrupted or killed, only while this process runs.
		cancel()
		if search != nil {
			_, _ = search()
		}
		return nil
	}
	utils.FetchCLIHeadCommit()
	if app != nil {
		started := make(chan struct{})
		go func() {
			defer close(started)
			app.Start(goVersion)
		}()
		defer func() {
			<-started
			reportTestProcessFailures(dir)
			app.Stop()
		}()
	}
	// Test processes request the settings while they upload.
	settings := make(chan settingsResult, 1)
	if c != nil {
		go func() {
			response, err := c.GetSettings()
			settings <- settingsResult{response, err}
		}()
	}
	uploaded := upload(ctx, cli, c, uploadEnabled, search)
	err := writeState(dir, utils.CLIGitUploadFile, uploaded)
	if err == nil {
		err = writeState(dir, utils.CLIGitImpactedFile, impacted(ctx, c, uploaded, settings))
	}
	_ = results.Close()
	// Test processes send their telemetry before go test exits, and the
	// application closes after them, in one request with the worker's data,
	// even when the worker stopped its work earlier.
	<-cli.exited
	return err
}

// collectRecord reads the checkout's Git record; tests replace it.
var collectRecord = func(context.Context) utils.CLIGitRecord {
	return utils.CollectCLIGitRecord()
}

// cliInput follows the CLI's lines.
type cliInput struct {
	// proceed receives the test processes' Go version once the CLI writes
	// Proceed; it is closed if the input ends without it.
	proceed chan string
	// exited is closed once go test has exited, or the CLI has gone.
	exited chan struct{}
	mu     sync.Mutex
	reason string // why the worker stopped
}

// listen reads the CLI's lines from the start; stop cancels the worker's work.
func listen(reader *bufio.Reader, stop context.CancelFunc) *cliInput {
	in := &cliInput{proceed: make(chan string, 1), exited: make(chan struct{})}
	go func() {
		proceeded, exited := false, false
		for {
			line, err := reader.ReadString('\n')
			verb, value, _ := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
			switch {
			case verb == Proceed && strings.HasSuffix(line, "\n") && !proceeded:
				proceeded = true
				in.proceed <- value
			case verb == Exited && !exited:
				exited = true
				if !proceeded {
					// go test exited without the state, as when preparation
					// failed: nothing needs the worker's first git commands.
					stop()
				}
				close(in.exited)
			case verb == Stop:
				in.stopped(value)
				stop()
			}
			if err != nil {
				if !proceeded {
					close(in.proceed)
				}
				if !exited {
					in.stopped("the CLI exited")
					stop()
					close(in.exited)
				}
				return
			}
		}
	}()
	return in
}

// goAhead waits for the CLI to hand the state over and returns the test
// processes' Go version. A quick go test can exit before the worker waits:
// the proceed line, read before exited, still counts.
func (in *cliInput) goAhead(ctx context.Context) (string, bool) {
	select {
	case version, ok := <-in.proceed:
		return version, ok
	case <-ctx.Done():
		return "", false
	case <-in.exited:
	}
	select {
	case version, ok := <-in.proceed:
		return version, ok
	default:
		// go test exited without the state, as when preparation failed.
		return "", false
	}
}

// interrupt stops the worker's work on a signal, as Stop does: the
// terminal's interrupt reaches the whole process group, the worker included.
// The worker still waits for go test to close the telemetry application.
func (in *cliInput) interrupt(ctx context.Context, interrupts <-chan os.Signal, stop context.CancelFunc) {
	select {
	case <-interrupts:
		in.stopped("signal")
		stop()
	case <-ctx.Done():
	}
}

func (in *cliInput) stopped(reason string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.reason == "" {
		in.reason = reason
	}
}

func (in *cliInput) stopReason() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.reason
}

// writeStateFile writes a state file; tests replace it.
var writeStateFile = utils.WriteCLIGitStateFile

// writeState writes a result that test processes wait for. If it fails, ddto
// records the result once the worker closes its results.
func writeState(dir, name string, value any) error {
	err := writeStateFile(dir, name, value)
	if err != nil {
		logError("civisibility: CLI state not written", "file:"+name)
	}
	return err
}

// logError reports a failure of the worker's work as a telemetry log.
func logError(message string, tags ...string) {
	telemetry.Log(telemetry.NewRecord(telemetry.LogError, message), telemetry.WithTags(tags))
}

// reportTestProcessFailures reports, under the invocation's application, the
// failures that test processes recorded: requests they could not deliver
// under it, and sequences that fell back to local numbers.
func reportTestProcessFailures(dir string) {
	for tag, count := range utils.CLITelemetryFailures(dir) {
		message := "civisibility: test process telemetry request failed"
		if tag == utils.CLITelemetrySequenceFailure {
			message = "civisibility: test process telemetry sequence unavailable"
		}
		for range count {
			logError(message, tag)
		}
	}
}

type settingsResult struct {
	response *net.SettingsResponseData
	err      error
}

// searchAhead starts the upload's first commit search; the returned function
// waits for it.
func searchAhead(c client, uploadEnabled bool) func() (*gitupload.SearchCommitsResponse, error) {
	if c == nil || !uploadEnabled {
		return nil
	}
	var response *gitupload.SearchCommitsResponse
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err = gitupload.SearchCommits(c)
	}()
	return func() (*gitupload.SearchCommitsResponse, error) {
		<-done
		return response, err
	}
}

// upload runs the test processes' repository upload, starting from the
// commit search made ahead. Test processes that disable it do not wait for
// this result. When the CLI stops the worker, the git commands and requests
// stop, the pack files are removed and the result records the stop.
func upload(ctx context.Context, cli *cliInput, c client, uploadEnabled bool, search func() (*gitupload.SearchCommitsResponse, error)) utils.CLIGitUpload {
	if !uploadEnabled {
		return utils.CLIGitUpload{Error: "repository upload disabled"}
	}
	if c == nil {
		return utils.CLIGitUpload{Error: errors.New("no CI Visibility client").Error()}
	}
	hooks := gitupload.ClientHooks(c)
	repeat := hooks.GetSearchCommits
	hooks.GetSearchCommits = func() (*gitupload.SearchCommitsResponse, error) {
		hooks.GetSearchCommits = repeat
		return search()
	}
	bytes, err := gitupload.Upload(hooks)
	if ctx.Err() != nil {
		reason := cli.stopReason()
		log.Warn("civisibility: ddto stopped the repository upload after %d bytes: %s", bytes, reason)
		logError("civisibility: CLI repository upload stopped", "reason:"+stopTag(reason))
		return utils.CLIGitUpload{Bytes: bytes, Error: "ddto stopped the repository upload: " + reason}
	}
	if err != nil {
		log.Error("civisibility: error uploading repository changes: %s", err.Error())
		logError("civisibility: CLI repository upload failed")
		return utils.CLIGitUpload{Bytes: bytes, Error: err.Error()}
	}
	log.Debug("civisibility: uploaded %d bytes in pack files", bytes)
	return utils.CLIGitUpload{Bytes: bytes}
}

// stopTag turns a stop reason into a telemetry tag value.
func stopTag(reason string) string {
	if reason == "timeout" || reason == "signal" {
		return reason
	}
	return "other"
}

// impacted computes the impacted-tests state when the settings enable it, as
// a test process does after the upload: the base branch discovery and the diff
// then need the history that the upload may have fetched.
func impacted(ctx context.Context, c client, uploaded utils.CLIGitUpload, settings <-chan settingsResult) impactedtests.CLIState {
	if c == nil {
		return impactedtests.CLIState{Reason: "no CI Visibility client"}
	}
	if ctx.Err() != nil {
		return impactedtests.CLIState{Reason: "ddto stopped the worker"}
	}
	result := <-settings
	response, err := result.response, result.err
	if err == nil && response != nil && response.RequireGit && uploaded.Error == "" {
		response, err = c.GetSettings()
	}
	switch {
	case err != nil:
		logError("civisibility: CLI settings request failed")
		return impactedtests.CLIState{Reason: "settings: " + err.Error()}
	case response == nil || !response.ImpactedTestsEnabled:
		return impactedtests.CLIState{Reason: "impacted tests disabled by the settings"}
	case !infra.BoolEnv(constants.CIVisibilityImpactedTestsDetectionEnabled, true):
		return impactedtests.CLIState{Reason: "impacted tests disabled by the environment"}
	}
	state := impactedtests.ComputeCLIState()
	if ctx.Err() != nil {
		return impactedtests.CLIState{Reason: "ddto stopped the worker"}
	}
	if !state.Computed {
		logError("civisibility: CLI impacted tests state not computed")
	}
	return state
}

// waitUsed waits until a test process marks the state as used, or go test
// exits, or the CLI stops the worker.
func waitUsed(ctx context.Context, dir string, exited <-chan struct{}) bool {
	used := filepath.Join(dir, utils.CLIGitUsedFile)
	delay := time.Millisecond
	for {
		if _, err := os.Stat(used); err == nil {
			return true
		}
		select {
		case <-exited:
			_, err := os.Stat(used)
			return err == nil && ctx.Err() == nil
		case <-ctx.Done():
			return false
		case <-time.After(delay):
			delay = min(2*delay, 5*time.Millisecond)
		}
	}
}
