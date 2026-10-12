// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package cligit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

type fakeClient struct {
	mu                          sync.Mutex
	searches, uploads, settings int
	impacted                    bool
}

func (c *fakeClient) GetSettings() (*net.SettingsResponseData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings++
	return &net.SettingsResponseData{ImpactedTestsEnabled: c.impacted}, nil
}

func (c *fakeClient) GetCommits([]string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.searches++
	return nil, nil
}

func (c *fakeClient) SendPackFiles(string, []string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uploads++
	return 42, nil
}

func (c *fakeClient) counts() (searches, uploads int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.searches, c.uploads
}

// usedState returns a state directory that a test process has used.
func usedState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, utils.CLIGitUsedFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// gitRepository makes the current directory a one-commit repository.
func gitRepository(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Worker", "-c", "user.email=worker@example.invalid", "commit", "--allow-empty", "-m", "Worker"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestRunUploadsOnlyWhenTold(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	fake := &fakeClient{}
	original := newClient
	newClient = func(context.Context, string, string) (client, appTelemetry) { return fake, nil }
	t.Cleanup(func() { newClient = original })

	stopped := t.TempDir()
	if err := Run(stopped, "", time.Minute, endAfterRecord{stopped}, newResults(), nil); err != nil {
		t.Fatal(err)
	}
	var record utils.CLIGitRecord
	if err := utils.ReadCLIGitStateFile(stopped, utils.CLIGitRecordFile, &record); err != nil || record.Commit.Sha == "" {
		t.Fatalf("record %+v, %v", record, err)
	}
	if searches, _ := fake.counts(); searches != 0 {
		t.Fatalf("worker without a go-ahead searched %d times", searches)
	}
	if _, err := os.Stat(filepath.Join(stopped, utils.CLIGitUploadFile)); !os.IsNotExist(err) {
		t.Fatalf("worker without a go-ahead recorded an upload: %v", err)
	}

	utils.ResetHandedCLIGitStateForTesting()
	// Told to proceed, a worker that no test process used searches ahead,
	// a read-only request, but does not upload.
	unusedClient := &fakeClient{}
	newClient = func(context.Context, string, string) (client, appTelemetry) { return unusedClient, nil }
	unused := t.TempDir()
	if err := Run(unused, "", time.Minute, strings.NewReader(Proceed+"\n"+Exited+"\n"), newResults(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unused, utils.CLIGitUploadFile)); !os.IsNotExist(err) {
		t.Fatalf("unused worker recorded an upload: %v", err)
	}
	if _, uploads := unusedClient.counts(); uploads != 0 {
		t.Fatalf("unused worker uploaded %d pack files", uploads)
	}
	newClient = func(context.Context, string, string) (client, appTelemetry) { return fake, nil }

	utils.ResetHandedCLIGitStateForTesting()
	proceeded := usedState(t)
	if err := Run(proceeded, "", time.Minute, strings.NewReader(Proceed+"\n"+Exited+"\n"), newResults(), nil); err != nil {
		t.Fatal(err)
	}
	var upload utils.CLIGitUpload
	if err := utils.ReadCLIGitStateFile(proceeded, utils.CLIGitUploadFile, &upload); err != nil || upload.Bytes != 42 || upload.Error != "" {
		t.Fatalf("upload %+v, %v", upload, err)
	}
	if searches, uploads := fake.counts(); searches != 1 || uploads != 1 {
		t.Fatalf("searches %d, uploads %d", searches, uploads)
	}
	var state impactedtests.CLIState
	if err := utils.ReadCLIGitStateFile(proceeded, utils.CLIGitImpactedFile, &state); err != nil || state.Computed || state.Reason == "" {
		t.Fatalf("impacted tests disabled by the settings: %+v, %v", state, err)
	}

	// With impacted tests enabled, the worker computes the analyzer's input.
	utils.ResetHandedCLIGitStateForTesting()
	fake.impacted = true
	enabled := usedState(t)
	if err := Run(enabled, "", time.Minute, strings.NewReader(Proceed+"\n"+Exited+"\n"), newResults(), nil); err != nil {
		t.Fatal(err)
	}
	if err := utils.ReadCLIGitStateFile(enabled, utils.CLIGitImpactedFile, &state); err != nil || !state.Computed || state.CurrentCommit == "" {
		t.Fatalf("impacted tests enabled: %+v, %v", state, err)
	}

	utils.ResetHandedCLIGitStateForTesting()
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	disabled := usedState(t)
	if err := Run(disabled, "", time.Minute, strings.NewReader(Proceed+"\n"+Exited+"\n"), newResults(), nil); err != nil {
		t.Fatal(err)
	}
	if searches, _ := fake.counts(); searches != 2 {
		t.Fatalf("disabled upload searched: %d", searches)
	}
	if err := utils.ReadCLIGitStateFile(disabled, utils.CLIGitUploadFile, &upload); err != nil || upload.Error == "" {
		t.Fatalf("disabled upload %+v, %v", upload, err)
	}
}

