package instrument

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every construct where Go accepts only some statements, or none.
const fileCoverageConstructs = `package lib

import "sync"

type Number interface{ ~int | ~float64 }

func Sum[T Number](xs []T) (total T) {
	for _, x := range xs {
		total += x
	}
	return
}

func Flow(n int) (out int) {
	defer func() { out++ }()
	switch {}
	switch n {
	case 0:
		out = 1
		fallthrough
	case 1:
		out += 2
	default:
	}
	var v interface{} = n
	switch v.(type) {
	}
	ch := make(chan int, 1)
	select {
	case ch <- n:
	default:
	}
	select {
	case x := <-ch:
		out += x
	}
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue outer
			}
			if i == 2 {
				break outer
			}
		}
	}
	i := 0
again:
	i++
	if i < 3 {
		goto again
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); out += 0 }()
	wg.Wait()
	if n > 100 {
		panic("big")
	} else if n > 50 {
		return -1
	} else {
	}
	func() {}()
	return out + i
}

func Empty() {}
`

const fileCoverageOther = `package lib

func Other() int { return 7 }
`

const fileCoverageData = `package lib

var Table = map[string]int{"a": 1}

func init() { Table["b"] = 2 }
`

func TestMarkFileCoverageKeepsLines(t *testing.T) {
	out, refusal := MarkFileCoverage("/src/lib/flow.go", []byte(fileCoverageConstructs), "__ddto_m", 0)
	if refusal != "" {
		t.Fatal(refusal)
	}
	body := strings.TrimPrefix(string(out), "//line /src/lib/flow.go:1\n")
	if body == string(out) {
		t.Fatalf("missing line directive:\n%s", out)
	}
	if got, want := strings.Count(body, "\n"), strings.Count(fileCoverageConstructs, "\n"); got != want {
		t.Fatalf("%d lines, want %d:\n%s", got, want, body)
	}
	for i, line := range strings.Split(body, "\n") {
		original := strings.Split(fileCoverageConstructs, "\n")[i]
		stripped := strings.ReplaceAll(fileCoverageSite.ReplaceAllString(line, ""), `; import __ddto_atomic "sync/atomic"`, "")
		if stripped != original {
			t.Fatalf("line %d changed beyond marks:\n%s\n%s", i+1, original, line)
		}
	}
	if again, _ := MarkFileCoverage("/src/lib/flow.go", []byte(fileCoverageConstructs), "__ddto_m", 0); !bytes.Equal(again, out) {
		t.Fatal("output is not deterministic")
	}
}

// fileCoverageSite matches one mark site of mark array __ddto_m.
var fileCoverageSite = regexp.MustCompile(`if __ddto_e := __ddto_atomic\.LoadUint64\(&__ddto_epoch\) \+ 1; .*?\{ break \} \} \};`)

// Sites go at the start of a statement list and after statements that end a
// basic block or can block: a call, other than to a builtin that cannot, or
// a channel operation. Pure computation in between gets none.
func TestMarkFileCoveragePlacement(t *testing.T) {
	src := "package p\n\nfunc F(ch chan int) int {\n\tx := 1\n\ty := x + 2\n\tz := g(y)\n\tw := <-ch\n\tv := len(\"ab\")\n\tif v > 0 {\n\t\tv++\n\t}\n\treturn x + y + z + w + v\n}\n\nfunc g(n int) int { return n }\n"
	out, refusal := MarkFileCoverage("/src/p/f.go", []byte(src), "__ddto_m", 0)
	if refusal != "" {
		t.Fatal(refusal)
	}
	// Each line of the function, without its sites, and whether it has one.
	marked := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		marked[strings.TrimSpace(fileCoverageSite.ReplaceAllString(line, ""))] = fileCoverageSite.MatchString(line)
	}
	for line, want := range map[string]bool{
		"func F(ch chan int) int {":      true, // the list starts
		"x := 1":                         false,
		"y := x + 2":                     false,
		"z := g(y)":                      false,
		"w := <-ch":                      true, // after a call
		"v := len(\"ab\")":               true, // after a receive
		"if v > 0 {":                     true, // after a call, and its body starts
		"v++":                            false,
		"return x + y + z + w + v":       true, // after the if, which ends a block
		"func g(n int) int { return n }": true,
	} {
		if got, ok := marked[line]; !ok || got != want {
			t.Errorf("%q: site %t (listed %t), want %t\n%s", line, got, ok, want, out)
		}
	}
}

