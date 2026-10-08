package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This consumer uses the selected toolchain, rather than the newer toolchain
// required by the frozen full SDK reference. It tests the actual compiler and
// coverage hooks together with Testify, goleak and both delivery modes.
func TestMiniToolchainFeatures(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":   "module example.com/toolchain\ngo 1.25.0\nrequire (\ngithub.com/stretchr/testify v1.10.0\ngo.uber.org/goleak v1.3.0\ngithub.com/kr/text v0.2.0\n)\n",
		"value.go": "package toolchain\nfunc Value()int{return 42}\n",
		"value_test.go": `package toolchain
import("fmt";"runtime";"testing";"github.com/stretchr/testify/suite";"go.uber.org/goleak")
type Values struct{suite.Suite}
func (s *Values) TestValue(){s.Equal(42,Value());goleak.VerifyNone(s.T())}
func TestValues(t *testing.T){fmt.Println("TOOLCHAIN="+runtime.Version());suite.Run(t,new(Values))}
func FuzzValue(f *testing.F){f.Add(42);f.Fuzz(func(t *testing.T,v int){if Value()!=42{t.Fatal(v)};goleak.VerifyNone(t)})}
func ExampleValue(){fmt.Println(Value())
// Output: 42
}
`,
	})
	// YAML dependency tests import kr/text without a module requirement. Pin
	// that existing fixture input so tidy works with an offline module cache.
	if out, stderr, code := command(t, dir, testEnv("GOTOOLCHAIN=local", "GOWORK=off"), "go", "mod", "tidy"); code != 0 {
		t.Fatal(out, stderr)
	}
	mod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	sum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
	for _, covered := range []bool{false, true} {
		bin := filepath.Join(t.TempDir(), executableName("features.test"))
		args := []string{"test", "-c", "-o", bin, "-mod=readonly"}
		if covered {
			args = append(args, "-coverpkg=./...,testing,github.com/stretchr/testify/suite,go.uber.org/goleak", "-covermode=atomic")
		}
		if os.Getenv("PARITY_TEST_MODE") == "race" {
			args = append(args, "-race")
		}
		out, stderr, code := command(t, dir, testEnv("GOTOOLCHAIN=local", "GOWORK=off", "DD_CIVISIBILITY_ENABLED=false"), driver, args...)
		if code != 0 {
			t.Fatalf("compile covered=%t: %s%s", covered, out, stderr)
		}
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("covered=%t/deferred=%t", covered, deferred), func(t *testing.T) {
				receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
				server := httptest.NewServer(http.HandlerFunc(receiver.handler))
				defer server.Close()
				env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "XDG_CACHE_HOME="+t.TempDir(), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), "GOTOOLCHAIN=local")
				out, stderr, code := command(t, dir, env, bin, "-test.count=1", "-test.v")
				if code != 0 || strings.Contains(stderr, "DATA RACE") || !strings.Contains(out, "TOOLCHAIN="+runtime.Version()) {
					t.Fatalf("selected toolchain/lifecycle failed: %d\n%s\n%s", code, out, stderr)
				}
				counts, err := countCIEvents(receiver.events)
				if err != nil {
					t.Fatal(err)
				}
				if counts.Sessions != 1 || counts.Tests != 5 {
					t.Fatalf("missing suite, seed or example events: %+v", counts)
				}
				method := false
				for _, event := range receiver.events {
					meta, _ := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] == "TestValues/TestValue" {
						method = true
						if !strings.HasSuffix(fmt.Sprint(meta["test.suite"]), "/Values") {
							t.Fatalf("Testify method lost its suite identity: %v", meta)
						}
					}
				}
				if !method {
					t.Fatal("Testify method event missing")
				}
				if err := validateEventGraph(receiver.events); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	gotMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	gotSum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
	if string(gotMod) != string(mod) || string(gotSum) != string(sum) {
		t.Fatal("instrumentation changed the consumer module")
	}
}

// The fixture itself asserts failures, cleanup, retries, corpus handling and
// event identities. This suite can run on the minimum without the full SDK.
func TestMiniFuzzExampleLifecycle(t *testing.T) {
	// The shared fixture calls its build-hook mode "orchestrion". With Mini,
	// that mode compiles through ddtest; it does not invoke Orchestrion.
	for _, mode := range []string{"manual", "orchestrion"} {
		fixture := prepareFuzzExampleFixture(t, "mini", mode)
		for _, scenario := range fuzzExampleScenarios {
			for _, deferred := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/deferred=%t", mode, scenario, deferred), func(t *testing.T) {
					runFuzzExampleScenario(t, fixture, scenario, deferred)
				})
			}
		}
	}
}
