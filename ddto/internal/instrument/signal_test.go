package instrument

import (
	"bytes"
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

// toolchainSignalSources returns the os/signal files that the toolchain's
// go/build context compiles for this platform.
func toolchainSignalSources(t *testing.T) (*build.Package, map[string][]byte) {
	t.Helper()
	pkg, err := build.Default.Import("os/signal", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, name := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, name)
		if files[path], err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	return pkg, files
}

// Supported toolchains keep what SignalHook uses. If one stops, Mini builds
// without the hook and handles no signal.
func TestToolchainProvidesSignalHook(t *testing.T) {
	_, files := toolchainSignalSources(t)
	edited, err := TransformSignal(files)
	if err != nil {
		t.Fatalf("%v; review SignalHook", err)
	}
	if len(edited) != 1 {
		t.Fatalf("edited %d files", len(edited))
	}
	for path, src := range edited {
		header, body, _ := bytes.Cut(src, []byte("\n"))
		if string(header) != "//line "+path+":1" {
			t.Errorf("header %q", header)
		}
		if got, want := bytes.Count(body, []byte("\n")), bytes.Count(files[path], []byte("\n")); got != want {
			t.Errorf("the edit moved os/signal's lines: %d lines, want %d", got, want)
		}
		for _, call := range []string{"__dd_civisibility_signalWantedElsewhere(", "cancel(sig, __dd_civisibility_resetSignal)", "cancel(sig, __dd_civisibility_ignoreSignal)"} {
			if n := bytes.Count(body, []byte(call)); n != 1 {
				t.Errorf("%d calls of %s", n, call)
			}
		}
	}
}

// The edited os/signal and SignalHook compile together: every unexported
// declaration that they use exists with the expected type.
func TestSignalHookTypeChecks(t *testing.T) {
	pkg, files := toolchainSignalSources(t)
	edited, err := TransformSignal(files)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for path, src := range files {
		if replacement, ok := edited[path]; ok {
			src = replacement
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, file)
	}
	hook, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, "zz_dd_ci_visibility_signal.go"), SignalHook, 0)
	if err != nil {
		t.Fatal(err)
	}
	parsed = append(parsed, hook)
	config := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := config.Check("os/signal", fset, parsed, nil); err != nil {
		t.Fatal(err)
	}
}

// SignalHook's linknames name variables that Mini's integrations package
// declares with the same types.
func TestSignalHookMatchesMiniRegistration(t *testing.T) {
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
	if err := SignalHookRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
	hook, err := sourceDeclarations(map[string][]byte{"zz_dd_ci_visibility_signal.go": []byte(SignalHook)})
	if err != nil {
		t.Fatal(err)
	}
	const integrations = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations."
	for _, decl := range signalRuntimeAPI {
		name, typ, _ := strings.Cut(strings.TrimPrefix(decl, "var "), " ")
		if !hook["var __dd_civisibility_"+name+" "+typ] || !hook["linkname __dd_civisibility_"+name+" "+integrations+name] {
			t.Errorf("SignalHook does not link %s", decl)
		}
	}
}

