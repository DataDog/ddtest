package runner

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/localtia"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
)

// Only DD_TIA_ENABLED enables local TIA; no flag does. DD_TIA_RECORD=false
// makes it skip without recording.
func TestLocalTIAEnvironment(t *testing.T) {
	for _, tc := range []struct {
		enabled, record string
		want, readOnly  bool
		warning         bool
	}{
		{"", "", false, false, false},
		{"true", "", true, false, false},
		{"true", "true", true, false, false},
		{"true", "false", true, true, false},
		{"1", "0", true, true, false},
		{"0", "", false, false, false},
		{"yes please", "", false, false, true},
		{"true", "sometimes", true, false, true},
	} {
		cli, warning := DefaultCLIOptions().WithLocalTIAEnvironment(tc.enabled, tc.record)
		if cli.LocalTIA != tc.want || cli.LocalTIAReadOnly != tc.readOnly || (warning != "") != tc.warning {
			t.Errorf("%q %q: LocalTIA=%t read-only=%t warning=%q", tc.enabled, tc.record, cli.LocalTIA, cli.LocalTIAReadOnly, warning)
		}
	}
	// Go test receives a flag with the name.
	if _, rest, err := ParseCLIArgs([]string{"--local-tia", "./..."}); err != nil || !slices.Equal(rest, []string{"--local-tia", "./..."}) {
		t.Fatalf("--local-tia: %q %v", rest, err)
	}
}

func TestLocalTIACoverageSource(t *testing.T) {
	for _, tc := range []struct {
		args []string
		plan Plan
		want string
	}{
		{nil, Plan{}, ""},
		{[]string{"-cover"}, Plan{}, ""},
		{[]string{"-covermode=set"}, Plan{}, ""},
		{[]string{"-cover", "-covermode=count"}, Plan{}, "counters count"},
		{[]string{"-race", "-cover"}, Plan{}, "counters atomic"},
		{[]string{"-coverpkg=./...", "-covermode=atomic"}, Plan{}, "counters atomic"},
		{nil, Plan{fileCoverage: true}, "file marks"},
		{nil, Plan{fileCoverage: true, fileCoverageContext: true}, "file marks with contexts"},
	} {
		opts, err := parseOptions(append(slices.Clone(tc.args), "."), "")
		if err != nil {
			t.Fatal(err)
		}
		if got := localTIACoverageSource(&tc.plan, opts); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.args, got, tc.want)
		}
	}
}

// The map's key changes with what changes the tests' build or behavior, and
// not with which tests run, how often, what go test prints, or the coverage
// that it collects: a run without coverage flags reads a map that a run with
// them recorded.
func TestLocalTIAConfiguration(t *testing.T) {
	env := &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1"}
	key := func(goflags string, args ...string) string {
		t.Helper()
		opts, err := parseOptions(args, goflags)
		if err != nil {
			t.Fatal(err)
		}
		return localTIAConfiguration(opts, goflags, env, "go1.27.3")
	}
	base := key("", "./...")
	for _, args := range [][]string{
		{"./a", "./b"},
		{"-run", "TestA", "-skip=TestB", "-count=1", "-v", "-json", "-timeout=1m", "./..."},
		{"-coverprofile=c.out", "-p=2", "-parallel=4", "-failfast", "./..."},
		{"-cover", "-covermode=atomic", "./..."},
		{"-covermode=count", "-coverpkg=./...", "./..."},
	} {
		if got := key("", args...); got != base {
			t.Errorf("%q changed the key", args)
		}
	}
	if got := key("-count=1 -v -cover", "./..."); got != base {
		t.Error("GOFLAGS that the map ignores changed the key")
	}
	changes := map[string]string{
		"-short":           key("", "-short", "./..."),
		"-tags":            key("", "-tags=integration", "./..."),
		"-race":            key("", "-race", "./..."),
		"GOFLAGS -tags":    key("-tags=integration", "./..."),
		"test binary flag": key("", "./...", "-args", "-update"),
		"go version":       localTIAConfiguration(mustParse(t, "./..."), "", env, "go1.26.0"),
		"GOARCH":           localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "arm64", CGOEnabled: "1"}, "go1.27.3"),
		"CGO_ENABLED":      localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "0"}, "go1.27.3"),
		"GOEXPERIMENT":     localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", GOEXPERIMENT: "nodwarf5"}, "go1.27.3"),
		"CGO_CFLAGS":       localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", Build: map[string]string{"CGO_CFLAGS": "-DANSWER=2"}}, "go1.27.3"),
		"GOAMD64":          localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", Build: map[string]string{"GOAMD64": "v3"}}, "go1.27.3"),
		"CC":               localTIAConfiguration(mustParse(t, "./..."), "", &goEnvironment{GOOS: "linux", GOARCH: "amd64", CGOEnabled: "1", Build: map[string]string{"CC": "clang"}}, "go1.27.3"),
	}
	for name, got := range changes {
		if got == base {
			t.Errorf("%s did not change the key", name)
		}
	}
}

