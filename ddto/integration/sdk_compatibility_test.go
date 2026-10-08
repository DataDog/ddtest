package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMiniUnsupportedSDKMirrorRetainsTestReporting(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod": "module example.com/sdkcompatibility\ngo 1.26.0\nrequire github.com/DataDog/dd-trace-go/v2 v2.10.1\n",
		"sdk_test.go": `package sdkcompatibility
import("testing";"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer")
func TestSDKCompatibility(t *testing.T){span,_:=tracer.StartSpanFromContext(t.Context(),"sdk.operation");span.Finish()}
`,
	})
	if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "tidy"); code != 0 {
		t.Fatal(out, stderr)
	}
	beforeMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	beforeSum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
	capture := new(miniWireCapture)
	server := httptest.NewServer(http.HandlerFunc(capture.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_API_KEY=fixture", "GOWORK=off")
	for i := 0; i < 2; i++ {
		out, stderr, code := command(t, dir, env, driver, "test", "-count=1", ".")
		if code != 0 || !strings.Contains(stderr, "SDK span copies disabled") {
			t.Fatalf("exit=%d\n%s%s", code, out, stderr)
		}
	}
	var tests, sessions int
	for _, payload := range capture.payloads {
		for _, row := range payload["events"].([]any) {
			event := row.(map[string]any)
			switch event["type"] {
			case "test":
				tests++
			case "test_session_end":
				sessions++
			case "span":
				t.Fatal("unsupported mirror still sent spans")
			}
		}
	}
	if tests != 2 || sessions != 2 {
		t.Fatalf("test reporting lost: tests=%d sessions=%d", tests, sessions)
	}
	if mod, _ := os.ReadFile(filepath.Join(dir, "go.mod")); string(mod) != string(beforeMod) {
		t.Fatal("client module changed")
	}
	if sum, _ := os.ReadFile(filepath.Join(dir, "go.sum")); string(sum) != string(beforeSum) {
		t.Fatal("client checksums changed")
	}
}
