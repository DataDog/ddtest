package instrument

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// toolchainTesting returns the sources of the toolchain's testing package,
// by path.
func toolchainTesting(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join(runtime.GOROOT(), "src", "testing")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, entry := range entries {
		if name := entry.Name(); !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if sources[filepath.Join(dir, name)], err = os.ReadFile(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return sources
}

// alarmPanicLines returns the lines, after line directives, of the calls in
// the bodies of (*M).startAlarm whose function is named fn.
func alarmPanicLines(t *testing.T, sources map[string][]byte, fn string) []int {
	t.Helper()
	var lines []int
	for name, src := range sources {
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok && f.Recv != nil && f.Name.Name == "startAlarm" {
				ast.Inspect(f.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && id.Name == fn {
							lines = append(lines, fs.Position(call.Pos()).Line)
						}
					}
					return true
				})
			}
		}
	}
	return lines
}

// The toolchain's -timeout alarm panics once, and the hook wraps that panic's
// message on its own line, which the timeout's stack shows.
func TestToolchainTimeoutAlarmTakesTheHook(t *testing.T) {
	sources := toolchainTesting(t)
	want := alarmPanicLines(t, sources, "panic")
	if len(want) != 1 {
		t.Fatalf("startAlarm panics on lines %v, want one; review TransformWithTimeout", want)
	}
	edited, err := TransformWithTimeout(sources)
	if err != nil {
		t.Fatal(err)
	}
	if !edited.Timeout {
		t.Fatal("the alarm was not edited")
	}
	for name, src := range edited.Files {
		sources[name] = src
	}
	if got := alarmPanicLines(t, sources, "__dd_civisibility_instrumentTestTimeout"); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("the hook is called on lines %v, want %v", got, want)
	}
	if got := alarmPanicLines(t, sources, "panic"); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("the alarm panics on lines %v, want %v", got, want)
	}
}

// Only TransformWithTimeout edits the alarm, and an alarm of another shape
// stays unchanged without failing the build.
func TestTimeoutHookIsOptional(t *testing.T) {
	sources := toolchainTesting(t)
	for name, transform := range map[string]func(map[string][]byte) (TestingSources, error){"Transform": Transform, "TransformWithFuzz": TransformWithFuzz} {
		edited, err := transform(sources)
		if err != nil {
			t.Fatal(err)
		}
		for _, src := range edited.Files {
			if edited.Timeout || bytes.Contains(src, []byte("instrumentTestTimeout")) {
				t.Fatalf("%s edited the alarm", name)
			}
		}
	}
	for name, src := range sources {
		if bytes.Contains(src, []byte("func (m *M) startAlarm()")) {
			sources[name] = bytes.Replace(src, []byte("\t\tpanic(fmt.Sprintf(\"test timed out"), []byte("\t\tif *timeout < 0 {\n\t\t\tpanic(\"negative\")\n\t\t}\n\t\tpanic(fmt.Sprintf(\"test timed out"), 1)
		}
	}
	if got := alarmPanicLines(t, sources, "panic"); len(got) != 2 {
		t.Fatalf("the altered alarm panics on lines %v, want two", got)
	}
	edited, err := TransformWithTimeout(sources)
	if err != nil {
		t.Fatalf("an alarm of another shape failed the build: %v", err)
	}
	for _, src := range edited.Files {
		if edited.Timeout || bytes.Contains(src, []byte("instrumentTestTimeout")) {
			t.Fatal("an alarm that panics twice was edited")
		}
	}
}

// TimeoutHook's linkname names Mini's gotesting.instrumentTestTimeout, which
// takes and returns the panic's message.
func TestTimeoutHookMatchesMiniRegistration(t *testing.T) {
	dir := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility", "integrations", "gotesting")
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
	if err := TimeoutHookRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
	hook, err := sourceDeclarations(map[string][]byte{"hooks.go": []byte("package testing\n" + TimeoutHook)})
	if err != nil {
		t.Fatal(err)
	}
	if !hook["func __dd_civisibility_instrumentTestTimeout(string) string"] || !hook["linkname __dd_civisibility_instrumentTestTimeout github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.instrumentTestTimeout"] {
		t.Error("TimeoutHook does not link Mini's gotesting.instrumentTestTimeout")
	}
}

// A runtime without the function, as the released ones, builds without the
// hook instead of failing to link.
func TestTimeoutHookRejectsOtherRuntimes(t *testing.T) {
	const runtime = "package gotesting\nimport _ \"unsafe\"\n//go:linkname instrumentTestTimeout\nfunc instrumentTestTimeout(message string) string { return message }\n"
	for name, source := range map[string]string{
		"missing":  "package gotesting\nfunc other() {}\n",
		"linkname": "package gotesting\nfunc instrumentTestTimeout(message string) string { return message }\n",
		"result":   strings.Replace(runtime, "(message string) string { return message }", "(message string) {}", 1),
	} {
		if err := TimeoutHookRuntimeError(map[string][]byte{"crash.go": []byte(source)}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := TimeoutHookRuntimeError(map[string][]byte{"crash.go": []byte(runtime)}); err != nil {
		t.Errorf("runtime: %v", err)
	}
}
