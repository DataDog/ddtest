package goenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSaveDistinguishesEmptyAndUnset(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	if got := Save("GOFLAGS"); got != SavedPrefix+"GOFLAGS==" {
		t.Fatalf("empty value: %q", got)
	}
	os.Unsetenv("GOFLAGS")
	if got := Save("GOFLAGS"); got != SavedPrefix+"GOFLAGS=" {
		t.Fatalf("unset value: %q", got)
	}
}

func TestRestoreAppliesAndRemovesSavedValues(t *testing.T) {
	t.Setenv("GOWORK", "/tmp/ddtest/go.work")
	t.Setenv("GOFLAGS", "'-mod=readonly'")
	t.Setenv(SavedPrefix+"GOWORK", "")
	t.Setenv(SavedPrefix+"GOFLAGS", "=-tags=a b")
	Restore()
	if value, ok := os.LookupEnv("GOWORK"); ok {
		t.Fatalf("unset GOWORK restored as %q", value)
	}
	if value := os.Getenv("GOFLAGS"); value != "-tags=a b" {
		t.Fatalf("GOFLAGS=%q", value)
	}
	for _, name := range Settings {
		if _, ok := os.LookupEnv(SavedPrefix + name); ok {
			t.Fatalf("saved %s remains visible", name)
		}
	}
}

// Initialization order is the contract: anything that imports os, directly or
// through os/exec, initializes after the restore package.
func TestRestoreImportsNoPackageThatCanStartCommands(t *testing.T) {
	for _, pkg := range []string{".", "./restore"} {
		out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		deps := strings.Fields(string(out))
		if slices.Contains(deps, "os") {
			t.Fatalf("%s depends on os: %v", pkg, deps)
		}
		if self := deps[len(deps)-1]; self >= "os" {
			t.Fatalf("%s must sort before os", self)
		}
	}
}

// The CLI's build-tool helpers import goenv to save values. The tools they run
// belong to the build, so importing goenv must keep ddtest's settings.
func TestImportingGoenvKeepsBuildSettings(t *testing.T) {
	if os.Getenv("DDTEST_GOENV_HELPER") != "" {
		fmt.Printf("GOWORK=%s\n", os.Getenv("GOWORK"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestImportingGoenvKeepsBuildSettings$")
	cmd.Env = append(os.Environ(), "DDTEST_GOENV_HELPER=1", "GOWORK=build.work", SavedPrefix+"GOWORK==caller.work")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "GOWORK=build.work\n") {
		t.Fatalf("importing goenv changed the build settings: %s", out)
	}
}

// A dependency that sorts first and reads GOWORK while it initializes still
// sees the caller's value when the binary includes Mini.
func TestRestoresBeforeEarlierDependencies(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	app, dep := filepath.Join(dir, "app"), filepath.Join(dir, "dep")
	for path, data := range map[string]string{
		filepath.Join(dep, "go.mod"):  "module a.example/dep\ngo 1.21\n",
		filepath.Join(dep, "dep.go"):  "package dep\nimport \"os\"\nvar Seen = os.Getenv(\"GOWORK\")\n",
		filepath.Join(app, "go.mod"):  "module example.com/app\ngo 1.25.0\nrequire (\n a.example/dep v0.0.0\n github.com/tonyredondo/dd-ci-testing-poc v0.0.0\n)\nreplace a.example/dep => ../dep\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + strconv.Quote(filepath.ToSlash(root)) + "\n",
		filepath.Join(app, "main.go"): "package main\nimport (\n \"fmt\"\n \"a.example/dep\"\n _ \"github.com/tonyredondo/dd-ci-testing-poc/testopt\"\n)\nfunc main() { fmt.Printf(\"%q\", dep.Seen) }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(dir, "app.exe")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	build.Dir = app
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOPROXY=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	run := exec.Command(binary)
	run.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), SavedPrefix+"GOWORK==caller.work")
	out, err := run.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"caller.work"` {
		t.Fatalf("dependency saw GOWORK=%s", out)
	}
}
