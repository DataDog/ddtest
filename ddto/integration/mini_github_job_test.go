package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ddto reads the GitHub Actions job ID once and hands it to each test process,
// which removes the variable before os initializes, so tests never see it.
func TestMiniGitHubJobIDHandoff(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test

import (
	"os"
	"testing"
)

func TestHandoffHidden(t *testing.T) {
	if value, ok := os.LookupEnv("DDTO_GITHUB_JOB_ID"); ok {
		t.Fatalf("handoff visible to the client: %q", value)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "handoff_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("handoff.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	github := []string{"GITHUB_SHA=0123456789abcdef0123456789abcdef01234567", "GITHUB_SERVER_URL=https://github.com", "GITHUB_REPOSITORY=tonyredondo/dd-ci-testing-poc", "GITHUB_RUN_ID=123", "GITHUB_RUN_ATTEMPT=1", "GITHUB_JOB=unit", "JOB_CHECK_RUN_ID="}
	for _, c := range []struct {
		handoff, jobID, url string
	}{
		{"DDTO_GITHUB_JOB_ID=4242", `"ci.job.id":"4242"`, `/actions/runs/123/job/4242"`},
		// The CLI found no ID: the job name and checks URL remain.
		{"DDTO_GITHUB_JOB_ID=", `"ci.job.id":"unit"`, `/commit/0123456789abcdef0123456789abcdef01234567/checks"`},
	} {
		capture := runCIWireCase(t, dir, bin, []string{"-test.run=^TestHandoffHidden$"}, append(github, c.handoff), false, false)
		payloads, err := json.Marshal(capture.payloads)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payloads), c.jobID) || !strings.Contains(string(payloads), c.url) {
			t.Errorf("%s: job ID %s and URL %s missing from %s", c.handoff, c.jobID, c.url, payloads)
		}
	}
}

// A -exec program starts before the test binary could remove a handed value,
// so ddto hands none and the program sees the caller's environment.
func TestMiniGitHubJobIDNotHandedThroughExec(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	launcherDir := t.TempDir()
	launcherSource := `package main

import (
	"os"
	"os/exec"
)

func main() {
	if value, ok := os.LookupEnv("DDTO_GITHUB_JOB_ID"); ok {
		os.Stderr.WriteString("launcher sees DDTO_GITHUB_JOB_ID=" + value + "\n")
		os.Exit(3)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
`
	for name, content := range map[string]string{"go.mod": "module launcher\n\ngo 1.25\n", "main.go": launcherSource} {
		if err := os.WriteFile(filepath.Join(launcherDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(launcherDir, executableName("launcher"))
	if out, stderr, code := command(t, launcherDir, testEnv(), "go", "build", "-o", launcher, "."); code != 0 {
		t.Fatalf("build launcher: %d %s %s", code, out, stderr)
	}
	env := testEnv("DD_CIVISIBILITY_ENABLED=false", "GITHUB_SHA=0123456789abcdef0123456789abcdef01234567", "JOB_CHECK_RUN_ID=")
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-exec", launcher, "-run=^TestPass$", ".")
	if code != 0 {
		t.Fatalf("-exec run: %d %s %s", code, out, stderr)
	}
}
