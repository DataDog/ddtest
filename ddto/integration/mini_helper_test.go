package integration

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// A top-level test's instrumented function marks itself with t.Helper only
// when a wrapper calls it: when tRunner calls it directly, the mark changes no
// location that testing reports, except at the stack depth that ADAPTATIONS.md
// describes. A test that calls t.Helper itself reaches
// that function's frame through its logs, cleanups and skips; with retries, a
// wrapper calls the function, and the report goes past its frame.
func TestMiniHelperMarksKeepLocations(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod": "module example.com/helperfixture\ngo 1.25\n",
		"helper_test.go": `package helperfixture

import "testing"

func TestSelfHelper(t *testing.T) {
	t.Helper()
	t.Log("self")
}

func TestSelfHelperCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		t.Log("cleanup")
	})
}

func TestSelfHelperSkip(t *testing.T) {
	t.Helper()
	t.Skip("skipped")
}

func TestSelfHelperSubtest(t *testing.T) {
	t.Helper()
	t.Run("sub", func(t *testing.T) {
		t.Helper()
		t.Log("sub")
	})
}

func logHelper(t *testing.T) { t.Helper(); t.Log("helper") }

func TestHelperCall(t *testing.T) { logHelper(t) }
`,
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	location := func(function string) string {
		for _, site := range adaptedMiniCallSites {
			if site.function == function {
				return path.Base(site.mini)
			}
		}
		t.Fatalf("no adapted call site for %s", function)
		return ""
	}
	direct := location("integrations/gotesting.(*M).executeInternalTest.func1")
	subtest := location("integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1.1")
	for _, tc := range []struct {
		name  string
		retry bool
		frame string
	}{
		{"direct", false, direct},
		{"retry-wrapper", true, location("integrations/gotesting.runRetryAttemptBody")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := executeProfile(t, dir, bin, []string{"-test.v"}, true, tc.retry, "")
			if got.code != 0 || len(got.events) == 0 {
				t.Fatalf("exit %d with %d events\n%s\n%s", got.code, len(got.events), got.out, got.stderr)
			}
			for _, want := range []string{tc.frame + ": self", tc.frame + ": cleanup", tc.frame + ": skipped", subtest + ": sub", "helper_test.go:33: helper"} {
				if !strings.Contains(got.out, "    "+want+"\n") {
					t.Fatalf("missing %q\n%s", want, got.out)
				}
			}
		})
	}
}
