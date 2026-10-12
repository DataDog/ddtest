package runner

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/cligit"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/envconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/impactedtests"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/globalconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// gitWorkerCommand is ddto's private entry point for the Git worker.
const gitWorkerCommand = "ci-git-worker"

// gitRecordWait bounds how long go test waits for the worker's Git record
// once preparation is done; test processes then read Git themselves.
var gitRecordWait = time.Second

// gitWorkerStopWait bounds how long a stopped worker takes to stop its git
// commands and requests, record what it did not finish and close the
// telemetry application; ddto then kills it.
var gitWorkerStopWait = 10 * time.Second

// Files that --keep-ci-state adds to the state.
const (
	ciStateWorkerLog = "worker.log"
	ciStateSummary   = "ddto.json"
)

// gitWorker runs, once per invocation, the Git work that every Mini test
// process of the invocation's checkout would repeat: see package cligit.
type gitWorker struct {
	state     string
	cmd       *exec.Cmd
	exited    chan struct{}
	debug     *cliDebug
	proceeded atomic.Bool

	inputMu     sync.Mutex
	input       io.WriteCloser
	inputClosed bool
	stopReason  atomic.Pointer[string]
	// stopping is closed when ddto first asks the worker to stop, at stopAt.
	stopping chan struct{}
	stopAt   time.Time

	uploadTimeout time.Duration
	stderr        io.Writer
	keep          bool
	keepZip       string
	log           *os.File
	summary       ciStateSummaryFile
}

// ciStateSummaryFile records ddto's view of the worker, in ddto.json.
type ciStateSummaryFile struct {
	GoVersion       string `json:"go_version,omitempty"`
	RecordReady     bool   `json:"record_ready"`
	CheckoutMatched bool   `json:"checkout_matched"`
	Launcher        bool   `json:"launcher"`
	Handed          bool   `json:"handed"`
	UploadTimeout   string `json:"upload_timeout"`
	// Worker is how the worker ended: exited, stopped or killed.
	Worker     string `json:"worker"`
	StopReason string `json:"stop_reason,omitempty"`
	// Waited is how long ddto waited for the worker after go test exited.
	Waited string `json:"waited_after_go_test"`
}

// startGitWorker starts the worker in dir while ddto prepares the build, if
// test processes would read Git: the Mini runtime with CI Visibility enabled,
// outside Bazel's offline modes, in a command that runs test binaries.
func startGitWorker(cli CLIOptions, opts options, dir string, debug *cliDebug, stderr io.Writer) *gitWorker {
	if cli.Runtime != Mini || opts.compileOnly || opts.printOnly {
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
	// The worker's telemetry application reports this process's runtime ID
	// and root session: the invocation is one application. The root session
	// reaches only the worker; ddto passes its caller's environment to go
	// test unchanged.
	cmd := exec.Command(executable, gitWorkerCommand, state, globalconfig.RuntimeID(), cli.GitUploadTimeout.String())
	// Test processes run in their package directories with PWD set; the
	// worker runs in the invocation's directory, with the same environment.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PWD="+dir, globalconfig.RootSessionEnv())
	cmd.Stderr = stderr
	var log *os.File
	if cli.KeepCIState {
		if log, err = os.Create(filepath.Join(state, ciStateWorkerLog)); err == nil {
			cmd.Stderr = io.MultiWriter(stderr, log)
		}
	}
	w, err := launchGitWorker(cmd, state, debug)
	if err != nil {
		debug.printf("git worker start failed: %v", err)
		if log != nil {
			_ = log.Close()
		}
		_ = os.RemoveAll(state)
		return nil
	}
	w.uploadTimeout, w.stderr, w.log = cli.GitUploadTimeout, stderr, log
	w.keep, w.keepZip = cli.KeepCIState, cli.KeepCIStateZip
	w.summary.UploadTimeout = cli.GitUploadTimeout.String()
	return w
}

// launchGitWorker starts cmd, the worker for state. The worker closes its
// output once it has written its results, or failed to, and keeps running
// until go test exits; ddto then records any result that the worker did not
// write, so that test processes stop waiting for it.
func launchGitWorker(cmd *exec.Cmd, state string, debug *cliDebug) (*gitWorker, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	results, output, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = output
	err = cmd.Start()
	_ = output.Close()
	if err != nil {
		_ = results.Close()
		return nil, err
	}
	w := &gitWorker{state: state, cmd: cmd, input: input, exited: make(chan struct{}), stopping: make(chan struct{}), debug: debug}
	recorded := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, results)
		_ = results.Close()
		w.recordMissingResults()
		close(recorded)
	}()
	go func() {
		_ = cmd.Wait()
		<-recorded
		close(w.exited)
	}()
	return w, nil
}

