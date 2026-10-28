package instrument

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
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
	if !strings.Contains(string(got), "if true { return EnabledModeDisabled, false }") || !strings.Contains(string(got), "os.Getenv") {
		t.Fatalf("missing gate or original body: %s", got)
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
	if strings.Count(string(got), "if true { return false }") != 2 || !strings.Contains(string(got), "TracingEnabled()bool{return true}") {
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
