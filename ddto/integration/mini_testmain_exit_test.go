package integration

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// A TestMain that returns without os.Exit exits with m.exitCode, which Go's
// generated main reads. A test that still fails after its retries must exit 1
// there too, also when the retries run in their own processes after M.Run.
func TestMiniTestMainWithoutExitKeepsRetryStatus(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":       "module example.com/testmainexit\ngo 1.25\n",
		"main_test.go": "package testmainexit\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) { m.Run() }\n\nfunc TestAlwaysFails(t *testing.T) { t.Error(\"fails\") }\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		receiver := &capture{retry: true}
		server := newServer(http.HandlerFunc(receiver.handler))
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode)
		out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.run=^TestAlwaysFails$")
		server.Close()
		if code != 1 {
			t.Errorf("%s retries: exit %d, want 1\n%s\n%s", mode, code, out, stderr)
		}
	}
}

// testopt.RunM(m) in a TestMain without os.Exit, built without ddto: Go's
// generated main reads m.exitCode, which must hold the result after retries.
func TestMiniManualRunMWithoutExitKeepsRetryStatus(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":       fmt.Sprintf("module example.com/manualexit\ngo 1.25.0\nrequire github.com/tonyredondo/dd-ci-testing-poc v0.0.0\nreplace github.com/tonyredondo/dd-ci-testing-poc => %q\n", filepath.ToSlash(root)),
		"main_test.go": "package manualexit\n\nimport (\n\t\"testing\"\n\n\t\"github.com/tonyredondo/dd-ci-testing-poc/testopt\"\n)\n\nfunc TestMain(m *testing.M) { testopt.RunM(m) }\n\nfunc TestAlwaysFails(t *testing.T) { t.Error(\"fails\") }\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), "go", "test", "-mod=mod", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		receiver := &capture{retry: true}
		server := newServer(http.HandlerFunc(receiver.handler))
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode)
		out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.run=^TestAlwaysFails$")
		server.Close()
		if code != 1 {
			t.Errorf("%s retries: exit %d, want 1\n%s\n%s", mode, code, out, stderr)
		}
	}
}
