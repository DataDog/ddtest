package instrument

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSDKCIEnvironmentGate(t *testing.T) {
	source := []byte("//line /client/envconfig.go:1:1\npackage envconfig\nimport \"os\"\ntype EnabledMode int\nconst EnabledModeDisabled EnabledMode=0\nfunc FromEnv()(EnabledMode,bool){ _=os.Getenv(\"DD_CIVISIBILITY_ENABLED\");return 1,true }\n")
	before := bytes.Clone(source)
	got, changed, err := TransformSDKCIEnvironment("covered.go", source)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !bytes.Equal(source, before) {
		t.Fatal("original source mutated")
	}
	if !strings.Contains(string(got), "if __ddtoMiniLinked { return EnabledModeDisabled, false };") || !strings.Contains(string(got), "os.Getenv") {
		t.Fatalf("missing gate or original body: %s", got)
	}
	if bytes.Count(got, []byte("\n")) != bytes.Count(source, []byte("\n")) {
		t.Fatalf("the gate moved later lines: %s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "rewritten.go", got, 0); err != nil {
		t.Fatal(err)
	}
}

func TestSDKCIEnvironmentRejectsAPIDrift(t *testing.T) {
	for _, body := range []string{
		"func FromEnv() bool {return true}",
		"func FromEnv(s string)(EnabledMode,bool){return 0,false}",
		"func FromEnv()(int,bool){return 0,false}",
		"func FromEnv()(EnabledMode,string){return 0,\"\"}",
		"func (x X) FromEnv()(EnabledMode,bool){return 0,false}",
		"func FromEnv()(EnabledMode,bool){return 0,false};func FromEnv()(EnabledMode,bool){return 0,false}",
	} {
		if _, _, err := TransformSDKCIEnvironment("env.go", []byte("package envconfig\n"+body)); !errors.Is(err, ErrUnsupportedAPI) {
			t.Fatalf("%s: %v", body, err)
		}
	}
	source := []byte("package envconfig\nfunc Other(){}")
	got, changed, err := TransformSDKCIEnvironment("other.go", source)
	if err != nil || changed || !bytes.Equal(got, source) {
		t.Fatal(changed, err)
	}
	if _, _, err := TransformSDKCIEnvironment("invalid.go", []byte("package")); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestSDKCIConfigGate(t *testing.T) {
	source := []byte("package config\ntype Config struct{ciVisibilityEnabled,ciVisibilityAgentless bool}\nfunc(c *Config)CIVisibilityEnabled()bool{return c.ciVisibilityEnabled}\nfunc(c *Config)CIVisibilityAgentlessActive()bool{return c.ciVisibilityEnabled && c.ciVisibilityAgentless}\nfunc(c *Config)TracingEnabled()bool{return true}\n")
	got, changed, err := TransformSDKCIConfig("config.go", source)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if strings.Count(string(got), "if __ddtoMiniLinked { return false };") != 2 || !strings.Contains(string(got), "TracingEnabled()bool{return true}") {
		t.Fatalf("%s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "config.go", got, 0); err != nil {
		t.Fatal(err)
	}
	missing := []byte("package config\nfunc(c *Config)CIVisibilityEnabled()bool{return true}")
	if _, _, err := TransformSDKCIConfig("config.go", missing); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatal(err)
	}
	wrong := bytes.Replace(source, []byte("CIVisibilityEnabled()bool"), []byte("CIVisibilityEnabled()string"), 1)
	if _, _, err := TransformSDKCIConfig("config.go", wrong); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatal(err)
	}
}

func TestSDKBazelModeGate(t *testing.T) {
	source := []byte("package bazel\ntype Mode struct{ PayloadFilesEnabled bool }\nfunc CurrentMode() Mode {\n\treturn Mode{PayloadFilesEnabled: true}\n}\nfunc IsPayloadFilesModeEnabled() bool { return CurrentMode().PayloadFilesEnabled }\n")
	got, changed, err := TransformSDKBazelMode("mode.go", source)
	if err != nil || !changed || !strings.Contains(string(got), "func CurrentMode() Mode {if __ddtoMiniLinked { return Mode{} };") {
		t.Fatalf("changed=%t err=%v\n%s", changed, err, got)
	}
	for _, drift := range []string{"func CurrentMode() *Mode {return nil}", "func CurrentMode(force bool) Mode {return Mode{}}", "func (m Mode) CurrentMode() Mode {return m}"} {
		if _, _, err := TransformSDKBazelMode("mode.go", []byte("package bazel\ntype Mode struct{}\n"+drift)); !errors.Is(err, ErrUnsupportedAPI) {
			t.Fatalf("%s: %v", drift, err)
		}
	}
	if _, _, err := TransformSDKBazelMode("mode.go", append(bytes.Clone(source), "var __ddtoMiniLinked = 1\n"...)); err == nil {
		t.Fatal("reserved guard name accepted")
	}
}

func TestSDKGuardFile(t *testing.T) {
	got, err := SDKGuardFile("/cache/dd-trace-go/v2@v2.11.2/internal/config/config.go", []byte("// Doc\npackage config\nfunc x(){}"))
	if err != nil {
		t.Fatal(err)
	}
	want := "//line /cache/dd-trace-go/v2@v2.11.2/internal/config/" + SDKGuardFileName + ":1\npackage config\n"
	if !strings.HasPrefix(got, want) || !strings.Contains(got, "//go:linkname __ddtoMiniLinked "+SDKMiniLinkedSymbol+"\nvar __ddtoMiniLinked bool\n") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, "= true") {
		t.Fatal("a value here would duplicate Mini's definition")
	}
	if _, err := SDKGuardFile("bad.go", []byte("not go")); err == nil {
		t.Fatal("invalid source accepted")
	}
}

