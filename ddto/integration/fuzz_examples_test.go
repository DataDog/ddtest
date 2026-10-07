package integration

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
)

const fuzzExampleSDKVersion = "v2.12.0-dev.3.0.20261001212005-7b32e1812cb5"
const fuzzExampleSDKCommit = "7b32e1812cb5c1fb807a63cc5042750f3d3cd672"
const fuzzFixtureSuffix = "civisibility/integrations/gotesting/fixtures/fuzzexamplesport"

// These are the SDK PR's complete fixture scenarios, including its own fatal,
// cleanup, duration, corpus and repeat-run assertions. Each source is recorded
// in testdata/fuzzexamples/SOURCE.json; adding a scenario requires both oracles.
var fuzzExampleScenarios = []string{
	"pass", "fuzz-failure", "seed-lifecycle", "root-cleanup-goexit",
	"fuzz-missing-call", "example-mismatch", "example-panic", "example-panic-nil",
	"test-management", "active-fuzz", "filtered", "fatal-shutdown",
	"skip-lifecycle", "parallel-duration", "corpus-lifecycle", "repeat-run",
}

type fuzzExampleFixture struct{ dir, binary, runtime, mode string }
type fuzzExampleResult struct {
	Scenario string       `json:"scenario"`
	Mode     string       `json:"mode"`
	Deferred bool         `json:"deferred"`
	SDK      eventCounts  `json:"sdk"`
	Mini     eventCounts  `json:"mini"`
	Timing   parityTiming `json:"timing"`
	Status   string       `json:"status"`
}

// TestMain owns these binaries across both the complete matrix and the campaign
// check. Each execution still has a fresh intake, report and process.
var sharedFuzzExamples = map[string]*struct {
	once    sync.Once
	fixture *fuzzExampleFixture
}{
	"sdk/manual": {}, "mini/manual": {},
	"sdk/orchestrion": {}, "mini/orchestrion": {},
	"sdk/orchestrion/coverage": {}, "mini/orchestrion/coverage": {},
}

func prepareFuzzExampleFixture(t *testing.T, backend, mode string) fuzzExampleFixture {
	return prepareFuzzExampleVariant(t, backend, mode, false)
}

func prepareFuzzExampleVariant(t *testing.T, backend, mode string, covered bool) fuzzExampleFixture {
	t.Helper()
	key := backend + "/" + mode
	if covered {
		key += "/coverage"
	}
	shared := sharedFuzzExamples[key]
	if shared == nil {
		t.Fatalf("unsupported fuzz fixture %s/%s", backend, mode)
	}
	shared.once.Do(func() {
		fixture := buildFuzzExampleFixture(t, backend, mode, covered)
		shared.fixture = &fixture
	})
	if shared.fixture == nil {
		t.Fatalf("fuzz fixture %s/%s did not build", backend, mode)
	}
	return *shared.fixture
}