// A call stays found after a later expression in the same statement, a
// function literal ends its block, and a call to anything named like a
// builtin counts: without types, the name proves nothing.
func TestMarkFileCoveragePlacementAccumulates(t *testing.T) {
	src := "package p\n\nfunc F(ch, done chan int) {\n\tx := wait(ch) + -1\n\tdone <- x\n\tf := func() {}\n\tf()\n\ty := len(\"ab\")\n\tdone <- y\n}\n\nfunc wait(ch chan int) int { return <-ch }\n\nfunc len(s string) int { return 0 }\n"
	out, refusal := MarkFileCoverage("/src/p/f.go", []byte(src), "__ddto_m", 0)
	if refusal != "" {
		t.Fatal(refusal)
	}
	for _, statement := range []string{"done <- x", "f()", "done <- y"} {
		if !regexp.MustCompile(fileCoverageSite.String() + regexp.QuoteMeta(statement)).Match(out) {
			t.Errorf("no site before %q:\n%s", statement, out)
		}
	}
}

func TestMarkFileCoverageRefusals(t *testing.T) {
	for name, src := range map[string]string{
		"cgo":                              "package p\n\n// int one() { return 1; }\nimport \"C\"\n\nfunc One() int { return int(C.one()) }\n",
		"directive //go:nosplit":           "package p\n\n//go:nosplit\nfunc F() {}\n",
		"directive //go:norace":            "package p\n\n//go:norace\nfunc F() {}\n",
		"identifier with prefix __ddto_":   "package p\n\nfunc F() { __ddto_x := 1; _ = __ddto_x }\n",
		"no code that runs after package":  fileCoverageData,
		"no code that runs after package ": "package p\n\ntype T struct{ A int }\n\nconst C = 1\n",
		"parse":                            "package p\n\nfunc F( {}\n",
	} {
		if _, refusal := MarkFileCoverage("/src/p/f.go", []byte(src), "__ddto_m", 0); !strings.HasPrefix(string(refusal), strings.TrimSpace(name)) {
			t.Errorf("%s: refusal %q", name, refusal)
		}
	}
	// Methods and ordinary functions are code even next to an init function.
	if _, refusal := MarkFileCoverage("/src/p/f.go", []byte("package p\n\nfunc init() {}\n\ntype T int\n\nfunc (T) M() {}\n"), "__ddto_m", 0); refusal != "" {
		t.Errorf("method: refusal %q", refusal)
	}
}