// The guards act only in a binary that links Mini. A stand-in SDK, rewritten
// as the compiles rewrite it, keeps CI Visibility and payload files when the
// binary does not link Mini, as a Bazel tool built with the instrumented SDK
// does, and turns them off when it links Mini's testopt. The stand-in's import
// paths sort before Mini's, so it initializes first, as dd-trace-go does.
func TestSDKCIGuardsActOnlyWithMini(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	ddto, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/guards\n\ngo 1.25.0\n\nrequire github.com/DataDog/ddtest/ddto v0.0.0\n\nreplace github.com/DataDog/ddtest/ddto => " + strconv.Quote(filepath.ToSlash(ddto)) + "\n",
		"report/line.go": "package report\nimport (\"fmt\";\"example.com/guards/bazel\";\"example.com/guards/config\";\"example.com/guards/envconfig\")\nvar AtInit = Line()\nfunc Line() string { c := &config.Config{}; _, env := envconfig.FromEnv(); return fmt.Sprint(c.CIVisibilityEnabled(), c.CIVisibilityAgentlessActive(), env, bazel.CurrentMode().PayloadFilesEnabled) }\n",
		"plain/main.go":  "package main\nimport (\"fmt\";\"example.com/guards/report\")\nfunc main(){ fmt.Println(report.AtInit, report.Line()) }\n",
		"mini/main.go":   "package main\nimport (\"fmt\";\"example.com/guards/report\";_ \"github.com/DataDog/ddtest/ddto/testopt\")\nfunc main(){ fmt.Println(report.AtInit, report.Line()) }\n",
	}
	guarded := map[string]struct {
		source    string
		transform func(string, []byte) ([]byte, bool, error)
	}{
		"config/config.go":     {"package config\nimport \"os\"\ntype Config struct{}\nfunc (c *Config) CIVisibilityEnabled() bool { return os.Getenv(\"DD_CIVISIBILITY_ENABLED\") == \"true\" }\nfunc (c *Config) CIVisibilityAgentlessActive() bool { return c.CIVisibilityEnabled() }\n", TransformSDKCIConfig},
		"envconfig/enabled.go": {"package envconfig\nimport \"os\"\ntype EnabledMode int\nconst EnabledModeDisabled EnabledMode = 0\nfunc FromEnv() (EnabledMode, bool) { return 1, os.Getenv(\"DD_CIVISIBILITY_ENABLED\") == \"true\" }\n", TransformSDKCIEnvironment},
		"bazel/mode.go":        {"package bazel\nimport \"os\"\ntype Mode struct{ PayloadFilesEnabled bool }\nfunc CurrentMode() Mode { return Mode{PayloadFilesEnabled: os.Getenv(\"DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES\") == \"true\"} }\n", TransformSDKBazelMode},
	}
	for name, g := range guarded {
		rewritten, changed, err := g.transform(name, []byte(g.source))
		if err != nil || !changed {
			t.Fatal(name, changed, err)
		}
		guard, err := SDKGuardFile(name, []byte(g.source))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(rewritten)
		files[filepath.ToSlash(filepath.Join(filepath.Dir(name), SDKGuardFileName))] = guard
	}
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for program, want := range map[string]string{"./plain": "true true true true true true true true", "./mini": "false false false false false false false false"} {
		cmd := exec.Command("go", "run", program)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "DD_CIVISIBILITY_ENABLED=true", "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("%s: want %q, got %q (%v)", program, want, out, err)
		}
	}
}