// endAfterRecord ends the worker's input once its record exists, as ddto
// closes it after reading the record without handing the state over.
type endAfterRecord struct{ dir string }

func (r endAfterRecord) Read([]byte) (int, error) {
	for {
		if _, err := os.Stat(filepath.Join(r.dir, utils.CLIGitRecordFile)); err == nil {
			return 0, io.EOF
		}
		time.Sleep(time.Millisecond)
	}
}

// fakeResults records that the worker closed its results.
type fakeResults struct {
	once   sync.Once
	closed chan struct{}
}

func newResults() *fakeResults { return &fakeResults{closed: make(chan struct{})} }

func (r *fakeResults) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

type fakeTelemetry struct {
	mu       sync.Mutex
	versions []string
	stops    int
}

func (f *fakeTelemetry) Start(languageVersion string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versions = append(f.versions, languageVersion)
}

func (f *fakeTelemetry) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
}

func (f *fakeTelemetry) calls() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.versions...), f.stops
}

// The worker starts the invocation's telemetry application once a test
// process uses the state, with the test processes' Go version, and closes it
// only after go test exits; a worker that no test process used sends none.
func TestRunOwnsTelemetryApplication(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	app := &fakeTelemetry{}
	original := newClient
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, app }
	t.Cleanup(func() { newClient = original })

	if err := Run(t.TempDir(), "", time.Minute, strings.NewReader(Proceed+" go1.99.0\n"+Exited+"\n"), newResults(), nil); err != nil {
		t.Fatal(err)
	}
	if versions, stops := app.calls(); len(versions) != 0 || stops != 0 {
		t.Fatalf("unused worker started %v and stopped %d times", versions, stops)
	}

	utils.ResetHandedCLIGitStateForTesting()
	used := usedState(t)
	input, cli := io.Pipe()
	done := make(chan error, 1)
	results := newResults()
	go func() { done <- Run(used, "", time.Minute, input, results, nil) }()
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
		t.Fatal(err)
	}
	var state impactedtests.CLIState
	if err := utils.WaitCLIGitStateFile(used, utils.CLIGitImpactedFile, &state, time.Minute); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("worker exited before go test: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if versions, stops := app.calls(); len(versions) != 1 || versions[0] != "go1.99.0" || stops != 0 {
		t.Fatalf("while go test runs: started %v, stopped %d times", versions, stops)
	}
	select {
	case <-results.closed:
	default:
		t.Fatal("the worker did not close its results once it wrote them")
	}
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = cli.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if versions, stops := app.calls(); len(versions) != 1 || stops != 1 {
		t.Fatalf("after go test: started %v, stopped %d times", versions, stops)
	}
}

