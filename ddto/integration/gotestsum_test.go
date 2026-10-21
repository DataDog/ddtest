package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// gotestsumVersion is the gotestsum that TestGotestsumReportsTests installs.
const gotestsumVersion = "v1.13.0"

// ddto gotestsum runs gotestsum with its own arguments, and a go first on its
// PATH that runs as ddto go: the go test that gotestsum runs reports to CI
// Visibility, here with an automatic retry, while the tests see gotestsum's
// PATH and their own flags, and gotestsum writes its own JUnit file. The same
// holds through go run and go tool, and for any wrapper with --go-shim.
func TestGotestsumReportsTests(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bin := t.TempDir()
	if out, stderr, code := command(t, t.TempDir(), testEnv("GOBIN="+bin, "GOFLAGS=", "GOWORK=off"), "go", "install", "gotest.tools/gotestsum@"+gotestsumVersion); code != 0 {
		t.Fatalf("install gotestsum: %s %s", out, stderr)
	}
	if err := os.MkdirAll(filepath.Join(dir, "gotestsum"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, filepath.Join(dir, "gotestsum"), map[string]string{"gotestsum_test.go": `package gotestsum

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// The tests take a flag that ddto also has, which is theirs here.
var runtimeFlag = flag.String("runtime", "", "")

var runs int

func TestRuntimeFlag(t *testing.T) {
	if *runtimeFlag != "container" {
		t.Fatalf("-runtime=%q", *runtimeFlag)
	}
}

// TestFlakyOnce fails on its first execution in the process.
func TestFlakyOnce(t *testing.T) {
	runs++
	if runs == 1 {
		t.Fatal("first run fails")
	}
}

func TestEnvironment(t *testing.T) {
	if value, set := os.LookupEnv("DDTO_GO_SHIM"); set {
		t.Fatalf("DDTO_GO_SHIM=%q reached the test", value)
	}
	if strings.Contains(os.Getenv("PATH"), "go-shim") {
		t.Fatalf("the go shim is on the test's PATH: %s", os.Getenv("PATH"))
	}
}
`})
	// A wrapper that runs go itself, as make or task would, and a test runner
	// of the module that runs go test.
	wrapper := filepath.Join(buildProgram(t, goWrapperProgram, "gowrapper"), executableName("gowrapper"))
	if err := os.MkdirAll(filepath.Join(dir, "testrunner"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, filepath.Join(dir, "testrunner"), map[string]string{"main.go": strings.Replace(goWrapperProgram, `exec.Command("go", os.Args[1:]...)`, `exec.Command("go", append([]string{"test"}, os.Args[1:]...)...)`, 1)})
	// go tool runs a tool that the module declares.
	if out, stderr, code := command(t, dir, testEnv("GOFLAGS=", "GOWORK=off"), "go", "get", "-tool", "gotest.tools/gotestsum@"+gotestsumVersion); code != 0 {
		t.Fatalf("go get -tool: %s %s", out, stderr)
	}
	receiver := &capture{retry: true}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	junit := filepath.Join(t.TempDir(), "junit.xml")
	env := prependPATH(testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1"), bin)
	gotestsumArgs := []string{"--format", "standard-verbose", "--", "-count=1", "./gotestsum", "--runtime=container"}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"gotestsum", append([]string{"gotestsum", "--junitfile", junit}, gotestsumArgs...)},
		// go run and go tool put $GOROOT/bin first on gotestsum's PATH.
		{"go run", append([]string{"go", "run", "gotest.tools/gotestsum@" + gotestsumVersion}, gotestsumArgs...)},
		{"go tool", append([]string{"go", "tool", "gotestsum"}, gotestsumArgs...)},
		{"--go-shim wrapper", []string{"--go-shim", wrapper, "test", "-v", "-count=1", "./gotestsum", "--runtime=container"}},
		{"--go-shim go run", []string{"--go-shim", "go", "run", "./testrunner", "-v", "-count=1", "./gotestsum", "--runtime=container"}},
		// The wrapper's go run reaches ddto through the shim, which keeps it
		// for the runner's go test.
		{"--go-shim wrapper go run", []string{"--go-shim", wrapper, "run", "./testrunner", "-v", "-count=1", "./gotestsum", "--runtime=container"}},
	} {
		receiver.mu.Lock()
		receiver.events, receiver.failures = nil, nil
		receiver.mu.Unlock()
		out, stderr, code := command(t, dir, env, driver, tc.args...)
		if code != 0 || !strings.Contains(out, "--- PASS: TestFlakyOnce") || !strings.Contains(out, "--- PASS: TestEnvironment") {
			t.Fatalf("ddto %s: exit %d\nSTDOUT:\n%s\nSTDERR:\n%s", tc.name, code, out, stderr)
		}
		got := summarizeSessionEvents(receiver)
		if want := []string{"TestEnvironment pass", "TestFlakyOnce fail", "TestFlakyOnce pass", "TestRuntimeFlag pass"}; !reflect.DeepEqual(got.tests, want) || len(got.sessions) != 1 {
			t.Fatalf("ddto %s: tests %v, sessions %v; want tests %v and one session\n%s", tc.name, got.tests, got.sessions, want, stderr)
		}
	}
	if data, err := os.ReadFile(junit); err != nil || !strings.Contains(string(data), `name="TestFlakyOnce"`) {
		t.Fatalf("gotestsum's JUnit file: %v\n%s", err, data)
	}
	// gotestsum's other commands run as gotestsum's.
	if out, _, code := command(t, dir, env, driver, "gotestsum", "--version"); code != 0 || strings.TrimSpace(out) != "gotestsum version "+gotestsumVersion {
		t.Fatalf("ddto gotestsum --version: exit %d %q", code, out)
	}
	if _, stderr, code := command(t, dir, env, driver, "--go-shim"); code != 2 || !strings.Contains(stderr, "--go-shim needs a command") {
		t.Fatalf("ddto --go-shim: exit %d %q", code, stderr)
	}
}

// goWrapperProgram runs go with its arguments.
const goWrapperProgram = `package main

import (
	"errors"
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command("go", os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	var exit *exec.ExitError
	if err := cmd.Run(); errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	} else if err != nil {
		os.Exit(126)
	}
}
`
