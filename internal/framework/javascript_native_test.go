package framework

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
)

type nativeRoutingExecutor struct {
	calls  int
	output []byte
	err    error
}

func (e *nativeRoutingExecutor) CombinedOutput(_ context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	e.calls++
	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, "message:"); ok {
			data := `{"pickle":{"id":"p","uri":"custom/check.js"}}` + "\n" + `{"testCase":{"pickleId":"p"}}` + "\n"
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				return nil, err
			}
		}
		if path, ok := strings.CutPrefix(arg, "--json="); ok {
			if err := os.WriteFile(path, e.output, 0600); err != nil {
				return nil, err
			}
		}
	}
	return e.output, e.err
}
func (e *nativeRoutingExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	output, err := e.CombinedOutput(ctx, name, args, env)
	return output, nil, err
}
func (e *nativeRoutingExecutor) Run(context.Context, string, []string, map[string]string) error {
	return errors.New("unexpected test execution")
}

func TestJavaScriptForceNativeDiscovery(t *testing.T) {
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY", "true")
	settings.Init()
	for _, name := range []string{"jest", "vitest", "mocha", "cypress", "playwright", "cucumber"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeJavaScriptPath(t, "custom/check.js")
			e := &nativeRoutingExecutor{}
			var f Framework
			switch name {
			case "jest":
				e.output = []byte(`["custom/check.js"]`)
				f = &Jest{executor: e}
			case "vitest":
				e.output = []byte(`[{"file":"custom/check.js"}]`)
				f = &Vitest{executor: e}
			case "mocha":
				e.output = []byte(mochaDiscoveryMarker + `["custom/check.js"]`)
				f = &Mocha{executor: e}
			case "cypress":
				e.output = []byte(cypressDiscoveryMarker + `{"testingType":"e2e","specFiles":["custom/check.js"]}`)
				f = &Cypress{executor: e}
			case "playwright":
				e.output = []byte(playwrightDiscoveryMarker + `{"files":["custom/check.js"]}`)
				f = &Playwright{executor: e}
			case "cucumber":
				f = &Cucumber{executor: e}
			}
			// Even an empty list resolved from the default glob must not bypass
			// forced native discovery of a custom layout.
			files, err := f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: f.TestPattern(), ExplicitFiles: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			if e.calls != 1 || !slices.Equal(files, []string{"custom/check.js"}) {
				t.Fatalf("calls=%d, files=%v", e.calls, files)
			}
			if !f.(NativeTestFileDiscoverer).NativeTestFileDiscoveryUsed() {
				t.Fatal("native discovery not reported")
			}
		})
	}
}

func TestJestStaticFallbackAndForce(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Cleanup(settings.Init)
	writeJavaScriptPath(t, "custom/check.js")
	if err := os.WriteFile("jest.config.js", []byte(`module.exports = readConfigAtRuntime()`), 0644); err != nil {
		t.Fatal(err)
	}
	e := &nativeRoutingExecutor{output: []byte(`["custom/check.js"]`)}
	j := &Jest{executor: e}
	if _, err := j.DiscoverTestFilesFast(context.Background(), discovery.TestFileSet{}); err == nil {
		t.Fatal("unresolved config accepted")
	}
	if e.calls != 0 {
		t.Fatal("fast analyzer started Node")
	}
	files, err := j.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil || e.calls != 1 || !j.NativeTestFileDiscoveryUsed() || !slices.Equal(files, []string{"custom/check.js"}) {
		t.Fatalf("%v %v calls=%d", files, err, e.calls)
	}
	e.err = errors.New("native failure")
	if _, err := j.DiscoverTestFiles(context.Background(), discovery.TestFileSet{}); err == nil {
		t.Fatal("native failure silently replaced with guessed files")
	}
}

func TestJestNativeJSONWithDiagnostics(t *testing.T) {
	t.Chdir(t.TempDir())
	writeJavaScriptPath(t, "checks/[].test.js")
	for _, output := range []string{
		"config diagnostics\n[\"checks/[].test.js\"]\n",
		"config diagnostics: [\"checks/[].test.js\"]\nlast diagnostic\n",
	} {
		j := &Jest{executor: &nativeRoutingExecutor{output: []byte(output)}}
		got, err := j.DiscoverTestFilesNative(context.Background(), discovery.TestFileSet{})
		if err != nil || !slices.Equal(got, []string{"checks/[].test.js"}) {
			t.Fatalf("%v %v", got, err)
		}
	}
	j := &Jest{executor: &nativeRoutingExecutor{output: []byte("[\"checks/[].test.js\"]\n[]\n")}}
	if _, err := j.DiscoverTestFilesNative(context.Background(), discovery.TestFileSet{}); err == nil {
		t.Fatal("ambiguous arrays silently accepted")
	}
}

func TestForcedVitestV1FailureDoesNotUseGlobs(t *testing.T) {
	t.Chdir(t.TempDir())
	writeJavaScriptPath(t, "src/a.test.js")
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_FORCE_FULL_TEST_DISCOVERY", "true")
	settings.Init()
	e := &vitestSequenceExecutor{outputs: [][]byte{[]byte("unknown option filesOnly"), []byte("broken config")}, errors: []error{errors.New("old CLI"), errors.New("broken config")}}
	v := &Vitest{executor: e}
	if _, err := v.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: v.TestPattern()}); err == nil {
		t.Fatal("forced native failure used globs")
	}
}
