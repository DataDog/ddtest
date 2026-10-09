package integration

import (
	"encoding/json"
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func prepareMiniFixture(t *testing.T) (string, string) {
	t.Helper()
	return prepareMiniFixtureWithTempDir(t, t.TempDir)
}

func prepareMiniFixtureWithTempDir(t *testing.T, tempDir func() string) (string, string) {
	t.Helper()
	dir, driver := prepareFixtureWithTempDir(t, false, tempDir)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mod", "edit", "-require=github.com/tonyredondo/dd-ci-testing-poc@v0.0.0"}, {"mod", "edit", "-replace=github.com/tonyredondo/dd-ci-testing-poc=" + root}} {
		out, stderr, code := command(t, dir, testEnv(), "go", args...)
		if code != 0 {
			t.Fatalf("mini module: %s %s", out, stderr)
		}
	}
	// The frozen SDK reference requires Go 1.26. The minimum-toolchain tests
	// exercise Mini on its own, without upgrading the toolchain through that SDK.
	toolchain := strings.Fields(strings.TrimPrefix(runtime.Version(), "devel "))[0]
	if version.Compare(toolchain, "go1.26") < 0 {
		mod := "module example.com/dd-ci-testing-fixture\ngo 1.25.0\nrequire github.com/tonyredondo/dd-ci-testing-poc v0.0.0\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + strconv.Quote(filepath.ToSlash(root)) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the fixture neutral: each CLI backend injects its own runtime. An
	// existing SDK blank import would also link the full SDK into the mini build.
	fixture := filepath.Join(dir, "sample_test.go")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	data = neutralMiniFixture(data)
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, driver
}

// Git can check the fixture out with CRLF on Windows. Normalize its line
// endings before removing the runtime import; source line numbers stay intact.
func neutralMiniFixture(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return []byte(strings.ReplaceAll(text, "\t_ \"github.com/DataDog/dd-trace-go/v2/civisibility\"\n", ""))
}
func TestMiniFixtureRuntimeImportLineEndings(t *testing.T) {
	source := "package fixture_test\nimport (\n\t_ \"github.com/DataDog/dd-trace-go/v2/civisibility\"\n\t\"testing\"\n)\n"
	want := strings.ReplaceAll(source, "\t_ \"github.com/DataDog/dd-trace-go/v2/civisibility\"\n", "")
	for _, ending := range []string{"\n", "\r\n"} {
		got := string(neutralMiniFixture([]byte(strings.ReplaceAll(source, "\n", ending))))
		if got != want {
			t.Fatalf("newline %q: got %q, want %q", ending, got, want)
		}
	}
}
func compileMiniPair(t *testing.T, dir, driver string, flags ...string) []string {
	t.Helper()
	return compileMiniPairWithTempDir(t, dir, driver, t.TempDir, flags...)
}

func compileMiniPairWithTempDir(t *testing.T, dir, driver string, tempDir func() string, flags ...string) []string {
	t.Helper()
	bins, builds := miniPairBuilds(dir, driver, tempDir, flags...)
	buildConcurrently(t, builds...)
	return bins
}

// miniPairBuilds describes the SDK and Mini compilations so callers can run
// them together with other independent builds, such as the reference.
func miniPairBuilds(dir, driver string, tempDir func() string, flags ...string) ([]string, []fixtureBuild) {
	var bins []string
	var builds []fixtureBuild
	for _, runtime := range []string{"sdk", "mini"} {
		bin := filepath.Join(tempDir(), executableName("fixture.test"))
		args := []string{"test", "--runtime=" + runtime, "-c", "-o", bin}
		args = append(args, flags...)
		args = append(args, ".")
		bins = append(bins, bin)
		builds = append(builds, fixtureBuild{name: "compile " + runtime, dir: dir, tool: driver, env: testEnv("DD_CIVISIBILITY_ENABLED=false"), args: args})
	}
	return bins, builds
}
func assertMiniEquivalent(t *testing.T, want, got execution) {
	t.Helper()
	if got.code != want.code || !reflect.DeepEqual(canonicalMiniEvents(got.events), canonicalMiniEvents(want.events)) {
		t.Fatalf("mini/SDK mismatch\nSDK exit=%d events=%v\nMINI exit=%d events=%v\nSDK stderr=%s\nMINI stderr=%s", want.code, want.events, got.code, got.events, want.stderr, got.stderr)
	}
}

// Relocation changes the library namespace and its source root. Canonicalize
// only those frames; retain application frames, library function names, file
// suffixes, line numbers, frame order, and every other event attribute.
func canonicalMiniEvents(events []string) []string {
	out := make([]string, 0, len(events))
	for _, encoded := range events {
		var event map[string]any
		if err := json.Unmarshal([]byte(encoded), &event); err != nil {
			panic(err)
		}
		if stack, ok := event["error.stack"].(string); ok {
			event["error.stack"] = canonicalMiniStack(stack)
		}
		data, err := json.Marshal(event)
		if err != nil {
			panic(err)
		}
		out = append(out, string(data))
	}
	sort.Strings(out)
	return out
}

func canonicalMiniStack(stack string) string {
	lines := strings.Split(stack, "\n")
	for i, line := range lines {
		for _, prefix := range []string{"github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/"} {
			if strings.HasPrefix(line, prefix) {
				lines[i] = "ci-runtime/" + strings.TrimPrefix(line, prefix)
				if i+1 < len(lines) {
					for _, sourceRoot := range []string{"/internal/civisibility/", "/internal/thirdparty/dd-trace-go/civisibility/"} {
						if offset := strings.Index(lines[i+1], sourceRoot); offset >= 0 {
							lines[i+1] = "\tinternal/civisibility/" + lines[i+1][offset+len(sourceRoot):]
							if strings.Contains(prefix, "dd-ci-testing-poc/") {
								function := strings.TrimPrefix(lines[i], "ci-runtime/")
								location := strings.TrimPrefix(lines[i+1], "\tinternal/civisibility/")
								lines[i+1] = "\tinternal/civisibility/" + canonicalMiniCallSite(function, location)
							}
							break
						}
					}
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// These calls moved with cleanup-aware shutdown and the fuzz lifecycle port. Match
// both the function and its exact location; other frames stay strict.
var adaptedMiniCallSites = []struct{ function, mini, sdk string }{
	{"integrations/gotesting.applyAdditionalFeaturesToTestFunc.func2", "integrations/gotesting/instrumentation.go:775", "integrations/gotesting/instrumentation.go:775"},
	{"integrations/gotesting.runTestWithRetry", "integrations/gotesting/instrumentation.go:1011", "integrations/gotesting/instrumentation.go:1011"},
	{"integrations/gotesting.runRetryAttemptCapabilityFallback", "integrations/gotesting/instrumentation.go:1138", "integrations/gotesting/instrumentation.go:1138"},
	{"integrations/gotesting.(*M).executeInternalTest.func1", "integrations/gotesting/testing.go:867", "integrations/gotesting/testing.go:864"},
	{"integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1.1", "integrations/gotesting/instrumentation_orchestrion.go:427", "integrations/gotesting/instrumentation_orchestrion.go:424"},
	{"integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1", "integrations/gotesting/instrumentation_orchestrion.go:433", "integrations/gotesting/instrumentation_orchestrion.go:430"},
	{"integrations/gotesting.runRetryAttemptBody", "integrations/gotesting/retry_attempt_runner.go:452", "integrations/gotesting/retry_attempt_runner.go:452"},
	{"integrations/gotesting.runFreshRetryAttemptOwner", "integrations/gotesting/retry_attempt_runner.go:202", "integrations/gotesting/retry_attempt_runner.go:202"},
}

// Keep the strict exception table tied to actual user-body calls. A moved call
// must update this table; normalizing an adjacent arbitrary frame is not valid.
func TestAdaptedMiniBodyCallSitesMatchSource(t *testing.T) {
	out, stderr, code := command(t, filepath.Join("..", "testdata", "fixture"), testEnv("GOWORK=off"), "go", "list", "-m", "-json", "github.com/DataDog/dd-trace-go/v2")
	var module struct{ Dir, Version string }
	if code != 0 || json.Unmarshal([]byte(out), &module) != nil || module.Version != sdkVersion || module.Dir == "" {
		t.Fatalf("SDK source unavailable or unpinned: %s\n%s", out, stderr)
	}
	calls := map[string]string{
		"integrations/gotesting.applyAdditionalFeaturesToTestFunc.func2":         "runTestWithRetry(&runTestWithRetryOptions{",
		"integrations/gotesting.runTestWithRetry":                                "runRetryAttemptCapabilityFallback(options, \"selected_subtest_fresh_layout_unavailable\")",
		"integrations/gotesting.runRetryAttemptCapabilityFallback":               "options.targetFunc(options.t)",
		"integrations/gotesting.(*M).executeInternalTest.func1":                  "testInfo.originalFunc(t)",
		"integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1.1": "f(currentT)",
		"integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1":   "wrappedFunc(t)",
		"integrations/gotesting.runRetryAttemptBody":                             "target(t)",
		"integrations/gotesting.runFreshRetryAttemptOwner":                       "runRetryAttemptBody(attempt, t, target)",
	}
	for _, site := range adaptedMiniCallSites {
		call, selected := calls[site.function]
		if !selected {
			continue
		}
		for _, source := range []struct{ root, location string }{
			{filepath.Join("..", "internal", "thirdparty", "dd-trace-go", "civisibility"), site.mini},
			{filepath.Join(module.Dir, "internal", "civisibility"), site.sdk},
		} {
			name, number, ok := strings.Cut(source.location, ":")
			line, err := strconv.Atoi(number)
			if !ok || err != nil {
				t.Fatal(source.location)
			}
			data, err := os.ReadFile(filepath.Join(source.root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(data), "\n")
			if line < 1 || line > len(lines) || strings.TrimSpace(lines[line-1]) != call {
				t.Fatalf("%s at %s no longer points to %s", source.location, source.root, call)
			}
		}
	}
}

func canonicalMiniCallSite(function, location string) string {
	for _, site := range adaptedMiniCallSites {
		if function == site.function && location == site.mini {
			return site.sdk
		}
	}
	return location
}

// Testify's Error Trace section contains file locations without function names.
// Only that section may use the exact source-only mapping.
func canonicalMiniAssertionLocation(location string) string {
	for _, site := range adaptedMiniCallSites {
		if location == site.mini {
			return site.sdk
		}
	}
	return location
}

func TestCanonicalMiniStackMapsOnlyAdaptedCallSite(t *testing.T) {
	sdk := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.(*M).executeInternalTest.func1\n\t/sdk/internal/civisibility/integrations/gotesting/testing.go:864\nexample.com/app.TestFailure\n\t/work/app_test.go:17"
	mini := "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).executeInternalTest.func1\n\t/poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/testing.go:867\nexample.com/app.TestFailure\n\t/work/app_test.go:17"
	want := canonicalMiniStack(sdk)
	if canonicalMiniStack(mini) != want {
		t.Fatal("adapted call site was not mapped to the pinned SDK")
	}
	for _, changed := range []string{strings.ReplaceAll(mini, "testing.go:867", "testing.go:868"), strings.ReplaceAll(mini, "app_test.go:17", "app_test.go:18"), strings.ReplaceAll(mini, "executeInternalTest.func1", "executeInternalTest.func2")} {
		if canonicalMiniStack(changed) == want {
			t.Fatal("canonicalization hid a different location or function")
		}
	}
}

// Canonicalization must be limited to the relocated library namespace/root.
func TestCanonicalMiniStackRetainsApplicationFrames(t *testing.T) {
	sdk := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils.CaptureError()\n\t/cache/sdk/internal/civisibility/utils/error.go:42\nexample.com/app.TestFailure()\n\t/work/app_test.go:17"
	mini := "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils.CaptureError()\n\t/work/poc/internal/thirdparty/dd-trace-go/civisibility/utils/error.go:42\nexample.com/app.TestFailure()\n\t/work/app_test.go:17"
	want := canonicalMiniStack(sdk)
	if got := canonicalMiniStack(mini); got != want {
		t.Fatalf("relocation mismatch:\n%s\n%s", want, got)
	}
	if !strings.Contains(want, "example.com/app.TestFailure()\n\t/work/app_test.go:17") {
		t.Fatal("application frame changed")
	}
	if canonicalMiniStack(strings.ReplaceAll(mini, "error.go:42", "error.go:43")) == want {
		t.Fatal("library source line was hidden")
	}
}

func TestMiniTestingCompatibility(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver)
	cases := []struct {
		name string
		args []string
	}{
		{"pass", []string{"-test.v", "-test.run=^Test(Pass|Skip|Skipf|SkipNow|Cleanup|Context|Parallel|Nested)$"}},
		{"count-shuffle", []string{"-test.count=2", "-test.shuffle=42", "-test.run=^Test(Pass|Cleanup|Parallel|Context|Nested)$"}},
		// Fuzz/Examples use the PR-specific reference in TestFuzzExampleParity.
		{"list", []string{"-test.list=TestPass"}},
		{"error", []string{"-test.run=^TestFailures$/^Error$", "-mode=fail"}},
		{"errorf", []string{"-test.run=^TestFailures$/^Errorf$", "-mode=fail"}},
		{"fail", []string{"-test.run=^TestFailures$/^Fail$", "-mode=fail"}},
		{"failnow", []string{"-test.run=^TestFailures$/^FailNow$", "-mode=fail"}},
		{"fatalf", []string{"-test.run=^TestFailures$/^Fatalf$", "-mode=fail"}},
		{"fatal", []string{"-test.run=^TestFailures$/^Fatal$", "-mode=fail"}},
		{"helper", []string{"-test.run=^TestHelper$", "-mode=fail"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				want := execute(t, dir, bins[0], tc.args, enabled, false)
				got := execute(t, dir, bins[1], tc.args, enabled, false)
				assertMiniEquivalent(t, want, got)
				assertMiniCIAttributes(t, want.wireEvents, got.wireEvents)
				if got.out != want.out {
					t.Fatalf("native output differs\nSDK:%s\nMINI:%s", want.out, got.out)
				}
				if enabled && tc.name != "list" && len(got.events) == 0 {
					t.Fatal("no actual CI events")
				}
			}
		})
	}
	t.Run("abnormal-exits", func(t *testing.T) {
		for _, mode := range []string{"panic", "goexit", "timeout"} {
			args := []string{"-test.run=^Test" + strings.ToUpper(mode[:1]) + mode[1:] + "$", "-mode=" + mode}
			if mode == "goexit" {
				args[0] = "-test.run=^TestGoexit$"
			}
			if mode == "timeout" {
				args = append(args, "-test.timeout=50ms")
			}
			for _, enabled := range []bool{false, true} {
				want := execute(t, dir, bins[0], args, enabled, false)
				got := execute(t, dir, bins[1], args, enabled, false)
				marker := map[string]string{"panic": "fixture panic", "goexit": "runtime.Goexit", "timeout": "test timed out"}[mode]
				assertMiniEquivalent(t, want, got)
				assertMiniCIAttributes(t, want.wireEvents, got.wireEvents)
				if got.code == 0 || !strings.Contains(got.stderr, marker) {
					t.Fatalf("abnormal exit missing: %s", got.stderr)
				}
			}
		}
	})
	t.Run("benchmarks", func(t *testing.T) {
		args := []string{"-test.run=^$", "-test.bench=BenchmarkWork", "-test.benchtime=1x"}
		want := execute(t, dir, bins[0], args, true, false)
		got := execute(t, dir, bins[1], args, true, false)
		assertMiniEquivalent(t, want, got)
		if got.code != 0 || len(got.events) == 0 {
			t.Fatal("benchmark not instrumented")
		}
	})
	t.Run("process-retry", func(t *testing.T) {
		args := []string{"-test.run=^TestRetry$", "-mode=retry"}
		want := execute(t, dir, bins[0], args, true, true)
		got := execute(t, dir, bins[1], args, true, true)
		assertMiniEquivalent(t, want, got)
		if got.code != 0 || len(got.events) < 2 {
			t.Fatal("retry did not execute")
		}
	})
	for _, profile := range []string{"disabled", "quarantined", "attempt_to_fix", "efd", "itr"} {
		t.Run(profile, func(t *testing.T) {
			args := []string{"-test.run=^TestManaged$", "-mode=managed"}
			if profile == "attempt_to_fix" {
				args[1] = "-mode=fix"
			}
			if profile == "efd" {
				args = []string{"-test.run=^TestPass$"}
			}
			want := executeProfile(t, dir, bins[0], args, true, false, profile)
			got := executeProfile(t, dir, bins[1], args, true, false, profile)
			assertMiniEquivalent(t, want, got)
			assertMiniCIAttributes(t, want.wireEvents, got.wireEvents)
			if len(got.events) == 0 {
				t.Fatal("policy did not emit events")
			}
		})
	}
}
func TestMiniRaceAndCoverage(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	for _, flag := range []string{"-race", "-cover"} {
		t.Run(flag, func(t *testing.T) {
			flags := []string{flag}
			if flag == "-cover" {
				flags = append(flags, "-covermode=atomic", "-coverpkg=./...")
			}
			bins := compileMiniPair(t, dir, driver, flags...)
			args := []string{"-test.run=^Test(Pass|Cleanup|Context|Parallel|Nested)$"}
			want := execute(t, dir, bins[0], args, true, false)
			got := execute(t, dir, bins[1], args, true, false)
			assertMiniEquivalent(t, want, got)
			if got.code != 0 || len(got.events) == 0 || strings.Contains(got.stderr, "DATA RACE") {
				t.Fatalf("%s failed: %s", flag, got.stderr)
			}
		})
	}
}
func TestMiniPropagationExtractedBySDK(t *testing.T) {
	dir, _ := prepareMiniFixture(t)
	probe := filepath.Join(dir, "probe")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	source := `package main
import (
 "context"
 "fmt"
 "github.com/tonyredondo/dd-ci-testing-poc/propagation"
 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)
func main(){
 parent,err:=propagation.New();if err!=nil{panic(err)}
 _=propagation.WithContext(context.Background(),parent)
 for _,format:=range []propagation.Format{propagation.W3C,propagation.Datadog}{
  carrier:=propagation.MapCarrier{};if err=propagation.Inject(parent,carrier,format);err!=nil{panic(err)}
  style:="tracecontext";if format==propagation.Datadog{style="datadog"}
  parser:=tracer.NewPropagator(&tracer.PropagatorConfig{ExtractStyle:style,InjectStyle:style,MaxTagsHeaderLen:512})
  extracted,err:=parser.Extract(tracer.TextMapCarrier(carrier));if err!=nil{panic(err)}
  if extracted.TraceIDBytes()!=parent.TraceID||extracted.SpanID()!=parent.SpanID{panic("lost remote parent identity")}
  fmt.Println(style,"accepted")
 }
}`
	if err := os.WriteFile(filepath.Join(probe, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", "run", "-mod=mod", "./probe")
	if code != 0 || !strings.Contains(out, "tracecontext accepted") || !strings.Contains(out, "datadog accepted") {
		t.Fatalf("SDK extraction: %d %s\n%s", code, out, stderr)
	}
}

func TestCanonicalMiniCallSitesRetainOtherLocations(t *testing.T) {
	for _, site := range []struct {
		function  string
		mini, sdk int
	}{
		{"instrumentTestingTFuncWithSourceOptions.func1.1", 427, 424},
		{"instrumentTestingTFuncWithSourceOptions.func1", 433, 430},
	} {
		function := "integrations/gotesting." + site.function
		location := fmt.Sprintf("integrations/gotesting/instrumentation_orchestrion.go:%d", site.mini)
		want := fmt.Sprintf("integrations/gotesting/instrumentation_orchestrion.go:%d", site.sdk)
		if got := canonicalMiniCallSite(function, location); got != want {
			t.Fatal(got, want)
		}
		if got := canonicalMiniCallSite(function+"Other", location); got != location {
			t.Fatal("changed function hidden")
		}
		if got := canonicalMiniCallSite(function, location+"0"); got != location+"0" {
			t.Fatal("changed line hidden")
		}
		sdk := "goroutine 1 [running]:\ngithub.com/DataDog/dd-trace-go/v2/internal/civisibility/" + function + "()\n\t/sdk/internal/civisibility/" + want + "\n"
		mini := "goroutine 1 [running]:\ngithub.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/" + function + "()\n\t/poc/internal/thirdparty/dd-trace-go/civisibility/" + location + "\n"
		if canonicalTestifyDiagnostic(sdk) != canonicalTestifyDiagnostic(mini) {
			t.Fatal("Testify diagnostic lost exact call-site mapping")
		}
		if canonicalTestifyDiagnostic(sdk) == canonicalTestifyDiagnostic(strings.ReplaceAll(mini, location, location+"0")) {
			t.Fatal("Testify diagnostic hid an unknown line")
		}
	}
}

func TestTestifyMovedCallSitesInFormattedDiagnostics(t *testing.T) {
	// Testify prints stacks with the test logger's indentation, and assertion
	// traces list source locations without function names.
	sdkPrefix := "/sdk/internal/civisibility/"
	miniPrefix := "/poc/internal/thirdparty/dd-trace-go/civisibility/"
	file := "integrations/gotesting/instrumentation_orchestrion.go:"
	sdk := "test panicked: fixture\n    goroutine 12 [running]:\n    github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingTFuncWithSourceOptions.func1(0x123)\n    \t" + sdkPrefix + file + "430 +0x123\n"
	mini := strings.ReplaceAll(sdk, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/")
	mini = strings.ReplaceAll(strings.ReplaceAll(mini, sdkPrefix, miniPrefix), file+"430", file+"433")
	if canonicalTestifyDiagnostic(sdk) != canonicalTestifyDiagnostic(mini) {
		t.Error("indented stack mapping lost")
	}
	sdk = "\n\tError Trace:\t/work/app_test.go:18\n\t            \t\t\t\t" + sdkPrefix + file + "430\n\tError:      \tfixture failed"
	mini = strings.ReplaceAll(strings.ReplaceAll(sdk, sdkPrefix, miniPrefix), file+"430", file+"433")
	if canonicalTestifyDiagnostic(sdk) != canonicalTestifyDiagnostic(mini) {
		t.Error("formatted assertion source mapping lost")
	}
	for _, changed := range []string{strings.ReplaceAll(mini, file+"433", file+"434"), strings.ReplaceAll(mini, "app_test.go:18", "app_test.go:19"), strings.ReplaceAll(mini, "fixture failed", "different failure")} {
		if canonicalTestifyDiagnostic(sdk) == canonicalTestifyDiagnostic(changed) {
			t.Error("formatted assertion erased a real difference")
		}
	}
	text := "message references " + miniPrefix + file + "433"
	if !strings.Contains(canonicalTestifyDiagnostic(text), file+"433") {
		t.Error("rewrote arbitrary message text")
	}
}
