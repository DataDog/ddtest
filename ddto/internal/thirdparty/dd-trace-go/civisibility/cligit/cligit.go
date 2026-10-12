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
	"regexp"
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

// featureClient requests the settings and the data that they enable, as a
// test process does before its first test.
type featureClient interface {
	GetSettings() (*net.SettingsResponseData, error)
	GetKnownTests() (*net.KnownTestsResponseData, error)
	GetSkippableTests() (*net.SkippableTestsResponse, error)
	GetTestManagementTests() (*net.TestManagementTestsResponseDataModules, error)
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
// told to proceed, while go test builds, it compares the local commits with
// the backend's and requests what the test processes described in dir request
// before their first test, as they would, all read-only requests. Once a test
// process uses the state, it starts the invocation's telemetry application,
// which the test processes that use the state report under, and uploads the
// missing commits, as each test process would, and records the end of the
// upload; if the settings enable impacted tests it then records the base
// commit and modified files. It closes results once it has written them,
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
	tests := utils.HandedCLITests()
	c = asTestProcesses(c, goVersion, tests)
	uploadEnabled := infra.BoolEnv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, true)
	var search func() (*gitupload.SearchCommitsResponse, error)
	var requests func()
	started := time.Now()
	requestNow := func(trigger string) {
		clients := testProcessClients(c, goVersion, tests)
		log.Debug("civisibility: CLI requests ahead started [trigger:%s go_version:%s clients:%d after:%s]", trigger, goVersion, len(clients), time.Since(started).Round(time.Microsecond))
		search = searchAhead(c, uploadEnabled)
		requests = requestAhead(clients)
	}
	// While go test builds, the worker requests for the test processes if
	// every one of them runs. Otherwise it waits until go test links a test
	// binary, which it does only for a result that is not in its cache, or a
	// test process starts: a run whose results all come from Go's test cache
	// sends no request.
	switch {
	case tests != nil && tests.RunsEveryTest:
		requestNow("runs_every_test")
	case tests == nil:
		log.Debug("civisibility: CLI requests ahead wait for a test process: no test description")
		fallthrough
	default:
		if waitFor(ctx, dir, cli.exited, utils.CLIGitLinkingFile, utils.CLIGitUsedFile) {
			trigger := utils.CLIGitUsedFile
			if _, err := os.Stat(filepath.Join(dir, utils.CLIGitLinkingFile)); err == nil {
				trigger = utils.CLIGitLinkingFile
			}
			requestNow(trigger)
		}
	}
	if !waitUsed(ctx, dir, cli.exited) {
		// No test process needs the search or the requests any more. The
		// search's git commands end, interrupted or killed, only while this
		// process runs.
		if ctx.Err() != nil {
			log.Debug("civisibility: CLI requests ahead stopped: ddto stopped the worker [reason:%s started:%t]", cli.stopReason(), requests != nil)
		} else {
			log.Debug("civisibility: CLI requests ahead not needed: go test exited before a test process used the state [started:%t]", requests != nil)
		}
		cancel()
		if search != nil {
			_, _ = search()
		}
		if requests != nil {
			requests()
		}
		return nil
	}
	if requests == nil {
		requestNow(utils.CLIGitUsedFile)
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
	// The requests made ahead end before the application closes, with their
	// metrics; they ended long before, unless go test was quicker.
	defer func() {
		cancel()
		requests()
	}()
	// Test processes request the settings while they upload; the read cache
	// usually has them from the requests made ahead.
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

// asTestProcesses returns c requesting as the test processes do, with their
// Go version and architecture, so that its settings request is theirs; tests
// replace it.
var asTestProcesses = func(c client, goVersion string, tests *utils.CLITests) client {
	nc, ok := c.(net.Client)
	if !ok {
		return c
	}
	goarch := ""
	if tests != nil {
		goarch = tests.GOARCH
	}
	if clients := net.CLITestProcessClients(nc, goVersion, goarch, nil); len(clients) == 1 {
		if tc, ok := clients[0].(client); ok {
			return tc
		}
	}
	return c
}

// testProcessClients returns clients that request as the test processes of
// tests' packages do, one per service that they report; tests replace it.
var testProcessClients = func(c client, goVersion string, tests *utils.CLITests) []featureClient {
	nc, ok := c.(net.Client)
	if !ok {
		return nil
	}
	goarch, dirs := "", []string(nil)
	if tests != nil {
		goarch = tests.GOARCH
		for _, p := range tests.Packages {
			dirs = append(dirs, p.Dir)
		}
	}
	var clients []featureClient
	for _, tc := range net.CLITestProcessClients(nc, goVersion, goarch, dirs) {
		clients = append(clients, tc)
	}
	return clients
}

// requestAhead requests, while go test builds, what the test processes request
// before their first test: the settings, and the known tests, skippable tests
// and test management data that the settings enable. The read cache shares
// the responses with the test processes; one whose request differs requests
// for itself, as before. The returned function waits for the requests.
func requestAhead(clients []featureClient) func() {
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Go(func() { requestFeatures(i, c) })
	}
	return wg.Wait
}

