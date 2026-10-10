package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const composedAPMSource = `package fixture_test
import (
 "context"
 "os"
 "testing"
 "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)
//dd:span span.name:poc.orchestrion.application
func composedApplication(ctx context.Context) {}
func TestComposedAPMHTTP(t *testing.T) {
 if err:=tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")),tracer.WithLogStartup(false));err!=nil {t.Fatal(err)}
 defer tracer.Stop()
 composedApplication(context.Background())
 tracer.Flush()
}
func TestComposedAPM(t *testing.T) {
 mt:=mocktracer.Start();defer mt.Stop()
 composedApplication(context.Background())
 spans:=mt.FinishedSpans()
 want:=0;if os.Getenv("POC_EXPECT_APM_WEAVING")=="true" {want=1}
 if len(spans)!=want {t.Fatalf("APM instrumentation: %d spans, want %d",len(spans),want)}
 if want>0 && spans[0].OperationName()!="poc.orchestrion.application" {t.Fatal(spans[0].OperationName())}
}
`

// Keep the full SDK testing advice enabled: composition must not depend on
// clients deleting integrations from their Orchestrion configuration.
func TestMiniOrchestrionComposition(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference == "" {
		t.Skip("ORCHESTRION_BIN not configured")
	}
	dir, driver := prepareMiniFixture(t)
	configureReferenceFixture(t, dir)
	installComposedAPM(t, dir)
	env := testEnv("DD_CIVISIBILITY_ENABLED=false", "PATH="+filepath.Dir(reference)+string(os.PathListSeparator)+os.Getenv("PATH"))
	tc := parityCase{Name: "APM-with-CI", Args: []string{"-test.run=^(TestPass|TestComposedAPM)$"}, MinTests: 2}
	baseline := filepath.Join(t.TempDir(), executableName("fixture.test"))
	output, stderr, code := command(t, dir, env, driver, "test", "-c", "-o", baseline, ".")
	if code != 0 {
		t.Fatalf("Mini baseline: %d %s %s", code, output, stderr)
	}
	want, mini := runParityCase(t, dir, baseline, tc)
	variants := []struct {
		name  string
		args  []string
		flags string
	}{
		{"toolexec", []string{"test", "-toolexec=" + quoteToolArgument(t, reference) + " toolexec"}, ""},
		{"go-tool-toolexec", []string{"test", "-toolexec=go tool orchestrion toolexec"}, ""},
		{"pin-check", []string{"orchestrion", "go", "test"}, ""},
		{"GOFLAGS", []string{"test"}, fmt.Sprintf("\"-toolexec=%s toolexec\"", quoteToolArgument(t, reference))},
		{"wrapper", []string{"orchestrion", "go", "test"}, ""},
		{"go-tool-wrapper", []string{"go", "tool", "orchestrion", "go", "test"}, ""},
		{"sdk-wrapper", []string{"orchestrion", "go", "test", "--runtime=sdk"}, ""},
		{"mini-after-sdk", []string{"orchestrion", "go", "test"}, ""},
	}
	unchangedFixtureFiles(t, dir, "go.mod", "go.sum", "orchestrion.tool.go", "orchestrion.yml", "sample_test.go", "composed_test.go")
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			args := append(append([]string(nil), variant.args...), "-c", "-o", bin, ".")
			output, stderr, code := command(t, dir, append(env, "GOFLAGS="+variant.flags, orchestrionPinChecked+"="+fmt.Sprint(variant.name != "pin-check")), driver, args...)
			if code != 0 {
				t.Fatalf("Mini + Orchestrion build: %d\n%s\n%s", code, output, stderr)
			}
			caseTC := tc
			baselineReceiver, baselineExecution := want, mini
			if variant.name == "sdk-wrapper" {
				// mocktracer.Start replaces the SDK's global CI tracer. Validate explicit
				// SDK selection with a normal test; Mini's independent tracer is tested above.
				caseTC.Args = []string{"-test.run=^TestPass$"}
				caseTC.MinTests = 1
				control := filepath.Join(t.TempDir(), executableName("fixture.test"))
				output, stderr, code = command(t, dir, env, driver, "test", "--runtime=sdk", "-c", "-o", control, ".")
				if code != 0 {
					t.Fatal(output, stderr)
				}
				baselineReceiver, baselineExecution = runParityCase(t, dir, control, caseTC)
			}
			caseTC.Env = []string{"POC_EXPECT_APM_WEAVING=true"}
			got, composed := runParityCase(t, dir, bin, caseTC)
			assertParityCase(t, caseTC, baselineReceiver, got, baselineExecution, composed)
			if variant.name != "sdk-wrapper" {
				assertComposedAPMDelivery(t, dir, bin)
			}
		})
	}
}

