package integration

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// localTIAFixture is a module with three packages that share a library: each
// test of alpha depends on other files, alpha and beta both have a test
// TestSame in a file same_test.go, and gamma's test uses only a constant of
// the library, which its coverage cannot show.
var localTIAFixture = map[string]string{
	"README.md":           "# fixture\n",
	"lib/one.go":          "package lib\n\nfunc One() int { return 1 }\n",
	"lib/two.go":          "package lib\n\nfunc Two() int { return 2 }\n",
	"lib/lib.go":          "package lib\n\nconst Limit = 3\n\nfunc Unused() {}\n",
	"gamma/gamma_test.go": "package gamma\n\nimport (\n\t\"testing\"\n\n\t\"example.com/tia/lib\"\n)\n\nfunc TestLimit(t *testing.T) {\n\tif lib.Limit < 1 {\n\t\tt.Fatal(lib.Limit)\n\t}\n}\n",
	"alpha/alpha.go": `package alpha

import "example.com/tia/lib"

func A() int { return lib.One() }

func B() int { return lib.Two() }
`,
	"alpha/alpha_test.go": `package alpha

import (
	"os"
	"testing"
)

func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal(A())
	}
}

func TestB(t *testing.T) {
	if B() != 2 {
		t.Fatal(B())
	}
}

func TestData(t *testing.T) {
	if data, err := os.ReadFile("testdata/input.txt"); err != nil || len(data) == 0 {
		t.Fatal(err)
	}
}

// TestUnskippable runs whatever local TIA selects.
//
//dd:test.unskippable
func TestUnskippable(t *testing.T) {}
`,
	"alpha/flaky_test.go":      "package alpha\n\nimport \"testing\"\n\nfunc TestFlaky(t *testing.T) {\n\tif broken := false; broken {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
	"alpha/same_test.go":       "package alpha\n\nimport \"testing\"\n\nfunc TestSame(t *testing.T) {}\n",
	"alpha/testdata/input.txt": "input\n",
	"beta/beta.go":             "package beta\n\nimport \"example.com/tia/lib\"\n\nfunc Beta() int { return lib.Two() * 10 }\n",
	"beta/beta_test.go":        "package beta\n\nimport \"testing\"\n\nfunc TestBeta(t *testing.T) {\n\tif Beta() != 20 {\n\t\tt.Fatal(Beta())\n\t}\n}\n",
	"beta/same_test.go":        "package beta\n\nimport \"testing\"\n\nfunc TestSame(t *testing.T) {\n\tif Beta() != 20 {\n\t\tt.Fatal(Beta())\n\t}\n}\n",
}

// localTIATests are the fixture's tests, by package and name.
var localTIATests = []string{"alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestFlaky", "alpha.TestSame", "alpha.TestUnskippable", "beta.TestBeta", "beta.TestSame", "gamma.TestLimit"}

// tiaIntake answers the test processes' requests with ITR's settings as
// itr says, and counts the requests for skippable tests and the per-test
// coverage payloads.
type tiaIntake struct {
	itr       bool
	mu        sync.Mutex
	skippable int
	coverage  int
}