func buildFuzzExampleFixture(t *testing.T, backend, mode string, covered bool) fuzzExampleFixture {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(sharedParityRoot, "fuzz-examples-")
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(root, "testdata/fuzzexamples"), dir)
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "github.com/DataDog/dd-trace-go/v2/internal/"
	dependency := "github.com/DataDog/dd-trace-go/v2 " + fuzzExampleSDKVersion
	extra := ""
	if backend == "mini" {
		prefix = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/"
		dependency = "github.com/tonyredondo/dd-ci-testing-poc v0.0.0"
		extra = "\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + fmt.Sprintf("%q", filepath.ToSlash(root)) + "\n"

	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(raw), "civisibility/integrations/gotesting/fixtures/itrbackfill/internal/", fuzzFixtureSuffix+"/internal/")
		if backend == "mini" {
			text = strings.ReplaceAll(text, "github.com/DataDog/dd-trace-go/v2/internal/", prefix)
			text = strings.ReplaceAll(text, "github.com/DataDog/dd-trace-go/v2/ddtrace/", prefix+"ddtrace/")
			text = strings.ReplaceAll(text, "github.com/tinylib/msgp/msgp", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp")
		}
		if covered && filepath.Base(path) == "app_test.go" {
			// Keep the SDK workload/assertions; add one production helper so -cover
			// has real counters rather than a package consisting only of _test.go files.
			for _, declaration := range []string{"func TestNormalSelection(t *testing.T) {", "func FuzzNativeParity(f *testing.F) {", "func ExampleNativeParity() {"} {
				if !strings.Contains(text, declaration) {
					t.Fatalf("coverage fixture declaration missing: %s", declaration)
				}
				text = strings.Replace(text, declaration, declaration+" coverageFixtureProbe(true); coverageFixtureProbe(false);", 1)
			}
		}
		return os.WriteFile(path, []byte(text), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if covered {
		raw := []byte("package app\nfunc coverageFixtureProbe(value bool) bool {if value {return true};return false}\n")
		if err := os.WriteFile(filepath.Join(dir, "app/coverage_probe.go"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	module := prefix + fuzzFixtureSuffix
	mod := "module " + module + "\n\ngo 1.26.0\n\nrequire " + dependency + "\n" + extra
	if backend == "sdk" && mode == "orchestrion" {
		mod += "\nrequire github.com/DataDog/orchestrion " + orchestrionVersion + "\n"
		if err := os.WriteFile(filepath.Join(dir, "orchestrion.tool.go"), []byte("//go:build tools\n\npackage fixture\nimport _ \"github.com/DataDog/orchestrion\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	if backend == "sdk" {
		if sum, err := os.ReadFile(filepath.Join(root, "testdata/fixture/go.sum")); err == nil {
			if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	out, stderr, code := command(t, dir, testEnv("GOFLAGS=", "GOWORK=off"), "go", "mod", "tidy")
	if code != 0 {
		t.Fatalf("prepare %s/%s: %s\n%s", backend, mode, out, stderr)
	}
	if backend == "sdk" {
		out, stderr, code := command(t, dir, testEnv("GOFLAGS=", "GOWORK=off"), "go", "list", "-m", "-json", "github.com/DataDog/dd-trace-go/v2")
		if code != 0 {
			t.Fatal(out, stderr)
		}
		var info struct {
			Version string
			Replace any
		}
		if err := json.Unmarshal([]byte(out), &info); err != nil {
			t.Fatal(err)
		}
		if info.Version != fuzzExampleSDKVersion || info.Replace != nil {
			t.Fatalf("SDK oracle changed: %+v", info)
		}
	}
	flags := []string{"-c", "-o", filepath.Join(dir, executableName("fixture.test")), "-mod=readonly"}
	if covered {
		flags = append(flags, "-cover", "-covermode=atomic", "-coverpkg="+module+"/app")
	}
	if os.Getenv("PARITY_TEST_MODE") == "race" {
		flags = append(flags, "-race")
	}
	tool := "go"
	args := append([]string{"test"}, flags...)
	if backend == "mini" && mode == "orchestrion" {
		tool = sharedDriver(t, root)
		args = append([]string{"test", "--runtime=mini"}, flags...)
	}
	if backend == "sdk" && mode == "orchestrion" {
		out, stderr, code = command(t, dir, testEnv("GOFLAGS=", "GOWORK=off"), "go", "list", "-m", "-json", "github.com/DataDog/dd-trace-go/v2")
		if code != 0 {
			t.Fatalf("locate SDK: %s %s", out, stderr)
		}
		var info struct{ Dir string }
		if err := json.Unmarshal([]byte(out), &info); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(info.Dir, "internal/civisibility/integrations/gotesting/orchestrion.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "orchestrion.yml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		tool = os.Getenv("ORCHESTRION_BIN")
		if tool == "" {
			t.Fatal("Orchestrion parity requires ORCHESTRION_BIN")
		}
		args = append([]string{"go", "test"}, flags...)
	}
	args = append(args, "./app")
	out, stderr, code = command(t, dir, testEnv("GOFLAGS=", "GOWORK=off", "DD_CIVISIBILITY_ENABLED=false"), tool, args...)
	if code != 0 {
		t.Fatalf("compile %s/%s: %s\n%s", backend, mode, out, stderr)
	}
	return fuzzExampleFixture{dir: dir, binary: filepath.Join(dir, executableName("fixture.test")), runtime: backend, mode: mode}
}

func fuzzExampleArgs(scenario string) []string {
	args := []string{"-test.count=1", "-test.timeout=2m"}
	switch scenario {
	case "corpus-lifecycle":
		return append(args, "-test.run=^TestFuzzCorpusLifecycle$", "-test.v=true")
	case "repeat-run":
		return append(args, "-test.run=^FuzzNativeParity$")
	case "fatal-shutdown":
		return append(args, "-test.run=^TestFuzzFatalShutdown$")
	case "skip-lifecycle":
		return append(args, "-test.run=^TestFuzzSkipLifecycle$")
	case "parallel-duration":
		return append(args, "-test.run=^TestFuzzParallelDuration$")
	case "seed-lifecycle":
		return append(args, "-test.run=^FuzzSeed(CleanupFailure|CleanupSkip|ParallelFailure)$")
	case "root-cleanup-goexit":
		return append(args, "-test.run=^FuzzRootCleanupGoexit$")
	case "fuzz-missing-call":
		return append(args, "-test.run=^FuzzMissingCall$")
	case "example-panic-nil":
		return append(args, "-test.run=^ExamplePanicNil$")
	case "test-management":
		return append(args, "-test.v=true", "-test.run=^(FuzzManaged|ExampleManaged)")
	case "active-fuzz":
		return append(args, "-test.run=^FuzzActiveOther$", "-test.fuzz=^FuzzNativeParity$", "-test.fuzztime=1x", "-test.parallel=1", "-test.fuzzcachedir=fuzzcache")
	case "filtered":
		return append(args, "-test.run=^TestNormalSelection$")
	default:
		return append(args, "-test.v=true", "-test.run=^(FuzzNative|ExampleNative)")
	}
}

func runFuzzExampleScenario(t *testing.T, fixture fuzzExampleFixture, scenario string, deferred bool) ([]map[string]any, time.Duration) {
	events, _, wall := runFuzzExampleWithCoverage(t, fixture, scenario, deferred, false)
	return events, wall
}

func runFuzzExampleWithCoverage(t *testing.T, fixture fuzzExampleFixture, scenario string, deferred, covered bool) ([]map[string]any, map[string][]byte, time.Duration) {
	t.Helper()
	report := filepath.Join(t.TempDir(), "events.jsonl")
	env := testEnv("GOFLAGS=", "GOWORK=off", "DD_FUZZ_EXAMPLE_MODE="+fixture.mode, "DD_FUZZ_EXAMPLE_SCENARIO="+scenario, "DD_FUZZ_EXAMPLE_REPORT="+report, "DD_FUZZ_EXAMPLE_CACHE_ROOT="+filepath.Dir(report), "XDG_CACHE_HOME="+filepath.Join(filepath.Dir(report), "default-cache"), "DD_SERVICE=fuzz-examples", "DD_CIVISIBILITY_DEFERRED_DELIVERY="+fmt.Sprint(deferred), "DD_FUZZ_EXAMPLE_COVERAGE="+fmt.Sprint(covered))
	if os.Getenv("FUZZ_EXAMPLE_DIAGNOSTICS") == "true" {
		env = append(env, "DD_TRACE_DEBUG=true")
	}
	if scenario == "example-panic-nil" {
		env = append(env, "GODEBUG=panicnil=1")
	}
	out, stderr, code, wall := commandWithTiming(t, filepath.Join(fixture.dir, "app"), env, fixture.binary, fuzzExampleArgs(scenario)...)
	if code != 0 {
		t.Fatalf("%s/%s/deferred=%t fixture assertions failed: exit=%d\n%s\n%s", fixture.runtime, scenario, deferred, code, out, stderr)
	}
	if strings.Contains(stderr, "DATA RACE") {
		t.Fatalf("race in %s: %s", scenario, stderr)
	}
	fallback := filepath.Join(filepath.Dir(report), "default-cache", "dd-trace-go", "civisibility-read-cache")
	if _, err := os.Stat(fallback); err == nil {
		t.Fatal("fixture child used the default read cache instead of its intake-owned root")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	file, err := os.Open(report)
	if err != nil {
		t.Fatalf("missing event evidence: %v\n%s\n%s", err, out, stderr)
	}
	defer file.Close()
	var events []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		var batch []map[string]any
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		if err := decoder.Decode(&batch); err != nil {
			t.Fatal(err)
		}
		for _, event := range batch {
			content := event["content"].(map[string]any)
			for _, key := range []string{"trace_id", "span_id", "parent_id", "test_session_id", "test_module_id", "test_suite_id"} {
				if number, ok := content[key].(json.Number); ok {
					id, err := strconv.ParseUint(string(number), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					content[key] = id
				}
			}
		}
		events = append(events, batch...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if err := validateEventGraph(events); err != nil {
		t.Fatalf("%s/%s hierarchy: %v", fixture.runtime, scenario, err)
	}
	var coverage map[string][]byte
	if covered {
		raw, err := os.ReadFile(report + ".coverage")
		if err != nil {
			t.Fatal(err)
		}
		coverage = map[string][]byte{}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for decoder.More() {
			var batch map[string][]byte
			if err := decoder.Decode(&batch); err != nil {
				t.Fatal(err)
			}
			for name, bitmap := range batch {
				name = filepath.ToSlash(name)
				name = strings.Replace(name, filepath.ToSlash(fixture.dir)+"/", "fixture/", 1)
				coverage[name] = bitmap
			}
		}
	}
	return events, coverage, wall
}

// Normalize only the known relocation of this fixture and incorporated SDK.
// All semantic event attributes remain subject to the ordinary CI comparator.
func normalizeFuzzExampleEvents(events []map[string]any, fixture fuzzExampleFixture) []map[string]any {
	raw, err := json.Marshal(events)
	if err != nil {
		panic(err)
	}
	replacements := []struct{ from, to string }{
		{filepath.ToSlash(fixture.dir) + "/", "fixture/"},
		{strings.ReplaceAll(fixture.dir, "\\", "\\\\") + "\\\\", "fixture/"},
		{"github.com/DataDog/dd-trace-go/v2/internal/", "ci-runtime/"},
		{"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/", "ci-runtime/"},
	}
	text := string(raw)
	for _, r := range replacements {
		text = strings.ReplaceAll(text, r.from, r.to)
	}
	var result []map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		panic(err)
	}
	// Versions and working directories differ between runtimes. Commands and
	// session names remain compared after the exact fixture-path relocation.
	for _, event := range result {
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		wantVersion := "v2.12.0-dev.3"
		if fixture.runtime == "mini" {
			wantVersion = minitracer.Version
		}
		if meta["library_version"] != wantVersion {
			panic(fmt.Sprintf("unexpected %s library_version: %v", fixture.runtime, meta["library_version"]))
		}
		delete(meta, "library_version")
		if stack, ok := meta["error.stack"].(string); ok {
			meta["error.stack"] = canonicalFuzzStack(stack)
		}
		for _, key := range []string{"test.working_directory", "ci.workspace_path"} {
			delete(meta, key)
		}
	}
	return result
}

// Ported functions keep their names/order, while their source lines move with
// documented adaptations. Application/fixture frames and unknown frames stay
// exact. The generated test main also gains a runtime import in the overlay.
func canonicalFuzzStack(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "created by ") {
			if at := strings.LastIndex(line, " in goroutine "); at >= 0 {
				if _, err := strconv.Atoi(line[at+14:]); err == nil {
					lines[i] = line[:at] + " in goroutine ID"
				}
			}
		} else if strings.HasPrefix(line, "goroutine ") {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) == 3 {
				if _, err := strconv.Atoi(parts[1]); err == nil {
					lines[i] = "goroutine ID " + parts[2]
				}
			}
		} else if strings.HasPrefix(line, "\t") {
			if at := strings.LastIndex(line, " +0x"); at >= 0 {
				lines[i] = line[:at]
			}
		} else if strings.HasSuffix(line, ")") {
			// debug.Stack includes process-local argument addresses. Keep the
			// complete function identity; outcome messages and fixture inputs
			// are compared separately.
			if at := strings.LastIndex(line, "("); at >= 0 {
				lines[i] = line[:at]
			}
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		function := strings.TrimPrefix(lines[i], "created by ")
		if strings.HasPrefix(function, "ci-runtime/") && !strings.Contains(function, "/fixtures/") {
			location := strings.ReplaceAll(lines[i+1], "\\", "/")
			colon := strings.LastIndex(location, ":")
			if colon >= 0 {
				lines[i+1] = "\tci-runtime/" + filepath.Base(location[:colon]) + ":adapted"
			}
		} else if function == "main.main" && strings.Contains(lines[i+1], "_testmain.go:") {
			lines[i+1] = "\t_testmain.go:generated"
		}
	}
	// Go 1.26 numbers the deferred-admission closure before the root wrapper
	// and can inline its factory into the descriptor loop. Only these two
	// documented root/defer names in testingF.go have equivalent ownership.
	for i := 0; i+1 < len(lines); i++ {
		for _, owner := range []string{
			"ci-runtime/civisibility/integrations/gotesting.(*M).executeInternalFuzzTarget",
			"ci-runtime/civisibility/integrations/gotesting.(*M).instrumentInternalFuzzTargets.(*M).executeInternalFuzzTarget",
		} {
			for _, suffix := range []string{"", ".1"} {
				if lines[i] == owner+".func2"+suffix && lines[i+1] == "\tci-runtime/testingF.go:adapted" {
					lines[i] = owner + ".func1" + suffix
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func TestFuzzExampleStackComparisonKeepsApplicationFrames(t *testing.T) {
	original := "ci-runtime/civisibility/integrations/gotesting.wrapper\n\t/sdk/internal/civisibility/wrapper.go:10\nci-runtime/civisibility/integrations/gotesting/fixtures/fuzzexamplesport/app.TestFoo\n\tfixture/app/app_test.go:31\n"
	adapted := strings.Replace(original, "/sdk/internal/civisibility/wrapper.go:10", "/port/internal/thirdparty/dd-trace-go/wrapper.go:14", 1)
	if canonicalFuzzStack(original) != canonicalFuzzStack(adapted) {
		t.Fatal("library relocation was not normalized")
	}
	for _, bad := range []string{strings.Replace(adapted, "app_test.go:31", "app_test.go:32", 1), strings.Replace(adapted, "app.TestFoo", "app.TestBar", 1), strings.Replace(adapted, "gotesting.wrapper", "gotesting.other", 1)} {
		if canonicalFuzzStack(original) == canonicalFuzzStack(bad) {
			t.Fatal("source or function mismatch hidden")
		}
	}
}

func TestFuzzExampleParity(t *testing.T) {
	if os.Getenv("ORCHESTRION_BIN") == "" {
		t.Skip("set ORCHESTRION_BIN to run the frozen SDK PR differential fixture")
	}
	var results []fuzzExampleResult
	for _, mode := range []string{"manual", "orchestrion"} {
		sdk := prepareFuzzExampleFixture(t, "sdk", mode)
		mini := prepareFuzzExampleFixture(t, "mini", mode)
		for _, scenario := range fuzzExampleScenarios {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				expected, sdkWall := runFuzzExampleScenario(t, sdk, scenario, false)
				for _, deferred := range []bool{false, true} {
					t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
						actual, miniWall := runFuzzExampleScenario(t, mini, scenario, deferred)
						a, b := normalizeFuzzExampleEvents(expected, sdk), normalizeFuzzExampleEvents(actual, mini)
						ac, err := countCIEvents(a)
						if err != nil {
							t.Fatal(err)
						}
						bc, err := countCIEvents(b)
						if err != nil {
							t.Fatal(err)
						}
						if ac != bc {
							t.Fatalf("event counts SDK=%+v Mini=%+v", ac, bc)
						}
						// The SDK assertions and validateEventGraph check native outcomes
						// and hierarchy. Compare CI attributes with per-process IDs excluded.
						x, y := ciWireEvents(a), ciWireEvents(b)
						if !reflect.DeepEqual(x, y) {
							for i := range x {
								if i >= len(y) || x[i] != y[i] {
									t.Fatalf("wire parity row %d\nSDK=%s\nMini=%s", i, x[i], y[i])
								}
							}
							t.Fatal("wire parity multiplicity differs")
						}
						results = append(results, fuzzExampleResult{Scenario: scenario, Mode: mode, Deferred: deferred, SDK: ac, Mini: bc, Timing: parityTiming{binaryTimingScope, sdkWall.Nanoseconds(), miniWall.Nanoseconds()}, Status: "passed"})
					})
				}
			})
		}
	}
	writeParityEvidence(t, "fuzz-examples", map[string]any{"sdk_commit": fuzzExampleSDKCommit, "sdk_version": fuzzExampleSDKVersion, "go": runtime.Version(), "os": runtime.GOOS, "scenarios": results})
}

func TestFuzzExampleFixtureProvenance(t *testing.T) {
	root := filepath.Join("..", "testdata", "fuzzexamples")
	var manifest struct {
		Commit   string
		Licenses []string
		Files    []struct {
			Path string
			Hash string `json:"sha256"`
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "SOURCE.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Commit != fuzzExampleSDKCommit || len(manifest.Licenses) == 0 {
		t.Fatal("fixture revision/license missing")
	}
	recorded := map[string]bool{}
	for _, entry := range manifest.Files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if recorded[entry.Path] || fmt.Sprintf("%x", sha256.Sum256(raw)) != entry.Hash {
			t.Fatalf("fixture drift: %s", entry.Path)
		}
		recorded[entry.Path] = true
	}
	for _, license := range manifest.Licenses {
		if !recorded[license] {
			t.Fatalf("unrecorded license: %s", license)
		}
	}
	if err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if !recorded[filepath.ToSlash(relative)] {
				return fmt.Errorf("unrecorded fixture source: %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

var fuzzCoverageScenarios = []string{"pass", "seed-lifecycle", "test-management", "active-fuzz", "skip-lifecycle", "parallel-duration", "filtered"}

func TestFuzzExampleCoverageParity(t *testing.T) {
	if os.Getenv("ORCHESTRION_BIN") == "" {
		t.Skip("set ORCHESTRION_BIN for covered SDK PR parity")
	}
	sdk := prepareFuzzExampleVariant(t, "sdk", "orchestrion", true)
	mini := prepareFuzzExampleVariant(t, "mini", "orchestrion", true)
	var results []fuzzExampleResult
	for _, scenario := range fuzzCoverageScenarios {
		t.Run(scenario, func(t *testing.T) {
			expected, sdkCoverage, sdkWall := runFuzzExampleWithCoverage(t, sdk, scenario, false, true)
			for _, deferred := range []bool{false, true} {
				t.Run(fmt.Sprint(deferred), func(t *testing.T) {
					actual, miniCoverage, miniWall := runFuzzExampleWithCoverage(t, mini, scenario, deferred, true)
					a, b := normalizeFuzzExampleEvents(expected, sdk), normalizeFuzzExampleEvents(actual, mini)
					if !reflect.DeepEqual(ciWireEvents(a), ciWireEvents(b)) {
						t.Fatalf("covered wire differs\nSDK %v\nMini %v", ciWireEvents(a), ciWireEvents(b))
					}
					if scenario == "filtered" && (len(sdkCoverage) == 0 || len(miniCoverage) == 0) {
						t.Fatal("ordinary test did not upload the production helper's coverage")
					}
					for _, events := range [][]map[string]any{a, b} {
						found := false
						for _, event := range events {
							if event["type"] == "test_session_end" {
								metrics, _ := event["content"].(map[string]any)["metrics"].(map[string]any)
								_, found = metrics["test.code_coverage.lines_pct"]
							}
						}
						if !found {
							t.Fatal("native global coverage metric missing")
						}
					}
					t.Logf("coverage files SDK=%d Mini=%d", len(sdkCoverage), len(miniCoverage))
					if !reflect.DeepEqual(sdkCoverage, miniCoverage) {
						t.Fatalf("coverage bitmaps differ: SDK %v Mini %v", sdkCoverage, miniCoverage)
					}
					ac, err := countCIEvents(a)
					if err != nil {
						t.Fatal(err)
					}
					bc, err := countCIEvents(b)
					if err != nil {
						t.Fatal(err)
					}
					results = append(results, fuzzExampleResult{Scenario: scenario, Mode: "orchestrion", Deferred: deferred, SDK: ac, Mini: bc, Timing: parityTiming{binaryTimingScope, sdkWall.Nanoseconds(), miniWall.Nanoseconds()}, Status: "passed"})
				})
			}
		})
	}
	writeParityEvidence(t, "fuzz-examples-coverage", map[string]any{"sdk_commit": fuzzExampleSDKCommit, "scenarios": results})
}

func TestFuzzRootStackAliasIsLimitedToThePort(t *testing.T) {
	owner := "ci-runtime/civisibility/integrations/gotesting.(*M).instrumentInternalFuzzTargets.(*M).executeInternalFuzzTarget"
	sdk := owner + ".func1.1\n\tci-runtime/testingF.go:adapted\nexample.com/app.FuzzValue\n\t/work/value_test.go:15\n"
	mini := strings.Replace(sdk, ".func1.1", ".func2.1", 1)
	if canonicalFuzzStack(sdk) != canonicalFuzzStack(mini) {
		t.Fatal("known Go 1.26 closure was not mapped")
	}
	for _, changed := range []string{
		strings.Replace(mini, ".func2.1", ".func2.9", 1),
		strings.Replace(mini, "executeInternalFuzzTarget", "differentFuzzTarget", 1),
		strings.Replace(mini, "value_test.go:15", "value_test.go:16", 1),
	} {
		if canonicalFuzzStack(changed) == canonicalFuzzStack(sdk) {
			t.Fatal("unknown function or application line was hidden")
		}
	}
}

func TestFuzzFixtureCacheIsolation(t *testing.T) {
	for _, backend := range []string{"sdk", "mini"} {
		t.Run(backend, func(t *testing.T) {
			fixture := prepareFuzzExampleFixture(t, backend, "manual")
			args := []string{"test", "-mod=readonly", "-count=1", "./internal/mockci"}
			if os.Getenv("PARITY_TEST_MODE") == "race" {
				args = append(args, "-race")
			}
			out, stderr, code := command(t, fixture.dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", args...)
			if code != 0 {
				t.Fatal(out, stderr)
			}
		})
	}
}