// requestFeatures requests what one test process would, deciding as
// ensureAdditionalFeaturesInitialization does. Test processes request again,
// and report, what fails here.
func requestFeatures(client int, c featureClient) {
	started := time.Now()
	settings, err := c.GetSettings()
	switch {
	case err != nil:
		log.Debug("civisibility: CLI request ahead settings failed [client:%d duration:%s error:%s]", client, time.Since(started).Round(time.Microsecond), redactError(err))
		return
	case settings == nil:
		log.Debug("civisibility: CLI request ahead settings empty [client:%d]", client)
		return
	case settings.RequireGit:
		// Settings that require Git change after the upload, which waits
		// for a test process.
		log.Debug("civisibility: CLI requests ahead stop: the settings require Git [client:%d]", client)
		return
	}
	testManagement := settings.TestManagement.Enabled && infra.BoolEnv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, true)
	var wg sync.WaitGroup
	result := func(name string, err error) {
		if err != nil {
			log.Debug("civisibility: CLI request ahead %s failed [client:%d error:%s]", name, client, redactError(err))
		}
	}
	if settings.KnownTestsEnabled {
		wg.Go(func() { _, err := c.GetKnownTests(); result("known tests", err) })
	}
	if settings.TestsSkipping {
		wg.Go(func() { _, err := c.GetSkippableTests(); result("skippable tests", err) })
	}
	if testManagement {
		wg.Go(func() { _, err := c.GetTestManagementTests(); result("test management", err) })
	}
	wg.Wait()
	log.Debug("civisibility: CLI requests ahead finished [client:%d known_tests:%t skippable:%t test_management:%t duration:%s]", client, settings.KnownTestsEnabled, settings.TestsSkipping, testManagement, time.Since(started).Round(time.Microsecond))
}

// urlUserinfo matches the user and password of a URL, which an error such as
// url.Error repeats: as Go's URL parser, everything in the authority up to its
// last @.
var urlUserinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/?#\s]*@`)

// redactError returns err's message without the credentials of a URL in it.
func redactError(err error) string {
	return urlUserinfo.ReplaceAllString(err.Error(), "${1}[redacted]@")
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
	return waitFor(ctx, dir, exited, utils.CLIGitUsedFile)
}

// waitFor waits until one of the named files exists in dir, or go test exits,
// or the CLI stops the worker; it reports whether one exists.
func waitFor(ctx context.Context, dir string, exited <-chan struct{}, names ...string) bool {
	exists := func() bool {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		return false
	}
	delay := time.Millisecond
	for {
		if exists() {
			return true
		}
		select {
		case <-exited:
			return exists() && ctx.Err() == nil
		case <-ctx.Done():
			return false
		case <-time.After(delay):
			delay = min(2*delay, 5*time.Millisecond)
		}
	}
}
