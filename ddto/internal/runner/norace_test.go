package runner

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

const noraceCases = `package p

import "strings"

var literal = func() {
	_ = "func notDeclared() {"
}

var (
	grouped = func(x struct{ a int }) {}
)

//go:noinline
func Declared[T interface{ ~int }](v T) struct{ b int } {
	s := ` + "`\nfunc insideRaw() {\n`" + `
	_, _ = s, '{'
	return struct{ b int }{}
}

func external(int) int

func (r *receiver[T]) Method() (interface {
	M()
}, error) { /* } */ return nil, nil }

type receiver[T any] struct{}

const c = 1; func sameLine() {
	if true { _ = strings.TrimSpace("") }
}

func multiline(
	a int,
) {
	a++
}
`

// Annotation must not change any token or its position, and must mark exactly
// the declarations that have a body.
func TestNoraceSourcePreservesPositions(t *testing.T) {
	sources := map[string][]byte{"cases.go": []byte(noraceCases)}
	for _, path := range miniRuntimeSources(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = data
	}
	annotatedFiles := 0
	for name, src := range sources {
		out, ok := appendNorace(nil, src)
		if !ok {
			// Only files without function bodies stay unchanged.
			file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				if fn, isFunc := decl.(*ast.FuncDecl); isFunc && fn.Body != nil {
					t.Errorf("%s: %s not annotated", name, fn.Name.Name)
				}
			}
			continue
		}
		annotatedFiles++
		assertSameTokens(t, name, src, out)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, out, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc {
				continue
			}
			// The compiler applies a directive to the next declaration.
			marked := noraceDirective.Match(out[:fset.File(fn.Pos()).Offset(fn.Pos())])
			if marked != (fn.Body != nil) {
				t.Errorf("%s: %s marked=%t with body=%t", name, fn.Name.Name, marked, fn.Body != nil)
			}
		}
	}
	if annotatedFiles < len(sources)/2 {
		t.Fatalf("annotated %d of %d files", annotatedFiles, len(sources))
	}
	if out, _ := appendNorace(nil, []byte(noraceCases)); bytes.Count(out, []byte("//go:norace")) != 4 {
		t.Fatalf("cases: %d annotations\n%s", bytes.Count(out, []byte("//go:norace")), out)
	}
}

func TestNoraceSourceLeavesUnsupportedFiles(t *testing.T) {
	for _, src := range []string{
		"package p\n//line other.go:10\nfunc f() {}\n",
		"package p\nfunc f() { /*line other.go:3:1*/ }\n",
		"package p\nfunc f() { _ = \"unterminated\n}\n",
		"package p\nfunc f() { /* unterminated }\n",
		"package p\nfunc f() {\n",
		"package p\nfunc f(x [2]int{}) {}\n",
		"package p\nvar f = func() {}\nfunc g()\n",
	} {
		if out, ok := appendNorace([]byte("prefix"), []byte(src)); ok || string(out) != "prefix" {
			t.Errorf("%q: annotated=%t\n%s", src, ok, out)
		}
	}
}

var noraceDirective = regexp.MustCompile(`(^|\n)//go:norace\n/\*line :[0-9]+:[0-9]+\*/$`)

func assertSameTokens(t *testing.T, name string, original, annotated []byte) {
	t.Helper()
	type item struct {
		tok       token.Token
		lit       string
		line, col int
	}
	scan := func(src []byte) []item {
		fset := token.NewFileSet()
		file := fset.AddFile(name, -1, len(src))
		var s scanner.Scanner
		s.Init(file, src, func(pos token.Position, msg string) { t.Errorf("%s: %v: %s", name, pos, msg) }, 0)
		var items []item
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				return items
			}
			position := fset.Position(pos)
			if position.Filename != name {
				t.Errorf("%s: token at %v names another file", name, position)
			}
			items = append(items, item{tok, lit, position.Line, position.Column})
		}
	}
	before, after := scan(original), scan(annotated)
	if len(before) != len(after) {
		t.Errorf("%s: %d tokens, then %d", name, len(before), len(after))
		return
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("%s: token %d was %+v, now %+v", name, i, before[i], after[i])
			return
		}
	}
}

