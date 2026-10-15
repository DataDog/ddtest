package runner

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// prepareSignalHook edits the toolchain's os/signal through the overlay and
// adds the hook; without a runtime that receives the registration, or
// without os/signal in the graph, the build keeps os/signal unchanged.
func TestPrepareSignalHook(t *testing.T) {
	native, err := build.Default.Import("os/signal", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	integrationsDir := filepath.Join(repo, "internal", "thirdparty", "dd-trace-go", "civisibility", "integrations")
	pkg, err := build.ImportDir(integrationsDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := func() []goPackage {
		return []goPackage{
			{ImportPath: "os/signal", Standard: true, DepOnly: true, Dir: native.Dir, GoFiles: native.GoFiles},
			{ImportPath: miniIntegrationsPackage, DepOnly: true, Dir: integrationsDir, GoFiles: pkg.GoFiles},
		}
	}
	prepare := func(listed []goPackage, replacements map[string]string) (*goPackage, string, map[string]string) {
		t.Helper()
		added := map[string]string{}
		signal, refusal, err := prepareSignalHook(listed, replacements, t.TempDir(), func(logical, content string) error {
			added[logical] = content
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return signal, refusal, added
	}

	replacements := map[string]string{}
	signal, refusal, added := prepare(listed(), replacements)
	if signal == nil || refusal != "" {
		t.Fatalf("hook refused: %s", refusal)
	}
	if len(added) != 1 || added[filepath.Join(native.Dir, signalHookFile)] != instrument.SignalHook {
		t.Fatalf("added %v", added)
	}
	backing := replacements[filepath.Join(native.Dir, "signal.go")]
	if len(replacements) != 1 || backing == "" {
		t.Fatalf("replacements %v", replacements)
	}
	if src, err := os.ReadFile(backing); err != nil || !strings.Contains(string(src), "__dd_civisibility_signalWantedElsewhere(") {
		t.Fatalf("edited os/signal: %v %s", err, src)
	}

	// An overlay that already replaces os/signal's file is read through.
	if _, refusal, _ := prepare(listed(), map[string]string{filepath.Join(native.Dir, "signal.go"): backing}); !strings.Contains(refusal, "already instrumented") {
		t.Fatalf("replaced os/signal: %q", refusal)
	}

	// A client can require another version of this module, whose runtime
	// lacks the registration that the hook links to.
	old := filepath.Join(t.TempDir(), "civisibility.go")
	if err := os.WriteFile(old, []byte("package integrations\n"), 0600); err != nil {
		t.Fatal(err)
	}
	withoutRegistration := listed()
	withoutRegistration[1].Dir, withoutRegistration[1].GoFiles = filepath.Dir(old), []string{"civisibility.go"}
	replacements = map[string]string{}
	if signal, refusal, added := prepare(withoutRegistration, replacements); signal != nil || refusal == "" || len(added) != 0 || len(replacements) != 0 {
		t.Fatalf("old runtime: %v %q %v %v", signal, refusal, added, replacements)
	}
	if signal, refusal, _ := prepare(listed()[1:], map[string]string{}); signal != nil || refusal == "" {
		t.Fatalf("no os/signal: %v %q", signal, refusal)
	}

	// A conflicting overlay file stops the plan, as for testing's hooks.
	conflict := errors.New("conflict")
	if _, _, err := prepareSignalHook(listed(), map[string]string{}, t.TempDir(), func(string, string) error { return conflict }); !errors.Is(err, conflict) {
		t.Fatalf("conflict: %v", err)
	}
}

// Coverage selections that reach os/signal translate its edited file for
// Go's cover tool, as they do for testing.
func TestSignalHookNeedsCoverOverlay(t *testing.T) {
	signal := goPackage{ImportPath: "os/signal", Standard: true, Dir: "/goroot/src/os/signal"}
	for _, tc := range []struct {
		patterns []string
		want     bool
	}{{[]string{"os/signal"}, true}, {[]string{"std"}, true}, {[]string{"all"}, true}, {[]string{"./..."}, false}, {nil, false}} {
		if got := needsCoverOverlay("/client", options{coverage: true, coverPatterns: tc.patterns}, nil, []goPackage{signal}); got != tc.want {
			t.Errorf("%v: %t, want %t", tc.patterns, got, tc.want)
		}
	}
}

// Go's cover tool opens os/signal's logical paths, so the tool wrapper sends
// it to ddto, which translates them to the overlay's files, as for testing.
func TestSignalHookCoverGoesThroughThePlan(t *testing.T) {
	args := []string{"cover", "-mode", "set", "signal.go"}
	for _, pkg := range []string{"os/signal", "os/signal [os/signal.test]", "testing"} {
		if !ToolNeedsPlan("cover", args, pkg) {
			t.Errorf("cover of %s skips the plan", pkg)
		}
	}
	if ToolNeedsPlan("cover", args, "os") || ToolNeedsPlan("testify", args, "os/signal") {
		t.Error("cover of other packages or without the cover mode reads the plan")
	}
}

// prepareExitHook adds the hook to testing when the toolchain's exithook and
// the selected runtime provide what it uses, and otherwise leaves testing as
// it is.
func TestPrepareExitHook(t *testing.T) {
	exithook, err := build.Default.Import("internal/runtime/exithook", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	integrationsDir := filepath.Join(repo, "internal", "thirdparty", "dd-trace-go", "civisibility", "integrations")
	pkg, err := build.ImportDir(integrationsDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := func() []goPackage {
		return []goPackage{
			{ImportPath: "internal/runtime/exithook", Standard: true, DepOnly: true, Dir: exithook.Dir, GoFiles: exithook.GoFiles},
			{ImportPath: miniIntegrationsPackage, DepOnly: true, Dir: integrationsDir, GoFiles: pkg.GoFiles},
		}
	}
	prepare := func(listed []goPackage) (string, map[string]string) {
		t.Helper()
		added := map[string]string{}
		refusal, err := prepareExitHook(listed, map[string]string{}, "/goroot/src/testing", func(logical, content string) error {
			added[logical] = content
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return refusal, added
	}
	if refusal, added := prepare(listed()); refusal != "" || len(added) != 1 || added[filepath.Join("/goroot/src/testing", exitHookFile)] != instrument.ExitHook {
		t.Fatalf("hook: %q %v", refusal, added)
	}

	old := filepath.Join(t.TempDir(), "civisibility.go")
	if err := os.WriteFile(old, []byte("package integrations\n"), 0600); err != nil {
		t.Fatal(err)
	}
	withoutForcedExit := listed()
	withoutForcedExit[1].Dir, withoutForcedExit[1].GoFiles = filepath.Dir(old), []string{"civisibility.go"}
	otherToolchain := listed()
	otherToolchain[0].Dir, otherToolchain[0].GoFiles = filepath.Dir(old), []string{"civisibility.go"}
	for name, listed := range map[string][]goPackage{"old runtime": withoutForcedExit, "other toolchain": otherToolchain, "no exithook": listed()[1:]} {
		if refusal, added := prepare(listed); refusal == "" || len(added) != 0 {
			t.Errorf("%s: %q %v", name, refusal, added)
		}
	}

	conflict := errors.New("conflict")
	if _, err := prepareExitHook(listed(), map[string]string{}, "/goroot/src/testing", func(string, string) error { return conflict }); !errors.Is(err, conflict) {
		t.Fatalf("conflict: %v", err)
	}
}
