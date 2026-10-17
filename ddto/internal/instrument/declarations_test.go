package instrument

import (
	"bytes"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"testing"
)

// declarationPackages are the packages whose declarations ddto checks: Mini's
// runtime packages and the toolchain's, as the build compiles them.
func declarationPackages(t *testing.T) map[string]map[string][]byte {
	t.Helper()
	mini := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility")
	dirs := map[string]string{
		"integrations": filepath.Join(mini, "integrations"),
		"gotesting":    filepath.Join(mini, "integrations", "gotesting"),
		"coverage":     filepath.Join(mini, "integrations", "gotesting", "coverage"),
		"utils":        filepath.Join(mini, "utils"),
	}
	packages := map[string]map[string][]byte{}
	for name, dir := range dirs {
		pkg, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		packages[name] = readPackage(t, pkg)
	}
	for _, path := range []string{"internal/runtime/exithook", "os/signal", "context", "internal/poll", "net/http", "testing", "runtime", "internal/singleflight"} {
		pkg, err := build.Default.Import(path, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		packages[path] = readPackage(t, pkg)
	}
	return packages
}

func readPackage(t *testing.T, pkg *build.Package) map[string][]byte {
	t.Helper()
	sources := map[string][]byte{}
	for _, name := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = src
	}
	return sources
}

// declaringSources keeps the file that declares anything that the checks can
// ask for: every declaration of these packages, rendered by
// sourceDeclarations, has a name that its own file contains.
func TestDeclaringSourcesKeepsEveryDeclaration(t *testing.T) {
	checked := 0
	for name, sources := range declarationPackages(t) {
		for path, src := range sources {
			decls, err := sourceDeclarations(map[string][]byte{path: src})
			if err != nil {
				t.Fatal(err)
			}
			for decl := range decls {
				if want := declaredName(decl); want == "" || !bytes.Contains(src, []byte(want)) {
					t.Errorf("%s: %s declares %q, which does not contain %q", name, filepath.Base(path), decl, want)
				}
				checked++
			}
		}
	}
	if checked < 10000 {
		t.Fatalf("only %d declarations checked", checked)
	}
}

// Each check decides as it did when it parsed every file of its package.
func TestDeclarationChecksMatchFullParse(t *testing.T) {
	packages := declarationPackages(t)
	checks := []struct {
		pkg  string
		want []string
	}{
		{"internal/runtime/exithook", exitHookAPI},
		{"integrations", exitRuntimeAPI},
		{"integrations", signalRuntimeAPI},
		{"integrations", panicRuntimeAPI},
		{"gotesting", timeoutRuntimeAPI},
		{"utils", sessionRuntimeAPI},
		{"coverage", fileCoverageRuntimeAPI},
		{"coverage", fileCoverageContextRuntimeAPI},
		{"os/signal", signalHookAPI},
		{"runtime", panicToolchainAPI},
		{"runtime", contextRuntimeAPI},
	}
	for _, check := range checks {
		sources := packages[check.pkg]
		full, err := sourceDeclarations(sources)
		if err != nil {
			t.Fatal(err)
		}
		// Each declaration alone, and one that no package declares, which
		// must still be reported missing.
		for _, decl := range append(append([]string{}, check.want...), "func missingDeclaration()") {
			want := ""
			if !full[decl] {
				want = fmt.Sprintf("lacks %s", decl)
			}
			got := ""
			if err := declarationsError(sources, []string{decl}, "lacks %s"); err != nil {
				got = err.Error()
			}
			if got != want {
				t.Errorf("%s, %q: got %q, want %q", check.pkg, decl, got, want)
			}
		}
		filtered := len(declaringSources(sources, check.want))
		t.Logf("%s: %d of %d files parsed", check.pkg, filtered, len(sources))
	}
}

func TestDeclaredName(t *testing.T) {
	for decl, want := range map[string]string{
		"func forcedExit()":                       "forcedExit",
		"linkname forcedExit":                     "forcedExit",
		"func (*FD) readLock() error":             "FD",
		"func (CounterMode) String() string":      "CounterMode",
		"field Hook.F func()":                     "Hook",
		"embed afterFuncCtx cancelCtx":            "afterFuncCtx",
		"var signalNotify func(chan<- os.Signal)": "signalNotify",
		"type CounterMode uint8":                  "CounterMode",
		"const NumCtrsOffset":                     "NumCtrsOffset",
		"func _ünicode1()":                        "_ünicode1",
		"func":                                    "",
		"func ()":                                 "",
	} {
		if got := declaredName(decl); got != want {
			t.Errorf("declaredName(%q) = %q, want %q", decl, got, want)
		}
	}
}

// A file that names no wanted declaration is not parsed, so a syntax error
// there no longer refuses the hook; the build reports it. A file that names
// one is parsed as before.
func TestDeclaringSourcesParsesOnlyNamingFiles(t *testing.T) {
	sources := map[string][]byte{
		"declares.go": []byte("package p\n\n//go:linkname forcedExit\nfunc forcedExit() {}\n"),
		"broken.go":   []byte("package p\n\nfunc broken( {\n"),
	}
	if err := ExitHookRuntimeError(sources); err != nil {
		t.Fatalf("an unrelated broken file refused the hook: %v", err)
	}
	sources["broken.go"] = []byte("package p\n\n// forcedExit\nfunc broken( {\n")
	if err := ExitHookRuntimeError(sources); err == nil {
		t.Fatal("a broken file that names the declaration was not parsed")
	}
	delete(sources, "broken.go")
	sources["declares.go"] = []byte("package p\n\n// forcedExit is gone.\n")
	if err := ExitHookRuntimeError(sources); err == nil {
		t.Fatal("a file that only mentions the name declared it")
	}
}

// go/parser drops carriage returns from comments, so a linkname directive
// split by one names the declaration: the filter keeps its file, as a full
// parse reads it. CRLF files keep matching too.
func TestDeclaringSourcesIgnoresCarriageReturns(t *testing.T) {
	sources := map[string][]byte{
		"api.go":  []byte("package integrations\nfunc forcedExit() {}\n"),
		"link.go": []byte("package integrations\nimport _ \"unsafe\"\n//go:linkname f\rorcedExit\nfunc f() {}\n"),
	}
	full, err := sourceDeclarations(sources)
	if err != nil {
		t.Fatal(err)
	}
	if !full["linkname forcedExit"] {
		t.Fatal("a full parse no longer reads the split directive; review this test")
	}
	if err := ExitHookRuntimeError(sources); err != nil {
		t.Fatalf("the split directive's file was left out: %v", err)
	}
	crlf := map[string][]byte{"crlf.go": []byte("package integrations\r\n\r\n//go:linkname forcedExit\r\nfunc forcedExit() {}\r\n")}
	if err := ExitHookRuntimeError(crlf); err != nil {
		t.Fatalf("a CRLF file was left out: %v", err)
	}
}
