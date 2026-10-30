package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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
	edits := func(pkg string, race bool) bool {
		t.Helper()
		edits, err := bazelEdits(pkg, race, compiler)
		if err != nil {
			t.Fatal(pkg, err)
		}
		return edits
	}
	for _, pkg := range []string{"testing", "os/signal", instrument.TestifySuiteImport, instrument.GoleakImport, sdkCIConfigPackage, sdkCIEnvironmentPackage, sdkCIBazelPackage, sdkTracerPackage} {
		if !edits(pkg, false) {
			t.Errorf("%s is not edited", pkg)
		}
	}
	for _, pkg := range []string{"fmt", "example.com/app", miniPackage, sdkTracerPackage + "_test", "github.com/stretchr/testify/assert"} {
		if edits(pkg, false) {
			t.Errorf("%s is edited", pkg)
		}
	}
	if !edits(miniPackage, true) || edits("example.com/app", true) {
		t.Error("-race edits Mini's packages, and only them")
	}
	// The runtime follows the SDK's configuration, which must be readable.
	for _, config := range []string{"", `{"contract":1`, `{"contract":1,"panicHook":false}`, `{"contract":1,"panicHook":true}`} {
		if config != "" {
			if err := os.WriteFile(filepath.Join(dir, bazelCompilerConfig), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
		}
		runtime, err := bazelEdits("runtime", false, compiler)
		if (err == nil) != strings.HasSuffix(config, "}") || runtime != strings.Contains(config, `"panicHook":true`) {
			t.Errorf("%q: edits the runtime %v, error %v", config, runtime, err)
		}
	}
}

// Edited copies keep the names of the files that rules_go gives relative to
// its execution root, so that -trimpath rewrites them as the originals.
func TestBazelEditedCopiesNameTheOriginals(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join("external", "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("external", "pkg", "a.go"), []byte("package pkg\n\nfunc A() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("importcfg", []byte("packagefile fmt=/sdk/pkg/fmt.a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &bazelCompile{argv: []string{"compile", "-p", miniPackage, "-importcfg", "importcfg", "--", filepath.Join("external", "pkg", "a.go")}, start: 6, temp: t.TempDir(), replaced: map[int][]byte{}, added: map[string]string{}}
	// The transforms write //line directives with these names.
	files, _, err := c.sources()
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if name != filepath.Join(root, "external", "pkg", "a.go") {
			t.Fatalf("source name %s", name)
		}
	}
	if err := c.norace(); err != nil {
		t.Fatal(err)
	}
	args, err := c.finish()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args[6])
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "external", "pkg", "a.go")
	if args[6] == c.argv[6] || !strings.HasPrefix(string(data), "//line "+original+":1\npackage pkg\n") || !strings.Contains(string(data), "//go:norace\n/*line :3:1*/func A") {
		t.Fatalf("%s:\n%s", args[6], data)
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

// A compiler killed by a signal, as the kernel does when memory runs out,
// says so and ends the edited compilation with 128+N.
func TestBazelCompilerKilledBySignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no signals")
	}
	real := filepath.Join(t.TempDir(), "compile.real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\nkill -9 $$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := runBazelCompiler(real, []string{"compile"}, &stderr); code != 128+9 || !strings.Contains(stderr.String(), "killed") {
		t.Fatalf("exit %d, %q", code, stderr.String())
	}
}
