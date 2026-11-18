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
	itr bool
	// efd turns Early Flake Detection on: every test of the fixture is new
	// and runs again without coverage.
	efd       bool
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
		fmt.Fprintf(w, `{"data":{"id":"tia","type":"ci_app_test_service_libraries_settings","attributes":{"code_coverage":false,"tests_skipping":%t,"itr_enabled":%t,"require_git":false,"known_tests_enabled":%t,"impacted_tests_enabled":false,"flaky_test_retries_enabled":false,"early_flake_detection":{"enabled":%t,"faulty_session_threshold":100,"slow_test_retries":{"5s":2}},"test_management":{"enabled":false}}}}`, in.itr, in.itr, in.efd, in.efd)
	case strings.HasSuffix(r.URL.Path, "/ci/libraries/tests"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"id":"tia","type":"ci_app_libraries_tests","attributes":{"tests":{"example.com/other":{"other_test.go":["TestOther"]}}}}}`)
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
	// readOnly runs local TIA with DD_TIA_RECORD=false; otherwise record,
	// when set, is DD_TIA_RECORD's value, whose default is true.
	readOnly bool
	record   string
	// refusal is the reason that local TIA is expected to give for not
	// running, or "" when it should run.
	refusal string
	// tests are the fixture's top-level tests, as package.Test.
	tests []string
	// chdir is go test's -C directory, or "".
	chdir string
	// written is when the fixture last wrote a file, in the past, so that
	// local TIA's file table vouches for its content as it would for a file
	// that a user edited earlier.
	written time.Time
}

// newTIAFixture writes files in a Git repository with a remote and returns
// it, run with ddto's local TIA and flags.
func newTIAFixture(t *testing.T, itr bool, files map[string]string, flags ...string) *tiaFixture {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &tiaFixture{t: t, dir: dir, driver: sharedDriver(t, root), flags: flags, intake: &tiaIntake{itr: itr}, written: time.Now().Add(-time.Hour), tests: localTIATests}
	f.write("go.mod", fmt.Sprintf("module example.com/tia\n\ngo 1.25.0\n\nrequire github.com/DataDog/ddtest/ddto v0.0.0\n\nreplace github.com/DataDog/ddtest/ddto => %q\n", filepath.ToSlash(root)))
	for name, content := range files {
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
	words := []string{"test"}
	if f.chdir != "" {
		words = append(words, "-C", f.chdir)
	}
	args = append(append(append(words, "-json", "-count=1"), f.flags...), args...)
	env := slices.Clone(f.env)
	switch {
	case f.readOnly:
		env = append(env, "DD_TIA_RECORD=false")
	case f.record != "":
		env = append(env, "DD_TIA_RECORD="+f.record)
	}
	out, stderr, code := command(f.t, f.dir, env, f.driver, append(args, "./...")...)
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
	refused := strings.Contains(run.output, "WARN") && strings.Contains(run.output, "local TIA")
	if refused != (f.refusal != "") || !strings.Contains(run.output, f.refusal) {
		f.t.Fatalf("local TIA refusal %t, want %q:\n%s", refused, f.refusal, run.output)
	}
	return run
}

// expect checks that exactly the tests in ran ran and passed, that every
// other test of the fixture was skipped, and that go test passed.
func (f *tiaFixture) expect(name string, run tiaRun, ran ...string) {
	f.t.Helper()
	f.expectFailing(name, run, nil, ran...)
}

// expectFailing is expect for a run in which the tests in failing ran and
// failed, and go test failed.
func (f *tiaFixture) expectFailing(name string, run tiaRun, failing []string, ran ...string) {
	f.t.Helper()
	want := map[string]string{}
	for _, test := range f.tests {
		want[test] = "skip"
	}
	for _, test := range ran {
		want[test] = "pass"
	}
	for _, test := range failing {
		want[test] = "fail"
	}
	if !maps.Equal(run.actions, want) {
		tests := maps.Clone(want)
		maps.Copy(tests, run.actions)
		var diff []string
		for _, test := range slices.Sorted(maps.Keys(tests)) {
			if run.actions[test] != want[test] {
				diff = append(diff, fmt.Sprintf("%s=%s want %s", test, run.actions[test], want[test]))
			}
		}
		f.t.Fatalf("%s: %s\n%s", name, strings.Join(diff, ", "), run.output)
	}
	if (run.code == 0) != (len(failing) == 0) {
		f.t.Fatalf("%s: go test exited with %d\n%s", name, run.code, run.output)
	}
}

// ddto's local TIA, with Go's counters: each run records what its tests
// covered, and the next one skips the tests whose dependencies did not
// change, whatever Datadog's ITR settings say.
func TestLocalTIACounters(t *testing.T) {
	f := newTIAFixture(t, false, localTIAFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
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
	flaky := []string{"alpha.TestFlaky"}
	f.edit("alpha/flaky_test.go", "broken := false", "broken := true")
	f.expectFailing("a test file changed", f.run(), flaky, "alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestSame", "alpha.TestUnskippable")
	f.expectFailing("the failed test runs again", f.run(), flaky, "alpha.TestUnskippable")
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
	f := newTIAFixture(t, true, localTIAFixture, "-race", "-cover", "-coverpkg=./...")
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
	f := newTIAFixture(t, false, localTIAFixture, "--experimental-file-coverage")
	f.expect("first run records", f.run(), localTIATests...)
	f.expect("no change", f.run(), "alpha.TestUnskippable")
	f.edit("lib/one.go", "return 1", "return 2 - 1")
	f.expect("a dependency of TestA changed", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.edit("alpha/flaky_test.go", "\t\tt.Fatal(\"broken\")", "\t\tt.Fatal(\"still broken\")")
	f.expect("a test's own file changed", f.run(), "alpha.TestFlaky", "alpha.TestUnskippable")
}

// With DD_TIA_RECORD=false, local TIA skips from what recording runs stored
// and records nothing: a test that a change affects runs, and loses its
// entry, until a recording run records it again.
func TestLocalTIAReadOnly(t *testing.T) {
	f := newTIAFixture(t, false, localTIAFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.readOnly = true
	f.expect("read-only without a map", f.run(), localTIATests...)
	f.readOnly = false
	f.expect("recording run", f.run(), localTIATests...)
	f.readOnly = true
	f.expect("read-only, no change", f.run(), "alpha.TestUnskippable")
	f.edit("lib/one.go", "return 1", "return 2 - 1")
	f.expect("read-only, a dependency of TestA changed", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.expect("read-only again: TestA has no entry", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.readOnly = false
	f.expect("recording run records TestA", f.run(), "alpha.TestA", "alpha.TestUnskippable")
	f.readOnly = true
	f.expect("read-only after recording", f.run(), "alpha.TestUnskippable")
}

// localTIAParallelFixture's tests each wait for the other: they pass only
// when they run in parallel.
var localTIAParallelFixture = map[string]string{
	"par/par_test.go": `package par