// When the worker cannot write a result, it closes its results at once, so
// that ddto records the result for the test processes waiting for it, and
// still closes the telemetry application only after go test exits.
func TestRunClosesResultsOnWriteFailure(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	t.Setenv(constants.CIVisibilityGitUploadEnabledEnvironmentVariable, "false")
	app := &fakeTelemetry{}
	originalClient, originalWrite := newClient, writeStateFile
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, app }
	writeStateFile = func(dir, name string, value any) error {
		if name == utils.CLIGitUploadFile {
			return errors.New("disk full")
		}
		return originalWrite(dir, name, value)
	}
	t.Cleanup(func() { newClient, writeStateFile = originalClient, originalWrite })

	used := usedState(t)
	input, cli := io.Pipe()
	results := newResults()
	done := make(chan error, 1)
	go func() { done <- Run(used, "", time.Minute, input, results, nil) }()
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-results.closed:
	case <-time.After(time.Minute):
		t.Fatal("the worker kept its results open after a failed write")
	}
	select {
	case err := <-done:
		t.Fatalf("worker exited before go test: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, stops := app.calls(); stops != 0 {
		t.Fatalf("telemetry stopped %d times before go test exited", stops)
	}
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = cli.Close()
	if err := <-done; err == nil {
		t.Fatal("the failed write was not reported")
	}
	if _, stops := app.calls(); stops != 1 {
		t.Fatalf("telemetry stopped %d times", stops)
	}
}

// blockingClient's pack upload waits until the worker stops its requests.
type blockingClient struct {
	fakeClient
	ctx     context.Context
	sending chan struct{}
}

func (c *blockingClient) SendPackFiles(string, []string) (int64, error) {
	close(c.sending)
	<-c.ctx.Done()
	return 0, c.ctx.Err()
}

// packDirectories lists the pack-objects directories left in dir.
func packDirectories(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var packs []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".dd-pack-objects") {
			packs = append(packs, entry.Name())
		}
	}
	return packs
}

// When ddto stops the worker during its upload, the worker stops the request,
// removes its pack files, records the stopped upload and still closes the
// telemetry application.
func TestRunStopsUploadOnRequest(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	temp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temp)
	}
	app := &fakeTelemetry{}
	sending := make(chan struct{})
	original := newClient
	newClient = func(ctx context.Context, _, _ string) (client, appTelemetry) {
		return &blockingClient{ctx: ctx, sending: sending}, app
	}
	t.Cleanup(func() { newClient = original })

	used := usedState(t)
	input, cli := io.Pipe()
	results := newResults()
	done := make(chan error, 1)
	go func() { done <- Run(used, "", time.Minute, input, results, nil) }()
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sending:
	case <-time.After(time.Minute):
		t.Fatal("the worker never sent its pack files")
	}
	if _, err := io.WriteString(cli, Stop+" timeout\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-results.closed:
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop its work")
	}
	// The telemetry application closes only after go test exits, after the
	// test processes' last requests.
	select {
	case err := <-done:
		t.Fatalf("the stopped worker exited before go test: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, stops := app.calls(); stops != 0 {
		t.Fatalf("telemetry stopped %d times before go test exited", stops)
	}
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not exit after go test")
	}
	_ = cli.Close()
	var upload utils.CLIGitUpload
	if err := utils.ReadCLIGitStateFile(used, utils.CLIGitUploadFile, &upload); err != nil || upload.Error != "ddto stopped the repository upload: timeout" {
		t.Fatalf("upload %+v, %v", upload, err)
	}
	var state impactedtests.CLIState
	if err := utils.ReadCLIGitStateFile(used, utils.CLIGitImpactedFile, &state); err != nil || state.Computed || state.Reason != "ddto stopped the worker" {
		t.Fatalf("impacted %+v, %v", state, err)
	}
	select {
	case <-results.closed:
	default:
		t.Fatal("the stopped worker did not close its results")
	}
	if _, stops := app.calls(); stops != 1 {
		t.Fatalf("telemetry stopped %d times", stops)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if packs := append(packDirectories(t, temp), packDirectories(t, filepath.Join(wd, ".git"))...); len(packs) != 0 {
		t.Fatalf("the stopped upload left pack files: %v", packs)
	}
}