// send writes a line to the worker, unless its input is closed.
func (w *gitWorker) send(line string) error {
	w.inputMu.Lock()
	defer w.inputMu.Unlock()
	if w.inputClosed {
		return os.ErrClosed
	}
	_, err := io.WriteString(w.input, line+"\n")
	return err
}

// closeInput closes the worker's input; before Exited, that stops it.
func (w *gitWorker) closeInput() {
	w.inputMu.Lock()
	defer w.inputMu.Unlock()
	if !w.inputClosed {
		w.inputClosed = true
		_ = w.input.Close()
	}
}

// stop asks the worker to stop its work, once: it stops its git commands and
// requests, removes its pack files, records what it did not finish and
// closes the telemetry application.
func (w *gitWorker) stop(reason string) {
	w.inputMu.Lock()
	first := w.stopReason.CompareAndSwap(nil, &reason)
	if first {
		w.stopAt = time.Now()
		close(w.stopping)
	}
	w.inputMu.Unlock()
	if first {
		w.debug.printf("git worker stop reason=%s", reason)
		_ = w.send(cligit.Stop + " " + reason)
	}
}

// stoppedAt returns when ddto first asked the worker to stop, or zero.
func (w *gitWorker) stoppedAt() time.Time {
	w.inputMu.Lock()
	defer w.inputMu.Unlock()
	return w.stopAt
}

// handOff decides whether test processes receive the worker's state, and
// returns the environment entries for go test. The state is handed when the
// record is ready in time and belongs to the checkout of a test package, and
// no launcher, which would see the variable first, starts the test binaries.
// Otherwise the worker stops and each test process reads Git, and reports
// its telemetry, as before. goVersion is the test processes' Go version,
// which the worker's telemetry application reports.
func (w *gitWorker) handOff(testDirs []string, launcher bool, goVersion string) []string {
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
	w.summary.GoVersion, w.summary.RecordReady, w.summary.CheckoutMatched, w.summary.Launcher = goVersion, ready, matched, launcher
	if !matched || launcher {
		w.closeInput()
		return nil
	}
	w.proceeded.Store(true)
	if err := w.send(cligit.Proceed + " " + goVersion); err != nil {
		w.closeInput()
		return nil
	}
	w.summary.Handed = true
	// The input stays open: finish writes Exited once go test exits.
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

// waitExit waits up to timeout for the worker to exit.
func (w *gitWorker) waitExit(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.exited:
		return true
	case <-timer.C:
		return false
	}
}

// finish runs after go test has exited, so no test process reads the state
// any more. It tells the worker, which exits at once if no test process used
// the state; one that was used finishes its upload first, for at most the
// upload timeout, as a test process waited for its own upload at exit. ddto
// then stops the worker, which ends its work without leaving pack files or
// lock files behind and closes the telemetry application. A worker stopped
// earlier, by a signal, gets the same time once go test has exited, since it
// closes the telemetry application only then. Only a worker that has not
// exited gitWorkerStopWait after both the stop and go test's exit is killed.
// finish then removes the state, or keeps it with --keep-ci-state.
func (w *gitWorker) finish() {
	if w == nil {
		return
	}
	waited := time.Now()
	_ = w.send(cligit.Exited)
	outcome := w.awaitExit(waited)
	if outcome == "exited" && w.stopReason.Load() != nil {
		outcome = "stopped"
	}
	w.closeInput()
	elapsed := time.Since(waited)
	w.debug.printf("git worker finish waited=%s worker=%s", elapsed.Round(time.Millisecond), outcome)
	w.summary.Worker, w.summary.Waited = outcome, elapsed.Round(time.Millisecond).String()
	if reason := w.stopReason.Load(); reason != nil {
		w.summary.StopReason = *reason
	}
	w.release()
}