// miniRuntimeSources lists the Go files of the packages that the Mini runtime
// links, the files that noraceOverlay annotates.
func miniRuntimeSources(t testing.TB) []string {
	out, err := exec.Command("go", "list", "-deps", "-json=Dir,GoFiles,Module", "../../testopt").Output()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for decoder := json.NewDecoder(bytes.NewReader(out)); decoder.More(); {
		var p goPackage
		if err := decoder.Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Module == nil || p.Module.Path != miniModule {
			continue
		}
		for _, name := range p.GoFiles {
			paths = append(paths, filepath.Join(p.Dir, name))
		}
	}
	if len(paths) < 100 {
		t.Fatalf("found %d Mini sources", len(paths))
	}
	return paths
}

func TestNoraceOverlay(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	plain := write("plain.go", "package p\n\nfunc f() {}\n")
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(plain, time.Time{}, modified); err != nil {
		t.Fatal(err)
	}
	replaced := write("replaced.go", "package p\n")
	backing := write("backing.go", "package p\n\nfunc g() {}\n")
	deleted := write("deleted.go", "package p\n\nfunc h() {}\n")
	empty := write("empty.go", "package p\n\nvar v = 1\n")
	client := write("client.go", "package c\n\nfunc main() {}\n")
	runtimeFile := write("runtime.go", "package testopt\n\nfunc Run() {}\n")
	mini := &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: miniModule, Main: true}
	listed := []goPackage{
		{ImportPath: "client", Dir: dir, GoFiles: []string{"client.go"}},
		{ImportPath: miniPackage, Dir: dir, GoFiles: []string{"runtime.go"}, Module: mini},
		{ImportPath: miniModule + "/internal", DepOnly: true, Dir: dir, GoFiles: []string{"plain.go", "replaced.go", "deleted.go", "empty.go"}, Module: mini},
	}
	replacements := map[string]string{replaced: backing, deleted: ""}
	// Mini's tests keep it instrumented.
	tested := append(listed, goPackage{ImportPath: miniModule + "/internal/minitracer", Dir: dir, GoFiles: []string{"plain.go"}, Module: mini})
	if files := noraceOverlay(tested, replacements, filepath.Join(dir, "plan"), ""); files != 0 || len(replacements) != 2 {
		t.Fatalf("annotated %d files of Mini's tests: %v", files, replacements)
	}
	// So does Mini's API when a client calls it.
	for _, client := range []goPackage{
		{ImportPath: "client", Imports: []string{miniModule + "/propagation"}},
		{ImportPath: "client", TestImports: []string{miniPackage}},
		{ImportPath: "client", XTestImports: []string{miniModule}},
	} {
		uses := append([]goPackage{client}, listed[1:]...)
		if files := noraceOverlay(uses, replacements, filepath.Join(dir, "plan"), ""); files != 0 || len(replacements) != 2 {
			t.Fatalf("annotated %d files for a client of Mini's API: %v", files, replacements)
		}
	}
	// Go rejects overlay entries beneath GOMODCACHE, where go install leaves
	// the CLI's runtime sources.
	if files := noraceOverlay(listed, replacements, filepath.Join(dir, "plan"), dir); files != 0 || len(replacements) != 2 {
		t.Fatalf("annotated %d files in GOMODCACHE: %v", files, replacements)
	}
	if files := noraceOverlay(listed, replacements, filepath.Join(dir, "plan"), filepath.Join(dir, "other")); files != 3 {
		t.Fatalf("annotated %d files: %v", files, replacements)
	}
	for logical, want := range map[string]string{plain: "func f", replaced: "func g", runtimeFile: "func Run"} {
		data, err := os.ReadFile(replacements[logical])
		if err != nil || !bytes.Contains(data, []byte("//go:norace\n/*line :3:1*/"+want)) {
			t.Errorf("%s: %q, %v", logical, data, err)
		}
	}
	// Go reuses its index of a package directory only for files that keep
	// their size and modification time.
	if info, err := os.Stat(replacements[plain]); err != nil || !info.ModTime().Equal(modified) {
		t.Errorf("annotated copy's modification time: %v, %v", info, err)
	}
	if replacements[deleted] != "" {
		t.Errorf("deleted file replaced by %q", replacements[deleted])
	}
	for _, path := range []string{empty, client} {
		if actual, ok := replacements[path]; ok {
			t.Errorf("%s replaced by %q", path, actual)
		}
	}
}

func BenchmarkNoraceSource(b *testing.B) {
	var sources [][]byte
	size := 0
	for _, path := range miniRuntimeSources(b) {
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		sources = append(sources, data)
		size += len(data)
	}
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for b.Loop() {
		var out []byte
		for _, src := range sources {
			out, _ = appendNorace(out[:0], src)
		}
	}
}
