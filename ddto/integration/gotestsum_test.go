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
// PATH and their own flags, and gotestsum writes its own JUnit file.
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
	receiver := &capture{retry: true}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	junit := filepath.Join(t.TempDir(), "junit.xml")
	env := prependPATH(testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1"), bin)
	out, stderr, code := command(t, dir, env, driver, "gotestsum", "--format", "standard-verbose", "--junitfile", junit, "--", "-count=1", "./gotestsum", "--runtime=container")
	if code != 0 || !strings.Contains(out, "--- PASS: TestFlakyOnce") || !strings.Contains(out, "--- PASS: TestEnvironment") {
		t.Fatalf("ddto gotestsum: exit %d\nSTDOUT:\n%s\nSTDERR:\n%s", code, out, stderr)
	}
	if data, err := os.ReadFile(junit); err != nil || !strings.Contains(string(data), `name="TestFlakyOnce"`) {
		t.Fatalf("gotestsum's JUnit file: %v\n%s", err, data)
	}
	got := summarizeSessionEvents(receiver)
	if want := []string{"TestEnvironment pass", "TestFlakyOnce fail", "TestFlakyOnce pass", "TestRuntimeFlag pass"}; !reflect.DeepEqual(got.tests, want) || len(got.sessions) != 1 {
		t.Fatalf("tests %v, sessions %v; want tests %v and one session\n%s", got.tests, got.sessions, want, stderr)
	}
	// gotestsum's other commands run as gotestsum's.
	if out, _, code := command(t, dir, env, driver, "gotestsum", "--version"); code != 0 || strings.TrimSpace(out) != "gotestsum version "+gotestsumVersion {
		t.Fatalf("ddto gotestsum --version: exit %d %q", code, out)
	}
}
