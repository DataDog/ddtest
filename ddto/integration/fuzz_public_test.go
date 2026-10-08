package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMiniPublicFuzzExampleAPI(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := `package public
import("fmt";"os";"testing";"github.com/tonyredondo/dd-ci-testing-poc/testopt")
func TestMain(m *testing.M){os.Exit(testopt.RunM(m))}
func FuzzPublic(f *testing.F){f.Add("seed");testopt.GetFuzz(f).Fuzz(func(t *testing.T,s string){if s!="seed"{t.Fatal(s)}})}
func Public(){}
func ExamplePublic(){fmt.Println("public")
// Output: public
}
`
	for name, text := range map[string]string{
		"go.mod":         fmt.Sprintf("module example.com/mini-public-fuzz\n\ngo 1.25.0\nrequire github.com/tonyredondo/dd-ci-testing-poc v0.0.0\nreplace github.com/tonyredondo/dd-ci-testing-poc => %q\n", filepath.ToSlash(root)),
		"public_test.go": source,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, executableName("public.test"))
	args := []string{"test", "-mod=mod", "-c", "-o", bin}
	if os.Getenv("PARITY_TEST_MODE") == "race" {
		args = append(args, "-race")
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", args...)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	out, stderr, code = command(t, dir, testEnv(), "go", "list", "-mod=readonly", "-test", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	for _, dependency := range strings.Split(out, "\n") {
		dependency = strings.TrimSpace(dependency)
		if at := strings.Index(dependency, " ["); at >= 0 {
			dependency = dependency[:at]
		}
		if dependency == "" {
			continue
		}
		if !strings.HasPrefix(dependency, "github.com/tonyredondo/dd-ci-testing-poc/") && !strings.HasPrefix(dependency, "example.com/mini-public-fuzz") {
			t.Fatalf("external runtime dependency: %s", dependency)
		}
	}
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
			server := httptest.NewServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "DD_SERVICE=mini-public-fuzz", "XDG_CACHE_HOME="+t.TempDir(), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred))
			out, stderr, code := command(t, dir, env, bin, "-test.count=1")
			if code != 0 || strings.Contains(stderr, "DATA RACE") {
				t.Fatal(code, out, stderr)
			}
			counts, err := countCIEvents(receiver.events)
			if err != nil {
				t.Fatal(err)
			}
			if counts != (eventCounts{Sessions: 1, Modules: 1, Suites: 1, Tests: 3}) {
				t.Fatalf("manual public API counts=%+v", counts)
			}
			if err := validateEventGraph(receiver.events); err != nil {
				t.Fatal(err)
			}
		})
	}
}
