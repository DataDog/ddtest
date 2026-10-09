package integration

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func cliDebugLines(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		const timestampLength = len("2006/01/02 15:04:05 ")
		if len(line) >= timestampLength && strings.HasPrefix(line[timestampLength:], version.BuildLogPrefix+" DEBUG: ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func TestCLIDebugBuildTransparency(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	for _, runtime := range []string{"mini", "sdk"} {
		t.Run(runtime, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			var hash [32]byte
			for i, enabled := range []string{"false", "true"} {
				env := testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG="+enabled, "DD_API_KEY=private-api-key", "PRIVATE_FLAG=private-env-value")
				out, stderr, code := command(t, dir, env, driver, "test", "--runtime="+runtime, "-c", "-o", bin, "-ldflags=-X=main.private=private-flag-value", ".")
				if code != 0 {
					t.Fatalf("build: %d %s %s", code, out, stderr)
				}
				logs := cliDebugLines(stderr)
				if i == 0 && logs != "" {
					t.Fatalf("disabled logs: %s", logs)
				}
				if i == 1 {
					for _, want := range []string{"runtime=" + runtime, "runtime module_version=", "resolve packages finished duration=", "instrument testing finished duration=", "resolve test libraries finished duration=", "plan ready", "tool selection", "go test finished duration=", "go test exit_code=0", "ddto exit_code=0"} {
						if !strings.Contains(logs, want) {
							t.Errorf("missing %q:\n%s", want, logs)
						}
					}
					for _, secret := range []string{"private-api-key", "private-env-value", "private-flag-value", "ldflags"} {
						if strings.Contains(logs, secret) {
							t.Errorf("debug exposed %q", secret)
						}
					}
				}
				data, err := os.ReadFile(bin)
				if err != nil {
					t.Fatal(err)
				}
				got := sha256.Sum256(data)
				if i == 0 {
					hash = got
				} else if got != hash {
					t.Fatal("debug changed the test executable")
				}
			}
		})
	}
}

func TestCLIDebugJSONAndResultCache(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	// Establish the cache, then change only the CLI debug setting.
	for _, enabled := range []string{"false", "true"} {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG="+enabled), driver, "test", "-json", "-run=^TestPass$", ".")
		if code != 0 {
			t.Fatalf("run: %d %s %s", code, out, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("stdout contains non-JSON: %q", line)
			}
		}
		if enabled == "true" && !strings.Contains(out, "(cached)") {
			t.Fatalf("debug invalidated result cache:\n%s\n%s", out, stderr)
		}
	}
}

func TestCLIDebugFailuresAndNativeBypass(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	invalidOverlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(invalidOverlay, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"test-failure", []string{"test", "-count=1", "-run=^TestFailures$/^Fail$", ".", "-args", "-mode=fail"}, 1, "go test exit_code=1"},
		{"package-failure", []string{"test", "./missing-package"}, 1, "go test exit_code=1"},
		{"overlay-failure", []string{"test", "-overlay=" + invalidOverlay, "."}, 2, "read user overlay finished duration="},
		{"help", []string{"test", "-help"}, 2, "instrumentation bypass help=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true"), driver, tc.args...)
			logs := cliDebugLines(stderr)
			if code != tc.code || !strings.Contains(logs, tc.want) {
				t.Fatalf("exit=%d want=%d logs=%s", code, tc.code, logs)
			}
			if tc.name == "package-failure" && !strings.Contains(logs, "status=error") {
				t.Fatal("Go package failure reported success")
			}
			if tc.name == "help" && strings.Contains(logs, "prepare started") {
				t.Fatal("help started instrumentation")
			}
		})
	}
}

// No initial package query should run against a module that cannot resolve Mini.
func TestCLIDebugEarlyLocalProvisioning(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":         "module example.com/earlymini\n\ngo 1.25.0\n",
		"client_test.go": "package earlymini\nimport \"testing\"\nfunc TestLocal(t *testing.T) {}\n",
	})
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true", "GOWORK=off", "GOPROXY=off"), driver, "test", "-count=1", ".")
	logs := cliDebugLines(stderr)
	if code != 0 {
		t.Fatalf("exit=%d %s %s", code, out, stderr)
	}
	if strings.Count(logs, "resolve packages started") != 1 || !strings.Contains(logs, "resolve runtime module finished duration=") {
		t.Fatalf("repeated package query: %s", logs)
	}
	provided, queried := strings.Index(logs, "provide runtime finished"), strings.Index(logs, "resolve packages started")
	if provided < 0 || queried < 0 || provided > queried {
		t.Fatalf("runtime provided after package query: %s", logs)
	}
}

func TestCLIDebugLocalProvisioning(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	_, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-droprequire=github.com/tonyredondo/dd-ci-testing-poc")
	if code != 0 {
		t.Fatal(stderr)
	}
	before, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	_, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true"), driver, "test", "-c", "-o", bin, ".")
	logs := cliDebugLines(stderr)
	if code != 0 || !strings.Contains(logs, "provide runtime finished duration=") || !strings.Contains(logs, "mini source=client-replacement local=true") || !strings.Contains(logs, "temporary_modfile=true") {
		t.Fatalf("exit=%d logs=%s stderr=%s", code, logs, stderr)
	}
	after, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("provisioning changed module: %v", err)
	}
}