func (in *tiaIntake) handler(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/setting"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"tia","type":"ci_app_test_service_libraries_settings","attributes":{"code_coverage":false,"tests_skipping":%t,"itr_enabled":%t,"require_git":false,"known_tests_enabled":false,"impacted_tests_enabled":false,"flaky_test_retries_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`, in.itr, in.itr)
	case strings.HasSuffix(r.URL.Path, "/skippable"):
		in.skippable++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"meta":{"correlation_id":"tia"},"data":[]}`)
	case strings.HasSuffix(r.URL.Path, "/search_commits"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	case strings.HasSuffix(r.URL.Path, "/citestcov"):
		in.coverage++
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

type tiaFixture struct {
	t           *testing.T
	dir, driver string
	env         []string
	flags       []string
	intake      *tiaIntake
	// written is when the fixture last wrote a file, in the past, so that
	// local TIA's file table vouches for its content as it would for a file
	// that a user edited earlier.
	written time.Time
}

// newTIAFixture writes the fixture in a Git repository with a remote and
// returns it, run with ddto's local TIA and flags.
func newTIAFixture(t *testing.T, itr bool, flags ...string) *tiaFixture {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &tiaFixture{t: t, dir: dir, driver: sharedDriver(t, root), flags: flags, intake: &tiaIntake{itr: itr}, written: time.Now().Add(-time.Hour)}
	f.write("go.mod", fmt.Sprintf("module example.com/tia\n\ngo 1.25.0\n\nrequire github.com/DataDog/ddtest/ddto v0.0.0\n\nreplace github.com/DataDog/ddtest/ddto => %q\n", filepath.ToSlash(root)))
	for name, content := range localTIAFixture {
		f.write(name, content)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"add", "."},
		{"-c", "user.name=TIA Fixture", "-c", "user.email=tia@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "Fixture"},
		{"remote", "add", "origin", "https://example.invalid/acme/tia.git"},
	} {
		if out, stderr, code := command(t, dir, testEnv(), "git", args...); code != 0 {
			t.Fatalf("git %v: %s %s", args, out, stderr)
		}
	}
	server := newServer(http.HandlerFunc(f.intake.handler))
	t.Cleanup(server.Close)
	f.env = featureEnv(t, server.URL, "DD_TIA_ENABLED=true")
	return f
}

func (f *tiaFixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	// Each write gets its own time, so go test sees every change.
	f.written = f.written.Add(time.Second)
	if err := os.Chtimes(path, f.written, f.written); err != nil {
		f.t.Fatal(err)
	}
}

func (f *tiaFixture) edit(name, old, new string) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(name)))
	if err != nil || !strings.Contains(string(data), old) {
		f.t.Fatalf("%s lacks %q: %v", name, old, err)
	}
	f.write(name, strings.Replace(string(data), old, new, 1))
}

// tiaRun is what one run's tests did, by package and name: pass, fail or
// skip.
type tiaRun struct {
	actions map[string]string
	code    int
	output  string
}

// run runs the fixture's tests with local TIA and -count=1: a result that go
// test takes from its cache keeps the selection of the run that produced it.
func (f *tiaFixture) run(args ...string) tiaRun {
	f.t.Helper()
	args = append(append([]string{"test", "-json", "-count=1"}, f.flags...), args...)
	out, stderr, code := command(f.t, f.dir, f.env, f.driver, append(args, "./...")...)
	run := tiaRun{actions: map[string]string{}, code: code, output: out + stderr}
	for _, line := range strings.Split(out, "\n") {
		var event struct{ Action, Package, Test, Output string }
		if json.Unmarshal([]byte(line), &event) != nil || event.Package == "" {
			continue
		}
		pkg := event.Package[strings.LastIndex(event.Package, "/")+1:]
		if event.Test != "" && !strings.Contains(event.Test, "/") && (event.Action == "pass" || event.Action == "fail" || event.Action == "skip") {
			run.actions[pkg+"."+event.Test] = event.Action
		}
	}
	if strings.Contains(run.output, "WARN") && strings.Contains(run.output, "local TIA") {
		f.t.Fatalf("local TIA refused:\n%s", run.output)
	}
	return run
}

// expect checks that exactly the tests in ran ran, and that every other
// test was skipped.
func (f *tiaFixture) expect(name string, run tiaRun, ran ...string) {
	f.t.Helper()
	want := map[string]string{}
	for _, test := range localTIATests {
		want[test] = "skip"
	}
	for _, test := range ran {
		want[test] = "pass"
	}
	for test, action := range run.actions {
		if action == "fail" {
			want[test] = "fail" // Checked by the caller.
		}
	}
	if !maps.Equal(run.actions, want) {
		var diff []string
		for _, test := range localTIATests {
			if run.actions[test] != want[test] {
				diff = append(diff, fmt.Sprintf("%s=%s want %s", test, run.actions[test], want[test]))
			}
		}
		f.t.Fatalf("%s: %s\n%s", name, strings.Join(diff, ", "), run.output)
	}
}