// A stop reaches the worker from its first git command on: a record
// collection that waits stops, and the worker writes no record.
func TestRunStopsDuringRecordCollection(t *testing.T) {
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original := collectRecord
	collectRecord = func(ctx context.Context) utils.CLIGitRecord {
		<-ctx.Done()
		return utils.CLIGitRecord{}
	}
	t.Cleanup(func() { collectRecord = original })
	// The input stays open after a line, as ddto keeps it while it waits.
	for _, input := range []struct {
		lines string
		end   bool
	}{{Stop + " timeout\n", false}, {Exited + "\n", false}, {"", true}} {
		dir := t.TempDir()
		reader, writer := io.Pipe()
		t.Cleanup(func() { writer.Close() })
		go func() {
			if input.lines != "" {
				_, _ = io.WriteString(writer, input.lines)
			}
			if input.end {
				writer.Close()
			}
		}()
		done := make(chan error, 1)
		go func() { done <- Run(dir, "", time.Minute, reader, newResults(), nil) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("input %+v did not stop the record collection", input)
		}
		if _, err := os.Stat(filepath.Join(dir, utils.CLIGitRecordFile)); !os.IsNotExist(err) {
			t.Fatalf("a stopped worker wrote its record: %v", err)
		}
	}
}

// slowSearchClient's commit search returns some time after the worker stops,
// as a git command that needs its kill after the interrupt.
type slowSearchClient struct {
	fakeClient
	ctx      context.Context
	started  chan struct{}
	finished atomic.Bool
}

func (c *slowSearchClient) GetCommits([]string) ([]string, error) {
	close(c.started)
	<-c.ctx.Done()
	time.Sleep(100 * time.Millisecond)
	c.finished.Store(true)
	return nil, c.ctx.Err()
}

// A worker that no test process used, because go test exited or ddto
// stopped it, cancels and waits for its search, whose git commands end only
// while the worker runs.
func TestRunWaitsForTheStoppedSearch(t *testing.T) {
	gitRepository(t)
	for _, end := range []string{Stop + " signal", Exited} {
		t.Run(end, func(t *testing.T) {
			t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
			// The debug log tells a stopped worker from an unused state.
			t.Setenv("DD_TRACE_DEBUG", "true")
			logs := new(log.RecordLogger)
			t.Cleanup(log.UseLogger(logs))
			t.Cleanup(func() { log.SetLevel(log.LevelWarn) })
			clients := make(chan *slowSearchClient, 1)
			original := newClient
			newClient = func(ctx context.Context, _, _ string) (client, appTelemetry) {
				search := &slowSearchClient{ctx: ctx, started: make(chan struct{})}
				clients <- search
				return search, nil
			}
			t.Cleanup(func() { newClient = original })
			// Every test process runs, so the worker searches while go test
			// builds.
			dir := t.TempDir()
			if err := utils.WriteCLIGitStateFile(dir, utils.CLITestsFile, utils.CLITests{RunsEveryTest: true}); err != nil {
				t.Fatal(err)
			}
			input, cli := io.Pipe()
			defer cli.Close()
			done := make(chan error, 1)
			go func() { done <- Run(dir, "", time.Minute, input, newResults(), nil) }()
			if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
				t.Fatal(err)
			}
			search := <-clients
			select {
			case <-search.started:
			case <-time.After(30 * time.Second):
				t.Fatal("the worker did not search while go test built")
			}
			if _, err := io.WriteString(cli, end+"\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the worker kept waiting for a search that no one needs")
			}
			if !search.finished.Load() {
				t.Fatal("the worker exited before its stopped search")
			}
			want := "CLI requests ahead not needed: go test exited before a test process used the state [started:true]"
			if end != Exited {
				want = "CLI requests ahead stopped: ddto stopped the worker [reason:signal started:true]"
			}
			if !strings.Contains(strings.Join(logs.Logs(), "\n"), want) {
				t.Fatalf("the debug log lacks %q:\n%s", want, strings.Join(logs.Logs(), "\n"))
			}
		})
	}
}