func mustParse(t *testing.T, args ...string) options {
	t.Helper()
	opts, err := parseOptions(args, "")
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

// graphFixture lists, as go list -deps -test would, the build with tests of
// package p of a repository, and writes p's testdata. elsewhere is outside the
// repository. Every path is absolute on the host, as go list's are: on
// Windows, /elsewhere would not be.
func graphFixture(t *testing.T) (root, elsewhere string, packages []goPackage) {
	t.Helper()
	root, elsewhere = t.TempDir(), t.TempDir()
	for _, name := range []string{"p/testdata/in.txt", "p/testdata/sub/deep.txt"} {
		writeFile(t, root, name, name)
	}
	main := &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: "ex", Dir: root, Main: true}
	dir := func(name string) string { return filepath.Join(root, name) }
	return root, elsewhere, []goPackage{
		{ImportPath: "testing", Standard: true, Dir: filepath.Join(elsewhere, "goroot", "src", "testing"), GoFiles: []string{"testing.go"}},
		{ImportPath: "ex/lib", Dir: dir("lib"), Module: main, GoFiles: []string{"lib.go"}, TestGoFiles: []string{"lib_test.go"}, SFiles: []string{"asm.s"}, Imports: []string{"outside.com/dep", "ex/cache"}},
		{ImportPath: "ex/kit", Dir: dir("kit"), Module: main, GoFiles: []string{"kit.go"}},
		{ImportPath: "ex/vendored", Dir: dir("vendor/x"), GoFiles: []string{"v.go"}},
		{ImportPath: "outside.com/dep", Dir: filepath.Join(elsewhere, "dep"), GoFiles: []string{"dep.go"}},
		{ImportPath: "ex/cache", Dir: dir("modcache/x"), GoFiles: []string{"cached.go"}},
		{ImportPath: "ex/p", Dir: dir("p"), Name: "p", Module: main, GoFiles: []string{"p.go", "consts.go"}, TestGoFiles: []string{"in_test.go"}, XTestGoFiles: []string{"x_test.go"}, EmbedFiles: []string{"data.txt"}, TestEmbedFiles: []string{"golden.txt"}, Imports: []string{"ex/lib"}, TestImports: []string{"ex/vendored", "testing"}, XTestImports: []string{"ex/back", "ex/kit", "ex/p", "testing"}},
		{ImportPath: "ex/p [ex/p.test]", ForTest: "ex/p", Dir: dir("p"), Name: "p", Module: main, GoFiles: []string{"p.go", "consts.go", "in_test.go"}, EmbedFiles: []string{"data.txt"}, Imports: []string{"ex/lib", "ex/vendored", "testing"}},
		// back imports p, and only p's tests import it: go list lists only its
		// copy recompiled for them.
		{ImportPath: "ex/back [ex/p.test]", ForTest: "ex/p", Dir: dir("back"), Name: "back", Module: main, GoFiles: []string{"back.go"}, Imports: []string{"ex/p [ex/p.test]"}},
		{ImportPath: "ex/p_test [ex/p.test]", ForTest: "ex/p", Dir: dir("p"), Name: "p_test", Module: main, GoFiles: []string{"x_test.go"}, Imports: []string{"ex/back [ex/p.test]", "ex/kit", "ex/p [ex/p.test]", "testing"}},
		{ImportPath: "ex/p.test", Dir: dir("p"), Name: "main", GoFiles: []string{filepath.Join(elsewhere, "go-build", "testmain.go")}, Imports: []string{"ex/p [ex/p.test]", "ex/p_test [ex/p.test]", "testing"}},
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fixtureGraph returns the graph of graphFixture, with every Go file's state
// saying it has code except consts.go.
func fixtureGraph(t *testing.T, coverPatterns ...string) tiaGraph {
	t.Helper()
	root, elsewhere, tested := graphFixture(t)
	g := localTIAGraph(tiaGraphInput{
		tested: tested, root: root, excluded: []string{filepath.Join(root, "modcache")},
		moduleFiles:   []string{filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"), filepath.Join(elsewhere, "go.work")},
		coverPatterns: coverPatterns, dir: root,
	})
	for name := range g.paths {
		g.Files[name] = tiaFile{Hash: "h", Code: filepath.Base(name) != "consts.go"}
	}
	return g
}

// The graph holds the repository's packages that the build links, from their
// own entries or the copies recompiled for tests, and C(P) is what P's test
// binary links of them, P's test files and testdata, and the module files.
func TestLocalTIAGraph(t *testing.T) {
	g := fixtureGraph(t)
	if got := slices.Sorted(maps.Keys(g.Packages)); !slices.Equal(got, []string{"ex/back", "ex/kit", "ex/lib", "ex/p", "ex/vendored"}) {
		t.Fatalf("packages %q", got)
	}
	p := g.Packages["ex/p"]
	if !p.Tested || !slices.Equal(p.Files, []string{"p/consts.go", "p/data.txt", "p/p.go"}) || !slices.Equal(p.TestFiles, []string{"p/golden.txt", "p/in_test.go", "p/x_test.go"}) ||
		!slices.Equal(p.Testdata, []string{"p/testdata/in.txt", "p/testdata/sub/deep.txt"}) || !slices.Equal(p.TestImports, []string{"ex/back", "ex/kit", "ex/p", "ex/vendored", "testing"}) {
		t.Fatalf("p %+v", p)
	}
	if lib := g.Packages["ex/lib"]; lib.Tested || !slices.Equal(lib.Files, []string{"lib/asm.s", "lib/lib.go"}) {
		t.Fatalf("lib %+v", lib)
	}
	files, _ := g.closure("ex/p")
	want := []string{"back/back.go", "go.mod", "go.sum", "kit/kit.go", "lib/asm.s", "lib/lib.go", "p/consts.go", "p/data.txt", "p/golden.txt", "p/in_test.go", "p/p.go", "p/testdata/in.txt", "p/testdata/sub/deep.txt", "p/x_test.go", "vendor/x/v.go"}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("C(P) %q\nwant %q", got, want)
	}
}

// What the coverage source cannot see joins every test's dependencies.
func TestLocalTIAUnseenFiles(t *testing.T) {
	nonGo := []string{"go.mod", "go.sum", "lib/asm.s", "p/data.txt", "p/golden.txt", "p/testdata/in.txt", "p/testdata/sub/deep.txt"}
	for _, tc := range []struct {
		name          string
		counters      bool
		coverPatterns []string
		unseen        []string
	}{
		// Without -coverpkg, Go's counters count only the package under test,
		// never test files nor a file without code after initialization.
		{"counters", true, nil, append(slices.Clone(nonGo), "back/back.go", "kit/kit.go", "lib/lib.go", "p/consts.go", "p/in_test.go", "p/x_test.go", "vendor/x/v.go")},
		// -coverpkg counts what it names; ./... names no vendored package.
		{"coverpkg", true, []string{"./..."}, append(slices.Clone(nonGo), "p/consts.go", "p/in_test.go", "p/x_test.go", "vendor/x/v.go")},
		{"narrow coverpkg", true, []string{"./lib"}, append(slices.Clone(nonGo), "back/back.go", "kit/kit.go", "p/consts.go", "p/in_test.go", "p/p.go", "p/x_test.go", "vendor/x/v.go")},
		// File marks see every Go file; those without marks are in every
		// test's coverage.
		{"file marks", false, nil, nonGo},
	} {
		g := fixtureGraph(t, tc.coverPatterns...)
		slices.Sort(tc.unseen)
		if got := g.unseen("ex/p", tc.counters, len(tc.coverPatterns) != 0); !slices.Equal(got, tc.unseen) {
			t.Errorf("%s: unseen %q\nwant %q", tc.name, got, tc.unseen)
		}
	}
}

// storedMap is a map of a test package p, which imports lib, whose tests
// recorded their status and coverage with Go's counters, and the same files
// as they are now, in a run that records with Go's counters too.
func storedMap() (stored, current *tiaMap) {
	test := func(name, status string, covered ...string) tiaTest {
		return tiaTest{Test: localtia.Test{Module: "ex/p", Suite: "p_test.go", Name: name}, Status: status, Source: "counters", Covered: covered}
	}
	stored = &tiaMap{
		Files:   map[string]tiaFile{"p/p.go": {Hash: "p1"}, "p/p_test.go": {Hash: "t1"}, "p/testdata/in.txt": {Hash: "d1"}, "lib/a.go": {Hash: "a1"}, "lib/b.go": {Hash: "b1"}, "go.mod": {Hash: "m1"}},
		Modules: []string{"go.mod"},
		Packages: map[string]*tiaNode{
			"ex/lib": {Files: []string{"lib/a.go", "lib/b.go"}},
			"ex/p": {Files: []string{"p/p.go"}, Imports: []string{"ex/lib"}, Tested: true, TestFiles: []string{"p/p_test.go"}, Testdata: []string{"p/testdata/in.txt"},
				Unseen: map[string][]string{"counters": {"go.mod", "p/testdata/in.txt"}},
				Tests: []tiaTest{
					test("TestA", "pass", "lib/a.go", "p/p_test.go"),
					test("TestB", "pass", "lib/b.go", "p/p_test.go"),
					test("TestFail", "fail", "p/p_test.go"),
					test("TestSkipped", "skip", "p/p_test.go"),
					test("TestNoCoverage", "pass"),
				}},
		},
	}
	current = &tiaMap{Files: maps.Clone(stored.Files), Modules: []string{"go.mod"}, Packages: map[string]*tiaNode{}, Source: "counters"}
	for path, node := range stored.Packages {
		copied := *node
		copied.Tests = nil
		current.Packages[path] = &copied
	}
	return stored, current
}

func skippedNames(skip []*tiaTest) []string {
	var names []string
	for _, t := range skip {
		names = append(names, t.Name)
	}
	return names
}

// Each rule of the selection: a test is skipped only when it passed with
// coverage and none of its dependencies changed, and no file of C(P) was
// added or removed.
func TestLocalTIASelectionRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*tiaMap)
		want   []string
	}{
		{"nothing changed", func(*tiaMap) {}, []string{"TestA", "TestB"}},
		{"a covered file changed", func(m *tiaMap) { m.Files["lib/b.go"] = tiaFile{Hash: "b2"} }, []string{"TestA"}},
		// TestA does not run b.go, but could use what it declares.
		{"a declaration changed", func(m *tiaMap) { m.Files["lib/b.go"] = tiaFile{Hash: "b2", Decl: "changed"} }, nil},
		{"an unseen file changed", func(m *tiaMap) { m.Files["p/testdata/in.txt"] = tiaFile{Hash: "d2"} }, nil},
		{"a file outside C(P) changed", func(m *tiaMap) { m.Files["README.md"] = tiaFile{Hash: "r2"} }, []string{"TestA", "TestB"}},
		{"a file was added", func(m *tiaMap) {
			m.Files["lib/c.go"] = tiaFile{Hash: "c1"}
			m.Packages["ex/lib"].Files = []string{"lib/a.go", "lib/b.go", "lib/c.go"}
		}, nil},
		{"an import was added", func(m *tiaMap) {
			m.Files["kit/kit.go"] = tiaFile{Hash: "k1"}
			m.Packages["ex/kit"] = &tiaNode{Files: []string{"kit/kit.go"}}
			m.Packages["ex/p"].Imports = []string{"ex/kit", "ex/lib"}
		}, nil},
		{"a testdata file was removed", func(m *tiaMap) { m.Packages["ex/p"].Testdata = nil }, nil},
		// A run that does not record has no unseen files of its own: the
		// recording's decide.
		{"read-only, no change", readOnly(func(*tiaMap) {}), []string{"TestA", "TestB"}},
		{"read-only, an unseen file changed", readOnly(func(m *tiaMap) { m.Files["p/testdata/in.txt"] = tiaFile{Hash: "d2"} }), nil},
		{"read-only, a file that only another source cannot see changed", readOnly(func(m *tiaMap) { m.Files["lib/b.go"] = tiaFile{Hash: "b2"} }), []string{"TestA"}},
		// A run that records with another source cannot see lib/b.go: the
		// tests that the counters recorded depend on it too.
		{"recording with another source", func(m *tiaMap) {
			m.Source = "file marks"
			m.Packages["ex/p"].Unseen = map[string][]string{"file marks": {"go.mod", "lib/b.go"}}
			m.Files["lib/b.go"] = tiaFile{Hash: "b2"}
		}, nil},
	} {
		stored, current := storedMap()
		tc.change(current)
		if got := skippedNames(skippable(stored, current, "ex/p")); !slices.Equal(got, tc.want) {
			t.Errorf("%s: skipped %q, want %q", tc.name, got, tc.want)
		}
	}
}

