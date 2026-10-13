package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A -coverpkg pattern that could select a standard package or one of the
// Mini module keeps text profiles; a pattern that only selects client
// packages gets the in-memory hook.
func TestCoverPatternReachesCollector(t *testing.T) {
	root := t.TempDir()
	client, mini, goroot := filepath.Join(root, "work", "client"), filepath.Join(root, "work", "mini"), filepath.Join(root, "go")
	for _, tc := range []struct {
		pattern  string
		miniMain bool
		reaches  bool
	}{
		{"all", false, true}, {"std", false, true}, {"cmd", false, true},
		{"work", false, false}, {"work", true, true}, {"tool", false, true}, {"tool", true, true},
		{"internal/...", false, true}, {".../coverage/...", false, true}, {"...", false, true}, {"testing", false, true},
		{"github.com/...", false, true}, {"github.com/tonyredondo/...", false, true}, {"github.com/tonyredondo/dd-ci-testing-poc", false, true},
		{"github.com/tonyredondo/dd-ci-testing-poc/internal/...", false, true}, {"github.com/tonyredondo/dd-ci-testing-poc...", false, true},
		{"example.com/app/...", false, false}, {"example.com/app", false, false}, {"github.com/other/...", false, false},
		{"github.com/tonyredondo/dd-ci-testing-pocket/...", false, false}, {"github.com/tonyredondo/dd-ci-testing-p...", false, true},
		{"./...", false, false}, {".", false, false}, {"../...", false, true}, {"../mini/...", false, true}, {"../other/...", false, false},
		{"../../...", false, true}, {filepath.Join(goroot, "src") + "/...", false, true}, {filepath.Join(root, "work") + "/...", false, true},
		{filepath.Join(root, "elsewhere") + "/...", false, false},
		{"..", false, true}, {"../mi...", false, true}, {"../mini/sub/...", false, true}, {"net...", false, true},
		{"github.com/tonyredondo", false, false},
	} {
		if got := coverPatternReachesCollector(tc.pattern, client, mini, tc.miniMain, []string{goroot}); got != tc.reaches {
			t.Errorf("%q (Mini main %t) = %t, want %t", tc.pattern, tc.miniMain, got, tc.reaches)
		}
	}
	// Relative patterns are resolved from go test's directory.
	for _, pattern := range []string{".", "./...", "./internal"} {
		if !coverPatternReachesCollector(pattern, mini, mini, false, []string{goroot}) {
			t.Errorf("%q from Mini's directory does not reach it", pattern)
		}
	}
}

func TestWithinDir(t *testing.T) {
	root := t.TempDir()
	a, ab := filepath.Join(root, "a"), filepath.Join(root, "a", "b")
	for _, tc := range []struct {
		path, dir string
		within    bool
	}{
		{ab, a, true}, {a, a, true}, {filepath.Join(a, "..b"), a, true},
		{a, ab, false}, {filepath.Join(root, "ab"), a, false}, {root, a, false}, {"relative", a, false},
	} {
		if got := withinDir(tc.path, tc.dir); got != tc.within {
			t.Errorf("withinDir(%q, %q) = %t, want %t", tc.path, tc.dir, got, tc.within)
		}
	}
}