// A rewritten package builds, vets and runs; each epoch raises exactly the
// marks of the files whose code ran after it started.
func TestMarkFileCoverageRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// go 1.18 is the oldest version that has generics; stdversion would
	// flag newer APIs in the generated code.
	write("go.mod", "module example.com/marks\n\ngo 1.18\n")
	for i, f := range []struct{ name, src string }{{"lib/flow.go", fileCoverageConstructs}, {"lib/other.go", fileCoverageOther}} {
		out, refusal := MarkFileCoverage(filepath.Join(dir, f.name), []byte(f.src), "__ddto_m", i)
		if refusal != "" {
			t.Fatal(refusal)
		}
		write(f.name, string(out))
	}
	write("lib/data.go", fileCoverageData)
	write("lib/zz_ddto_file_coverage.go", FileCoverageBank{Package: "lib", Marks: "__ddto_m", Names: []string{"example.com/marks/lib/flow.go", "example.com/marks/lib/other.go"}, Always: []string{"example.com/marks/lib/data.go"}, Shared: true}.Source())
	write("main.go", `package main

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"example.com/marks/lib"
)

//go:linkname epoch `+FileCoveragePackage+`.fileCoverageEpoch
var epoch uint64

//go:linkname banks `+FileCoveragePackage+`.fileCoverageBanksV1
var banks unsafe.Pointer

type bank struct {
	next    unsafe.Pointer
	version uintptr
	marks   *uint64
	names   *string
	n       uintptr
	always  *string
	nAlways uintptr
}

func marked(e uint64) []string {
	var files []string
	for p := atomic.LoadPointer(&banks); p != nil; p = (*bank)(p).next {
		b := (*bank)(p)
		marks := unsafe.Slice(b.marks, b.n)
		names := unsafe.Slice(b.names, b.n)
		for i := range marks {
			if atomic.LoadUint64(&marks[i]) >= e+1 {
				files = append(files, names[i])
			}
		}
		for _, name := range unsafe.Slice(b.always, b.nAlways) {
			files = append(files, "always:"+name)
		}
	}
	return files
}

func main() {
	fmt.Println("before", marked(1))
	atomic.AddUint64(&epoch, 1)
	fmt.Println(lib.Flow(0), lib.Sum([]int{1, 2}))
	fmt.Println("first", marked(1))
	atomic.AddUint64(&epoch, 1)
	lib.Empty()
	fmt.Println("second", marked(2))
	atomic.AddUint64(&epoch, 1)
	lib.Other()
	fmt.Println("third", marked(3))
}
`)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(goBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off", "GOTOOLCHAIN=local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("vet", "-atomic", "-bools", "-buildtag", "-directive", "-errorsas", "-ifaceassert", "-nilfunc", "-printf", "-stdversion", "-stringintconv", "-tests", "./...")
	got := run("run", ".")
	want := "before [always:example.com/marks/lib/data.go]\n" +
		"7 3\n" +
		"first [example.com/marks/lib/flow.go always:example.com/marks/lib/data.go]\n" +
		"second [example.com/marks/lib/flow.go always:example.com/marks/lib/data.go]\n" +
		"third [example.com/marks/lib/other.go always:example.com/marks/lib/data.go]\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFileCoverageRuntimeError(t *testing.T) {
	current := map[string][]byte{"c.go": []byte("package coverage\n\nimport \"unsafe\"\n\nvar (\n\tfileCoverageEpoch   uint64\n\tfileCoverageBanksV1 unsafe.Pointer\n)\n")}
	if err := FileCoverageRuntimeError(current); err != nil {
		t.Fatal(err)
	}
	older := map[string][]byte{"c.go": []byte("package coverage\n\nvar mode string\n")}
	if err := FileCoverageRuntimeError(older); err == nil || !strings.Contains(err.Error(), "fileCoverageEpoch") {
		t.Fatalf("older runtime: %v", err)
	}
	if err := FileCoverageContextRuntimeError(current); err == nil {
		t.Fatal("a runtime without context banks reads them")
	}
	// The declarations that ddto checks are Mini's own, with their types.
	dir := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility", "integrations", "gotesting", "coverage")
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	mini := map[string][]byte{}
	for _, name := range names {
		if !strings.HasSuffix(name, "_test.go") {
			if mini[name], err = os.ReadFile(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := FileCoverageRuntimeError(mini); err != nil {
		t.Fatal(err)
	}
	if err := FileCoverageContextRuntimeError(mini); err != nil {
		t.Fatal(err)
	}
}

// A byte order mark stays out of the rewritten file: it is valid only at
// the start, where the line directive goes.
func TestMarkFileCoverageByteOrderMark(t *testing.T) {
	out, refusal := MarkFileCoverage("/src/p/f.go", []byte("\uFEFFpackage p\n\nfunc F() {}\n"), "__ddto_m", 0)
	if refusal != "" {
		t.Fatal(refusal)
	}
	if bytes.Contains(out, []byte("\uFEFF")) || !bytes.HasPrefix(out, []byte("//line /src/p/f.go:1\npackage p")) {
		t.Fatalf("rewritten:\n%q", out)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "f.go", out, 0); err != nil {
		t.Fatal(err)
	}
}

// A function literal in an initializer runs while the package initializes:
// it does not make the file's code run after that.
func TestMarkFileCoverageInitializerLiteral(t *testing.T) {
	src := "package p\n\nvar Limit = func() int { return 5 }()\n"
	if _, refusal := MarkFileCoverage("/src/p/f.go", []byte(src), "__ddto_m", 0); !strings.HasPrefix(string(refusal), "no code") {
		t.Fatalf("refusal %q", refusal)
	}
}

// The prefix check comes before the refusals that leave a file unmarked, and
// covers files that are never marked.
func TestFileCoverageNameClash(t *testing.T) {
	cgo := "package p\n\nimport \"C\"\n\nvar __ddto_epoch uint64\n"
	if _, refusal := MarkFileCoverage("/src/p/c.go", []byte(cgo), "__ddto_m", 0); refusal != FileCoverageNameClash {
		t.Fatalf("cgo file: refusal %q", refusal)
	}
	if err := FileCoverageNameClashError("/src/p/c.go", []byte(cgo)); err == nil {
		t.Fatal("no clash for a cgo file")
	}
	if err := FileCoverageNameClashError("/src/p/f.go", []byte("package p\n\n// __ddto_ in a comment\nfunc F() {}\n")); err != nil {
		t.Fatalf("clash for a comment: %v", err)
	}
}

// A change inside the body of a function or method that runs after package
// initialization, to an ordinary comment or to formatting leaves a file's
// declarations as they were; any other change, which a test can depend on
// without running the file's code, changes them, and so do comments that are
// code.
func TestGoFileDeclarations(t *testing.T) {
	const base = `//go:build linux
// +build linux

// Package p.
package p

/*
#include <stdlib.h>
*/
import "C"

import "strings"

//go:embed data.txt
var data string

//go:linkname now time.now
func now() (int64, int32, int64)

// Limit is documented.
const Limit = 3

var Default = strings.ToUpper("a") // a trailing comment

var (
	Hook = func() int { return 1 }
	List = []int{1, 2}
)

type Item struct {
	Name string ` + "`json:\"name\"`" + `
}

type Namer interface{ Name() string }

func init() { Default = "b" }

//export Use
func Use(n int) int {
	// A comment inside the body.
	return n + Limit
}

//line p.go:100
func (i Item) Label() string { return i.Name }
`
	declarations := func(src string) string {
		t.Helper()
		got, code, ok := GoFileDeclarations([]byte(src))
		if !ok || !code {
			t.Fatalf("GoFileDeclarations(%q) = %q, %t, %t", src, got, code, ok)
		}
		return string(got)
	}
	want := declarations(base)
	if strings.Contains(want, "n + Limit") || strings.Contains(want, "Limit is documented") || !strings.Contains(want, `"b"`) {
		t.Fatalf("declarations of the base file:\n%s", want)
	}
	for _, tc := range []struct {
		name, old, new string
		same           bool
	}{
		{"function body", "return n + Limit", "return Limit + n", true},
		{"comment in a body", "// A comment inside the body.", "// Another comment.", true},
		{"method body", "return i.Name }", "return \"x\" + i.Name }", true},
		{"doc comment", "// Limit is documented.", "// Limit bounds the items.", true},
		{"trailing comment", "// a trailing comment", "// another one", true},
		{"package comment", "// Package p.", "// Package p does things.", true},
		{"spacing", "const Limit = 3", "const  Limit=3", true},
		{"blank lines", "type Namer interface", "\n\ntype Namer interface", true},
		{"line breaks", "List = []int{1, 2}", "List = []int{\n\t\t1,\n\t\t2,\n\t}", true},
		{"a list on one line", "var (\n\tHook = func() int { return 1 }\n\tList = []int{1, 2}\n)", "var (Hook = func() int { return 1 }; List = []int{1, 2})", true},
		{"constant", "const Limit = 3", "const Limit = 4", false},
		{"variable initializer", `strings.ToUpper("a")`, `strings.ToUpper("c")`, false},
		{"package-level function literal", "return 1 }", "return 2 }", false},
		{"init body", `Default = "b"`, `Default = "d"`, false},
		{"type", "Name string", "Name, Title string", false},
		{"struct tag", `json:"name"`, `json:"title"`, false},
		{"interface", "Name() string }", "Name() (string, error) }", false},
		{"function signature", "func Use(n int) int", "func Use(n int64) int", false},
		{"method signature", "func (i Item) Label", "func (i *Item) Label", false},
		{"import", `import "strings"`, `import strings "strings"`, false},
		{"go: directive", "//go:embed data.txt", "//go:embed other.txt", false},
		{"linkname", "//go:linkname now time.now", "//go:linkname now time.nanotime", false},
		{"build constraint", "//go:build linux", "//go:build darwin", false},
		{"legacy build constraint", "// +build linux", "// +build darwin", false},
		{"line directive", "//line p.go:100", "//line p.go:200", false},
		{"export", "//export Use", "//export UseIt", false},
		{"cgo preamble", "#include <stdlib.h>", "#include <stdio.h>", false},
	} {
		if !strings.Contains(base, tc.old) {
			t.Fatalf("%s: base lacks %q", tc.name, tc.old)
		}
		if got := declarations(strings.Replace(base, tc.old, tc.new, 1)); (got == want) != tc.same {
			t.Errorf("%s: declarations same=%t, want %t", tc.name, got == want, tc.same)
		}
	}
	if _, code, ok := GoFileDeclarations([]byte("package p\n\nconst A = 1\n\nfunc init() {}\n")); !ok || code {
		t.Errorf("a file whose only code runs at initialization: code=%t ok=%t", code, ok)
	}
	if _, _, ok := GoFileDeclarations([]byte("package p\nfunc {")); ok {
		t.Error("a file that does not parse")
	}
}
