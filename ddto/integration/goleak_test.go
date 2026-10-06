package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Goleak belongs to this temporary consumer, never to Mini's runtime graph.
// Its module-cache sources exercise the selective compiler entry, not an overlay.
func TestMiniGoleakIntegration(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import (
 "context"
 "io"
 "net/http"
 "os"
 "testing"
 "go.uber.org/goleak"
 "github.com/tonyredondo/dd-ci-testing-poc/testopt"
 "example.com/leakhelper"
)
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
func TestClean(t *testing.T) { goleak.VerifyNone(t) }
func TestIgnoreCurrent(t *testing.T) { goleak.VerifyNone(t, goleak.IgnoreCurrent()) }
func TestCallerOptions(t *testing.T) {
 options:=make([]goleak.Option,1,10)
 options[0]=goleak.IgnoreAnyFunction("example.com/nonexistent")
 if err:=goleak.Find(options...);err!=nil {t.Fatal(err)}
 if options[:cap(options)][1]!=nil {t.Fatal("caller option slice changed")}
}
func TestExternalHelper(t *testing.T) { leakhelper.Check(t) }
func TestInvalidOptions(t *testing.T) {
 if goleak.Find(goleak.Cleanup(func(int){})) == nil { t.Fatal("lost invalid option error") }
 goleak.VerifyNone(t)
}
func TestAfterDelivery(t *testing.T) {
 c, err := testopt.New(testopt.Config{MaxEvents:1, Transport:testopt.TransportConfig{Endpoint:os.Getenv("DD_TRACE_AGENT_URL")+"/evp_proxy/v2/api/v2/citestcycle"}})
 if err != nil {t.Fatal(err)}
 defer c.Close(context.Background())
 for range 4 { s,_:=c.StartSpan(context.Background(),"manual");s.Finish() }
 goleak.VerifyNone(t)
}
func TestUserLeak(t *testing.T) {
 ch:=make(chan struct{})
 t.Cleanup(func(){close(ch)})
 go func(){<-ch}()
 goleak.VerifyNone(t)
}
func TestUserHTTPLeak(t *testing.T) {
 response,err:=http.Get(os.Getenv("DD_TRACE_AGENT_URL")+"/info")
 if err!=nil {t.Fatal(err)}
 io.Copy(io.Discard,response.Body);response.Body.Close()
 t.Cleanup(http.DefaultClient.CloseIdleConnections)
 goleak.VerifyNone(t)
}
func TestParallelA(t *testing.T) {t.Parallel()}
func TestParallelB(t *testing.T) {t.Parallel()}
`
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	helper := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":    "module example.com/leakhelper\n\ngo 1.26.0\nrequire go.uber.org/goleak v1.3.0\n",
		"helper.go": "package leakhelper\nimport \"go.uber.org/goleak\"\nfunc Check(t goleak.TestingT) {goleak.VerifyNone(t)}\n",
	} {
		if err := os.WriteFile(filepath.Join(helper, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"mod", "edit", "-require=go.uber.org/goleak@v1.3.0", "-require=example.com/leakhelper@v0.0.0", "-replace=example.com/leakhelper=" + helper},
	} {
		out, stderr, code := command(t, dir, testEnv(), "go", args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
	}
	bin := filepath.Join(t.TempDir(), executableName("goleak.test"))
	// The combination also proves that generated covered Find inputs are rewritten.
	flags := []string{"-mod=mod", "-race", "-cover", "-covermode=atomic", "-coverpkg=./...,go.uber.org/goleak"}
	args := append([]string{"test", "--runtime=mini", "-x", "-c", "-o", bin}, flags...)
	args = append(args, ".")
	for i := range 2 {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
		if code != 0 {
			t.Fatalf("compile %d: %s %s", i, out, stderr)
		}
		if i == 1 && len(compilerTraceLines(stderr)) != 0 {
			t.Fatal("unchanged goleak build did not reuse Go's cache", stderr)
		}
	}
	for _, deferred := range []string{"false", "true"} {
		t.Run("deferred="+deferred, func(t *testing.T) {
			for _, tc := range []struct {
				run, marker string
				want        int
			}{
				{"Test(Clean|IgnoreCurrent|CallerOptions|ExternalHelper|InvalidOptions|AfterDelivery|ParallelA|ParallelB)", "", 0},
				{"TestUserLeak", "TestUserLeak.func", 1},
				{"TestUserHTTPLeak", "persistConn", 1},
			} {
				receiver := &parityReceiver{policy: policySettings{Coverage: true}, side: map[string][][]byte{}, requests: map[string]int{}}
				server := httptest.NewServer(http.HandlerFunc(receiver.handler))
				env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=true", "DD_CIVISIBILITY_DEFERRED_DELIVERY="+deferred)
				out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.run=^"+tc.run+"$", "-test.timeout=20s")
				server.Close()
				if code != tc.want || tc.marker != "" && !strings.Contains(out+stderr, tc.marker) {
					t.Fatalf("%s: exit %d, want %d\n%s\n%s", tc.run, code, tc.want, out, stderr)
				}
			}
		})
	}
}
