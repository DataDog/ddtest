package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
							// The deferred lease adds six lines before this unchanged
							// call site in the port. Map this exact Mini location back
							// to the frozen SDK; retain all other source locations.
							if strings.Contains(prefix, "dd-ci-testing-poc/") && lines[i] == "ci-runtime/integrations/gotesting.(*M).executeInternalTest.func1" && lines[i+1] == "\tinternal/civisibility/integrations/gotesting/testing.go:844" {
								lines[i+1] = "\tinternal/civisibility/integrations/gotesting/testing.go:838"
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

func TestCanonicalMiniStackMapsOnlyAdaptedCallSite(t *testing.T) {
	sdk := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.(*M).executeInternalTest.func1\n\t/sdk/internal/civisibility/integrations/gotesting/testing.go:838\nexample.com/app.TestFailure\n\t/work/app_test.go:17"
	mini := "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).executeInternalTest.func1\n\t/poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/testing.go:844\nexample.com/app.TestFailure\n\t/work/app_test.go:17"
	want := canonicalMiniStack(sdk)
	if canonicalMiniStack(mini) != want {
		t.Fatal("adapted call site was not mapped to the pinned SDK")
	}
	for _, changed := range []string{strings.ReplaceAll(mini, "testing.go:844", "testing.go:845"), strings.ReplaceAll(mini, "app_test.go:17", "app_test.go:18"), strings.ReplaceAll(mini, "executeInternalTest.func1", "executeInternalTest.func2")} {
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
		{"examples-fuzz-seeds", []string{"-test.run=^(ExampleAdd|FuzzAdd)$"}},
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
				if enabled && tc.name != "list" && tc.name != "examples-fuzz-seeds" && len(got.events) == 0 {
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
