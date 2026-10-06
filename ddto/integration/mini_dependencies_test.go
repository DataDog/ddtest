package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMiniConsumerAddsOnlyOwnModule(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	client := t.TempDir()
	mod := []byte("module example.com/mini-consumer\n\ngo 1.26.0\n\nrequire github.com/tonyredondo/dd-ci-testing-poc v0.0.0\n\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + strconv.Quote(filepath.ToSlash(root)) + "\n")
	if err := os.WriteFile(filepath.Join(client, "go.mod"), mod, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("package main\nimport _ \"github.com/tonyredondo/dd-ci-testing-poc/testopt\"\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(client, "main.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	env := testEnv("GOWORK=off", "GOPROXY=off")
	if stdout, stderr, code := command(t, client, env, "go", "mod", "tidy"); code != 0 {
		t.Fatalf("client tidy failed: %s%s", stdout, stderr)
	}
	stdout, stderr, code := command(t, client, env, "go", "mod", "edit", "-json")
	if code != 0 {
		t.Fatalf("client go.mod failed: %s%s", stdout, stderr)
	}
	var parsed struct{ Require []struct{ Path string } }
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Require) != 1 || parsed.Require[0].Path != "github.com/tonyredondo/dd-ci-testing-poc" {
		t.Fatalf("consumer requires extra modules: %+v", parsed.Require)
	}
	// Go's minimal version selection applies this module's requirements to
	// consumers. Its only requirements are its own test dependencies, at old
	// versions, so a consumer keeps any newer version it already selects.
	stdout, stderr, code = command(t, root, testEnv(), "go", "mod", "edit", "-json")
	if code != 0 {
		t.Fatalf("module requirements: %s%s", stdout, stderr)
	}
	var own struct {
		Require []struct{ Path, Version string }
	}
	if err := json.Unmarshal([]byte(stdout), &own); err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{"example.com/mini-consumer": "", "github.com/tonyredondo/dd-ci-testing-poc": "v0.0.0"}
	for _, r := range own.Require {
		declared[r.Path] = r.Version
		if r.Path == "github.com/stretchr/testify" && !versionAtMost(r.Version, maxTestifyRequirement) {
			t.Fatalf("testify %s raises consumers' versions; keep it at %s or lower", r.Version, maxTestifyRequirement)
		}
	}
	stdout, stderr, code = command(t, client, env, "go", "list", "-m", "-f", "{{.Path}} {{.Version}}", "all")
	if code != 0 {
		t.Fatalf("client module graph failed: %s%s", stdout, stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		path, version, _ := strings.Cut(line, " ")
		if want, ok := declared[path]; !ok || want != version {
			t.Fatalf("consumer module graph gained %s %s", path, version)
		}
	}
	stdout, stderr, code = command(t, client, env, "go", "list", "-deps", "-json", ".")
	if code != 0 {
		t.Fatalf("client graph failed: %s%s", stdout, stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	for {
		var pkg struct {
			ImportPath string
			Standard   bool
			Module     *struct{ Path string }
		}
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if !pkg.Standard && pkg.Module != nil && pkg.Module.Path != "github.com/tonyredondo/dd-ci-testing-poc" && pkg.Module.Path != "example.com/mini-consumer" {
			t.Fatalf("external runtime package %s from %s", pkg.ImportPath, pkg.Module.Path)
		}
	}
}

// maxTestifyRequirement is the newest Testify this module may require: the
// oldest release that builds its tests and requires a yaml.v3 without
// CVE-2022-28948. Consumers with Testify v1.7.5 or newer keep their version.
const maxTestifyRequirement = "v1.7.5"

// versionAtMost compares release versions of the form vMAJOR.MINOR.PATCH.
func versionAtMost(version, limit string) bool {
	parse := func(v string) [3]int {
		var parts [3]int
		fmt.Sscanf(strings.TrimPrefix(v, "v"), "%d.%d.%d", &parts[0], &parts[1], &parts[2])
		return parts
	}
	a, b := parse(version), parse(limit)
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return true
}
