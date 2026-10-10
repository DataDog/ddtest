package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

func TestFuzzHookTargetsSelectedRuntime(t *testing.T) {
	for _, runtime := range []Runtime{Mini, SDK} {
		hooks := hooksForRuntime(runtime, false)
		prefix := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/"
		other := "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/"
		if runtime == Mini {
			prefix, other = other, prefix
		}
		target := prefix + "integrations/gotesting.instrumentTestingFuzzFunc"
		if strings.Count(hooks, target) != 1 || strings.Contains(hooks, other) {
			t.Fatalf("%s fuzz hook linked to the wrong runtime", runtime)
		}
	}
}

// Only Mini links the parallel-stop hook, and only when testing declares the
// counter it increments; the SDK runtime has no registration target.
func TestParallelStopHookOnlyForMini(t *testing.T) {
	if !strings.HasSuffix(hooksForRuntime(Mini, true), instrument.ParallelStopHook) {
		t.Fatal("Mini hooks lack the parallel-stop hook")
	}
	if strings.Contains(hooksForRuntime(Mini, false), "registerTestingParallelStop") {
		t.Fatal("hook added although testing lacks the counter")
	}
	if strings.Contains(hooksForRuntime(SDK, true), "registerTestingParallelStop") {
		t.Fatal("SDK hooks link a Mini-only target")
	}
}

// The runtime import joins a package's internal tests unless the package has
// an external test package, or the runtime imports the package: only an
// external test package can import it back without an import cycle.
func TestRuntimeImportTestPackage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	plan, err := PrepareRuntime(t.Context(), root, []string{"./internal/runner", "./internal/testassert", "./internal/minitracer"}, Mini)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		plan.Release()
		if err := os.RemoveAll(plan.Dir); err != nil {
			t.Error(err)
		}
	})
	data, err := os.ReadFile(plan.File)
	if err != nil {
		t.Fatal(err)
	}
	var overlay Overlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		"runner":     "package runner\n",          // Internal tests only.
		"testassert": "package testassert_test\n", // External tests.
		"minitracer": "package minitracer_test\n", // Imported by the runtime.
	} {
		backing := overlay.Replace[filepath.Join(root, "internal", dir, "zz_dd_ci_visibility_test.go")]
		source, err := os.ReadFile(backing)
		if err != nil || !strings.HasPrefix(string(source), want+"import __dd_ci_runtime ") {
			t.Fatalf("%s: %q %v", dir, source, err)
		}
	}
}
