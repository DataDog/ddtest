package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMiniCLIWithoutSDK(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-droprequire=github.com/DataDog/dd-trace-go/v2")
	if code != 0 {
		t.Fatalf("remove SDK: %s %s", out, stderr)
	}
	env := testEnv("DD_CIVISIBILITY_ENABLED=false", "GOFLAGS=-tags=poc_extra")
	for _, args := range [][]string{
		{"test", "--runtime=mini", "-count=1", "-json", "-run=^(TestPass|TestTagged|TestOther)$", "./..."},
		{"test", "--runtime=mini", "./notests"},
	} {
		out, stderr, code = command(t, dir, env, driver, args...)
		if code != 0 {
			t.Fatalf("mini without SDK: %d %s %s", code, out, stderr)
		}
	}
	for i := 0; i < 2; i++ {
		out, stderr, code = command(t, dir, env, driver, "test", "--runtime=mini", "-run=^TestPass$", ".")
		if code != 0 || i == 1 && !strings.Contains(out, "(cached)") {
			t.Fatalf("native result cache: %d %s %s", code, out, stderr)
		}
	}
	// The default runtime is Mini and must not link the SDK.
	before, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("default.test"))
	_, stderr, code = command(t, dir, env, driver, "test", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("Mini default failed: %d %s", code, stderr)
	}
	symbols, stderr, code := command(t, dir, testEnv(), "go", "tool", "nm", bin)
	if code != 0 || strings.Contains(symbols, "github.com/DataDog/dd-trace-go/v2/internal/civisibility") || !strings.Contains(symbols, "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer") {
		t.Fatalf("default did not select Mini: %d %s", code, stderr)
	}
	if after, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(after) != string(before) {
		t.Fatalf("default Mini build edited go.mod: %v", err)
	}
	// Explicit SDK selection must still provide the pinned SDK, including
	// when the option follows the package and the module does not require it.
	_, stderr, code = command(t, dir, env, driver, "test", "-c", "-o", bin, ".", "--runtime", "sdk")
	if code != 0 {
		t.Fatalf("SDK could not be provided: %d %s", code, stderr)
	}
	symbols, stderr, code = command(t, dir, testEnv(), "go", "tool", "nm", bin)
	if code != 0 || !strings.Contains(symbols, "github.com/DataDog/dd-trace-go/v2/internal/civisibility") || strings.Contains(symbols, "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer") {
		t.Fatalf("explicit SDK selection failed: %d %s", code, stderr)
	}
	if after, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(after) != string(before) {
		t.Fatalf("providing the SDK edited go.mod: %v", err)
	}
	_, stderr, code = command(t, dir, env, driver, "test", "--runtime=unknown", ".")
	if code != 2 || !strings.Contains(stderr, "runtime must be") {
		t.Fatalf("unknown backend accepted: %d %s", code, stderr)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, code = command(t, root, testEnv(), "go", "list", "-deps", "-f", "{{.ImportPath}}", "./testopt")
	if code != 0 || strings.Contains(out, "github.com/DataDog/") {
		t.Fatalf("SDK dependency leaked: %d %s %s", code, out, stderr)
	}
	out, stderr, code = command(t, root, testEnv(), "go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./cmd/ddtest")
	if code != 0 {
		t.Fatal(stderr)
	}
	for _, line := range strings.Fields(out) {
		if !strings.HasPrefix(line, "github.com/tonyredondo/dd-ci-testing-poc/") {
			t.Fatalf("driver external dependency: %s", line)
		}
	}
	t.Run("existing-overlay", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "sample.go")
		if err := os.WriteFile(file, []byte("package fixture\nfunc Add(a,b int)int{return a+b+1}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(dir, "sample.go"): file}})
		if err != nil {
			t.Fatal(err)
		}
		overlay := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(overlay, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		for _, fromEnv := range []bool{false, true} {
			env := testEnv("DD_CIVISIBILITY_ENABLED=false")
			args := []string{"test", "--runtime=mini", "-count=1", "-run=^TestPass$", "."}
			if fromEnv {
				env = append(env, "GOFLAGS=-overlay="+overlay)
			} else {
				args = append(args, "-overlay="+overlay)
			}
			out, stderr, code := command(t, dir, env, driver, args...)
			if code != 1 || !strings.Contains(out, "addition") {
				t.Fatalf("user overlay not applied: %d %s %s", code, out, stderr)
			}
		}
	})
}

func TestMiniPublicTestingContext(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("context";"testing";"github.com/tonyredondo/dd-ci-testing-poc/testopt";"github.com/tonyredondo/dd-ci-testing-poc/propagation")
func TestPortableContext(t *testing.T){
 ctx:=testopt.Context(t)
 parent,ok:=propagation.FromContext(ctx);if !ok{t.Fatal("missing test identity")}
 t.Cleanup(func(){if ctx.Err()!=context.Canceled{t.Error("native cancellation lost")}})
 for _,format:=range []propagation.Format{propagation.W3C,propagation.Datadog}{
  carrier:=propagation.MapCarrier{};if err:=propagation.Inject(parent,carrier,format);err!=nil{t.Fatal(err)}
  remote,err:=propagation.Extract(carrier,format);if err!=nil||remote.TraceID!=parent.TraceID||remote.SpanID!=parent.SpanID{t.Fatal("lost test identity")}
 }
 t.Run("child",func(t *testing.T){child,ok:=propagation.FromContext(testopt.Context(t));if !ok||child.SpanID==parent.SpanID{t.Fatal("child identity missing")}})
}`
	if err := os.WriteFile(filepath.Join(dir, "portable_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-race", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile public context: %d %s %s", code, out, stderr)
	}
	got := execute(t, dir, bin, []string{"-test.run=^TestPortableContext$"}, true, false)
	if got.code != 0 || len(got.events) != 5 || strings.Contains(got.stderr, "DATA RACE") {
		t.Fatalf("public test context: %d %v %s %s", got.code, got.events, got.out, got.stderr)
	}
}
