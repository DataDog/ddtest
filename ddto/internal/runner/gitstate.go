package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/cligit"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/envconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
)

// gitWorkerCommand is ddto's private entry point for the Git worker.
const gitWorkerCommand = "ci-git-worker"

// gitRecordWait bounds how long go test waits for the worker's Git record
// once preparation is done; test processes then read Git themselves.
var gitRecordWait = time.Second

// gitWorkerExitWait bounds the wait for the worker after go test exits, as a
// test process waits for its own repository upload before exiting.
var gitWorkerExitWait = time.Minute

// gitWorker runs, once per invocation, the Git work that every Mini test
// process of the invocation's checkout would repeat: see package cligit.
type gitWorker struct {
	state     string
	cmd       *exec.Cmd
	input     io.WriteCloser
	exited    chan struct{}
	debug     *cliDebug
	proceeded atomic.Bool
}

// startGitWorker starts the worker in dir while ddto prepares the build, if
// test processes would read Git: the Mini runtime with CI Visibility enabled,
// outside Bazel's offline modes, in a command that runs test binaries.
func startGitWorker(runtime Runtime, opts options, dir string, debug *cliDebug, stderr io.Writer) *gitWorker {
	if runtime != Mini || opts.compileOnly || opts.printOnly {
		return nil
	}
	if mode, _ := envconfig.FromEnv(); !envconfig.Enabled(mode) {
		return nil
	}
	if mode := bazel.CurrentMode(); mode.ManifestEnabled || mode.PayloadFilesEnabled {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	state, err := os.MkdirTemp("", "ddto-git-")
	if err != nil {
		return nil
	}
	cmd := exec.Command(executable, gitWorkerCommand, state)
	// Test processes run in their package directories with PWD set; the
	// worker runs in the invocation's directory, with the same environment.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PWD="+dir)
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		debug.printf("git worker start failed: %v", err)
		_ = os.RemoveAll(state)
		return nil
	}
	w := &gitWorker{state: state, cmd: cmd, input: input, exited: make(chan struct{}), debug: debug}
	go func() {
		_ = cmd.Wait()
		w.recordMissingResults()
		close(w.exited)
	}()
	return w
}

// handOff decides whether test processes receive the worker's state, and
// returns the environment entries for go test. The state is handed when the
// record is ready in time and belongs to the checkout of a test package, and
// no launcher, which would see the variable first, starts the test binaries.
// Otherwise the worker stops and each test process reads Git as before.
func (w *gitWorker) handOff(testDirs []string, launcher bool) []string {
	if w == nil {
		return nil
	}
	record, ready := w.record(gitRecordWait)
	matched := false
	for _, dir := range testDirs {
		if ready && utils.SameCheckout(dir, record.GitFolder) {
			matched = true
			break
		}
	}
	w.debug.printf("git worker record_ready=%t checkout_matched=%t launcher=%t", ready, matched, launcher)
	if !matched || launcher {
		_ = w.input.Close()
		return nil
	}
	w.proceeded.Store(true)
	if _, err := fmt.Fprintln(w.input, cligit.Proceed); err != nil {
		_ = w.input.Close()
		return nil
	}
	// The input stays open: closing it tells the worker that go test exited.
	return []string{goenv.GitStateHandoff + "=" + w.state}
}

// recordMissingResults writes the results that a worker told to proceed did
// not write before it exited, so test processes waiting for them continue.
func (w *gitWorker) recordMissingResults() {
	if !w.proceeded.Load() {
		return
	}
	var upload utils.CLIGitUpload
	if utils.ReadCLIGitStateFile(w.state, utils.CLIGitUploadFile, &upload) != nil {
		w.debug.printf("git worker exited without an upload result")
		_ = utils.WriteCLIGitStateFile(w.state, utils.CLIGitUploadFile, utils.CLIGitUpload{Error: "ddto git worker exited"})
	}
	var state impactedtests.CLIState
	if utils.ReadCLIGitStateFile(w.state, utils.CLIGitImpactedFile, &state) != nil {
		_ = utils.WriteCLIGitStateFile(w.state, utils.CLIGitImpactedFile, impactedtests.CLIState{Reason: "ddto git worker exited"})
	}
}

// record waits up to timeout for the worker's Git record.
func (w *gitWorker) record(timeout time.Duration) (utils.CLIGitRecord, bool) {
	var record utils.CLIGitRecord
	deadline := time.Now().Add(timeout)
	delay := time.Millisecond
	for {
		if utils.ReadCLIGitStateFile(w.state, utils.CLIGitRecordFile, &record) == nil {
			return record, true
		}
		select {
		case <-w.exited:
			return record, utils.ReadCLIGitStateFile(w.state, utils.CLIGitRecordFile, &record) == nil
		default:
		}
		if !time.Now().Before(deadline) {
			return record, false
		}
		time.Sleep(delay)
		delay = min(2*delay, 20*time.Millisecond)
	}
}

// finish waits for the worker, at most timeout, and removes its state. It
// runs after go test has exited, so no test process reads the state any more.
// Closing the input lets a worker that no test process used exit without
// uploading; one that was used finishes its upload first.
func (w *gitWorker) finish(timeout time.Duration) {
	if w == nil {
		return
	}
	_ = w.input.Close()
	waited := time.Now()
	defer func() { w.debug.printf("git worker finish waited=%s", time.Since(waited).Round(time.Millisecond)) }()
	select {
	case <-w.exited:
	case <-time.After(timeout):
		w.debug.printf("git worker still running after %s; stopping it", timeout)
		_ = w.cmd.Process.Kill()
		<-w.exited
	}
	_ = os.RemoveAll(w.state)
}

// relaySignals forwards each signal to go test through the returned channel
// and stops the worker, whose work only saves time: test processes wait for
// its results, and an interrupted command should not wait for an upload.
func (w *gitWorker) relaySignals(signals <-chan os.Signal) <-chan os.Signal {
	if w == nil {
		return signals
	}
	relayed := make(chan os.Signal, cap(signals))
	go func() {
		for s := range signals {
			_ = w.cmd.Process.Kill()
			relayed <- s
		}
	}()
	return relayed
}

// RunGitWorker is ddto's private Git worker entry point.
func RunGitWorker(_ context.Context, state string, stdin io.Reader, stderr io.Writer) int {
	if err := cligit.Run(state, stdin); err != nil {
		fmt.Fprintln(stderr, "ddto git worker:", err)
		return 1
	}
	return 0
}
