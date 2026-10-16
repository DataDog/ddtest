package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fileCoverageTree writes files under root and returns root.
func fileCoverageTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPrepareFileCoverage(t *testing.T) {
	code := func(pkg string) string { return "package " + pkg + "\n\nfunc F() int { return 1 }\n" }
	root := fileCoverageTree(t, map[string]string{
		"repo/a/a.go":         code("a"),
		"repo/a/a_test.go":    code("a"),
		"repo/a/x_test.go":    code("a_test"),
		"repo/a/data.txt":     "data",
		"repo/a/user.go":      code("a"),
		"repo/a/user.backing": "package a\n\nfunc FromOverlay() int { return 2 }\n",
		"repo/a/hooked.go":    code("a"),
		"ddto-hook.go":        code("a"),
		"repo/a/deleted.go":   code("a"),
		"repo/t/t_test.go":    code("t"),
		"repo/c/c.go":         "package c\n\n// int one() { return 1; }\nimport \"C\"\n",
		"repo/c/c2.go":        code("c"),
		"repo/e/e.go":         code("e"),
		"repo/e/e_test.go":    code("e"),
		"repo/e/sub/s.go":     code("sub"),
		"repo/u/u.go":         code("u"),
		"repo/u/u_test.go":    code("u"),
		"repo/cache/m/m.go":   code("m"),
		"repo/q/q.go":         code("q"),
		"outside/dep/dep.go":  code("dep"),
	})
	repo := filepath.Join(root, "repo")
	dir := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	tested := []goPackage{
		{Dir: dir("repo/a"), Name: "a", ImportPath: "example.com/r/a", GoFiles: []string{"a.go", "user.go", "hooked.go", "deleted.go"}, TestGoFiles: []string{"a_test.go"}, XTestGoFiles: []string{"x_test.go"}, EmbedFiles: []string{"data.txt"}},
		{Dir: dir("repo/a"), Name: "a", ImportPath: "example.com/r/a [example.com/r/a.test]", ForTest: "example.com/r/a"},
		{Dir: dir("repo/a"), Name: "a_test", ImportPath: "example.com/r/a_test [example.com/r/a.test]", ForTest: "example.com/r/a"},
		// go list -test's test main: its Go file is in the build cache.
		{Dir: dir("repo/a"), Name: "main", ImportPath: "example.com/r/a.test", GoFiles: []string{dir("gocache/testmain-d")}},
		// q imports a and only a's tests import q: listed only as recompiled.
		{Dir: dir("repo/q"), Name: "q", ImportPath: "example.com/r/q [example.com/r/a.test]", ForTest: "example.com/r/a", GoFiles: []string{"q.go"}},
		{Dir: dir("repo/t"), Name: "t", ImportPath: "example.com/r/t", TestGoFiles: []string{"t_test.go"}},
		{Dir: dir("repo/t"), Name: "t", ImportPath: "example.com/r/t [example.com/r/t.test]", ForTest: "example.com/r/t"},
		{Dir: dir("repo/c"), Name: "c", ImportPath: "example.com/r/c", GoFiles: []string{"c2.go"}, CgoFiles: []string{"c.go"}},
		// e's test embeds its own e.go, and e embeds sub's s.go: both keep
		// their content.
		{Dir: dir("repo/e"), Name: "e", ImportPath: "example.com/r/e", GoFiles: []string{"e.go"}, TestGoFiles: []string{"e_test.go"}, EmbedFiles: []string{"sub/s.go"}, TestEmbedFiles: []string{"e.go"}},
		{Dir: dir("repo/e"), Name: "e", ImportPath: "example.com/r/e [example.com/r/e.test]", ForTest: "example.com/r/e"},
		{Dir: dir("repo/e/sub"), Name: "sub", ImportPath: "example.com/r/e/sub", GoFiles: []string{"s.go"}},
		// u's tests are not built: only its own bank.
		{Dir: dir("repo/u"), Name: "u", ImportPath: "example.com/r/u", GoFiles: []string{"u.go"}, TestGoFiles: []string{"u_test.go"}},
		{Dir: dir("repo/cache/m"), Name: "m", ImportPath: "example.com/m", GoFiles: []string{"m.go"}},
		{Dir: dir("outside/dep"), Name: "dep", ImportPath: "example.com/dep", GoFiles: []string{"dep.go"}},
		{Dir: dir("repo/mini"), Name: "coverage", ImportPath: miniCoveragePackage, GoFiles: []string{"file_coverage.go"}},
		{Dir: dir("repo/std"), Name: "fmt", ImportPath: "fmt", Standard: true, GoFiles: []string{"print.go"}},
	}
	user, hooked, deleted := filepath.Join(dir("repo/a"), "user.go"), filepath.Join(dir("repo/a"), "hooked.go"), filepath.Join(dir("repo/a"), "deleted.go")
	replacements := map[string]string{user: filepath.Join(dir("repo/a"), "user.backing"), hooked: filepath.Join(root, "ddto-hook.go"), deleted: ""}
	userOverlay := map[string]bool{user: true, deleted: true}
	generated := map[string]string{}
	add := func(logical, content string) error {
		generated[logical] = content
		replacements[logical] = "generated"
		return nil
	}
	summary, refusal, err := prepareFileCoverage(tested, []string{repo}, []string{dir("repo/cache")}, replacements, userOverlay, t.TempDir(), add)
	if err != nil || refusal != "" {
		t.Fatalf("refusal %q, err %v", refusal, err)
	}
	if summary.packages != 7 {
		t.Errorf("%d packages, want a, t, c, e, e/sub, q and u", summary.packages)
	}
	want := map[string][]string{
		"repo/a/zz_ddto_file_coverage.go":        {"package a\n", "__ddto_epoch", strconv.Quote(filepath.Join(dir("repo/a"), "a.go")), strconv.Quote(user), strconv.Quote(filepath.Join(dir("repo/a"), "data.txt")), strconv.Quote(hooked)},
		"repo/a/zz_ddto_file_coverage_test.go":   {"package a\n", "__ddto_t [1]uint64", "a_test.go"},
		"repo/a/zz_ddto_file_coverage_x_test.go": {"package a_test\n", "__ddto_epoch", "__ddto_x [1]uint64", "x_test.go"},
		"repo/t/zz_ddto_file_coverage_test.go":   {"package t\n", "__ddto_epoch", "__ddto_t [1]uint64", "t_test.go"},
		"repo/c/zz_ddto_file_coverage.go":        {"package c\n", "__ddto_m [1]uint64", "c2.go", strconv.Quote(filepath.Join(dir("repo/c"), "c.go"))},
		"repo/u/zz_ddto_file_coverage.go":        {"package u\n", "__ddto_m [1]uint64"},
		"repo/e/zz_ddto_file_coverage.go":        {"package e\n", strconv.Quote(filepath.Join(dir("repo/e"), "e.go"))},
		"repo/e/zz_ddto_file_coverage_test.go":   {"package e\n", "__ddto_t [1]uint64"},
		"repo/e/sub/zz_ddto_file_coverage.go":    {"package sub\n", strconv.Quote(filepath.Join(dir("repo/e/sub"), "s.go"))},
		"repo/q/zz_ddto_file_coverage.go":        {"package q\n", "__ddto_m [1]uint64", "q.go"},
	}
	var got []string
	for logical := range generated {
		rel, _ := filepath.Rel(root, logical)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	var wantNames []string
	for name := range want {
		wantNames = append(wantNames, name)
	}
	slices.Sort(wantNames)
	if !slices.Equal(got, wantNames) {
		t.Fatalf("generated %v, want %v", got, wantNames)
	}
	for name, parts := range want {
		content := generated[dir(name)]
		for _, part := range parts {
			if !strings.Contains(content, part) {
				t.Errorf("%s lacks %q:\n%s", name, part, content)
			}
		}
	}
	// The internal test bank shares its package's declarations.
	if strings.Contains(generated[dir("repo/a/zz_ddto_file_coverage_test.go")], "__ddto_epoch") {
		t.Error("internal test bank redeclares the epoch next to its package's bank")
	}
	// a has a.go and user.go marked, hooked.go for every test, deleted.go in neither.
	regular := generated[dir("repo/a/zz_ddto_file_coverage.go")]
	if !strings.Contains(regular, "__ddto_m [2]uint64") || strings.Contains(regular, "deleted.go") {
		t.Errorf("regular bank of a:\n%s", regular)
	}
	marked, err := os.ReadFile(replacements[user])
	if err != nil || !strings.Contains(string(marked), "FromOverlay") || !strings.Contains(string(marked), "__ddto_atomic.LoadUint64(&__ddto_m[1])") {
		t.Errorf("user overlay file: err %v\n%s", err, marked)
	}
	if replacements[hooked] != filepath.Join(root, "ddto-hook.go") || replacements[deleted] != "" {
		t.Errorf("ddto's and deleted replacements changed: %q %q", replacements[hooked], replacements[deleted])
	}
	for _, untouched := range []string{"repo/cache/m/m.go", "outside/dep/dep.go", "repo/u/u_test.go", "repo/e/e.go", "repo/e/sub/s.go"} {
		if _, ok := replacements[dir(untouched)]; ok {
			t.Errorf("%s was rewritten", untouched)
		}
	}
	// Marked: a.go, user.go, a_test.go, x_test.go, t_test.go, c2.go, u.go,
	// e_test.go and q.go. For every test: data.txt, hooked.go and c.go, and
	// e.go and s.go twice each, as embedded files and as files that keep
	// their content; Mini reports each name once.
	if summary.marked != 9 || summary.always != 7 {
		t.Errorf("summary %+v, want 9 marked and 7 entries for every test", summary)
	}
}

func TestPrepareFileCoverageCollision(t *testing.T) {
	root := fileCoverageTree(t, map[string]string{
		"a/a.go":                     "package a\n\nfunc F() {}\n",
		"a/zz_ddto_file_coverage.go": "package a\n",
	})
	tested := []goPackage{{Dir: filepath.Join(root, "a"), Name: "a", ImportPath: "example.com/a", GoFiles: []string{"a.go"}}}
	replacements := map[string]string{}
	_, refusal, err := prepareFileCoverage(tested, []string{root}, nil, replacements, nil, t.TempDir(), func(string, string) error {
		t.Fatal("added a file after a collision")
		return nil
	})
	if err != nil || !strings.HasPrefix(refusal, "generated file already exists") || len(replacements) != 0 {
		t.Fatalf("refusal %q, err %v, replacements %v", refusal, err, replacements)
	}
}

// A file with the generated code's prefix refuses file coverage before the
// overlay changes: its package's bank could redeclare the identifier.
func TestPrepareFileCoverageNameClash(t *testing.T) {
	root := fileCoverageTree(t, map[string]string{
		"a/a.go": "package a\n\nfunc F() {}\n",
		"b/b.go": "package b\n\nvar __ddto_m int\n\nfunc G() {}\n",
	})
	tested := []goPackage{
		{Dir: filepath.Join(root, "a"), Name: "a", ImportPath: "example.com/a", GoFiles: []string{"a.go"}},
		{Dir: filepath.Join(root, "b"), Name: "b", ImportPath: "example.com/b", GoFiles: []string{"b.go"}},
	}
	replacements := map[string]string{}
	_, refusal, err := prepareFileCoverage(tested, []string{root}, nil, replacements, nil, t.TempDir(), func(string, string) error {
		t.Fatal("added a file after a name clash")
		return nil
	})
	if err != nil || !strings.HasSuffix(refusal, "identifier with prefix __ddto_") || len(replacements) != 0 {
		t.Fatalf("refusal %q, err %v, replacements %v", refusal, err, replacements)
	}
}

// A package whose import path ends in .test is instrumented; only the test
// main that go list synthesizes, with its Go file in the build cache, is not.
func TestPrepareFileCoverageTestSuffix(t *testing.T) {
	root := fileCoverageTree(t, map[string]string{"helper/h.go": "package helper\n\nfunc F() {}\n"})
	tested := []goPackage{
		{Dir: filepath.Join(root, "helper"), Name: "helper", ImportPath: "example.com/helper.test", GoFiles: []string{"h.go"}},
		{Dir: filepath.Join(root, "helper"), Name: "main", ImportPath: "example.com/other.test", GoFiles: []string{filepath.Join(root, "cache", "testmain")}},
	}
	generated := map[string]bool{}
	_, refusal, err := prepareFileCoverage(tested, []string{root}, nil, map[string]string{}, nil, t.TempDir(), func(logical, _ string) error {
		generated[logical] = true
		return nil
	})
	if err != nil || refusal != "" || !generated[filepath.Join(root, "helper", fileCoverageBankFile)] || len(generated) != 1 {
		t.Fatalf("refusal %q, err %v, generated %v", refusal, err, generated)
	}
}

// Embed patterns resolve through the overlay: a bank they would match
// refuses file coverage, through a glob or a directory that holds it.
func TestPrepareFileCoverageEmbedPatterns(t *testing.T) {
	patterns := []string{"*coverage.go", "all:sub", "sub/*"}
	if runtime.GOOS == "windows" {
		// A backslash refuses file coverage on Windows, whatever it matches.
		patterns = append(patterns, `sub\*coverage.go`, `.\*coverage.go`, `\*coverage.go`, `other\x.txt`)
	}
	for _, pattern := range patterns {
		root := fileCoverageTree(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n", "sub/s.go": "package sub\n\nfunc G() {}\n"})
		tested := []goPackage{
			{Dir: root, Name: "a", ImportPath: "example.com/a", GoFiles: []string{"a.go"}, EmbedPatterns: []string{pattern}},
			{Dir: filepath.Join(root, "sub"), Name: "sub", ImportPath: "example.com/a/sub", GoFiles: []string{"s.go"}},
		}
		replacements := map[string]string{}
		_, refusal, err := prepareFileCoverage(tested, []string{root}, nil, replacements, nil, t.TempDir(), func(string, string) error {
			t.Fatal("added a bank that an embed pattern matches")
			return nil
		})
		if err != nil || !strings.Contains(refusal, "embed pattern") || len(replacements) != 0 {
			t.Errorf("%s: refusal %q, err %v", pattern, refusal, err)
		}
	}
}

// Files that are never marked are checked for the prefix too: embedded and
// cgo files still compile next to the bank.
func TestPrepareFileCoverageNameClashUnmarked(t *testing.T) {
	for _, tc := range []struct{ name, file string }{{"embedded", "e.go"}, {"cgo", "c.go"}} {
		root := fileCoverageTree(t, map[string]string{
			"a.go": "package a\n\nfunc F() {}\n",
			"e.go": "package a\n\nvar __ddto_epoch uint64\n\nfunc E() {}\n",
			"c.go": "package a\n\nimport \"C\"\n\nvar __ddto_banks int\n",
		})
		p := goPackage{Dir: root, Name: "a", ImportPath: "example.com/a", GoFiles: []string{"a.go"}}
		if tc.name == "embedded" {
			p.GoFiles, p.TestEmbedFiles = append(p.GoFiles, "e.go"), []string{"e.go"}
		} else {
			p.CgoFiles = []string{"c.go"}
		}
		_, refusal, err := prepareFileCoverage([]goPackage{p}, []string{root}, nil, map[string]string{}, nil, t.TempDir(), func(string, string) error {
			t.Fatal("added a bank next to a clashing file")
			return nil
		})
		if err != nil || !strings.Contains(refusal, tc.file) || !strings.HasSuffix(refusal, "identifier with prefix __ddto_") {
			t.Errorf("%s: refusal %q, err %v", tc.name, refusal, err)
		}
	}
}

// A test binary that can load plugins keeps Go's counters: a plugin built
// without the marks would not load next to the marked packages it shares.
func TestPrepareFileCoverageRefusesPlugins(t *testing.T) {
	root := fileCoverageTree(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	tested := []goPackage{
		{Dir: root, Name: "a", ImportPath: "example.com/a", GoFiles: []string{"a.go"}},
		{Name: "plugin", ImportPath: "plugin", Standard: true},
	}
	replacements := map[string]string{}
	_, refusal, err := prepareFileCoverage(tested, []string{root}, nil, replacements, nil, t.TempDir(), func(string, string) error {
		t.Fatal("added a bank in a build with plugin")
		return nil
	})
	if err != nil || refusal != "plugin" || len(replacements) != 0 {
		t.Fatalf("refusal %q, err %v, replacements %v", refusal, err, replacements)
	}
}

func TestRepositoryRoots(t *testing.T) {
	root := fileCoverageTree(t, map[string]string{"repo/.git/HEAD": "ref: refs/heads/main\n", "repo/sub/go.mod": "module x\n", "plain/go.mod": "module y\n"})
	if got := repositoryRoots(filepath.Join(root, "repo", "sub"), nil); !slices.Equal(got, []string{filepath.Join(root, "repo")}) {
		t.Errorf("inside Git: %v", got)
	}
	plain := filepath.Join(root, "plain")
	listed := []goPackage{{Module: &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: "y", Dir: plain, Main: true}}}
	if got := repositoryRoots(plain, listed); !slices.Equal(got, []string{plain}) {
		t.Errorf("outside Git: %v", got)
	}
}

func TestFileCoverageRefusal(t *testing.T) {
	coverage := fileCoverageTree(t, map[string]string{
		"new/c.go": "package coverage\n\nimport \"unsafe\"\n\nvar (\n\tfileCoverageEpoch   uint64\n\tfileCoverageBanksV1 unsafe.Pointer\n)\n",
		"old/c.go": "package coverage\n\nvar mode string\n",
	})
	runtime := func(dir string) []goPackage {
		return []goPackage{{Dir: filepath.Join(coverage, dir), Name: "coverage", ImportPath: miniCoveragePackage, GoFiles: []string{"c.go"}}}
	}
	mini := &Plan{mini: true}
	for _, tc := range []struct {
		name   string
		plan   *Plan
		opts   options
		vendor bool
		listed []goPackage
		want   string
	}{
		{"sdk", &Plan{}, options{}, false, runtime("new"), "the SDK runtime"},
		{"cover", mini, options{coverage: true}, false, runtime("new"), "Go's coverage flags are set"},
		{"race", mini, options{race: true}, false, runtime("new"), "-race"},
		{"orchestrion", &Plan{mini: true, orchestrion: true}, options{}, false, runtime("new"), "Orchestrion's -toolexec"},
		{"vendor", mini, options{}, true, runtime("new"), "ddto provides the vendored dependencies from its cache"},
		{"unlisted", mini, options{}, false, nil, "the runtime's coverage package not listed"},
		{"older runtime", mini, options{}, false, runtime("old"), "the runtime's coverage package lacks var fileCoverageEpoch uint64"},
		{"supported", mini, options{}, false, runtime("new"), ""},
	} {
		if got := fileCoverageRefusal(tc.plan, tc.opts, tc.vendor, tc.listed, map[string]string{}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
