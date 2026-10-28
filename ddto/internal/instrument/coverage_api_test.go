package instrument

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The hook's registration matches Mini's own coverage package.
func TestCoverageHookMatchesMiniRegistration(t *testing.T) {
	dir := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility", "integrations", "gotesting", "coverage")
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
	if err := CoverageHookRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
}

// A selected runtime without the registration, or with another signature,
// keeps text profiles instead of failing to link.
func TestCoverageHookRuntimeErrorRejectsOtherRuntimes(t *testing.T) {
	for name, source := range map[string]string{
		"missing":    "package coverage\nfunc other() {}\n",
		"parameters": "package coverage\nfunc registerTestingCoverage(func() ([]string, []uint32, string), func([]uint32) ([]uint32, int)) {}\n",
		"result":     "package coverage\nfunc registerTestingCoverage(func() ([]string, []uint32, string), func([]uint32) ([]uint32, bool), func([]uint32) []byte) {}\n",
		"method":     "package coverage\ntype r struct{}\nfunc (r) registerTestingCoverage(func() ([]string, []uint32, string), func([]uint32) ([]uint32, int), func([]uint32) []byte) {}\n",
		"generic":    "package coverage\nfunc registerTestingCoverage[T any](func() ([]string, []uint32, string), func([]uint32) ([]uint32, int), func([]uint32) []byte) {}\n",
	} {
		if err := CoverageHookRuntimeError(map[string][]byte{"coverage.go": []byte(source)}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	source := "package coverage\nfunc registerTestingCoverage(meta func() ([]string, []uint32, string), counters func([]uint32) ([]uint32, int), profile func([]uint32) []byte) {}\n"
	if err := CoverageHookRuntimeError(map[string][]byte{"coverage.go": []byte(source)}); err != nil {
		t.Errorf("named parameters: %v", err)
	}
	if err := CoverageHookRuntimeError(nil); err == nil {
		t.Error("no sources: accepted")
	}
}

// coverageHookAPI lists every toolchain declaration that CoverageHook uses,
// so that a toolchain without one keeps text profiles instead of failing to
// build testing.
func TestCoverageHookAPICoversItsUses(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "zz_dd_ci_visibility_coverage.go", CoverageHook, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := config.Check("testing", fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	declares := func(path string, prefixes ...string) bool {
		for _, decl := range coverageHookAPI[path] {
			for _, prefix := range prefixes {
				if strings.HasPrefix(decl, prefix) {
					return true
				}
			}
		}
		return false
	}
	owner := func(typ types.Type) string {
		if pointer, ok := typ.(*types.Pointer); ok {
			typ = pointer.Elem()
		}
		if named, ok := typ.(*types.Named); ok {
			return named.Obj().Name()
		}
		return ""
	}
	checked := 0
	for sel, selection := range info.Selections {
		obj := selection.Obj()
		if obj.Pkg() == nil || coverageHookAPI[obj.Pkg().Path()] == nil {
			continue
		}
		checked++
		path, name, recv := obj.Pkg().Path(), obj.Name(), owner(selection.Recv())
		if recv == "" {
			// A field of a package variable, such as rtcov.Meta.List.
			if ident, ok := ast.Unparen(sel.X).(*ast.SelectorExpr); ok {
				recv = ident.Sel.Name
			}
		}
		if !declares(path, "field "+recv+"."+name+" ", "func ("+recv+") "+name+"(", "func (*"+recv+") "+name+"(") {
			t.Errorf("coverageHookAPI lacks %s.%s of %s", recv, name, path)
		}
	}
	for ident, obj := range info.Uses {
		if obj.Pkg() == nil || coverageHookAPI[obj.Pkg().Path()] == nil || obj.Parent() != obj.Pkg().Scope() {
			continue
		}
		checked++
		path, name := obj.Pkg().Path(), obj.Name()
		var prefixes []string
		switch obj.(type) {
		case *types.Func:
			prefixes = []string{"func " + name + "("}
		case *types.Const:
			prefixes = []string{"const " + name}
		case *types.TypeName:
			prefixes = []string{"type " + name + " ", "field " + name + "."}
		case *types.Var:
			prefixes = []string{"field " + name + "."}
		}
		if !declares(path, prefixes...) {
			t.Errorf("coverageHookAPI lacks %s of %s, used at %s", name, path, fset.Position(ident.Pos()))
		}
	}
	if checked == 0 {
		t.Fatal("no uses checked")
	}
}

// A toolchain that lacks any declaration the hook uses keeps text profiles.
func TestCoverageHookToolchainErrorReportsMissingDeclarations(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	goroot := strings.TrimSpace(string(out))
	for dir, decls := range coverageHookAPI {
		for _, decl := range decls {
			fake := t.TempDir()
			for copyDir := range coverageHookAPI {
				src := filepath.Join(goroot, "src", filepath.FromSlash(copyDir))
				dst := filepath.Join(fake, "src", filepath.FromSlash(copyDir))
				if err := os.MkdirAll(dst, 0700); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(src)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					name := entry.Name()
					if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || copyDir == "runtime" && name != "covercounter.go" {
						continue
					}
					data, err := os.ReadFile(filepath.Join(src, name))
					if err != nil {
						t.Fatal(err)
					}
					if copyDir == dir {
						data = []byte(removeDeclaration(t, string(data), decl))
					}
					if err := os.WriteFile(filepath.Join(dst, name), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Renaming can remove more than one declaration; any is reported.
			if err := CoverageHookToolchainError(fake); err == nil {
				t.Errorf("accepted without %q in %s", decl, dir)
			}
		}
	}
}

// removeDeclaration renames what a declaration fingerprint names, so that the
// file no longer declares it.
func removeDeclaration(t *testing.T, source, decl string) string {
	t.Helper()
	kind, rest, _ := strings.Cut(decl, " ")
	switch kind {
	case "linkname":
		return strings.ReplaceAll(source, "//go:linkname "+strings.Fields(rest)[0], "//go:linkname renamed_"+strings.Fields(rest)[0])
	case "field":
		owner, field, _ := strings.Cut(strings.Fields(rest)[0], ".")
		_ = owner
		return replaceIdentifier(source, field, field+"Renamed")
	case "func":
		name := strings.Fields(strings.TrimPrefix(rest, "(*"))[0]
		if strings.HasPrefix(rest, "(") {
			name = strings.Fields(rest)[1]
		}
		name, _, _ = strings.Cut(name, "(")
		return replaceIdentifier(source, name, name+"Renamed")
	default: // type, const
		name := strings.Fields(rest)[0]
		return replaceIdentifier(source, name, name+"Renamed")
	}
}

// replaceIdentifier renames whole-word occurrences of name.
func replaceIdentifier(source, name, renamed string) string {
	var out strings.Builder
	for i := 0; i < len(source); {
		j := strings.Index(source[i:], name)
		if j < 0 {
			out.WriteString(source[i:])
			break
		}
		j += i
		end := j + len(name)
		before := j == 0 || !isIdentByte(source[j-1])
		after := end == len(source) || !isIdentByte(source[end])
		out.WriteString(source[i:j])
		if before && after {
			out.WriteString(renamed)
		} else {
			out.WriteString(name)
		}
		i = end
	}
	return out.String()
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
