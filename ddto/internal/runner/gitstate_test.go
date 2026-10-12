package runner

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/cligit"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

// TestGitWorkerHelperProcess is a worker for the tests below. By default it
// reads its go-ahead, closes its results without writing them, and runs until
// its input ends. Other modes record every line it receives in
// DDTO_GIT_WORKER_LINES and exit on the line that DDTO_GIT_WORKER_HELPER
// names, or never ("hang").
func TestGitWorkerHelperProcess(t *testing.T) {
	mode := os.Getenv("DDTO_GIT_WORKER_HELPER")
	if mode == "" {
		t.Skip("helper process")
	}
	input := bufio.NewReader(os.Stdin)
	if mode == "1" {
		_, _ = input.ReadString('\n')
		_ = os.Stdout.Close()
		_, _ = io.Copy(io.Discard, input)
		os.Exit(0)
	}
	if mode == "hang" {
		time.Sleep(time.Hour)
	}
	lines, err := os.OpenFile(os.Getenv("DDTO_GIT_WORKER_LINES"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(3)
	}
	for {
		line, err := input.ReadString('\n')
		_, _ = lines.WriteString(line)
		if err != nil || strings.HasPrefix(line, mode) {
			_ = lines.Close()
			os.Exit(0)
		}
	}
}

// helperWorker launches the helper in mode, recording its lines in a file.
func helperWorker(t *testing.T, mode string) (*gitWorker, string) {
	t.Helper()
	lines := filepath.Join(t.TempDir(), "lines")
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitWorkerHelperProcess$")
	cmd.Env = append(os.Environ(), "DDTO_GIT_WORKER_HELPER="+mode, "DDTO_GIT_WORKER_LINES="+lines)
	w, err := launchGitWorker(cmd, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.stderr = io.Discard
	t.Cleanup(func() {
		_ = w.cmd.Process.Kill()
		<-w.exited
	})
	return w, lines
}

func readLines(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// A worker still uploading when the upload timeout ends is asked to stop;
// it is not killed.
func TestGitWorkerFinishStopsAfterUploadTimeout(t *testing.T) {
	w, lines := helperWorker(t, cligit.Stop)
	w.uploadTimeout = 50 * time.Millisecond
	w.finish()
	if got := readLines(t, lines); got != cligit.Exited+"\n"+cligit.Stop+" timeout\n" {
		t.Fatalf("worker received %q", got)
	}
	if w.summary.Worker != "stopped" || w.summary.StopReason != "timeout" {
		t.Fatalf("summary %+v", w.summary)
	}
	if _, err := os.Stat(w.state); !os.IsNotExist(err) {
		t.Fatalf("state kept without --keep-ci-state: %v", err)
	}
}

// Only a worker that does not stop is killed.
func TestGitWorkerFinishKillsAWorkerThatDoesNotStop(t *testing.T) {
	w, _ := helperWorker(t, "hang")
	original := gitWorkerStopWait
	gitWorkerStopWait = 100 * time.Millisecond
	t.Cleanup(func() { gitWorkerStopWait = original })
	w.uploadTimeout = 50 * time.Millisecond
	started := time.Now()
	w.finish()
	if w.summary.Worker != "killed" || time.Since(started) > 30*time.Second {
		t.Fatalf("summary %+v after %s", w.summary, time.Since(started))
	}
}

// A signal stops the worker instead of killing it, and is still relayed.
func TestGitWorkerSignalStopsTheWorker(t *testing.T) {
	w, lines := helperWorker(t, cligit.Stop)
	signals := make(chan os.Signal, 1)
	relayed := w.relaySignals(signals)
	signals <- os.Interrupt
	if got := <-relayed; got != os.Interrupt {
		t.Fatalf("relayed %v", got)
	}
	if !w.waitExit(30 * time.Second) {
		t.Fatal("the signalled worker did not stop")
	}
	if got := readLines(t, lines); got != cligit.Stop+" signal\n" {
		t.Fatalf("worker received %q", got)
	}
	w.finish()
	if w.summary.Worker != "stopped" || w.summary.StopReason != "signal" {
		t.Fatalf("summary %+v", w.summary)
	}
}

// --keep-ci-state keeps the state with ddto's summary, in place or archived.
func TestGitWorkerKeepsCIState(t *testing.T) {
	for _, archived := range []bool{false, true} {
		w, _ := helperWorker(t, cligit.Exited)
		if err := os.WriteFile(filepath.Join(w.state, utils.CLIGitRecordFile), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		w.uploadTimeout, w.keep, w.stderr = time.Minute, true, &stderr
		zipPath := filepath.Join(t.TempDir(), "nested", "state.zip")
		if archived {
			w.keepZip = zipPath
		}
		w.finish()
		if !archived {
			for _, name := range []string{utils.CLIGitRecordFile, ciStateSummary} {
				if _, err := os.Stat(filepath.Join(w.state, name)); err != nil {
					t.Fatalf("kept state lacks %s: %v", name, err)
				}
			}
			if !strings.Contains(stderr.String(), "kept the CI state in "+w.state) {
				t.Fatalf("stderr %q", stderr.String())
			}
			_ = os.RemoveAll(w.state)
			continue
		}
		archive, err := zip.OpenReader(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, file := range archive.File {
			names = append(names, file.Name)
		}
		_ = archive.Close()
		slices.Sort(names)
		if want := []string{ciStateSummary, utils.CLIGitRecordFile}; !slices.Equal(names, want) {
			t.Fatalf("archive holds %v, want %v", names, want)
		}
		if _, err := os.Stat(w.state); !os.IsNotExist(err) || !strings.Contains(stderr.String(), "archived the CI state in "+zipPath) {
			t.Fatalf("state after archiving: %v, stderr %q", err, stderr.String())
		}
	}
}

// A worker that closes its results without writing them, as after a failed
// write, still runs until go test exits; ddto records the results at once,
// so test processes stop waiting for them.
func TestGitWorkerRecordsResultsWhenOutputCloses(t *testing.T) {
	state := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitWorkerHelperProcess$")
	cmd.Env = append(os.Environ(), "DDTO_GIT_WORKER_HELPER=1")
	w, err := launchGitWorker(cmd, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.proceeded.Store(true)
	if _, err := fmt.Fprintln(w.input, "proceed"); err != nil {
		t.Fatal(err)
	}
	var upload utils.CLIGitUpload
	if err := utils.WaitCLIGitStateFile(state, utils.CLIGitUploadFile, &upload, time.Minute); err != nil || upload.Error == "" {
		t.Fatalf("upload %+v, %v", upload, err)
	}
	select {
	case <-w.exited:
		t.Fatal("the worker exited before go test")
	default:
	}
	_ = w.input.Close()
	select {
	case <-w.exited:
	case <-time.After(time.Minute):
		t.Fatal("the worker did not exit after go test")
	}
}

// A worker stopped by a signal gets one gitWorkerStopWait from go test's
// exit, never two, and a signal during the upload wait ends that wait.
func TestGitWorkerFinishBoundsAStopBySignal(t *testing.T) {
	original := gitWorkerStopWait
	gitWorkerStopWait = 2 * time.Second
	t.Cleanup(func() { gitWorkerStopWait = original })

	before, _ := helperWorker(t, "hang")
	before.uploadTimeout = time.Hour
	before.stop("signal")
	started := time.Now()
	before.finish()
	if elapsed := time.Since(started); before.summary.Worker != "killed" || elapsed > 3500*time.Millisecond {
		t.Fatalf("signal before finish: %+v after %s", before.summary, elapsed)
	}

	during, _ := helperWorker(t, "hang")
	during.uploadTimeout = time.Hour
	finished := make(chan struct{})
	go func() {
		during.finish()
		close(finished)
	}()
	time.Sleep(100 * time.Millisecond)
	during.stop("signal")
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("a signal did not end the upload wait")
	}
	if during.summary.Worker != "killed" || during.summary.StopReason != "signal" {
		t.Fatalf("signal during the wait: %+v", during.summary)
	}
}

// The archive keeps a link as a link, without the file it points to.
func TestZipDirectoryKeepsLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating links needs privileges on Windows")
	}
	dir, outside := t.TempDir(), filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside the state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.zip")
	if err := zipDirectory(dir, path); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 || archive.File[0].Mode()&os.ModeSymlink == 0 {
		t.Fatalf("archive %v", archive.File)
	}
	content, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	target, _ := io.ReadAll(content)
	_ = content.Close()
	if string(target) != outside {
		t.Fatalf("the link holds %q, want its target %q", target, outside)
	}
}