// A quick go test can exit before the worker waits for the go-ahead: the
// worker then reads proceed and exited together, and still uploads for the
// test processes that used the state.
func TestRunProceedsWhenGoTestExitedFirst(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	fake := &fakeClient{}
	original, originalRecord := newClient, collectRecord
	newClient = func(context.Context, string, string) (client, appTelemetry) { return fake, nil }
	t.Cleanup(func() { newClient, collectRecord = original, originalRecord })
	for i := 1; i <= 20; i++ {
		utils.ResetHandedCLIGitStateForTesting()
		input := &drainedReader{Reader: strings.NewReader(Proceed + " go1.99.0\n" + Exited + "\n"), drained: make(chan struct{})}
		collectRecord = func(ctx context.Context) utils.CLIGitRecord {
			// Both lines are read once the reader is asked for more.
			<-input.drained
			return originalRecord(ctx)
		}
		dir := usedState(t)
		if err := Run(dir, "", time.Minute, input, newResults(), nil); err != nil {
			t.Fatal(err)
		}
		if _, uploads := fake.counts(); uploads != i {
			t.Fatalf("run %d: %d uploads after go test exited before the go-ahead", i, uploads)
		}
	}
}

// drainedReader closes drained once its reader has no more data.
type drainedReader struct {
	io.Reader
	drained chan struct{}
	once    sync.Once
}

func (r *drainedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil {
		r.once.Do(func() { close(r.drained) })
	}
	return n, err
}

// go test can exit before ddto hands the state over, as when preparation
// fails: the worker then exits at once.
func TestRunEndsWhenGoTestExitsWithoutTheState(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original := newClient
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, nil }
	t.Cleanup(func() { newClient = original })
	input, cli := io.Pipe()
	defer cli.Close()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- Run(dir, "", time.Minute, input, newResults(), nil) }()
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker kept waiting for proceed after go test exited")
	}
}

// A signal, such as the terminal's interrupt, which reaches the worker
// itself, stops its work as a stop line does.
func TestRunStopsOnInterrupt(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	sending := make(chan struct{})
	original := newClient
	newClient = func(ctx context.Context, _, _ string) (client, appTelemetry) {
		return &blockingClient{ctx: ctx, sending: sending}, nil
	}
	t.Cleanup(func() { newClient = original })
	used := usedState(t)
	input, cli := io.Pipe()
	interrupts := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() { done <- Run(used, "", time.Minute, input, newResults(), interrupts) }()
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
		t.Fatal(err)
	}
	<-sending
	interrupts <- os.Interrupt
	var upload utils.CLIGitUpload
	if err := utils.WaitCLIGitStateFile(used, utils.CLIGitUploadFile, &upload, time.Minute); err != nil || upload.Error != "ddto stopped the repository upload: signal" {
		t.Fatalf("upload %+v, %v", upload, err)
	}
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = cli.Close()
}

// featureFake records the requests that the worker makes for the test
// processes; with ctx, its settings request returns only once ctx is done.
type featureFake struct {
	settings net.SettingsResponseData
	ctx      context.Context
	finished atomic.Bool
	mu       sync.Mutex
	calls    []string
}

func (f *featureFake) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *featureFake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := append([]string(nil), f.calls...)
	slices.Sort(calls)
	return calls
}

func (f *featureFake) GetSettings() (*net.SettingsResponseData, error) {
	f.record("settings")
	if f.ctx != nil {
		<-f.ctx.Done()
		time.Sleep(100 * time.Millisecond)
		f.finished.Store(true)
		return nil, f.ctx.Err()
	}
	settings := f.settings
	return &settings, nil
}

func (f *featureFake) GetKnownTests() (*net.KnownTestsResponseData, error) {
	f.record("known")
	return &net.KnownTestsResponseData{}, nil
}

func (f *featureFake) GetSkippableTests() (*net.SkippableTestsResponse, error) {
	f.record("skippable")
	return &net.SkippableTestsResponse{}, nil
}

func (f *featureFake) GetTestManagementTests() (*net.TestManagementTestsResponseDataModules, error) {
	f.record("management")
	return &net.TestManagementTestsResponseDataModules{}, nil
}

