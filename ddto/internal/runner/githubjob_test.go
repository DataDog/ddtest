package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
)

func TestHandGitHubJobID(t *testing.T) {
	diagnostics := t.TempDir()
	original := gitHubDiagnosticsDirs
	gitHubDiagnosticsDirs = func() []string { return []string{diagnostics} }
	t.Cleanup(func() { gitHubDiagnosticsDirs = original })
	t.Setenv("JOB_CHECK_RUN_ID", "")
	t.Setenv("GITHUB_SHA", "0123456789abcdef")

	if got := handGitHubJobID(Mini, nil)(); !reflect.DeepEqual(got, []string{goenv.GitHubJobIDHandoff + "="}) {
		t.Fatalf("without Worker logs: %q", got)
	}
	log := `[2026-10-11 10:00:00Z INFO Worker] Job message:
{"job":{"t":2,"d":[{"k":"check_run_id","v":4242.0}]}}
`
	if err := os.WriteFile(filepath.Join(diagnostics, "Worker_20261011.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	if got := handGitHubJobID(Mini, nil)(); !reflect.DeepEqual(got, []string{goenv.GitHubJobIDHandoff + "=4242"}) {
		t.Fatalf("Worker log: %q", got)
	}
	// The SDK runtime, a numeric JOB_CHECK_RUN_ID and other CI providers need
	// no scan.
	if got := handGitHubJobID(SDK, nil)(); got != nil {
		t.Fatalf("SDK: %q", got)
	}
	t.Setenv("JOB_CHECK_RUN_ID", "77")
	if got := handGitHubJobID(Mini, nil)(); got != nil {
		t.Fatalf("JOB_CHECK_RUN_ID: %q", got)
	}
	t.Setenv("JOB_CHECK_RUN_ID", "")
	os.Unsetenv("GITHUB_SHA")
	if got := handGitHubJobID(Mini, nil)(); got != nil {
		t.Fatalf("outside GitHub Actions: %q", got)
	}
}

// A scan that outlasts preparation does not hold up go test: the test
// processes scan for themselves.
func TestHandGitHubJobIDDoesNotWaitForAStalledScan(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	originalDirs, originalWait := gitHubDiagnosticsDirs, gitHubJobIDWait
	gitHubDiagnosticsDirs = func() []string {
		<-release
		return nil
	}
	gitHubJobIDWait = 50 * time.Millisecond
	t.Cleanup(func() { gitHubDiagnosticsDirs, gitHubJobIDWait = originalDirs, originalWait })
	t.Setenv("JOB_CHECK_RUN_ID", "")
	t.Setenv("GITHUB_SHA", "0123456789abcdef")

	started := time.Now()
	if got := handGitHubJobID(Mini, nil)(); got != nil {
		t.Fatalf("stalled scan handed %q", got)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("waited %s", elapsed)
	}
}
