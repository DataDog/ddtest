package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A subtest that a helper in another package creates belongs to the module of
// the test that runs it, as in dd-trace-go #5401; its suite and source stay
// the helper's file. The module keys known tests, Early Flake Detection and
// test management, so the helper's package would match none of their data.
func TestMiniHelperSubtestKeepsTheTestModule(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "helper"), 0700); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":           "module example.com/helpersubtest\ngo 1.25\n",
		"helper/helper.go": "package helper\n\nimport \"testing\"\n\nfunc Run(t *testing.T) {\n\tt.Run(\"child\", func(t *testing.T) {})\n}\n",
		"uses_test.go":     "package helpersubtest\n\nimport (\n\t\"testing\"\n\n\t\"example.com/helpersubtest/helper\"\n)\n\nfunc TestUsesHelper(t *testing.T) { helper.Run(t) }\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	got := executeProfile(t, dir, bin, []string{"-test.v"}, true, false, "")
	if got.code != 0 {
		t.Fatalf("exit %d\n%s\n%s", got.code, got.out, got.stderr)
	}
	meta := map[string]map[string]any{}
	for _, event := range got.wireEvents {
		content, _ := event["content"].(map[string]any)
		tags, _ := content["meta"].(map[string]any)
		if event["type"] == "test" && tags != nil {
			meta[tags["test.name"].(string)] = tags
		}
	}
	parent, child := meta["TestUsesHelper"], meta["TestUsesHelper/child"]
	if parent == nil || child == nil {
		t.Fatalf("missing test events: %v\n%s", meta, got.stderr)
	}
	if parent["test.module"] != "example.com/helpersubtest" || child["test.module"] != parent["test.module"] {
		t.Fatalf("modules: parent %v, child %v", parent["test.module"], child["test.module"])
	}
	// Outside a Git checkout, the source file is absolute.
	if source, _ := child["test.source.file"].(string); child["test.suite"] != "helper.go" || !strings.HasSuffix(filepath.ToSlash(source), "/helper/helper.go") {
		t.Fatalf("child suite %v, source %v", child["test.suite"], child["test.source.file"])
	}
}