// While go test builds, before any test process uses the state, the worker
// requests what the test processes request before their first test, for each
// of their services, as the settings and the environment decide.
func TestRunRequestsAheadForTestProcesses(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original, originalClients, originalView := newClient, testProcessClients, asTestProcesses
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, nil }
	t.Cleanup(func() { newClient, testProcessClients, asTestProcesses = original, originalClients, originalView })
	enabled := net.SettingsResponseData{KnownTestsEnabled: true, TestsSkipping: true}
	enabled.TestManagement.Enabled = true
	for _, tc := range []struct {
		name       string
		settings   net.SettingsResponseData
		management string
		want       []string
	}{
		{"all enabled", enabled, "", []string{"known", "management", "settings", "skippable"}},
		{"test management disabled by the environment", enabled, "false", []string{"known", "settings", "skippable"}},
		{"settings that require Git", net.SettingsResponseData{RequireGit: true, KnownTestsEnabled: true}, "", []string{"settings"}},
		{"nothing enabled", net.SettingsResponseData{}, "", []string{"settings"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable, tc.management)
			if tc.management == "" {
				if err := os.Unsetenv(constants.CIVisibilityTestManagementEnabledEnvironmentVariable); err != nil {
					t.Fatal(err)
				}
			}
			utils.ResetHandedCLIGitStateForTesting()
			services := []*featureFake{{settings: tc.settings}, {settings: tc.settings}}
			var version, viewVersion string
			var described *utils.CLITests
			testProcessClients = func(_ client, goVersion string, tests *utils.CLITests) []featureClient {
				version, described = goVersion, tests
				return []featureClient{services[0], services[1]}
			}
			// The worker's own requests are the test processes' too.
			view := &fakeClient{}
			asTestProcesses = func(_ client, goVersion string, _ *utils.CLITests) client {
				viewVersion = goVersion
				return view
			}
			dir := t.TempDir()
			// ddto describes the test processes, which all run, before it
			// tells the worker to proceed.
			if err := utils.WriteCLIGitStateFile(dir, utils.CLITestsFile, utils.CLITests{GOARCH: "arm64", RunsEveryTest: true, Packages: []utils.CLITestPackage{{Dir: "/pkg"}}}); err != nil {
				t.Fatal(err)
			}
			input, cli := io.Pipe()
			defer cli.Close()
			done := make(chan error, 1)
			go func() { done <- Run(dir, "", time.Minute, input, newResults(), nil) }()
			if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
				t.Fatal(err)
			}
			searched := func() bool {
				searches, _ := view.counts()
				return searches == 1
			}
			for deadline := time.Now().Add(30 * time.Second); len(services[0].requests()) < len(tc.want) || len(services[1].requests()) < len(tc.want) || !searched(); time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatalf("requests before any test process: %v %v, searched %t", services[0].requests(), services[1].requests(), searched())
				}
			}
			if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			for _, service := range services {
				if got := service.requests(); !slices.Equal(got, tc.want) {
					t.Fatalf("requests %v, want %v", got, tc.want)
				}
			}
			if version != "go1.99.0" || described == nil || described.GOARCH != "arm64" || len(described.Packages) != 1 {
				t.Fatalf("requested for %q and %+v", version, described)
			}
			if searches, _ := view.counts(); viewVersion != "go1.99.0" || searches != 1 {
				t.Fatalf("the worker searched %d times as the test processes of %q", searches, viewVersion)
			}
		})
	}
}

