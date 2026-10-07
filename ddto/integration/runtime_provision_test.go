package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// go mod tidy removes the runtime requirement: nothing in the module imports it,
// because ddtest injects that import at build time. ddtest must still work, and
// must leave go.mod and go.sum exactly as they were.
func TestMiniRuntimeWithoutRequirementLeavesModuleUntouched(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	read := func() []byte {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		sum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
		return append(mod, sum...)
	}
	for _, edit := range []string{
		"-droprequire=github.com/tonyredondo/dd-ci-testing-poc", // As after go mod tidy.
		"-dropreplace=github.com/tonyredondo/dd-ci-testing-poc", // The checkout that built ddtest provides it.
	} {
		if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", edit); code != 0 {
			t.Fatal(out, stderr)
		}
		before := read()
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-count=1", "-run=^TestPass$", ".")
		if code != 0 {
			t.Fatalf("%s: exit=%d\n%s\n%s", edit, code, out, stderr)
		}
		if !bytes.Equal(before, read()) {
			t.Fatalf("%s: ddtest modified go.mod or go.sum", edit)
		}
	}
}

func TestMiniLocalProvisionAvoidsProxy(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	var requests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "module lookup should not be necessary", http.StatusForbidden)
	}))
	defer proxy.Close()
	for _, goVersion := range []string{"1.21.0", "1.25.0", "1.26.0"} {
		t.Run(goVersion, func(t *testing.T) {
			dir := t.TempDir()
			original := "module example.com/localmini\n\ngo " + goVersion + "\n"
			writeBuildFixture(t, dir, map[string]string{
				"go.mod":         original,
				"client_test.go": "package localmini\nimport \"testing\"\nfunc TestLocal(t *testing.T) {}\n",
			})
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOPROXY="+proxy.URL, "GONOPROXY=none"), driver, "test", "-mod=readonly", "-count=1", "-json")
			if code != 0 {
				t.Fatalf("exit=%d\n%s\n%s", code, out, stderr)
			}
			if requests.Load() != 0 || strings.Contains(stderr, "go get") {
				t.Fatalf("local runtime caused %d requests: %s", requests.Load(), stderr)
			}
			if contents, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(contents) != original {
				t.Fatalf("client go.mod changed: %s, %v", contents, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "go.sum")); !os.IsNotExist(err) {
				t.Fatalf("client go.sum was created: %v", err)
			}
		})
	}
}

func TestSDKProvisionProgressUsesStderr(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":         "module example.com/sdkprogress\n\ngo 1.26.0\n",
		"client_test.go": "package sdkprogress\nimport \"testing\"\nfunc TestSDK(t *testing.T) {}\n",
	})
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=sdk", "-count=1", "-json")
	if code != 0 {
		t.Fatalf("exit=%d\n%s\n%s", code, out, stderr)
	}
	if !strings.Contains(stderr, version.BuildLogPrefix+" INFO: preparing runtime with go get ") || !strings.Contains(stderr, "go: added github.com/DataDog/dd-trace-go/v2 ") {
		t.Fatalf("missing provisioning progress: %s", stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	var passed bool
	for {
		var event struct{ Action, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("progress corrupted go test JSON: %v\n%s", err, out)
		}
		passed = passed || event.Test == "TestSDK" && event.Action == "pass"
	}
	if !passed {
		t.Fatal("missing successful test event")
	}
}