// coverageHookRefusal accepts a client-only selection and refuses plugins,
// standard or Mini packages under test, unclear selections and runtimes
// without the registration.
func TestCoverageHookRefusal(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	goroot := strings.TrimSpace(string(out))
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	miniModuleInfo := &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: miniModule, Dir: repo}
	clientModule := &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: "example.com/app", Dir: "/client", Main: true}
	coverageDir := filepath.Join(repo, "internal", "thirdparty", "dd-trace-go", "civisibility", "integrations", "gotesting", "coverage")
	entries, err := os.ReadDir(coverageDir)
	if err != nil {
		t.Fatal(err)
	}
	var coverageFiles []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			coverageFiles = append(coverageFiles, name)
		}
	}
	base := func() []goPackage {
		return []goPackage{
			{ImportPath: "example.com/app", Dir: "/client", Module: clientModule, TestGoFiles: []string{"app_test.go"}, Imports: []string{"fmt"}},
			{ImportPath: "testing", Standard: true, TestGoFiles: []string{"testing_test.go"}},
			{ImportPath: miniPackage, Module: miniModuleInfo, DepOnly: true},
			{ImportPath: miniCoveragePackage, Dir: coverageDir, GoFiles: slices.Clone(coverageFiles), Module: miniModuleInfo, DepOnly: true},
		}
	}
	refusal := func(opts options, listed []goPackage, testPlugin bool) string {
		return coverageHookRefusal("/client", opts, miniPackage, []string{goroot}, listed, testPlugin, nil)
	}
	if got := refusal(options{coverage: true}, base(), false); got != "" {
		t.Fatalf("client package under test refused: %s", got)
	}
	if got := refusal(options{coverage: true, coverPatterns: []string{"./..."}}, base(), false); got != "" {
		t.Fatalf("client selection refused: %s", got)
	}
	// go test instruments the named packages with tests, not dependencies,
	// testing or the runtime that ddto names.
	listed := append(base(),
		goPackage{ImportPath: "fmt", Standard: true, DepOnly: true, TestGoFiles: []string{"fmt_test.go"}},
		goPackage{ImportPath: "errors", Standard: true},
		goPackage{ImportPath: miniPackage + "/other", Module: miniModuleInfo},
	)
	listed[2].DepOnly, listed[2].TestGoFiles = false, []string{"runtime_test.go"}
	if got := refusal(options{coverage: true}, listed, false); got != "" {
		t.Fatalf("packages that go test does not instrument refused: %s", got)
	}
	// With -coverpkg, only the selection is instrumented.
	listed = append(base(), goPackage{ImportPath: "fmt", Standard: true, TestGoFiles: []string{"fmt_test.go"}})
	if got := refusal(options{coverage: true, coverPatterns: []string{"./..."}}, listed, false); got != "" {
		t.Fatalf("standard package under test with a client selection refused: %s", got)
	}
	cases := map[string]func() (options, []goPackage, bool){
		"plugin in graph": func() (options, []goPackage, bool) {
			return options{coverage: true}, append(base(), goPackage{ImportPath: "plugin", Standard: true, DepOnly: true}), false
		},
		"plugin imported": func() (options, []goPackage, bool) {
			listed := base()
			listed[0].Imports = []string{"plugin"}
			return options{coverage: true}, listed, false
		},
		"plugin imported by tests": func() (options, []goPackage, bool) {
			listed := base()
			listed[0].TestImports = []string{"plugin"}
			return options{coverage: true}, listed, false
		},
		"plugin imported by external tests": func() (options, []goPackage, bool) {
			listed := base()
			listed[0].XTestImports = []string{"plugin"}
			return options{coverage: true}, listed, false
		},
		"plugin behind a test import": func() (options, []goPackage, bool) { return options{coverage: true}, base(), true },
		"standard selection": func() (options, []goPackage, bool) {
			return options{coverage: true, coverPatterns: []string{"./...", "testing"}}, base(), false
		},
		"Mini package under test": func() (options, []goPackage, bool) {
			listed := base()
			listed[0].Module = miniModuleInfo
			return options{coverage: true}, listed, false
		},
		"standard package under test": func() (options, []goPackage, bool) {
			return options{coverage: true}, append(base(), goPackage{ImportPath: "fmt", Standard: true, TestGoFiles: []string{"fmt_test.go"}}), false
		},
		"standard package under external tests": func() (options, []goPackage, bool) {
			return options{coverage: true}, append(base(), goPackage{ImportPath: "fmt", Standard: true, XTestGoFiles: []string{"fmt_test.go"}}), false
		},
		"runtime without coverage package": func() (options, []goPackage, bool) {
			return options{coverage: true}, base()[:3], false
		},
		"runtime outside a module": func() (options, []goPackage, bool) {
			listed := base()
			listed[3].Module = nil
			return options{coverage: true, coverPatterns: []string{"./..."}}, listed, false
		},
		"runtime without registration": func() (options, []goPackage, bool) {
			listed := base()
			listed[3].Dir, listed[3].GoFiles = filepath.Join(repo, "internal", "runner"), []string{"coverage_hook.go"}
			return options{coverage: true}, listed, false
		},
	}
	for name, setup := range cases {
		opts, listed, testPlugin := setup()
		if got := refusal(opts, listed, testPlugin); got == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	// testing is built from the first root: the GOROOT mirror when there is one.
	if got := coverageHookRefusal("/client", options{coverage: true}, miniPackage, []string{t.TempDir(), goroot}, base(), false, nil); got == "" {
		t.Error("toolchain without the hook's declarations: accepted")
	}

	// The registration must be in a file that the build compiles, as the
	// overlay has it.
	listed = base()
	listed[3].GoFiles = slices.DeleteFunc(listed[3].GoFiles, func(name string) bool { return name == "memory_coverage.go" })
	if got := refusal(options{coverage: true}, listed, false); got == "" {
		t.Error("registration in a file the build excludes: accepted")
	}
	registration := filepath.Join(coverageDir, "memory_coverage.go")
	src, err := os.ReadFile(registration)
	if err != nil {
		t.Fatal(err)
	}
	same, without := filepath.Join(t.TempDir(), "same.go"), filepath.Join(t.TempDir(), "without.go")
	if err := os.WriteFile(same, src, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(without, []byte("package coverage\n"), 0600); err != nil {
		t.Fatal(err)
	}
	overlaid := func(path, to string) string {
		return coverageHookRefusal("/client", options{coverage: true}, miniPackage, []string{goroot}, base(), false, map[string]string{path: to})
	}
	if got := overlaid(registration, same); got != "" {
		t.Errorf("overlay with the registration refused: %s", got)
	}
	if got := overlaid(filepath.Join(coverageDir, "process_coverage.go"), ""); got != "" {
		t.Errorf("overlay deleting another file refused: %s", got)
	}
	if overlaid(registration, without) == "" || overlaid(registration, "") == "" {
		t.Error("overlay without the registration's file: accepted")
	}
}
