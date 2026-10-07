package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// A compile-only guard cannot validate a package recovered from Go's cache.
// Keep selected-version validation before the build, including patched vendors.
func TestTestifyVersionGuardWithWarmVendoredSources(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/vendor-cache-guard\n\ngo 1.26.0\nrequire (\ngithub.com/tonyredondo/dd-ci-testing-poc v0.0.0\ngithub.com/stretchr/testify v1.11.1\n)\nreplace github.com/tonyredondo/dd-ci-testing-poc => %s\nreplace github.com/stretchr/testify => github.com/stretchr/testify v1.11.1\n", filepath.ToSlash(root))
	source := `package guard
import (
 "testing"
 "github.com/stretchr/testify/suite"
 _ "github.com/tonyredondo/dd-ci-testing-poc/testopt"
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
	// Consistent metadata with unchanged vendored sources is a supported Go
	// build input. Even if the API happens to fit, our minimum-version guard
	// applies: the suite builds without instrumentation and the cached
	// instrumented object is not reused.
	for _, name := range []string{"go.mod", "vendor/modules.txt"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.ReplaceAll(string(data), "=> github.com/stretchr/testify v1.11.1", "=> github.com/stretchr/testify v1.3.0")
		if edited == string(data) {
			t.Fatal("versioned replacement missing", name)
		}
		if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
	if code != 0 || !strings.Contains(stderr, version.BuildLogPrefix+" WARN: Testify v1.3.0 is not instrumented") || strings.Contains(stderr, "tool-overlay") {
		t.Fatalf("cached unsupported vendor: exit=%d\n%s\n%s", code, out, stderr)
	}
}