// readOnly makes a change in a run that does not record.
func readOnly(change func(*tiaMap)) func(*tiaMap) {
	return func(m *tiaMap) {
		change(m)
		m.Source = ""
		for _, node := range m.Packages {
			node.Unseen = nil
		}
	}
}

func testNames(m *tiaMap, pkg string) []string {
	var names []string
	for _, t := range m.Packages[pkg].Tests {
		names = append(names, t.Name)
	}
	return names
}

// A run's records replace the entries of the tests that ran; the others
// stay while they remain valid with the run's files, so that a test that
// -run left out, or a test of a package that the run did not list, still
// runs after a change that it depends on.
func TestLocalTIAMerge(t *testing.T) {
	ranA := localtia.TestRecord{Test: localtia.Test{Module: "ex/p", Suite: "p_test.go", Name: "TestA"}, Status: "fail", Files: []localtia.File{{Path: "p/p_test.go"}, {Path: "lib/a.go", Bitmap: []byte{0x80}}, {Path: "../outside/x.go"}, {Path: "/abs/y.go"}}}
	all := []string{"TestA", "TestB", "TestFail", "TestNoCoverage", "TestSkipped"}

	// Nothing changed: TestA's entry is replaced, the others are kept.
	stored, current := storedMap()
	merged := mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, all) {
		t.Fatalf("kept %q", got)
	}
	a := merged.Packages["ex/p"].Tests[0]
	if a.Status != "fail" || !slices.Equal(a.Covered, []string{"lib/a.go", "p/p_test.go"}) || len(a.Bitmaps) != 1 || a.Bitmaps["lib/a.go"][0] != 0x80 {
		t.Fatalf("TestA %+v", a)
	}

	// b.go changed while only TestA ran: TestB's entry is no longer valid.
	stored, current = storedMap()
	current.Files["lib/b.go"] = tiaFile{Hash: "b2"}
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA", "TestFail", "TestNoCoverage", "TestSkipped"}) {
		t.Fatalf("after a change TestB depends on, kept %q", got)
	}
	if merged.Files["lib/b.go"].Hash != "b2" {
		t.Fatal("the table was not updated")
	}

	// b.go's declarations changed while only TestA ran: no other test of p
	// that did not run stays.
	stored, current = storedMap()
	current.Files["lib/b.go"] = tiaFile{Hash: "b2", Decl: "changed"}
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA"}) {
		t.Fatalf("after a declaration change, kept %q", got)
	}

	// A run that does not record merges no records, and still drops what its
	// changes affect.
	stored, current = storedMap()
	readOnly(func(m *tiaMap) { m.Files["lib/b.go"] = tiaFile{Hash: "b2"} })(current)
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA", "TestFail", "TestNoCoverage", "TestSkipped"}) || merged.Packages["ex/p"].Tests[0].Status != "pass" || merged.Packages["ex/p"].Unseen["counters"] == nil {
		t.Fatalf("after a run that does not record, kept %q", got)
	}

	// A run that records with another source keeps the counters' tests
	// only through both sources' unseen files, and each source's unseen
	// files while a test that it recorded stays.
	stored, current = storedMap()
	current.Source = "file marks"
	current.Packages["ex/p"].Unseen = map[string][]string{"file marks": {"go.mod", "lib/b.go"}}
	current.Files["lib/b.go"] = tiaFile{Hash: "b2"}
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA"}) || merged.Packages["ex/p"].Tests[0].Source != "file marks" {
		t.Fatalf("after recording with another source, kept %q", got)
	}
	if unseen := merged.Packages["ex/p"].Unseen; len(unseen) != 1 || unseen["file marks"] == nil {
		t.Fatalf("unseen files by source %v", unseen)
	}

	// A run that lists a tested package only as a dependency keeps its tests,
	// unless what they depend on changed.
	stored, current = storedMap()
	stored.Packages["ex/lib"].Tested, stored.Packages["ex/lib"].TestFiles = true, []string{"lib/a_test.go"}
	stored.Files["lib/a_test.go"] = tiaFile{Hash: "l1"}
	stored.Packages["ex/lib"].Unseen = map[string][]string{"counters": {"go.mod"}}
	stored.Packages["ex/lib"].Tests = []tiaTest{{Test: localtia.Test{Module: "ex/lib", Suite: "a_test.go", Name: "TestLib"}, Status: "pass", Source: "counters", Covered: []string{"lib/a.go", "lib/a_test.go"}}}
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/lib"); !slices.Equal(got, []string{"TestLib"}) {
		t.Fatalf("a package listed as a dependency kept %q", got)
	}
	current.Files["lib/a.go"] = tiaFile{Hash: "a2"}
	merged = mergeTIAMap(stored, current, nil)
	if got := testNames(merged, "ex/lib"); len(got) != 0 {
		t.Fatalf("after a change it depends on, a package listed as a dependency kept %q", got)
	}

	// A run that did not list p, which another run's change to b.go
	// affects, loses TestB too.
	stored, current = storedMap()
	delete(current.Packages, "ex/p")
	current.Files = map[string]tiaFile{"lib/a.go": {Hash: "a1"}, "lib/b.go": {Hash: "b2"}}
	merged = mergeTIAMap(stored, current, nil)
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA", "TestFail", "TestNoCoverage", "TestSkipped"}) {
		t.Fatalf("p, not listed, kept %q", got)
	}

	// A file was added: only the tests that ran keep an entry.
	stored, current = storedMap()
	current.Files["lib/c.go"] = tiaFile{Hash: "c1"}
	current.Packages["ex/lib"].Files = []string{"lib/a.go", "lib/b.go", "lib/c.go"}
	merged = mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}})
	if got := testNames(merged, "ex/p"); !slices.Equal(got, []string{"TestA"}) {
		t.Fatalf("after an added file, kept %q", got)
	}

	// A process that ended without completing its records loses every test.
	stored, current = storedMap()
	if got := testNames(mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {incomplete: true}}), "ex/p"); len(got) != 0 {
		t.Fatalf("after a crash, kept %q", got)
	}

	// A package whose files changed while it ran keeps none of its records,
	// and its other tests stay as they were.
	stored, current = storedMap()
	if got := testNames(mergeTIAMap(stored, current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}, changed: true}}), "ex/p"); !slices.Equal(got, all) || stored.Packages["ex/p"].Tests[0].Status != "pass" {
		t.Fatalf("after a change during the run, kept %q", got)
	}

	// A first run starts the package's entry.
	_, current = storedMap()
	if got := testNames(mergeTIAMap(emptyTIAMap(), current, map[string]tiaPackageRun{"ex/p": {records: []localtia.TestRecord{ranA}}}), "ex/p"); !slices.Equal(got, []string{"TestA"}) {
		t.Fatalf("first run %q", got)
	}
}

