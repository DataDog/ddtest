package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// A test-only helper can initialize SDK CI before testing.M.Run reaches Mini.
// Exercise dependency discovery through that helper, without Orchestrion.
func TestMiniLegacySDKShim(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	helper := filepath.Join(dir, "legacyshim")
	if err := os.Mkdir(helper, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"legacyshim/shim.go": `package legacyshim
import (
 "testing"
 _ "unsafe"
 _ "github.com/DataDog/dd-trace-go/v2/civisibility"
)
//go:linkname sdkInstrumentM github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingM
func sdkInstrumentM(*testing.M) func(int)
func Run(m *testing.M) int {
 finalize:=sdkInstrumentM(m)
 code:=m.Run()
 finalize(code)
 return code
}
`,
		"sample_test.go": `package fixture_test
import (
 "os"
 "testing"
 "example.com/dd-ci-testing-fixture/legacyshim"
)
func TestMain(m *testing.M) { os.Exit(legacyshim.Run(m)) }
func TestPass(t *testing.T) { t.Run("child",func(t *testing.T){}) }
func TestFail(t *testing.T) { t.Fatal("client failure") }
`,
		"manual_apm_test.go": `package fixture_test
import (
 "os"
 "testing"
 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)
func TestComposedAPMHTTP(t *testing.T) {
 if err:=tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")),tracer.WithLogStartup(false));err!=nil {t.Fatal(err)}
 defer tracer.Stop()
 span:=tracer.StartSpan("poc.orchestrion.application")
 span.Finish()
 tracer.Flush()
}
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, sdk := range []string{sdkVersion, "v2.11.0-rc.2"} {
		t.Run(sdk, func(t *testing.T) {
			if sdk != sdkVersion {
				out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-replace=github.com/DataDog/dd-trace-go/v2=github.com/DataDog/dd-trace-go/v2@"+sdk)
				if code != 0 {
					t.Fatal(out, stderr)
				}
			}
			// Native SDK builds on both sides of Mini must retain their original
			// CI behavior: the compiler-input rewrite cannot poison Go's cache.
			checkNativeSDK := func() {
				t.Helper()
				bin := filepath.Join(t.TempDir(), executableName("native.test"))
				out, stderr, code := command(t, dir, testEnv(), "go", "test", "-mod=mod", "-c", "-o", bin, ".")
				if code != 0 {
					t.Fatal(out, stderr)
				}
				wire, result := runParityCase(t, dir, bin, parityCase{Name: "native-SDK-shim", Args: []string{"-test.run=^TestPass$"}, MinTests: 1})
				counts, err := countCIEvents(wire.events)
				if err != nil || result.code != 0 || counts.Sessions != 1 || counts.Tests != 1 {
					t.Fatalf("native SDK changed: %+v exit %d %v", counts, result.code, err)
				}
				for _, payload := range wire.payloads {
					meta := payload["metadata"].(map[string]any)["*"].(map[string]any)
					if value, _ := meta["library_version"].(string); !strings.HasPrefix(value, "v2.") {
						t.Fatalf("native SDK was gated: %v", meta["library_version"])
					}
				}
			}
			checkNativeSDK()
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "-mod=mod", "-c", "-o", bin, ".")
			if code != 0 {
				t.Fatal(out, stderr)
			}
			for _, enabled := range []string{"parent", "true"} {
				for _, deferred := range []bool{false, true} {
					for _, failure := range []bool{false, true} {
						t.Run(fmt.Sprintf("enabled=%s/deferred=%t/failure=%t", enabled, deferred, failure), func(t *testing.T) {
							name, wantTests, wantExit := "TestPass", 2, 0
							if failure {
								name, wantTests, wantExit = "TestFail", 1, 1
							}
							tc := parityCase{Name: "SDK-manual-shim", Args: []string{"-test.run=^" + name + "$"}, MinTests: wantTests, WantExit: wantExit, Env: []string{"DD_CIVISIBILITY_ENABLED=" + enabled, fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred)}}
							wire, result := runParityCase(t, dir, bin, tc)
							counts, err := countCIEvents(wire.events)
							if err != nil || result.code != wantExit || counts.Sessions != 1 || counts.Modules != 1 || counts.Suites != 1 || counts.Tests != wantTests {
								t.Fatalf("CI owners: %+v exit %d %v\n%s", counts, result.code, err, result.stderr)
							}
							if err = validateEventGraph(wire.events); err != nil {
								t.Fatal(err)
							}
							for _, payload := range wire.payloads {
								meta := payload["metadata"].(map[string]any)["*"].(map[string]any)
								if meta["library_version"] != version.Number {
									t.Fatalf("unexpected CI runtime: %v", meta["library_version"])
								}
							}
						})
					}
				}
			}
			assertComposedAPMDelivery(t, dir, bin)
			checkNativeSDK()
		})
	}
}