// A runtime without the variables, or with other types, builds without the
// hook.
func TestSignalHookRuntimeErrorRejectsOtherRuntimes(t *testing.T) {
	const both = "package integrations\nimport \"os\"\nvar (\n\tsignalNotify func(chan<- os.Signal, chan<- int, ...os.Signal) []os.Signal\n\tsignalStop func(chan<- os.Signal)\n)\n"
	for name, source := range map[string]string{
		"missing":      "package integrations\nfunc other() {}\n",
		"old function": "package integrations\nimport _ \"unsafe\"\n//go:linkname registerSignalNotify\nfunc registerSignalNotify(notify func(chan<- os.Signal, ...os.Signal)) {}\n",
		"notify type":  strings.Replace(both, "...os.Signal", "[]os.Signal", 1),
		"no observer":  strings.Replace(both, "chan<- int, ", "", 1),
		"no result":    strings.Replace(both, ") []os.Signal\n", ")\n", 1),
		"no stop":      strings.Replace(both, "\tsignalStop func(chan<- os.Signal)\n", "", 1),
	} {
		if err := SignalHookRuntimeError(map[string][]byte{"civisibility.go": []byte(source)}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := SignalHookRuntimeError(map[string][]byte{"civisibility.go": []byte(both)}); err != nil {
		t.Errorf("both variables: %v", err)
	}
}

const signalSource = `package signal

import (
	"os"
	"sync"
)

const numSig = 65

var handlers struct {
	sync.Mutex
	m        map[chan<- os.Signal]*handler
	ref      [numSig]int64
	stopping []stopping
}

type stopping struct {
	c chan<- os.Signal
	h *handler
}

type handler struct{ mask [3]uint32 }

func (h *handler) want(sig int) bool { return h.mask[sig/32]>>uint(sig&31)&1 != 0 }

func (h *handler) set(sig int) { h.mask[sig/32] |= 1 << uint(sig&31) }

func signum(sig os.Signal) int { return 0 }

func enableSignal(sig int) {}

func ignoreSignal(sig int) {}

func signalIgnored(sig int) bool { return false }

func Notify(c chan<- os.Signal, sig ...os.Signal) {}

func Stop(c chan<- os.Signal) {}

func Reset(sig ...os.Signal) {
	cancel(sig, disableSignal)
}

func Ignore(sig ...os.Signal) {
	cancel(sig, ignoreSignal)
}

func cancel(sigs []os.Signal, action func(int)) {}

func disableSignal(n int) {}

func process(sig os.Signal) {
	n := signum(sig)
	for c, h := range handlers.m {
		if h.want(n) {
			select {
			case c <- sig:
			default:
			}
		}
	}
	for _, d := range handlers.stopping {
		if d.h.want(n) {
			select {
			case d.c <- sig:
			default:
			}
		}
	}
}
`

// The edits name process's own channel and signal, and touch only process's
// delivery to handlers.m and the actions of Reset's and Ignore's cancel.
func TestTransformSignalEditsTheDelivery(t *testing.T) {
	source := strings.NewReplacer("for c, h := range", "for ch, hd := range", "if h.want(n) {\n\t\t\tselect {\n\t\t\tcase c <- sig", "if hd.want(n) {\n\t\t\tselect {\n\t\t\tcase ch <- sig",
		"func Reset(sig ...os.Signal) {\n\tcancel(sig, disableSignal)", "func Reset(s ...os.Signal) {\n\tcancel(s, disableSignal)").Replace(signalSource)
	edited, err := TransformSignal(map[string][]byte{"signal.go": []byte(source)})
	if err != nil {
		t.Fatal(err)
	}
	got := string(edited["signal.go"])
	for _, want := range []string{
		"if hd.want(n) && !__dd_civisibility_signalWantedElsewhere(ch, n) {",
		"\tcancel(s, __dd_civisibility_resetSignal)\n",
		"\tcancel(sig, __dd_civisibility_ignoreSignal)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("edited source lacks %q", want)
		}
	}
	if strings.Count(got, "__dd_civisibility_") != 3 || strings.Count(got, "\n") != strings.Count(source, "\n")+1 {
		t.Errorf("edited source edits more or moves lines:\n%s", got)
	}
}

// A toolchain whose os/signal differs builds without the hook instead of
// failing to compile os/signal.
func TestTransformSignalRejectsOtherShapes(t *testing.T) {
	for name, source := range map[string]string{
		"no stopping list":     strings.Replace(signalSource, "\tstopping []stopping\n", "", 1),
		"stopping channel":     strings.Replace(signalSource, "\tc chan<- os.Signal\n", "\tc chan os.Signal\n", 1),
		"want":                 strings.Replace(signalSource, "want(sig int) bool", "want(sig uint32) bool", 1),
		"handlers map":         strings.Replace(signalSource, "m        map[chan<- os.Signal]*handler", "m        map[chan<- os.Signal]handler", 1),
		"Notify":               strings.Replace(signalSource, "func Notify(c chan<- os.Signal, sig ...os.Signal) {}", "func Notify(c chan<- os.Signal, sig []os.Signal) {}", 1),
		"Stop":                 strings.Replace(signalSource, "func Stop(c chan<- os.Signal) {}", "func Stop(c chan os.Signal) {}", 1),
		"no mutex":             strings.Replace(signalSource, "\tsync.Mutex\n", "\tmu sync.Mutex\n", 1),
		"Reset body":           strings.Replace(signalSource, "\tcancel(sig, disableSignal)\n", "\tcancel(sig, disableSignal)\n\t_ = 0\n", 1),
		"Reset action":         strings.Replace(signalSource, "\tcancel(sig, disableSignal)\n", "\tcancel(sig, ignoreSignal)\n", 1),
		"Reset argument":       strings.Replace(signalSource, "\tcancel(sig, disableSignal)\n", "\tcancel(nil, disableSignal)\n", 1),
		"no Reset":             strings.Replace(signalSource, "func Reset(", "func reset(", 1),
		"no enableSignal":      strings.Replace(signalSource, "func enableSignal(sig int) {}", "", 1),
		"no Ignore":            strings.Replace(signalSource, "func Ignore(", "func ignore(", 1),
		"Ignore action":        strings.Replace(signalSource, "\tcancel(sig, ignoreSignal)\n", "\tcancel(sig, disableSignal)\n", 1),
		"no ignoreSignal":      strings.Replace(signalSource, "func ignoreSignal(sig int) {}", "", 1),
		"no signalIgnored":     strings.Replace(signalSource, "func signalIgnored(sig int) bool { return false }", "", 1),
		"ref":                  strings.Replace(signalSource, "\tref      [numSig]int64\n", "\tref      []int64\n", 1),
		"no process":           strings.Replace(signalSource, "func process(", "func deliver(", 1),
		"other loop":           strings.Replace(signalSource, "range handlers.m {\n\t\tif h.want(n)", "range handlers.m {\n\t\t_ = 0\n\t\tif h.want(n)", 1),
		"other condition":      strings.Replace(signalSource, "if h.want(n) {\n\t\t\tselect {\n\t\t\tcase c <-", "if n > 0 {\n\t\t\tselect {\n\t\t\tcase c <-", 1),
		"two deliveries":       strings.Replace(signalSource, "\tfor _, d := range handlers.stopping {", "\tfor c, h := range handlers.m {\n\t\tif h.want(n) {\n\t\t\t_ = c\n\t\t}\n\t}\n\tfor _, d := range handlers.stopping {", 1),
		"already instrumented": signalSource + "\nvar __dd_civisibility_signals int\n",
	} {
		if _, err := TransformSignal(map[string][]byte{"signal.go": []byte(source)}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := TransformSignal(map[string][]byte{"signal.go": []byte(signalSource)}); err != nil {
		t.Fatalf("the reference shape: %v", err)
	}
}
