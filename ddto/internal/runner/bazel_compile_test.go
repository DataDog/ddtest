package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/instrument"
)

func TestBazelCanonicalPackage(t *testing.T) {
	for importmap, want := range map[string]string{
		"testing":                                   "testing",
		"github.com/stretchr/testify/suite":         "github.com/stretchr/testify/suite",
		"example.com/app/vendor/go.uber.org/goleak": "go.uber.org/goleak",
		"vendor/go.uber.org/goleak":                 "go.uber.org/goleak",
		"a/vendor/b/vendor/github.com/x/y":          "github.com/x/y",
		"example.com/vendorized/go.uber.org/goleak": "example.com/vendorized/go.uber.org/goleak",
		"example.com/app/other/github.com/x/y":      "example.com/app/other/github.com/x/y",
	} {
		if got := bazelCanonicalPackage(importmap); got != want {
			t.Errorf("%s: got %s, want %s", importmap, got, want)
		}
	}
}

func TestBazelEditsDispatch(t *testing.T) {
	dir := t.TempDir()
	compiler := filepath.Join(dir, "compile")
	for _, pkg := range []string{"testing", "os/signal", instrument.TestifySuiteImport, instrument.GoleakImport, sdkCIConfigPackage, sdkCIEnvironmentPackage, sdkCIBazelPackage, sdkTracerPackage} {
		if !bazelEdits(pkg, false, compiler) {
			t.Errorf("%s is not edited", pkg)
		}
	}
	for _, pkg := range []string{"runtime", "fmt", "example.com/app", miniPackage, sdkTracerPackage + "_test", "github.com/stretchr/testify/assert"} {
		if bazelEdits(pkg, false, compiler) {
			t.Errorf("%s is edited", pkg)
		}
	}
	if !bazelEdits(miniPackage, true, compiler) || bazelEdits("example.com/app", true, compiler) {
		t.Error("-race edits Mini's packages, and only them")
	}
	if err := os.WriteFile(filepath.Join(dir, bazelCompilerConfig), []byte(`{"contract":1,"panicHook":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !bazelEdits("runtime", false, compiler) {
		t.Error("the panic hook's configuration does not edit the runtime")
	}
}

func TestRunsAsBazelCompiler(t *testing.T) {
	dir := t.TempDir()
	compiler := filepath.Join(dir, "compile")
	if RunsAsBazelCompiler(compiler) {
		t.Fatal("a compile without compile.real runs as the instrumented compiler")
	}
	if err := os.WriteFile(filepath.Join(dir, "compile.real"), nil, 0700); err != nil {
		t.Fatal(err)
	}
	if !RunsAsBazelCompiler(compiler) || RunsAsBazelCompiler(filepath.Join(dir, "ddtest")) {
		t.Fatal("the instrumented compiler is named compile, next to compile.real")
	}
}

func TestBazelCompilerArguments(t *testing.T) {
	rulesGo := []string{"-p", "testing", "-importcfg", "cfg", "-race", "-pack", "--", "a.go", "b.go"}
	if compilerFlag(rulesGo, "-p") != "testing" || !compilerBoolFlag(rulesGo, "-race") || bazelSourceStart(rulesGo) != 7 {
		t.Fatal("rules_go's arguments")
	}
	cmdGo := []string{"-o", "x.a", "-p=os/signal", "-complete", "/goroot/src/os/signal/doc.go", "/goroot/src/os/signal/signal.go"}
	if compilerFlag(cmdGo, "-p") != "os/signal" || compilerBoolFlag(cmdGo, "-race") || bazelSourceStart(cmdGo) != 4 {
		t.Fatal("cmd/go's arguments")
	}
	if compilerFlag([]string{"--", "-p", "x.go"}, "-p") != "" || compilerBoolFlag([]string{"--", "-race"}, "-race") {
		t.Fatal("a source named like a flag")
	}
}

func TestAddStandardImports(t *testing.T) {
	dir := t.TempDir()
	root := filepath.ToSlash(filepath.Join(dir, "pkg", "linux_amd64"))
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "linux_amd64", "internal", "runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "linux_amd64", "internal", "runtime", "exithook.a"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "importcfg")
	if err := os.WriteFile(config, []byte("packagefile fmt="+root+"/fmt.a\npackagefile sync/atomic="+root+"/sync/atomic.a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"compile", "-importcfg", config, "-pack", "a.go"}
	hook := []byte("package testing\nimport (\n\t\"internal/runtime/exithook\"\n\t\"sync/atomic\"\n\t_ \"unsafe\"\n)\n")
	got, err := addStandardImports(args, [][]byte{hook}, t.TempDir())
	if err != nil || got[2] == config {
		t.Fatal(got, err)
	}
	data, err := os.ReadFile(got[2])
	if err != nil || !bytes.Contains(data, []byte("packagefile internal/runtime/exithook="+root+"/internal/runtime/exithook.a\n")) || bytes.Count(data, []byte("sync/atomic=")) != 1 {
		t.Fatalf("%s %v", data, err)
	}
	if got, err := addStandardImports(args, [][]byte{[]byte("package testing\nimport \"fmt\"\n")}, t.TempDir()); err != nil || got[2] != config {
		t.Fatal("an import that the configuration has changed it", got, err)
	}
	for name, content := range map[string]string{
		"a module": "package testing\nimport \"example.com/x\"\n",
		"missing":  "package testing\nimport \"internal/missing\"\n",
	} {
		if _, err := addStandardImports(args, [][]byte{[]byte(content)}, t.TempDir()); err == nil {
			t.Errorf("%s import accepted", name)
		}
	}
	if _, err := addStandardImports([]string{"compile", "a.go"}, [][]byte{hook}, dir); err == nil || !strings.Contains(err.Error(), "-importcfg") {
		t.Fatal("a compilation without -importcfg", err)
	}
}

func TestRunBazelRejectsBazelCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunBazel("ddtest instrument", []string{"test", "//..."}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "runs no Bazel command") {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, args := range [][]string{{}, {"--mini"}, {"--mini", "--goroot=x", "dir"}, {"--goroot=x", "dir"}, {"--compiler=x", "dir"}, {"--mini", "--panic-hook", "dir"}} {
		stderr.Reset()
		if code := RunBazel("ddtest instrument", args, &stdout, &stderr); code != 2 {
			t.Errorf("%q: exit %d: %s", args, code, stderr.String())
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := RunBazel("ddtest instrument", []string{"--mini", dir}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "not empty") {
		t.Fatalf("a directory that is not empty: exit %d: %s", code, stderr.String())
	}
}