// ddto's local TIA, with Go's counters: each run records what its tests
// covered, and the next one skips the tests whose dependencies did not
// change, whatever Datadog's ITR settings say.
func TestLocalTIACounters(t *testing.T) {
	f := newTIAFixture(t, false, "-cover", "-covermode=atomic")
	everything := slices.Clone(localTIATests)

	f.expect("first run records", f.run(), everything...)
	f.expect("no change", f.run(), "alpha.TestUnskippable")

	// The edits stay uncommitted: validity comes from content hashes.
	f.edit("lib/one.go", "return 1", "return 2 - 1")
	if out, _, _ := command(t, f.dir, testEnv(), "git", "status", "--porcelain"); !strings.Contains(out, "lib/one.go") {
		t.Fatalf("worktree is clean: %q", out)
	}
	f.expect("a dependency of TestA changed", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.expect("after recording TestA", f.run(), "alpha.TestUnskippable")

	// Each package has its own TestSame in same_test.go.
	f.edit("beta/beta.go", "* 10", "* 5 * 2")
	f.expect("beta changed", f.run(), "alpha.TestUnskippable", "beta.TestBeta", "beta.TestSame")

	f.edit("alpha/testdata/input.txt", "input", "changed input")
	f.expect("alpha's testdata changed", f.run(), "alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestFlaky", "alpha.TestSame", "alpha.TestUnskippable")

	f.write("lib/three.go", "package lib\n\nfunc Three() int { return 3 }\n")
	f.expect("a file was added to a dependency", f.run(), everything...)

	f.edit("README.md", "fixture", "local TIA fixture")
	f.expect("a file outside every package changed", f.run(), "alpha.TestUnskippable")

	// A failing test runs until it passes again.
	f.edit("alpha/flaky_test.go", "broken := false", "broken := true")
	run := f.run()
	if run.code == 0 || run.actions["alpha.TestFlaky"] != "fail" {
		t.Fatalf("TestFlaky did not fail: %d %v", run.code, run.actions)
	}
	f.expect("a test file changed", run, "alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestSame", "alpha.TestUnskippable")
	run = f.run()
	if run.actions["alpha.TestFlaky"] != "fail" {
		t.Fatalf("the failed test did not run again: %v", run.actions)
	}
	f.expect("the failed test runs again", run, "alpha.TestUnskippable")
	f.edit("alpha/flaky_test.go", "broken := true", "broken := false")
	f.expect("the failed test fixed", f.run(), "alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestFlaky", "alpha.TestSame", "alpha.TestUnskippable")

	// A run that -run narrows records what ran and keeps the other tests,
	// except those that the change since their last run affects.
	f.edit("lib/two.go", "return 2", "return 1 + 1")
	partial := f.run("-run", "^TestA$")
	if len(partial.actions) != 1 || partial.actions["alpha.TestA"] != "skip" {
		t.Fatalf("-run TestA: %v\n%s", partial.actions, partial.output)
	}
	f.expect("after a partial run", f.run(), "alpha.TestB", "alpha.TestUnskippable", "beta.TestBeta", "beta.TestSame")

	// gamma's test uses lib.Limit without running lib's code: a change to
	// what lib.go declares runs every test whose package links lib, and a
	// change to a function's body only the tests that ran it.
	f.edit("lib/lib.go", "Limit = 3", "Limit = 4")
	f.expect("a constant changed", f.run(), everything...)
	f.edit("lib/lib.go", "func Unused() {}", "func Unused() { _ = Limit }")
	f.expect("only a body changed", f.run(), "alpha.TestUnskippable")

	if f.intake.skippable != 0 || f.intake.coverage != 0 {
		t.Fatalf("requests for skippable tests %d, coverage payloads %d; want none", f.intake.skippable, f.intake.coverage)
	}
}

// Under -race, Go's counters count atomically. Datadog's ITR is enabled here,
// without per-test coverage: local TIA requests no skippable tests and
// sends no coverage.
func TestLocalTIARace(t *testing.T) {
	f := newTIAFixture(t, true, "-race", "-cover")
	f.expect("first run records", f.run(), localTIATests...)
	f.expect("no change", f.run(), "alpha.TestUnskippable")
	f.edit("lib/one.go", "return 1", "return 2 - 1")
	f.expect("a dependency of TestA changed", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	if f.intake.skippable != 0 || f.intake.coverage != 0 {
		t.Fatalf("requests for skippable tests %d, coverage payloads %d; want none", f.intake.skippable, f.intake.coverage)
	}
}

// With file marks, which also see test files, a test depends on the test
// files that it runs, not on every test file of its package.
func TestLocalTIAFileMarks(t *testing.T) {
	f := newTIAFixture(t, false, "--experimental-file-coverage")
	f.expect("first run records", f.run(), localTIATests...)
	f.expect("no change", f.run(), "alpha.TestUnskippable")
	f.edit("lib/one.go", "return 1", "return 2 - 1")
	f.expect("a dependency of TestA changed", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.edit("alpha/flaky_test.go", "\t\tt.Fatal(\"broken\")", "\t\tt.Fatal(\"still broken\")")
	f.expect("a test's own file changed", f.run(), "alpha.TestFlaky", "alpha.TestUnskippable")
}
