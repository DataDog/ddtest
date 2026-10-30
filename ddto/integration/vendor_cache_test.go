package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/version"
)

// A compile-only guard cannot validate a package recovered from Go's cache.
// Keep the API validation before the build, including patched vendors.
func TestTestifyAPIGuardWithWarmVendoredSources(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/vendor-cache-guard\n\ngo 1.26.0\nrequire (\ngithub.com/DataDog/ddtest/ddto v0.0.0\ngithub.com/stretchr/testify v1.11.1\n)\nreplace github.com/DataDog/ddtest/ddto => %s\nreplace github.com/stretchr/testify => github.com/stretchr/testify v1.11.1\n", filepath.ToSlash(root))
	source := `package guard
import (
 "testing"
 "github.com/stretchr/testify/suite"
 _ "github.com/DataDog/ddtest/ddto/testopt"
)
type ExampleSuite struct {suite.Suite}
func(s *ExampleSuite) TestPass() {s.True(true)}
func TestSuite(t *testing.T) {suite.Run(t, new(ExampleSuite))}
`
	for name, body := range map[string]string{"go.mod": mod, "suite_test.go": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}} {
		out, stderr, code := command(t, dir, testEnv(), "go", args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
	}
	args := []string{"test", "--runtime=mini", "-mod=vendor", "-x", "-c", "-o", filepath.Join(dir, executableName("fixture.test")), "."}
	for i := 0; i < 2; i++ {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
		if i == 1 && len(compilerTraceLines(stderr)) != 0 {
			t.Fatal("unchanged vendor sources did not reuse cache", stderr)
		}
	}
	// The code decides, not the version: a vendored Testify whose Run entry
	// changed builds without the integration, and the cached instrumented
	// object is not reused.
	path := filepath.Join(dir, "vendor", "github.com", "stretchr", "testify", "suite", "suite.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "func Run(t *testing.T, suite TestingSuite) {", "func Run(t *testing.T, suite TestingSuite, _ ...struct{}) {", 1)
	if edited == string(data) {
		t.Fatal("vendored Testify has no Run entry")
	}
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
	if code != 0 || !strings.Contains(stderr, version.BuildLogPrefix+" WARN: Testify v1.11.1 is not instrumented") || !strings.Contains(stderr, "unsupported library API") || ddtoToolexec(stderr) {
		t.Fatalf("changed vendored entry: exit=%d\n%s\n%s", code, out, stderr)
	}
}
