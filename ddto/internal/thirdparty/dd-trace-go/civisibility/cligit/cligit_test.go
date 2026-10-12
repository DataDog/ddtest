// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package cligit

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
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
			clients := make(chan *slowSearchClient, 1)
			original := newClient
			newClient = func(ctx context.Context, _, _ string) (client, appTelemetry) {
				search := &slowSearchClient{ctx: ctx, started: make(chan struct{})}
				clients <- search
				return search, nil
			}
			t.Cleanup(func() { newClient = original })
			input, cli := io.Pipe()
			defer cli.Close()
			done := make(chan error, 1)
			go func() { done <- Run(t.TempDir(), "", time.Minute, input, newResults(), nil) }()
			if _, err := io.WriteString(cli, Proceed+" go1.99.0\n"); err != nil {
				t.Fatal(err)
			}
			search := <-clients
			<-search.started
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