// awaitExit waits for the worker, which go test's exit was told at
// exitedAt: for the upload timeout, unless a signal stops the worker first,
// then for gitWorkerStopWait from both the stop and exitedAt. It returns
// how the worker ended: exited or killed.
func (w *gitWorker) awaitExit(exitedAt time.Time) string {
	if w.stoppedAt().IsZero() {
		timer := time.NewTimer(w.uploadTimeout)
		select {
		case <-w.exited:
			timer.Stop()
			return "exited"
		case <-w.stopping:
		case <-timer.C:
			w.debug.printf("git worker still running %s after go test", w.uploadTimeout)
			w.stop("timeout")
		}
		timer.Stop()
	}
	if w.waitExit(time.Until(maxTime(w.stoppedAt(), exitedAt).Add(gitWorkerStopWait))) {
		return "exited"
	}
	w.debug.printf("git worker did not stop within %s; killing it", gitWorkerStopWait)
	_ = w.cmd.Process.Kill()
	<-w.exited
	return "killed"
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// release removes the state or, with --keep-ci-state, keeps it with ddto's
// summary and the worker's log, as a directory or a zip archive.
func (w *gitWorker) release() {
	if w.log != nil {
		_ = w.log.Close()
	}
	if !w.keep {
		_ = os.RemoveAll(w.state)
		return
	}
	if data, err := json.MarshalIndent(w.summary, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(w.state, ciStateSummary), append(data, '\n'), 0o600)
	}
	if w.keepZip == "" {
		fmt.Fprintln(w.stderr, version.BuildLogPrefix+" INFO: kept the CI state in "+w.state)
		return
	}
	if err := zipDirectory(w.state, w.keepZip); err != nil {
		fmt.Fprintln(w.stderr, version.BuildLogPrefix+" WARN: could not archive the CI state: "+err.Error()+"; kept it in "+w.state)
		return
	}
	_ = os.RemoveAll(w.state)
	fmt.Fprintln(w.stderr, version.BuildLogPrefix+" INFO: archived the CI state in "+w.keepZip)
}

// zipDirectory archives dir's files and directories, with paths relative to
// dir, in a new zip file at path.
func zipDirectory(dir, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	archive := zip.NewWriter(temp)
	err = filepath.WalkDir(dir, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || file == dir {
			return err
		}
		name, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		if entry.IsDir() {
			header.Name += "/"
			_, err = archive.CreateHeader(header)
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			// A link is archived as a link, never followed.
			target, err := os.Readlink(file)
			if err != nil {
				return err
			}
			writer, err := archive.CreateHeader(header)
			if err == nil {
				_, err = io.WriteString(writer, target)
			}
			return err
		}
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(file)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(writer, source)
		return err
	})
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temp.Name())
	}
	return err
}

// relaySignals forwards each signal to go test through the returned channel
// and stops the worker, which ends its work as finish describes: an
// interrupted command should not wait for an upload.
func (w *gitWorker) relaySignals(signals <-chan os.Signal) <-chan os.Signal {
	if w == nil {
		return signals
	}
	relayed := make(chan os.Signal, cap(signals))
	go func() {
		for s := range signals {
			w.stop("signal")
			relayed <- s
		}
	}()
	return relayed
}

// RunGitWorker is ddto's private Git worker entry point. Its arguments are
// the state directory and, optionally, the runtime ID of the invocation's
// telemetry application, without which the worker owns no telemetry, and the
// upload timeout that test processes wait for. It closes results once it has
// written them.
func RunGitWorker(_ context.Context, args []string, stdin io.Reader, results io.Closer, stderr io.Writer) int {
	state, runtimeID, uploadWait := args[0], "", defaultGitUploadTimeout
	if len(args) > 1 {
		runtimeID = args[1]
	}
	if len(args) > 2 {
		wait, err := time.ParseDuration(args[2])
		if err != nil {
			fmt.Fprintln(stderr, "ddto git worker:", err)
			return 2
		}
		uploadWait = wait
	}
	// The terminal's signals reach the worker too: it stops its work as for
	// a stop line, instead of exiting with its pack files left behind. These
	// are the signals that ddto forwards to go test.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, forwardedSignals...)
	defer signal.Stop(interrupts)
	if err := cligit.Run(state, runtimeID, uploadWait, stdin, results, interrupts); err != nil {
		fmt.Fprintln(stderr, "ddto git worker:", err)
		return 1
	}
	return 0
}
