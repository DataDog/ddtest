package instrument

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Supported toolchains keep what ExitHook uses. If one stops, Mini builds
// without the hook and os.Exit during the tests loses their events again.
func TestToolchainProvidesExitHook(t *testing.T) {
	pkg, err := build.Default.Import("internal/runtime/exithook", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, name := range pkg.GoFiles {
		if sources[name], err = os.ReadFile(filepath.Join(pkg.Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ExitHookToolchainError(sources); err != nil {
		t.Fatalf("%v; review ExitHook", err)
	}
}

// ExitHook compiles as a file of package testing against the toolchain's
// exithook, and exitHookAPI lists what it uses there.
func TestExitHookTypeChecks(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "zz_dd_ci_visibility_exit.go", ExitHook, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	config := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := config.Check("testing", fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, obj := range info.Uses {
		if obj.Pkg() != nil && obj.Pkg().Path() == "internal/runtime/exithook" {
			used[obj.Name()] = true
		}
	}
	for name := range used {
		listed := false
		for _, decl := range exitHookAPI {
			// A function, a field of Hook, or Hook itself.
			listed = listed || strings.Contains(decl, " "+name+"(") || strings.Contains(decl, "."+name+" ") || strings.Contains(decl, "("+name+")")
		}
		if !listed {
			t.Errorf("exitHookAPI lacks %s", name)
		}
	}
	if len(used) == 0 {
		t.Fatal("no exithook uses checked")
	}
}

// ExitHook's linkname names Mini's integrations.forcedExit, which Mini
// declares without parameters.
func TestExitHookMatchesMiniRegistration(t *testing.T) {
	dir := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility", "integrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if sources[name], err = os.ReadFile(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := ExitHookRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
	hook, err := sourceDeclarations(map[string][]byte{"zz_dd_ci_visibility_exit.go": []byte(ExitHook)})
	if err != nil {
		t.Fatal(err)
	}
	if !hook["func __dd_civisibility_forcedExit()"] || !hook["linkname __dd_civisibility_forcedExit github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.forcedExit"] {
		t.Error("ExitHook does not link Mini's integrations.forcedExit")
	}
}

// A runtime without forcedExit builds without the hook instead of failing to
// link, and so does a toolchain whose exithook differs.
func TestExitHookRejectsOtherRuntimesAndToolchains(t *testing.T) {
	const runtime = "package integrations\nimport _ \"unsafe\"\n//go:linkname forcedExit\nfunc forcedExit() {}\n"
	for name, source := range map[string]string{
		"missing":   "package integrations\nfunc other() {}\n",
		"linkname":  "package integrations\nfunc forcedExit() {}\n",
		"parameter": strings.Replace(runtime, "forcedExit() {}", "forcedExit(code int) {}", 1),
	} {
		if err := ExitHookRuntimeError(map[string][]byte{"civisibility.go": []byte(source)}); err == nil {
			t.Errorf("runtime %s: accepted", name)
		}
	}
	if err := ExitHookRuntimeError(map[string][]byte{"civisibility.go": []byte(runtime)}); err != nil {
		t.Errorf("runtime: %v", err)
	}
	const toolchain = "package exithook\ntype Hook struct {\n\tF            func()\n\tRunOnFailure bool\n}\nfunc Add(h Hook) {}\n"
	for name, source := range map[string]string{
		"F":            strings.Replace(toolchain, "F            func()", "F            func(int)", 1),
		"RunOnFailure": strings.Replace(toolchain, "\tRunOnFailure bool\n", "", 1),
		"Add":          strings.Replace(toolchain, "func Add(h Hook)", "func Add(h *Hook)", 1),
	} {
		if err := ExitHookToolchainError(map[string][]byte{"hooks.go": []byte(source)}); err == nil {
			t.Errorf("toolchain %s: accepted", name)
		}
	}
	if err := ExitHookToolchainError(map[string][]byte{"hooks.go": []byte(toolchain)}); err != nil {
		t.Errorf("toolchain: %v", err)
	}
}