// The stored map names each file once, by index, and keeps what decodes to
// the same map; a hash that a racy modification time cannot vouch for is
// stored without that time.
func TestLocalTIAMapEncoding(t *testing.T) {
	m, _ := storedMap()
	m.Packages["ex/p"].Tests[0].Bitmaps = map[string][]byte{"lib/a.go": {0x40}}
	m.Packages["ex/p"].Tests[1].Bitmaps = map[string][]byte{"lib/b.go": {0x40}}
	m.Files["lib/a.go"] = tiaFile{Size: 3, ModTime: 7, Hash: "a1", Decl: "d1", Code: true}
	m.Files["lib/b.go"] = tiaFile{Size: 3, ModTime: 9, Hash: "b1", racy: true}
	m.Files["unused.go"] = tiaFile{Hash: "u"}
	stored := encodeTIAMap(m)
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var read tiaMapFile
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	decoded, ok := read.decode()
	if !ok {
		t.Fatal("decode")
	}
	if len(read.Bitmaps) != 1 || decoded.Packages["ex/p"].Tests[1].Bitmaps["lib/b.go"][0] != 0x40 {
		t.Fatalf("bitmaps %v", read.Bitmaps)
	}
	if len(read.Files) != 6 || decoded.Files["unused.go"].Hash != "" {
		t.Fatalf("files %+v", read.Files)
	}
	if got := decoded.Files["lib/a.go"]; got != (tiaFile{Size: 3, ModTime: 7, Hash: "a1", Decl: "d1", Code: true}) {
		t.Fatalf("lib/a.go %+v", got)
	}
	if got := decoded.Files["lib/b.go"]; got.ModTime != 0 || got.Hash != "b1" {
		t.Fatalf("racy lib/b.go %+v", got)
	}
	want, got := m.Packages["ex/p"], decoded.Packages["ex/p"]
	if !sameStructure(want, got) || !slices.Equal(got.Unseen["counters"], want.Unseen["counters"]) || got.Tests[0].Source != "counters" || len(got.Tests) != len(want.Tests) ||
		!slices.Equal(got.Tests[0].Covered, want.Tests[0].Covered) || got.Tests[0].Bitmaps["lib/a.go"][0] != 0x40 {
		t.Fatalf("p %+v", got)
	}
	read.Packages["ex/p"].Tests[0].Covered[0] = 99
	if _, ok := read.decode(); ok {
		t.Fatal("an index out of range decoded")
	}
}

