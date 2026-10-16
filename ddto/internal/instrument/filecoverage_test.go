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
var fileCoverageSite = regexp.MustCompile(`if __ddto_e := __ddto_atomic\.LoadUint64\(&__ddto_epoch\); .*?\{ break \} \} \};`)

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
			if atomic.LoadUint64(&marks[i]) >= e {
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
	if err := FileCoverageParallelRuntimeError(current); err == nil {
		t.Fatal("a runtime without FileCoverageParallel keeps t.Parallel")
	}
	parallel := map[string][]byte{"c.go": []byte("package coverage\n\nfunc FileCoverageParallel() bool { return false }\n")}
	if err := FileCoverageParallelRuntimeError(parallel); err != nil {
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
