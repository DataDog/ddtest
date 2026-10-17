package runner

import (
	"context"
	"errors"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// The CLI's own runtime sources pass every runtime check that ddto runs, on
// the targets that it builds for. ownSources skips these checks for them on
// that basis, so a change to Mini that fails one fails here first.
func TestMiniRuntimePassesEveryRuntimeCheck(t *testing.T) {
	checks := map[string]struct {
		dir   string
		check func(map[string][]byte) error
	}{
		"SignalHookRuntimeError":          {"integrations", instrument.SignalHookRuntimeError},
		"ExitHookRuntimeError":            {"integrations", instrument.ExitHookRuntimeError},
		"PanicHookRuntimeError":           {"integrations", instrument.PanicHookRuntimeError},
		"TimeoutHookRuntimeError":         {"integrations/gotesting", instrument.TimeoutHookRuntimeError},
		"SessionRuntimeError":             {"utils", instrument.SessionRuntimeError},
		"FileCoverageRuntimeError":        {"integrations/gotesting/coverage", instrument.FileCoverageRuntimeError},
		"FileCoverageContextRuntimeError": {"integrations/gotesting/coverage", instrument.FileCoverageContextRuntimeError},
		"CoverageHookRuntimeError":        {"integrations/gotesting/coverage", instrument.CoverageHookRuntimeError},
	}
	for name := range runtimeChecksUsed(t) {
		if _, ok := checks[name]; !ok {
			t.Errorf("the runner calls instrument.%s, which this test does not run on Mini's sources", name)
		}
	}
	mini := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility")
	for _, target := range []struct{ goos, goarch string }{{"linux", "amd64"}, {"linux", "386"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		context := build.Default
		context.GOOS, context.GOARCH = target.goos, target.goarch
		for name, check := range checks {
			pkg, err := context.ImportDir(filepath.Join(mini, filepath.FromSlash(check.dir)), 0)
			if err != nil {
				t.Fatal(err)
			}
			sources := map[string][]byte{}
			for _, file := range pkg.GoFiles {
				path := filepath.Join(pkg.Dir, file)
				if sources[path], err = os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := check.check(sources); err != nil {
				t.Errorf("%s/%s: %s: %v", target.goos, target.goarch, name, err)
			}
		}
	}
}

// runtimeChecksUsed returns the instrument functions named *RuntimeError that
// the runner's sources call.
func runtimeChecksUsed(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "instrument" && strings.HasSuffix(sel.Sel.Name, "RuntimeError") {
					used[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	if len(used) < 8 {
		t.Fatalf("found only %d runtime checks: %v", len(used), used)
	}
	return used
}

func ownTestPackage(dir, module, moduleDir string) goPackage {
	p := goPackage{Dir: dir, GoFiles: []string{"a.go"}}
	p.Module = &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: module, Dir: moduleDir}
	return p
}

// Only the packages of Mini's module whose module root is one that
// cliMiniRoot returned come from the CLI's own sources: another revision of
// Mini inside that root, such as a worktree under testdata, is another module.
func TestMarkOwnRuntime(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "mini")
	ctx := withOwnRuntimeRoots(context.Background())
	noteOwnRuntimeRoot(ctx, "")
	noteOwnRuntimeRoot(ctx, root)
	replaced := ownTestPackage(filepath.Join(root, "internal", "utils"), miniModule, filepath.Join(base, "cache", "mini@v1"))
	replaced.Module.Replace = &struct{ Path, Dir, Version string }{Path: root, Dir: root}
	listed := []goPackage{
		ownTestPackage(filepath.Join(root, "internal", "integrations"), miniModule, root),
		replaced,
		ownTestPackage(filepath.Join(base, "client-mini", "internal", "integrations"), miniModule, filepath.Join(base, "client-mini")),
		ownTestPackage(filepath.Join(base, "mini-other", "internal"), miniModule, filepath.Join(base, "mini-other")),
		ownTestPackage(filepath.Join(root, "testdata", "old-mini", "internal"), miniModule, filepath.Join(root, "testdata", "old-mini")),
		ownTestPackage(filepath.Join(root, "testdata", "fixture"), "example.com/fixture", root),
		{Dir: filepath.Join(root, "internal", "nomodule")},
	}
	if marked := markOwnRuntime(ctx, listed, "", ""); marked != 2 {
		t.Fatalf("marked %d packages, want 2", marked)
	}
	for i, want := range []bool{true, true, false, false, false, false, false} {
		if listed[i].ownRuntime != want {
			t.Errorf("package %s: ownRuntime=%t, want %t", listed[i].Dir, listed[i].ownRuntime, want)
		}
	}
	other := []goPackage{ownTestPackage(filepath.Join(root, "internal"), miniModule, root)}
	if markOwnRuntime(context.Background(), other, "", "") != 0 || other[0].ownRuntime {
		t.Fatal("a context without roots marked a package")
	}
}

// The CLI's exact version in GOMODCACHE is its own, whatever selects it; a
// checkout, which can hold local edits or another revision, is not.
func TestMarkOwnRuntimeFindsOnlyImmutableSources(t *testing.T) {
	cache := t.TempDir()
	cached := filepath.Join(cache, miniModule+"@v1.2.3")
	if err := os.MkdirAll(filepath.Join(cached, "testopt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cached, "go.mod"), []byte(testRuntimeFiles["go.mod"]), 0600); err != nil {
		t.Fatal(err)
	}
	if roots := cliMiniRoots(cache, "v1.2.3"); len(roots) != 1 || roots[0] != cached {
		t.Fatalf("roots %v, want %s", roots, cached)
	}
	listed := []goPackage{
		ownTestPackage(filepath.Join(cached, "internal"), miniModule, cached),
		ownTestPackage(filepath.Join(cache, miniModule+"@v9.9.9", "internal"), miniModule, filepath.Join(cache, miniModule+"@v9.9.9")),
	}
	if markOwnRuntime(context.Background(), listed, cache, "v1.2.3") != 1 || !listed[0].ownRuntime {
		t.Fatal("the CLI's version in GOMODCACHE was not the only package marked")
	}
	source, err := filepath.Abs("provide.go")
	if err != nil {
		t.Fatal(err)
	}
	ctx := withOwnRuntimeRoots(context.Background())
	checkout := cliMiniRoot(ctx, source, "", "")
	if checkout == "" {
		t.Fatal("no checkout root")
	}
	inCheckout := []goPackage{ownTestPackage(filepath.Join(checkout, "internal", "thirdparty"), miniModule, checkout)}
	if markOwnRuntime(ctx, inCheckout, "", "") != 0 {
		t.Fatal("the CLI's checkout counted as its own sources")
	}
}

// cliMiniRoot notes a release's extracted sources and the CLI's version in
// GOMODCACHE, but not its checkout.
func TestCLIMiniRootNotesOnlyImmutableSources(t *testing.T) {
	useTestUserCache(t)
	source, err := filepath.Abs("provide.go")
	if err != nil {
		t.Fatal(err)
	}
	moduleCache := t.TempDir()
	cached := filepath.Join(moduleCache, miniModule+"@v1.2.3")
	if err := os.MkdirAll(filepath.Join(cached, "testopt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cached, "go.mod"), []byte(testRuntimeFiles["go.mod"]), 0600); err != nil {
		t.Fatal(err)
	}
	bundled := &bundledMini{env: testGoEnvironment(t), dir: ".", archive: testRuntimeArchive(t, testRuntimeFiles)}
	defer bundled.release()
	noted := func(ctx context.Context) []string { return ctx.Value(ownRuntimeKey{}).(*ownRuntimeRoots).roots }

	ctx := withOwnRuntimeRoots(withBundledMini(t.Context(), bundled))
	if root := cliMiniRoot(ctx, source, moduleCache, "v1.2.3"); root == "" || len(noted(ctx)) != 0 {
		t.Fatalf("the checkout %q was noted: %v", root, noted(ctx))
	}
	release := cliMiniRoot(ctx, "", moduleCache, "v1.2.3")
	if got := noted(ctx); len(got) != 1 || got[0] != filepath.Clean(release) {
		t.Fatalf("noted %v, want the release's sources %s", got, release)
	}
	ctx = withOwnRuntimeRoots(t.Context())
	if root := cliMiniRoot(ctx, "", moduleCache, "v1.2.3"); root != cached {
		t.Fatalf("root %s, want %s", root, cached)
	}
	if got := noted(ctx); len(got) != 1 || got[0] != cached {
		t.Fatalf("noted %v, want %s", got, cached)
	}
}

// A check runs unless the package is the CLI's own and no overlay entry is in
// its directory: one that replaces, adds or deletes a file. Go leaves a deleted
// file out of GoFiles.
func TestCheckSourcesSkipsOnlyUnreplacedOwnPackages(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refuse := func(map[string][]byte) error { return errors.New("refused") }
	p := ownTestPackage(dir, miniModule, dir)
	if got := checkSources(&p, nil, refuse); got != "refused" {
		t.Fatalf("a package that is not the CLI's own skipped its check: %q", got)
	}
	p.ownRuntime = true
	if got := checkSources(&p, nil, refuse); got != "" {
		t.Fatalf("the CLI's own package was checked: %q", got)
	}
	for name, overlay := range map[string]map[string]string{
		"replaced": {filepath.Join(dir, "a.go"): filepath.Join(dir, "a.go")},
		"deleted":  {filepath.Join(dir, "crash.go"): ""},
		"added":    {filepath.Join(dir, "new.go"): filepath.Join(dir, "a.go")},
	} {
		if got := checkSources(&p, overlay, refuse); got != "refused" {
			t.Errorf("a package with a %s file skipped its check: %q", name, got)
		}
	}
	elsewhere := map[string]string{filepath.Join(dir, "sub", "a.go"): ""}
	if got := checkSources(&p, elsewhere, refuse); got != "" {
		t.Fatalf("an overlay of another directory made the package checked: %q", got)
	}
}
