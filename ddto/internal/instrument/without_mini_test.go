package instrument

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const withoutMiniFixture = `package native

import (
	"fmt"
	"os"
	"os/signal"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestPass(t *testing.T) {}

func TestFail(t *testing.T) { t.Errorf("failed with %d", 1) }

func TestFatal(t *testing.T) { t.Fatal("fatal") }

func TestSkip(t *testing.T) { t.Skipf("skipped %s", "here") }

func TestSkipNow(t *testing.T) { t.SkipNow() }

func TestSub(t *testing.T) {
	t.Run("a", func(t *testing.T) {})
	t.Run("b", func(t *testing.T) { t.Error("sub failed") })
}

func TestParallel(t *testing.T) {
	for _, name := range []string{"x", "y"} {
		t.Run(name, func(t *testing.T) { t.Parallel() })
	}
}

func TestSignal(t *testing.T) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
}

func BenchmarkRun(b *testing.B) {
	b.Run("inner", func(b *testing.B) {
		for range b.N {
		}
	})
}

func FuzzSeed(f *testing.F) {
	f.Add(1)
	f.Fuzz(func(t *testing.T, n int) {})
}

func Example() {
	fmt.Println("example")
	// Output: example
}
`

// A test binary built with Mini's edits to testing and os/signal but without
// Mini links and runs as natively, with the same output and exit status. Bazel
// builds such binaries with the instrumented SDK: tools, and tests that the
// rule does not instrument.
func TestInstrumentedTestingWithoutMiniRunsNatively(t *testing.T) {
	if testing.Short() {
		t.Skip("builds testing twice")
	}
	tmp := t.TempDir()
	env := append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	listed := map[string]struct {
		Dir     string
		GoFiles []string
	}{}
	list := exec.Command("go", "list", "-json=ImportPath,Dir,GoFiles", "testing", "os/signal", "internal/runtime/exithook")
	list.Env = env
	out, err := list.Output()
	if err != nil {
		t.Fatal(err)
	}
	for decoder := json.NewDecoder(bytes.NewReader(out)); decoder.More(); {
		var p struct {
			ImportPath, Dir string
			GoFiles         []string
		}
		if err := decoder.Decode(&p); err != nil {
			t.Fatal(err)
		}
		listed[p.ImportPath] = struct {
			Dir     string
			GoFiles []string
		}{p.Dir, p.GoFiles}
	}
	sources := func(pkg string) map[string][]byte {
		files := map[string][]byte{}
		for _, name := range listed[pkg].GoFiles {
			path := filepath.Join(listed[pkg].Dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			files[path] = data
		}
		return files
	}
	replace := map[string]string{}
	add := func(logical string, data []byte) {
		backing := filepath.Join(tmp, "overlay", strings.ReplaceAll(filepath.ToSlash(logical), "/", "_"))
		if err := os.MkdirAll(filepath.Dir(backing), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(backing, data, 0600); err != nil {
			t.Fatal(err)
		}
		replace[logical] = backing
	}
	rewritten, err := TransformWithTimeout(sources("testing"))
	if err != nil {
		t.Fatal(err)
	}
	for logical, data := range rewritten.Files {
		add(logical, data)
	}
	testingDir := listed["testing"].Dir
	add(filepath.Join(testingDir, "zz_dd_ci_visibility_hooks.go"), []byte(MiniHooks(rewritten.ParallelStop, rewritten.Timeout)))
	if err := ExitHookToolchainError(sources("internal/runtime/exithook")); err != nil {
		t.Fatal(err)
	}
	add(filepath.Join(testingDir, "zz_dd_ci_visibility_exit.go"), []byte(ExitHook))
	signals, err := TransformSignal(sources("os/signal"))
	if err != nil {
		t.Fatal(err)
	}
	for logical, data := range signals {
		add(logical, data)
	}
	add(filepath.Join(listed["os/signal"].Dir, "zz_dd_ci_visibility_signal.go"), []byte(SignalHook))
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(tmp, "overlay.json")
	module := filepath.Join(tmp, "module")
	for name, data := range map[string][]byte{overlayFile: overlay, filepath.Join(module, "go.mod"): []byte("module example.com/native\n\ngo 1.25.0\n"), filepath.Join(module, "native_test.go"): []byte(withoutMiniFixture)} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(binary string, args ...string) (string, int) {
		build := exec.Command("go", append([]string{"test", "-c", "-o", binary}, args...)...)
		build.Dir, build.Env = module, env
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", args, err, out)
		}
		cmd := exec.Command(binary, "-test.v", "-test.bench=.", "-test.benchtime=1x", "-test.parallel=1")
		cmd.Dir, cmd.Env = module, env
		out, err := cmd.CombinedOutput()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	native, nativeCode := run(filepath.Join(tmp, "native.test"))
	edited := filepath.Join(tmp, "edited.test")
	instrumented, instrumentedCode := run(edited, "-overlay="+overlayFile)
	// The edits are in the binary, and Mini is not.
	nm := exec.Command("go", "tool", "nm", edited)
	nm.Env = env
	symbols, err := nm.Output()
	if err != nil {
		t.Fatal(err)
	}
	// The hooks' variables carry Mini's names, without a value (B, not D);
	// no Mini code is linked.
	hook := regexp.MustCompile(`(?m)^\s*[0-9a-f]+ B github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.testingHookTFunc$`)
	if !hook.Match(symbols) || regexp.MustCompile(`(?m)^\s*[0-9a-f]+ [TD] github.com/DataDog/ddtest/`).Match(symbols) {
		t.Fatalf("the edited binary lacks the edits or links Mini:\n%s", regexp.MustCompile(`(?m)^.*DataDog/ddtest.*$`).FindAll(symbols, -1))
	}
	normalize := regexp.MustCompile(`\(\d+\.\d+s\)|\s+\d+\s+\d+(\.\d+)? ns/op.*|\t\d+(\.\d+)?s\b`)
	if nativeCode != 1 || instrumentedCode != nativeCode || normalize.ReplaceAllString(instrumented, "") != normalize.ReplaceAllString(native, "") {
		t.Fatalf("native exit %d:\n%s\nedited exit %d:\n%s", nativeCode, native, instrumentedCode, instrumented)
	}
}