// A file whose size and modification time match its stored state is not
// read again; one that changed is, and so is one whose stored time was too
// recent to vouch for its content. After go test, changes come from stats.
func TestLocalTIAFileStates(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	paths := map[string]string{}
	for _, name := range []string{"same.go", "changed.go", "racy.go", "new.go"} {
		paths[name] = writeFile(t, dir, name, "package x\n")
		if err := os.Chtimes(paths[name], old, old); err != nil {
			t.Fatal(err)
		}
	}
	paths["missing.go"] = filepath.Join(dir, "missing.go")
	size := int64(len("package x\n"))
	known := map[string]tiaFile{
		"same.go":    {Size: size, ModTime: old.UnixNano(), Hash: "stored"},
		"changed.go": {Size: size, ModTime: old.Add(-time.Second).UnixNano(), Hash: "stored"},
		"racy.go":    {Size: size, ModTime: 0, Hash: "stored"},
	}
	states, read := fileStates(paths, known, time.Now())
	if read != 3 || states["same.go"].Hash != "stored" || states["changed.go"].Hash == "stored" || states["racy.go"].Hash == "stored" ||
		states["new.go"].Hash == "" || states["new.go"].Code || states["missing.go"].Hash != "" {
		t.Fatalf("read %d, states %+v", read, states)
	}
	if states, _ := fileStates(map[string]string{"fresh.go": writeFile(t, dir, "fresh.go", "package x\nfunc F() {}\n")}, nil, time.Now()); !states["fresh.go"].racy || !states["fresh.go"].Code {
		t.Fatalf("a file written now %+v", states["fresh.go"])
	}
	// The declarations' hash ignores function bodies.
	body := map[string]string{"a.go": writeFile(t, dir, "a.go", "package x\nconst A = 1\nfunc F() int { return A }\n"), "b.go": writeFile(t, dir, "b.go", "package x\nconst A = 1\nfunc F() int { return A + 0 }\n"), "c.go": writeFile(t, dir, "c.go", "package x\nconst A = 2\nfunc F() int { return A }\n")}
	if states, _ := fileStates(body, nil, time.Now()); states["a.go"].Decl == "" || states["a.go"].Decl != states["b.go"].Decl || states["a.go"].Decl == states["c.go"].Decl || states["a.go"].Hash == states["b.go"].Hash {
		t.Fatalf("declaration hashes %+v", states)
	}

	if changed := statsChanged(paths, states); len(changed) != 0 {
		t.Fatalf("changed without a change: %v", changed)
	}
	later := old.Add(time.Minute)
	if err := os.Chtimes(paths["same.go"], later, later); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "missing.go", "package x\n")
	if changed := statsChanged(paths, states); !maps.Equal(changed, map[string]bool{"same.go": true, "missing.go": true}) {
		t.Fatalf("changed %v", changed)
	}
}

