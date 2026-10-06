package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