import "testing"

var first, second = make(chan struct{}), make(chan struct{})

func TestFirst(t *testing.T) {
	t.Parallel()
	close(first)
	<-second
}

func TestSecond(t *testing.T) {
	t.Parallel()
	close(second)
	<-first
}
`,
}

// A run that does not record collects no per-test coverage of its own, which
// Go's counters would need top-level tests to run one at a time for: they
// keep t.Parallel.
func TestLocalTIAReadOnlyKeepsParallel(t *testing.T) {
	f := newTIAFixture(t, false, localTIAParallelFixture, "-cover", "-covermode=atomic", "-parallel=2", "-timeout=1m")
	f.readOnly = true
	run := f.run()
	if run.code != 0 || run.actions["par.TestFirst"] != "pass" || run.actions["par.TestSecond"] != "pass" {
		t.Fatalf("parallel tests did not pass together: %d %v\n%s", run.code, run.actions, run.output)
	}
}

// A read-only run without coverage flags, as a pull request's or a
// developer's, reads the map that a run with them recorded, and depends on
// what that recording's coverage could not see.
func TestLocalTIAReadOnlyWithoutCoverageFlags(t *testing.T) {
	f := newTIAFixture(t, false, localTIAFixture)
	f.expect("recording run", f.run("-cover", "-covermode=atomic", "-coverpkg=./..."), localTIATests...)
	f.readOnly = true
	f.expect("read-only without coverage flags, no change", f.run(), "alpha.TestUnskippable")
	f.edit("alpha/testdata/input.txt", "input", "changed input")
	f.expect("read-only, a file that the recording could not see changed", f.run(), "alpha.TestA", "alpha.TestB", "alpha.TestData", "alpha.TestFlaky", "alpha.TestSame", "alpha.TestUnskippable")
}

// localTIACoverFixture's app tests a library that has tests of its own.
var localTIACoverFixture = map[string]string{
	"lib/value.go":      "package lib\n\nfunc Value() int { return 1 }\n",
	"lib/other.go":      "package lib\n\nfunc Other() int { return 0 }\n",
	"lib/value_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { _ = Value() }\n",
	"app/app_test.go":   "package app\n\nimport (\n\t\"testing\"\n\n\t\"example.com/tia/lib\"\n)\n\nfunc TestApp(t *testing.T) {\n\tif lib.Value() != 1 {\n\t\tt.Fatal(lib.Value())\n\t}\n}\n",
}

// Without -coverpkg, Go's counters count only the package under test, and a
// test of app would not show that it ran lib's code: local TIA only skips,
// and records nothing. With -coverpkg, a test depends on the files of lib
// that it ran.
func TestLocalTIACoverPackages(t *testing.T) {
	f := newTIAFixture(t, false, localTIACoverFixture, "-cover", "-covermode=atomic")
	f.tests = []string{"app.TestApp", "lib.TestValue"}
	f.refusal = "needs -coverpkg"
	f.expect("without -coverpkg", f.run(), f.tests...)
	f.edit("lib/other.go", "return 0", "return 0 + 0")
	f.expect("without -coverpkg, nothing is skipped", f.run(), f.tests...)

	f.flags, f.refusal = append(f.flags, "-coverpkg=./..."), ""
	f.expect("first run with -coverpkg", f.run(), f.tests...)
	f.edit("lib/other.go", "return 0 + 0", "return 0 * 1")
	f.expect("-coverpkg: a body that no test ran changed", f.run())
	f.edit("lib/value.go", "Value() int { return 1 }", "Value() int { return 2 }")
	f.expectFailing("-coverpkg: a dependency's body changed", f.run(), []string{"app.TestApp"}, "lib.TestValue")
}

// localTIASubtestFixture's TestCases has a subtest that fails.
var localTIASubtestFixture = map[string]string{
	"sub/sub_test.go": "package sub\n\nimport \"testing\"\n\nfunc TestCases(t *testing.T) {\n\tt.Run(\"A\", func(t *testing.T) {})\n\tt.Run(\"B\", func(t *testing.T) { t.Fatal(\"B\") })\n}\n",
}

// A run that -run narrows to some subtests of a top-level test records
// nothing: its coverage and status would not be the whole test's.
func TestLocalTIASubtestNarrowing(t *testing.T) {
	f := newTIAFixture(t, false, localTIASubtestFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.tests = []string{"sub.TestCases"}
	f.expect("only the passing subtest", f.run("-run", "^TestCases$/^A$"), "sub.TestCases")
	f.expectFailing("every subtest", f.run(), []string{"sub.TestCases"})
}

// Local TIA hashes the files in the repository, not those that a user's
// -overlay compiles instead: it refuses to run with one. (The fixture builds
// without coverage flags: with -coverpkg, ddto does not compile a covered
// package from the overlay's file, which is another defect.)
func TestLocalTIAUserOverlay(t *testing.T) {
	f := newTIAFixture(t, false, localTIACoverFixture)
	f.tests = []string{"app.TestApp", "lib.TestValue"}
	backing := filepath.Join(t.TempDir(), "value.go")
	writeBacking := func(value string) {
		if err := os.WriteFile(backing, []byte("package lib\n\nfunc Value() int { return "+value+" }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeBacking("1")
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	data, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(f.dir, "lib", "value.go"): backing}})
	if err := os.WriteFile(overlay, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.flags = append(f.flags, "-overlay="+overlay)
	f.refusal = "-overlay"
	f.expect("first run", f.run(), f.tests...)
	writeBacking("20") // Another size: Go trusts a file whose size and time match.
	f.expectFailing("the overlay's file changed", f.run(), []string{"app.TestApp"}, "lib.TestValue")
}

// localTIAInitFixture's tests depend on code that runs before them: a
// package's initialization and TestMain.
var localTIAInitFixture = map[string]string{
	"lib/state.go":      "package lib\n\nvar initial = compute()\n\nfunc Initial() int { return initial }\n",
	"lib/compute.go":    "package lib\n\nfunc compute() int { return 1 }\n",
	"lib/prepare.go":    "package lib\n\nvar Prepared int\n\nfunc Prepare() { Prepared = 1 }\n",
	"app/main_test.go":  "package app\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/tia/lib\"\n)\n\nfunc TestMain(m *testing.M) {\n\tlib.Prepare()\n\tos.Exit(m.Run())\n}\n",
	"app/state_test.go": "package app\n\nimport (\n\t\"testing\"\n\n\t\"example.com/tia/lib\"\n)\n\nfunc TestInitial(t *testing.T) {\n\tif lib.Initial() != 1 {\n\t\tt.Fatal(lib.Initial())\n\t}\n}\n\nfunc TestPrepared(t *testing.T) {\n\tif lib.Prepared != 1 {\n\t\tt.Fatal(lib.Prepared)\n\t}\n}\n",
}

// Code that runs before a package's first test, at initialization or in
// TestMain, is in no test's coverage: its files join every test of the
// package.
func TestLocalTIACodeBeforeTests(t *testing.T) {
	for _, flags := range [][]string{{"-cover", "-covermode=atomic", "-coverpkg=./..."}, {"--experimental-file-coverage"}, {"--experimental-file-coverage=context"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			f := newTIAFixture(t, false, localTIAInitFixture, flags...)
			f.tests = []string{"app.TestInitial", "app.TestPrepared"}
			f.expect("first run records", f.run(), f.tests...)
			f.expect("no change", f.run())
			f.edit("lib/compute.go", "return 1", "return 2")
			f.expectFailing("a function that initialization ran changed", f.run(), []string{"app.TestInitial"}, "app.TestPrepared")
			f.edit("lib/compute.go", "return 2", "return 1")
			f.expect("fixed", f.run(), f.tests...)
			f.edit("lib/prepare.go", "Prepared = 1", "Prepared = 2")
			f.expectFailing("a function that TestMain ran changed", f.run(), []string{"app.TestPrepared"}, "app.TestInitial")
		})
	}
}

// localTIAEmbedFixture embeds one of its own Go files as text.
var localTIAEmbedFixture = map[string]string{
	"emb/emb.go":      "package emb\n\nimport _ \"embed\"\n\n//go:embed render.go\nvar Source string\n",
	"emb/render.go":   "package emb\n\nfunc Render() string { return \"v1\" }\n",
	"emb/emb_test.go": "package emb\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestSource(t *testing.T) {\n\tif !strings.Contains(Source, \"v1\") {\n\t\tt.Fatal(Source)\n\t}\n}\n",
}

// An embedded file is data that a test can read without running it: every
// test of the package depends on it, whatever its extension.
func TestLocalTIAEmbeddedGoFile(t *testing.T) {
	f := newTIAFixture(t, false, localTIAEmbedFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.tests = []string{"emb.TestSource"}
	f.expect("first run records", f.run(), f.tests...)
	f.expect("no change", f.run())
	f.edit("emb/render.go", "v1", "v2")
	f.expectFailing("the embedded file changed", f.run(), f.tests)
}

// localTIARetryFixture's test fails its first attempt and passes its retry.
var localTIARetryFixture = map[string]string{
	"retry/retry_test.go": "package retry\n\nimport \"testing\"\n\nvar attempts int\n\nfunc TestFlaky(t *testing.T) {\n\tattempts++\n\tif attempts == 1 {\n\t\tt.Fatal(\"first attempt\")\n\t}\n}\n",
}

// A test that passed only on a retry is not skipped: its coverage comes from
// the attempt that failed.
func TestLocalTIARetriedPass(t *testing.T) {
	f := newTIAFixture(t, false, localTIARetryFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.env = append(f.env, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true")
	f.tests = []string{"retry.TestFlaky"}
	f.expect("passed on a retry", f.run(), f.tests...)
	f.expect("runs again", f.run(), f.tests...)
}

// Early Flake Detection runs a new test again without coverage, in the test
// process or, in process mode, in child processes: the test is not skipped
// next time, since its other executions could have run other code. Once it
// is no longer new, it runs once more and is recorded.
func TestLocalTIARepeatedExecutions(t *testing.T) {
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode, func(t *testing.T) {
			f := newTIAFixture(t, false, localTIACoverFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
			f.tests = []string{"app.TestApp", "lib.TestValue"}
			f.env = append(f.env, "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode)
			f.intake.efd = true
			f.expect("new tests, run again by Early Flake Detection", f.run(), f.tests...)
			f.intake.efd = false
			f.expect("known tests", f.run(), f.tests...)
			f.expect("unchanged", f.run())
		})
	}
}

// A run that records, by default or with DD_TIA_RECORD=true, but has no
// per-test coverage that local TIA can use, does not refuse: it says so, and
// only skips, as a read-only run does, recording nothing.
func TestLocalTIARecordFallback(t *testing.T) {
	f := newTIAFixture(t, false, localTIACoverFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.tests = []string{"app.TestApp", "lib.TestValue"}
	f.expect("recording with Go's counters", f.run(), f.tests...)
	for _, tc := range []struct {
		name, record, refusal string
		flags                 []string
		ran                   []string
	}{
		{"no coverage flags", "", "local TIA (DD_TIA_RECORD): recording needs per-test coverage", nil, nil},
		// Counters of other packages than the recording's take no line
		// coverage from the map: the tests run for their lines.
		{"counters without -coverpkg", "true", "local TIA (DD_TIA_RECORD): recording with Go's coverage counters needs -coverpkg", []string{"-cover", "-covermode=atomic"}, f.tests},
	} {
		f.flags, f.record, f.refusal = tc.flags, tc.record, tc.refusal
		run := f.run()
		f.expect(tc.name, run, tc.ran...)
		if !strings.Contains(run.output, "tests only skip, nothing is recorded") {
			t.Fatalf("%s: no fallback warning:\n%s", tc.name, run.output)
		}
	}
	// Go's counters in set mode cannot complete the coverage profile with
	// the lines of skipped tests: local TIA refuses, recording or not, and
	// every test runs.
	f.flags = []string{"-cover", "-covermode=set", "-coverpkg=./..."}
	for _, readOnly := range []bool{false, true} {
		f.readOnly, f.record = readOnly, ""
		f.refusal = "local TIA (DD_TIA_ENABLED): Go's coverage counters in set mode cannot complete the coverage profile"
		f.expect(fmt.Sprintf("counters in set mode, read-only %t", readOnly), f.run(), f.tests...)
	}
	f.readOnly = false
	f.flags, f.record, f.refusal = nil, "", "local TIA (DD_TIA_RECORD): recording needs per-test coverage"
	f.edit("lib/value.go", "return 1", "return 1 + 0")
	f.expect("a change: both tests run", f.run(), f.tests...)
	f.expect("nothing was recorded: both run again", f.run(), f.tests...)
}

// localTIAVendorFixture vendors a dependency and does not require ddto, whose
// runtime the CLI then provides through a workspace of its own.
var localTIAVendorFixture = map[string]string{
	"go.mod":          "module example.com/tia\n\ngo 1.25.0\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ./depsrc\n",
	"depsrc/go.mod":   "module example.com/dep\n\ngo 1.25.0\n",
	"depsrc/dep.go":   "package dep\n\nfunc Value() int { return 1 }\n",
	"app/app_test.go": "package app\n\nimport (\n\t\"testing\"\n\n\t\"example.com/dep\"\n)\n\nfunc TestApp(t *testing.T) {\n\tif dep.Value() != 1 {\n\t\tt.Fatal(dep.Value())\n\t}\n}\n",
}

// When the CLI provides a vendored project's dependencies from its own
// workspace, Go compiles them from outside the repository: local TIA
// refuses, as file coverage does.
func TestLocalTIAProvidedVendor(t *testing.T) {
	f := newTIAFixture(t, false, localTIAVendorFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	if out, stderr, code := command(t, f.dir, testEnv("GOFLAGS=-mod=mod"), "go", "mod", "vendor"); code != 0 {
		t.Fatalf("go mod vendor: %s %s", out, stderr)
	}
	f.tests = []string{"app.TestApp"}
	f.refusal = "vendor"
	f.expect("first run", f.run(), f.tests...)
	f.edit("vendor/example.com/dep/dep.go", "return 1", "return 2")
	f.expectFailing("a vendored dependency changed", f.run(), f.tests)
}

// A coverage source is the set of packages that Go's counters count, not
// -coverpkg's text: ./... from app counts app alone, and from the
// repository's root also lib. A run with the broader scope, which -run keeps
// from running app's test, keeps what the narrower recording could not see.
// (Run, a test that the narrower scope recorded would run again: its record
// has no line coverage of lib for the broader counters.)
func TestLocalTIACoverageScope(t *testing.T) {
	f := newTIAFixture(t, false, localTIACoverFixture, "-cover", "-covermode=atomic", "-coverpkg=./...")
	f.tests = []string{"app.TestApp"}
	f.chdir = "app"
	f.expect("recording from app", f.run(), "app.TestApp")
	f.chdir, f.tests = "", []string{"lib.TestValue"}
	f.expect("recording lib from the root", f.run("-run", "TestValue"), "lib.TestValue")
	f.tests = []string{"app.TestApp", "lib.TestValue"}
	f.edit("lib/value.go", "Value() int { return 1 }", "Value() int { return 2 }")
	// Without counters, which would run app's test for its line coverage.
	f.readOnly, f.flags = true, nil
	f.expectFailing("read-only after a change in lib", f.run(), []string{"app.TestApp"}, "lib.TestValue")
}

// A test that file marks recorded has no line bitmaps: a run that collects
// Go's counters runs it, through ITR's safeguard for candidates without line
// coverage, so that the coverage profile holds its lines.
func TestLocalTIALineCoverageFromAnotherSource(t *testing.T) {
	f := newTIAFixture(t, false, localTIACoverFixture, "--experimental-file-coverage")
	f.tests = []string{"app.TestApp", "lib.TestValue"}
	f.expect("recording with file marks", f.run(), f.tests...)
	f.expect("file marks again", f.run())
	profile := filepath.Join(t.TempDir(), "cover.out")
	f.flags, f.readOnly = []string{"-cover", "-covermode=atomic", "-coverpkg=./...", "-coverprofile=" + profile}, true
	f.expect("read-only with Go's counters", f.run(), f.tests...)
}