// A worker that no test process used cancels the requests that it made ahead
// and waits for them, as for its commit search.
func TestRunWaitsForItsStoppedRequests(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original, originalClients := newClient, testProcessClients
	contexts := make(chan context.Context, 1)
	newClient = func(ctx context.Context, _, _ string) (client, appTelemetry) {
		contexts <- ctx
		return &fakeClient{}, nil
	}
	request := &featureFake{}
	testProcessClients = func(client, string, *utils.CLITests) []featureClient { return []featureClient{request} }
	t.Cleanup(func() { newClient, testProcessClients = original, originalClients })
	dir := t.TempDir()
	if err := utils.WriteCLIGitStateFile(dir, utils.CLITestsFile, utils.CLITests{RunsEveryTest: true}); err != nil {
		t.Fatal(err)
	}
	input, cli := io.Pipe()
	defer cli.Close()
	done := make(chan error, 1)
	go func() { done <- Run(dir, "", time.Minute, input, newResults(), nil) }()
	request.ctx = <-contexts
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"+Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker kept waiting for requests that no one needs")
	}
	if !request.finished.Load() {
		t.Fatal("the worker exited before its stopped requests")
	}
}

// The worker requests its own settings, and those of the test processes, with
// the test processes' Go version and architecture, not its own: then they are
// one request, which the test processes find in the read cache.
func TestWorkerRequestsAsTestProcesses(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data struct {
				Attributes struct {
					Configurations map[string]string `json:"configurations"`
				} `json:"attributes"`
			} `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests = append(requests, body.Data.Attributes.Configurations)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{}}}`)
	}))
	defer server.Close()
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "false")
	t.Setenv("DD_TRACE_AGENT_URL", server.URL)
	t.Setenv("DD_SERVICE", "poc")
	// The CI tags, which other tests leave from their repositories, come
	// from these.
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.invalid/acme/poc.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "1111111111111111111111111111111111111111")
	utils.ResetCITags()
	t.Cleanup(utils.ResetCITags)
	net.SetReadCacheHooksForTesting(t.TempDir(), time.Now, os.Getpid, func() int { return 1 }, time.Sleep)
	t.Cleanup(net.ResetReadCacheHooksForTesting)
	c, _ := net.NewCLIClient(context.Background(), t.TempDir(), "")
	if c == nil {
		t.Fatal("no client")
	}
	tests := &utils.CLITests{GOARCH: "arm64", Packages: []utils.CLITestPackage{{Dir: t.TempDir()}}}
	if _, err := asTestProcesses(c, "go1.99.0", tests).GetSettings(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range testProcessClients(c, "go1.99.0", tests) {
		if _, err := tc.GetSettings(); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || requests[0]["runtime.version"] != "go1.99.0" || requests[0]["os.architecture"] != "arm64" {
		t.Fatalf("settings requests %v", requests)
	}
}

// When every result may come from Go's test cache, the worker requests nothing
// before a test process uses its state: a run whose results all come from the
// cache sends no request. Once a test process starts, the worker searches the
// commits and requests for the test processes.
func TestRunRequestsNothingWhileResultsMayBeCached(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original, originalClients, originalView := newClient, testProcessClients, asTestProcesses
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, nil }
	t.Cleanup(func() { newClient, testProcessClients, asTestProcesses = original, originalClients, originalView })
	for _, used := range []bool{false, true} {
		utils.ResetHandedCLIGitStateForTesting()
		request := &featureFake{}
		testProcessClients = func(client, string, *utils.CLITests) []featureClient { return []featureClient{request} }
		view := &fakeClient{}
		asTestProcesses = func(client, string, *utils.CLITests) client { return view }
		dir := usedState(t)
		if !used {
			dir = t.TempDir()
		}
		if err := utils.WriteCLIGitStateFile(dir, utils.CLITestsFile, utils.CLITests{Packages: []utils.CLITestPackage{{Dir: "/pkg"}}}); err != nil {
			t.Fatal(err)
		}
		if err := Run(dir, "", time.Minute, strings.NewReader(Proceed+" go1.99.0\n"+Exited+"\n"), newResults(), nil); err != nil {
			t.Fatal(err)
		}
		searches, _ := view.counts()
		if requested := len(request.requests()) > 0; requested != used || (used && searches != 1) {
			t.Fatalf("used %t: requests %v, searches %d", used, request.requests(), searches)
		}
	}
}

