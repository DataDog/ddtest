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

// The toolchain's gopanic takes the hook just before it prints an
// unrecovered panic, on preprintpanics' line. If it stops, --panic-hook
// warns and builds without the hook.
func TestToolchainGopanicTakesTheHook(t *testing.T) {
	pkg, err := build.Default.Import("runtime", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, name := range pkg.GoFiles {
		if sources[name], err = os.ReadFile(filepath.Join(pkg.Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	edited, err := TransformPanic(sources)
	if err != nil {
		t.Fatalf("%v; review TransformPanic", err)
	}
	if len(edited) != 1 {
		t.Fatalf("edited %d files, want gopanic's", len(edited))
	}
	for name, src := range edited {
		original := string(sources[name])
		got := string(src)
		if strings.Count(got, "__dd_civisibility_") != 5 || strings.Count(got, "\n") != strings.Count(original, "\n")+1 {
			t.Fatalf("%s: the edits add more than the calls or move lines", name)
		}
		// fatal and throw pass their message to fatalthrow, which copies it
		// where it starts the panic, each on its line.
		gotLines, originalLines := strings.Split(got, "\n"), strings.Split(original, "\n")
		edited := map[string]string{
			"fatalthrow(throwTypeRuntime)":   "fatalthrow(throwTypeRuntime, s)",
			"fatalthrow(throwTypeUser)":      "fatalthrow(throwTypeUser, s)",
			"func fatalthrow(t throwType) {": "func fatalthrow(t throwType, __dd_civisibility_message string) {",
			"startpanic_m()":                 "if startpanic_m() { __dd_civisibility_copyFatal(__dd_civisibility_message) }",
		}
		seen := 0
		for i, line := range originalLines {
			if want, ok := edited[strings.TrimSpace(line)]; ok {
				seen++
				if strings.TrimSpace(gotLines[i+1]) != want {
					t.Errorf("%s:%d: %q, want %q", name, i+1, gotLines[i+1], want)
				}
			}
		}
		if seen != len(edited) {
			t.Errorf("%s: found %d of the %d edited lines", name, seen, len(edited))
		}
		var hookLine, nativeLine int
		for i, line := range strings.Split(got, "\n") {
			if strings.Contains(line, "preprintpanics(&p); if __dd_civisibility_unhandledPanic != nil { __dd_civisibility_unhandledPanic(p.arg) }") {
				hookLine = i // The line directive comes first.
			}
		}
		for i, line := range strings.Split(original, "\n") {
			if strings.TrimSpace(line) == "preprintpanics(&p)" {
				nativeLine = i + 1
			}
		}
		if hookLine == 0 || hookLine != nativeLine {
			t.Fatalf("%s: the hook is on line %d, preprintpanics on line %d", name, hookLine, nativeLine)
		}
	}
}

// PanicHook compiles as a file of package runtime, which may import only
// unsafe, against what panicToolchainAPI lists of the runtime.
func TestPanicHookTypeChecks(t *testing.T) {
	const runtime = `package runtime

import "unsafe"

type uintptrValue struct{ v uintptr }

func (u *uintptrValue) Load() uintptr { return u.v }

var crashFD uintptrValue

func write(fd uintptr, p unsafe.Pointer, n int32) int32 { return n }
`
	fset := token.NewFileSet()
	var files []*ast.File
	for name, src := range map[string]string{panicHookName: PanicHook, "runtime.go": runtime} {
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		if name != panicHookName {
			continue
		}
		for _, spec := range file.Imports {
			if spec.Path.Value != `"unsafe"` {
				t.Errorf("PanicHook imports %s", spec.Path.Value)
			}
		}
	}
	if _, err := (&types.Config{Importer: importer.Default()}).Check("runtime", fset, files, nil); err != nil {
		t.Fatal(err)
	}
	for _, used := range []string{"var crashFD ", "func write("} {
		listed := false
		for _, decl := range panicToolchainAPI {
			listed = listed || strings.HasPrefix(decl, used)
		}
		if !listed {
			t.Errorf("panicToolchainAPI lacks %s", used)
		}
	}
}

const panicHookName = "zz_dd_ci_visibility_panic.go"

// PanicHook's linkname names Mini's integrations.unhandledPanic, of the same
// type.
func TestPanicHookMatchesMiniRegistration(t *testing.T) {
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
	if err := PanicHookRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
	hook, err := sourceDeclarations(map[string][]byte{panicHookName: []byte(PanicHook)})
	if err != nil {
		t.Fatal(err)
	}
	if !hook["var __dd_civisibility_unhandledPanic func(any)"] || !hook["linkname __dd_civisibility_unhandledPanic github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.unhandledPanic"] {
		t.Error("PanicHook does not link Mini's integrations.unhandledPanic")
	}
}

// A runtime without the variable, as the released ones, builds without the
// hook, and so does a toolchain whose gopanic differs.
func TestPanicHookRejectsOtherRuntimesAndToolchains(t *testing.T) {
	for name, source := range map[string]string{
		"missing": "package integrations\nfunc other() {}\n",
		"type":    "package integrations\nvar unhandledPanic func(string)\n",
	} {
		if err := PanicHookRuntimeError(map[string][]byte{"crash.go": []byte(source)}); err == nil {
			t.Errorf("runtime %s: accepted", name)
		}
	}
	if err := PanicHookRuntimeError(map[string][]byte{"crash.go": []byte("package integrations\nvar unhandledPanic func(any)\n")}); err != nil {
		t.Errorf("runtime: %v", err)
	}
	const gopanic = "package runtime\nimport (\n\t\"internal/runtime/atomic\"\n\t\"unsafe\"\n)\nvar crashFD atomic.Uintptr\nfunc write(fd uintptr, p unsafe.Pointer, n int32) int32 { return n }\n" +
		"type _panic struct {\n\targ any\n}\nfunc preprintpanics(p *_panic) {}\nfunc gopanic(e any) {\n\tvar p _panic\n\tpreprintpanics(&p)\n\tfatalpanic(&p)\n}\n" +
		"func throw(s string) {\n\tsystemstack(func() {\n\t\tprint(\"fatal error: \")\n\t\tprintindented(s)\n\t})\n\tfatalthrow(throwTypeRuntime)\n}\n" +
		"func fatal(s string) {\n\tsystemstack(func() {\n\t\tprint(\"fatal error: \")\n\t\tprintindented(s)\n\t})\n\tfatalthrow(throwTypeUser)\n}\n" +
		"type throwType uint32\nfunc startpanic_m() bool { return true }\nfunc fatalthrow(t throwType) {\n\tsystemstack(func() {\n\t\tif isSecureMode() {\n\t\t\texit(2)\n\t\t}\n\t\tstartpanic_m()\n\t})\n}\n"
	for name, source := range map[string]string{
		"no gopanic":           strings.Replace(gopanic, "gopanic", "gopanic2", 1),
		"no preprintpanics":    strings.Replace(gopanic, "\tpreprintpanics(&p)\n", "", 1),
		"nested":               strings.Replace(gopanic, "\tpreprintpanics(&p)\n", "\tif true {\n\t\tpreprintpanics(&p)\n\t}\n", 1),
		"twice":                strings.Replace(gopanic, "\tpreprintpanics(&p)\n", "\tpreprintpanics(&p)\n\tpreprintpanics(&p)\n", 1),
		"other argument":       strings.Replace(gopanic, "\tpreprintpanics(&p)", "\tpreprintpanics(nil)", 1),
		"no arg field":         strings.Replace(gopanic, "\targ any\n", "\tvalue any\n", 1),
		"preprintpanics":       strings.Replace(gopanic, "func preprintpanics(p *_panic) {}", "func preprintpanics(p _panic) {}", 1),
		"no crashFD":           strings.Replace(gopanic, "var crashFD atomic.Uintptr\n", "", 1),
		"no fatalthrow call":   strings.Replace(gopanic, "\tfatalthrow(throwTypeUser)\n", "", 1),
		"throw message":        strings.Replace(gopanic, "func throw(s string) {", "func throw(string) {", 1),
		"no startpanic_m call": strings.Replace(gopanic, "\t\tstartpanic_m()\n", "", 1),
		"already instrumented": gopanic + "var __dd_civisibility_unhandledPanic func(any)\n",
	} {
		if _, err := TransformPanic(map[string][]byte{"panic.go": []byte(source)}); err == nil {
			t.Errorf("toolchain %s: accepted", name)
		}
	}
	edited, err := TransformPanic(map[string][]byte{"panic.go": []byte(gopanic)})
	if err != nil {
		t.Fatal(err)
	}
	got := string(edited["panic.go"])
	if !strings.Contains(got, "\tpreprintpanics(&p); if __dd_civisibility_unhandledPanic != nil { __dd_civisibility_unhandledPanic(p.arg) }\n") || !strings.Contains(got, "\tfatalthrow(throwTypeRuntime, s)\n") || !strings.Contains(got, "\tfatalthrow(throwTypeUser, s)\n") || !strings.Contains(got, "func fatalthrow(t throwType, __dd_civisibility_message string) {") || !strings.Contains(got, "\t\tif startpanic_m() { __dd_civisibility_copyFatal(__dd_civisibility_message) }\n") {
		t.Fatalf("edited runtime:\n%s", got)
	}
}