// Exercise the real selective compiler hooks together, including cover's
// logical-source translation. Each binary must preserve Mini's native output,
// retry policies and event counts while Orchestrion owns application code.
func TestMiniOrchestrionLibrariesAndDelivery(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference == "" {
		t.Skip("ORCHESTRION_BIN not configured")
	}
	dir, driver := prepareTestifyFixture(t, true)
	source := `package fixture_test
import ("testing"; "strings"; "go.uber.org/goleak")
func TestComposedGoleak(t *testing.T) {goleak.VerifyNone(t)}
func TestComposedRealLeak(t *testing.T) {
 done:=make(chan struct{});t.Cleanup(func(){close(done)})
 go func(){<-done}()
 err:=goleak.Find()
 if err==nil || !strings.Contains(err.Error(),"TestComposedRealLeak.func2") {t.Fatalf("client leak was not detected: %v",err)}
 // Keep the assertion deterministic; goleak's goroutine IDs change per process.
 t.Error("client goroutine leaked")
}
`
	if err := os.WriteFile(filepath.Join(dir, "composed_goleak_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	goGet(t, dir, "go.uber.org/goleak@v1.3.0")
	variants := []struct {
		name  string
		flags []string
	}{
		{"plain", nil},
		{"coverage", []string{"-coverpkg=./...,testing,github.com/stretchr/testify/suite", "-covermode=atomic"}},
		{"race", []string{"-race"}},
		{"race-coverage", []string{"-race", "-coverpkg=./...,testing,github.com/stretchr/testify/suite", "-covermode=atomic"}},
	}
	cases := []parityCase{
		{Name: "Testify-pass", Args: []string{"-test.run=^TestParitySuite$/^Test(Pass|Skip)$"}, MinTests: 3},
		{Name: "Testify-failure", Args: []string{"-test.run=^TestParitySuite$/^TestAssertFailure$"}, WantExit: 1, MinTests: 2},
		{Name: "Testify-external-helper", Args: []string{"-test.run=^TestParityExternalHelper$/^TestPass$"}, MinTests: 2},
		{Name: "goleak", Args: []string{"-test.run=^TestComposedGoleak$"}, MinTests: 1},
		{Name: "real-leak", Args: []string{"-test.run=^TestComposedRealLeak$"}, WantExit: 1, MinTests: 1},
		{Name: "parallel", Args: []string{"-test.run=^TestParallel$", "-test.parallel=8", "-test.count=2"}, MinTests: 18},
		{Name: "fuzz-seeds", Args: []string{"-test.run=^FuzzAdd$"}, MinTests: 1},
		{Name: "examples", Args: []string{"-test.run=^ExampleAdd$"}, MinTests: 1},
		{Name: "auto-retry-in-process", Args: []string{"-test.run=^TestParitySuite$/^TestFlaky$"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process"}, Policy: policySettings{Retry: true}, MinTests: 3},
		{Name: "auto-retry-process", Args: []string{"-test.run=^TestParitySuite$/^TestFlaky$"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process"}, Policy: policySettings{Retry: true}, MinTests: 3},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			binaries := make([]string, 2)
			for i := range binaries {
				binaries[i] = filepath.Join(t.TempDir(), executableName("fixture.test"))
				args := append([]string{"test", "-c", "-o", binaries[i]}, variant.flags...)
				if i == 1 {
					args = append(args, "-toolexec="+quoteToolArgument(t, reference)+" toolexec")
				}
				args = append(args, ".")
				out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
				if code != 0 {
					t.Fatalf("compile %d: %d %s %s", i, code, out, stderr)
				}
			}
			variantCases := append([]parityCase(nil), cases...)
			if variant.name == "coverage" || variant.name == "race-coverage" {
				variantCases = append(variantCases, parityCase{Name: "per-test-coverage", Args: []string{"-test.run=^TestParitySuite$/^Test(Pass|Nested)$"}, Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 4})
			}
			for _, deferred := range []bool{false, true} {
				t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
					for _, tc := range variantCases {
						t.Run(tc.Name, func(t *testing.T) {
							tc.Env = append(tc.Env, fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred))
							want, native := runParityCase(t, dir, binaries[0], tc)
							got, composed := runParityCase(t, dir, binaries[1], tc)
							assertParityCase(t, tc, want, got, native, composed)
						})
					}
				})
			}
		})
	}
}

// dd-go can already contain manual SDK hooks. They must not start a second CI
// runtime or disable Mini when it owns the test build.
func TestMiniOrchestrionLegacySDKShim(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference == "" {
		t.Skip("ORCHESTRION_BIN not configured")
	}
	dir, driver := prepareMiniFixture(t)
	configureReferenceFixture(t, dir)
	installComposedAPM(t, dir)
	original := filepath.Join(dir, "sample_test.go")
	// A dedicated client test module reproduces the old finalizer-only ABI.
	source := `package fixture_test
import (
 "os"
 "testing"
 _ "unsafe"
 _ "github.com/DataDog/dd-trace-go/v2/civisibility"
)
//go:linkname sdkInstrumentM github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingM
func sdkInstrumentM(*testing.M) func(int)
func TestMain(m *testing.M) {
 finalize:=sdkInstrumentM(m)
 code:=m.Run()
 finalize(code)
 os.Exit(code)
}
func TestPass(t *testing.T) {}
`
	if err := os.WriteFile(original, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, sdk := range []string{sdkVersion, "v2.11.0-rc.2"} {
		t.Run(sdk, func(t *testing.T) {
			if sdk != sdkVersion {
				out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-replace=github.com/DataDog/dd-trace-go/v2=github.com/DataDog/dd-trace-go/v2@"+sdk)
				if code != 0 {
					t.Fatal(out, stderr)
				}
			}
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "-toolexec="+quoteToolArgument(t, reference)+" toolexec", "-mod=mod", "-c", "-o", bin, ".")
			if code != 0 {
				t.Fatal(out, stderr)
			}
			assertComposedAPMDelivery(t, dir, bin)
			for _, enabled := range []string{"true", "parent"} {
				for _, deferred := range []bool{false, true} {
					tc := parityCase{Name: "SDK-manual-shim", Args: []string{"-test.run=^TestPass$"}, MinTests: 1, Env: []string{"DD_CIVISIBILITY_ENABLED=" + enabled, fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred)}}
					wire, result := runParityCase(t, dir, bin, tc)
					counts, err := countCIEvents(wire.events)
					if err != nil || result.code != 0 || counts.Sessions != 1 || counts.Modules != 1 || counts.Suites != 1 || counts.Tests != 1 {
						t.Fatalf("CI owners: %+v exit %d %v\n%s", counts, result.code, err, result.stderr)
					}
					if err = validateEventGraph(wire.events); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

// A real SDK tracer must still deliver APM over HTTP while Mini reports exactly
// one test graph. This is separate from the mocktracer weaving assertion.
func assertComposedAPMDelivery(t *testing.T, dir, bin string) {
	t.Helper()
	for _, agentless := range []bool{true, false} {
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("APM-HTTP-agentless=%t-deferred=%t", agentless, deferred), func(t *testing.T) { assertComposedAPMDeliveryMode(t, dir, bin, agentless, deferred) })
		}
	}
}

func assertComposedAPMDeliveryMode(t *testing.T, dir, bin string, agentless, deferred bool) {
	t.Helper()
	receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
	var mu sync.Mutex
	var apm []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0.4/traces" {
			data, err := decodedRequestBody(r)
			if err != nil {
				receiver.fail(err)
				w.WriteHeader(400)
				return
			}
			decoded, err := decodeMsgpack(data)
			if err != nil {
				receiver.fail(err)
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			apm = append(apm, decoded)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"rate_by_service":{}}`)
			return
		}
		receiver.handler(w, r)
	}))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", fmt.Sprintf("DD_CIVISIBILITY_AGENTLESS_ENABLED=%t", agentless), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "POC_APM_ADDR="+strings.TrimPrefix(server.URL, "http://"), "DD_TRACE_AGENT_PROTOCOL_VERSION=0.4")
	out, stderr, code := command(t, dir, env, bin, "-test.run=^TestComposedAPMHTTP$")
	server.Close() // Drain handlers before inspecting payloads and failure diagnostics.
	if code != 0 {
		t.Fatal(code, out, stderr)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.failures) > 0 {
		t.Fatal(receiver.failures)
	}
	counts, err := countCIEvents(receiver.events)
	if err != nil || counts.Sessions != 1 || counts.Modules != 1 || counts.Suites != 1 || counts.Tests != 1 {
		t.Fatal("CI ownership", counts, err, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	var spans []map[string]any
	for _, payload := range apm {
		for _, trace := range payload.([]any) {
			for _, span := range trace.([]any) {
				spans = append(spans, span.(map[string]any))
			}
		}
	}
	if len(spans) != 1 || spans[0]["name"] != "poc.orchestrion.application" {
		t.Fatalf("APM payload: %+v; requests: %+v; %s", spans, receiver.requests, stderr)
	}
}

// Check source and module files after all nested builds and failed commands.
func unchangedFixtureFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	originals := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = data
	}
	t.Cleanup(func() {
		for name, want := range originals {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("client file changed: %s (%v)", name, err)
			}
		}
	})
}

func TestMiniOrchestrionInlineAndFailures(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference == "" {
		t.Skip("ORCHESTRION_BIN not configured")
	}
	dir, driver := prepareMiniFixture(t)
	configureReferenceFixture(t, dir)
	unchangedFixtureFiles(t, dir, "go.mod", "go.sum", "orchestrion.yml", "orchestrion.tool.go", "sample_test.go")
	for _, fail := range []bool{false, true} {
		tc := parityCase{Name: "inline", Args: []string{"orchestrion", "go", "test", "-count=1", "-run=^TestPass$", "."}, MinTests: 1, Env: []string{"PATH=" + filepath.Dir(reference) + string(os.PathListSeparator) + os.Getenv("PATH")}}
		if fail {
			tc.Args = []string{"orchestrion", "go", "test", "-count=1", "-run=^TestFailures$", ".", "-args", "-mode=fail"}
			tc.WantExit = 1
			tc.MinTests = 7
		}
		receiver, result := runParityCase(t, dir, driver, tc)
		counts, err := countCIEvents(receiver.events)
		if err != nil || result.code != tc.WantExit || counts.Sessions != 1 || counts.Modules != 1 || counts.Tests != tc.MinTests {
			t.Fatalf("inline: %+v exit %d want %d %v\n%s", counts, result.code, tc.WantExit, err, result.stderr)
		}
		if err = validateEventGraph(receiver.events); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"orchestrion", "go", "test", "--runtime=invalid"}, {"orchestrion", "go", "build"}, {"go", "tool", "orchestrion", "go"}} {
		out, stderr, code := command(t, dir, testEnv(), driver, args...)
		if code != 2 || strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") || stderr == "" {
			t.Fatal(args, code, out, stderr)
		}
	}
	missing := filepath.Join(t.TempDir(), executableName("orchestrion"))
	out, stderr, code := command(t, dir, testEnv(), driver, "test", "-toolexec="+quoteToolArgument(t, missing)+" toolexec", "-c", "-o", filepath.Join(t.TempDir(), "fixture.test"), ".")
	// exec errors quote Windows paths with escaped backslashes. Accept the
	// same filename in either native diagnostic representation.
	if code != 2 || !(strings.Contains(stderr, missing) || strings.Contains(stderr, fmt.Sprintf("%q", missing))) {
		t.Fatal(code, out, stderr)
	}
}

func installComposedAPM(t *testing.T, dir string) {
	t.Helper()
	output, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-tool=github.com/DataDog/orchestrion")
	if code != 0 {
		t.Fatal(output, stderr)
	}
	for name, source := range map[string]string{
		"orchestrion.tool.go": "//go:build tools\n\npackage fixture\nimport (\n _ \"github.com/DataDog/orchestrion\"\n _ \"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer\"\n)\n",
		"composed_test.go":    composedAPMSource,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Use the client-pinned Go tool and SDK, independently of the newer reference
// binary used by the differential suite.
func TestMiniOrchestrionClientPinnedTool(t *testing.T) {
	if os.Getenv("ORCHESTRION_BIN") == "" {
		t.Skip("ORCHESTRION_BIN not configured")
	}
	dir, driver := prepareMiniFixture(t)
	goGet(t, dir, "github.com/DataDog/orchestrion@v1.6.1", "github.com/DataDog/dd-trace-go/v2@v2.11.0-rc.2", "golang.org/x/tools@"+orchestrionToolsVersion)
	installComposedAPM(t, dir)
	// An ordinary client only declares Orchestrion and the SDK. Mini is supplied
	// through a temporary modfile, including when -C changes the working directory.
	out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-droprequire=github.com/tonyredondo/dd-ci-testing-poc", "-dropreplace=github.com/tonyredondo/dd-ci-testing-poc")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	unchangedFixtureFiles(t, dir, "go.mod", "go.sum", "sample_test.go", "orchestrion.tool.go", "composed_test.go")
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code = command(t, filepath.Dir(dir), testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "go", "tool", "orchestrion", "go", "test", "-C", dir, "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	tc := parityCase{Name: "client-tool", Args: []string{"-test.run=^TestComposedAPM$"}, Env: []string{"POC_EXPECT_APM_WEAVING=true"}, MinTests: 1}
	receiver, result := runParityCase(t, dir, bin, tc)
	counts, err := countCIEvents(receiver.events)
	if err != nil || result.code != 0 || counts.Sessions != 1 || counts.Modules != 1 || counts.Tests != 1 {
		t.Fatal(counts, err, result.code, result.stderr)
	}
	assertComposedAPMDelivery(t, dir, bin)
}