// When every result may come from Go's test cache, go test linking a test
// binary is enough: it links one only for a result that is not in its cache,
// so the worker requests for the test processes while the binary links,
// before any of them starts.
func TestRunRequestsWhenGoTestLinks(t *testing.T) {
	gitRepository(t)
	t.Cleanup(utils.ResetHandedCLIGitStateForTesting)
	original, originalClients := newClient, testProcessClients
	newClient = func(context.Context, string, string) (client, appTelemetry) { return &fakeClient{}, nil }
	request := &featureFake{}
	testProcessClients = func(client, string, *utils.CLITests) []featureClient { return []featureClient{request} }
	t.Cleanup(func() { newClient, testProcessClients = original, originalClients })
	dir := t.TempDir()
	if err := utils.WriteCLIGitStateFile(dir, utils.CLITestsFile, utils.CLITests{Packages: []utils.CLITestPackage{{Dir: "/pkg"}}}); err != nil {
		t.Fatal(err)
	}
	input, cli := io.Pipe()
	defer cli.Close()
	done := make(chan error, 1)
	go func() { done <- Run(dir, "", time.Minute, input, newResults(), nil) }()
	if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, utils.CLIGitLinkingFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); len(request.requests()) == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the worker did not request when go test linked a test binary")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, utils.CLIGitUsedFile)); !os.IsNotExist(err) {
		t.Fatalf("a test process used the state: %v", err)
	}
	if _, err := io.WriteString(cli, Exited+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The worker's debug lines show request errors without a URL's credentials,
// which url.Error repeats.
func TestRequestAheadErrorsHideURLCredentials(t *testing.T) {
	err := fmt.Errorf("settings: %w", &url.Error{Op: "Post", URL: "https://ci:SYNTHETIC_SECRET@intake.example/%zz?x=1", Err: errors.New("invalid URL escape")})
	got := redactError(err)
	if strings.Contains(got, "SYNTHETIC_SECRET") || !strings.Contains(got, "https://[redacted]@intake.example/") || !strings.Contains(got, "invalid URL escape") {
		t.Fatalf("redactError = %q", got)
	}
	if got := redactError(errors.New("unexpected status code 403")); got != "unexpected status code 403" {
		t.Fatalf("redactError changed a message without a URL: %q", got)
	}
	// Go's parser takes the authority's last @, and accepts @ and ' in a
	// password.
	for _, raw := range []string{"https://ci:prefix@SYNTHETIC_SECRET@intake.example/%zz", "https://ci:prefix'SYNTHETIC_SECRET@intake.example/%zz", "https://ci:SYNTHETIC_SECRET@intake.example"} {
		got := redactError(&url.Error{Op: "Post", URL: raw, Err: errors.New("EOF")})
		if strings.Contains(got, "SYNTHETIC_SECRET") || !strings.Contains(got, "https://[redacted]@intake.example") {
			t.Fatalf("redactError(%q) = %q", raw, got)
		}
	}

	t.Setenv("DD_TRACE_DEBUG", "true")
	log.SetLevel(log.LevelDebug)
	t.Cleanup(func() { log.SetLevel(log.LevelWarn) })
	logs := new(log.RecordLogger)
	t.Cleanup(log.UseLogger(logs))
	requestFeatures(0, failingFeatures{err})
	all := strings.Join(logs.Logs(), "\n")
	if strings.Contains(all, "SYNTHETIC_SECRET") || !strings.Contains(all, "CLI request ahead settings failed [client:0") {
		t.Fatalf("the debug log of a failed request: %s", all)
	}
}

// failingFeatures fails its settings request with err.
type failingFeatures struct{ err error }

func (f failingFeatures) GetSettings() (*net.SettingsResponseData, error) { return nil, f.err }
func (f failingFeatures) GetKnownTests() (*net.KnownTestsResponseData, error) {
	return nil, f.err
}
func (f failingFeatures) GetSkippableTests() (*net.SkippableTestsResponse, error) {
	return nil, f.err
}
func (f failingFeatures) GetTestManagementTests() (*net.TestManagementTestsResponseDataModules, error) {
	return nil, f.err
}