func TestLocalTIASelectionCoverage(t *testing.T) {
	if tiaSelection("/repo/p", nil) != nil {
		t.Fatal("a selection that skips nothing")
	}
	one, two := filebitmap.FromLineCount(8), filebitmap.FromLineCount(8)
	one.Set(1)
	two.Set(4)
	selection := tiaSelection("/repo/p", []*tiaTest{
		{Test: localtia.Test{Name: "TestA"}, Bitmaps: map[string][]byte{"p/a.go": one.GetBuffer()}},
		{Test: localtia.Test{Name: "TestB"}, Bitmaps: map[string][]byte{"p/a.go": two.GetBuffer(), "p/b.go": two.GetBuffer()}},
	})
	union := filebitmap.NewFileBitmapFromBytes(selection.Coverage["p/a.go"])
	if selection.Dir != "/repo/p" || len(selection.Skip) != 2 || !union.Get(1) || !union.Get(4) || len(selection.Coverage) != 2 {
		t.Fatalf("selection %+v", selection)
	}
}

// After go test, the CLI reads only this run's records, and a package whose
// files changed meanwhile, which only their stats show, or whose process did
// not complete its records, merges none.
func TestLocalTIAFinish(t *testing.T) {
	root, state, mapDir := t.TempDir(), t.TempDir(), t.TempDir()
	run := &localTIARun{state: state, mapDir: mapDir, key: "key", current: tiaGraph{tiaMap: emptyTIAMap(), paths: map[string]string{}, testdata: map[string]string{}}}
	for _, name := range []string{"a", "b", "c", "d"} {
		file := name + "/" + name + "_test.go"
		run.current.paths[file] = writeFile(t, root, file, "package "+name)
		run.current.Packages["ex/"+name] = &tiaNode{dir: filepath.Join(root, name), Tested: true, TestFiles: []string{file}}
	}
	run.current.Files, _ = fileStates(run.current.paths, nil, time.Now())
	run.current.Source = "counters"
	records := func(name string, complete bool, tests ...string) {
		file := localtia.Records{Dir: filepath.Join(root, name), Complete: complete}
		for _, test := range tests {
			file.Tests = append(file.Tests, localtia.TestRecord{Test: localtia.Test{Name: test}, Status: "pass", Files: []localtia.File{{Path: name + "/" + name + "_test.go"}}})
		}
		data, _ := json.Marshal(file)
		writeFile(t, state, localtia.RecordsFilePrefix+name+".json", string(data))
	}
	records("a", true, "TestA")
	records("b", false)
	records("c", true, "TestC")
	// d's result came from Go's cache: it has no records.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(run.current.paths["c/c_test.go"], later, later); err != nil {
		t.Fatal(err)
	}

	run.finish(t.Context())
	merged, err := run.loadMap()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]string{"ex/a": {"TestA"}, "ex/b": nil, "ex/c": nil, "ex/d": nil} {
		if got := testNames(merged, name); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// One go list -deps -test query serves preparation's consumers of go list
// -deps, and file coverage's of go list -deps -test of the caller's
// packages: both get what their own queries list.
func TestTestListingSplits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/m\n\ngo 1.25\n")
	writeFile(t, dir, "lib/lib.go", "package lib\n\nimport \"strings\"\n\nfunc L() string { return strings.ToUpper(\"l\") }\n")
	writeFile(t, dir, "only/only.go", "package only\n\nimport \"example.com/m/p\"\n\nfunc O() int { return p.F() }\n")
	writeFile(t, dir, "p/p.go", "package p\n\nimport \"example.com/m/lib\"\n\nfunc F() int { return len(lib.L()) }\n")
	writeFile(t, dir, "p/p_test.go", "package p\n\nimport (\n\t\"net/url\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) { _ = url.URL{} }\n")
	writeFile(t, dir, "p/x_test.go", "package p_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/only\"\n)\n\nfunc TestO(t *testing.T) { only.O() }\n")
	writeFile(t, dir, "cmd/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "cmd/main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestCmd(t *testing.T) {}\n")
	list := func(args ...string) []goPackage {
		t.Helper()
		cmd := goCommand(t.Context(), dir, append([]string{"list", "-e", "-json=" + listFields + "," + testListFields}, args...)...)
		packages, err := readPackages(t.Context(), cmd, "list")
		if err != nil {
			t.Fatal(err)
		}
		return packages
	}
	paths := func(packages []goPackage) []string {
		var out []string
		for _, p := range packages {
			out = append(out, p.ImportPath)
		}
		return out
	}
	both := list("-deps", "-test", "./p", "./cmd", "testing", "strings")
	if got, want := paths(plainListing(both)), paths(list("-deps", "./p", "./cmd", "testing", "strings")); !slices.Equal(got, want) {
		t.Errorf("plain listing %q\nwant %q", got, want)
	}
	if got, want := slices.Sorted(slices.Values(paths(callerTestsListing(both, "testing", "strings")))), slices.Sorted(slices.Values(paths(list("-deps", "-test", "./p", "./cmd")))); !slices.Equal(got, want) {
		t.Errorf("caller's tests %q\nwant %q", got, want)
	}
}

// The map has format version 1, and a map of any other version, or one that
// does not parse, is ignored and rebuilt.
func TestLocalTIAMapVersion(t *testing.T) {
	if localTIAMapVersion != 1 {
		t.Fatalf("map version %d", localTIAMapVersion)
	}
	m, _ := storedMap()
	run := &localTIARun{mapDir: t.TempDir(), key: "key"}
	for _, tc := range []struct {
		name     string
		data     func() []byte
		packages int
	}{
		{"this version", func() []byte { data, _ := json.Marshal(encodeTIAMap(m)); return data }, 2},
		{"another version", func() []byte {
			stored := encodeTIAMap(m)
			stored.Version = 2
			data, _ := json.Marshal(stored)
			return data
		}, 0},
		{"not JSON", func() []byte { return []byte("{") }, 0},
	} {
		writeFile(t, run.mapDir, "key.json", string(tc.data()))
		run.loaded = nil
		loaded, err := run.loadMap()
		if err != nil || len(loaded.Packages) != tc.packages {
			t.Errorf("%s: %d packages, %v", tc.name, len(loaded.Packages), err)
		}
	}
}

// A -run or -skip pattern with a slash can select some subtests of a test.
func TestNarrowsSubtests(t *testing.T) {
	for _, tc := range []struct {
		goflags string
		args    []string
		want    bool
	}{
		{"", []string{"./..."}, false},
		{"", []string{"-run", "^TestA$", "./..."}, false},
		{"", []string{"-skip=TestB", "./..."}, false},
		{"", []string{"-run", "^TestA$/^x$", "./..."}, true},
		{"", []string{"-run=TestA/x", "./..."}, true},
		{"", []string{"-skip", "/slow", "./..."}, true},
		{"", []string{"-test.run=TestA/x", "./..."}, true},
		{"", []string{"./...", "-args", "-test.run=TestA/x"}, true},
		{"-run=TestA/x", []string{"./..."}, true},
		{"", []string{"./a/b", "-v"}, false},
	} {
		opts, err := parseOptions(tc.args, tc.goflags)
		if err != nil {
			t.Fatal(err)
		}
		if got := narrowsSubtests(opts, tc.goflags); got != tc.want {
			t.Errorf("%q %q: %t, want %t", tc.goflags, tc.args, got, tc.want)
		}
	}
}
