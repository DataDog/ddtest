package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The code decides whether Testify is instrumented, not its version. v1.3.0
// predates the former v1.4.0 minimum but has the same Run entry and runs each
// method with t.Run, so its suite methods report with their suite.
func TestTestifyV130IsInstrumentedByItsCode(t *testing.T) {
	driver := sharedDriver(t, "..")
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/testify-v130\n\ngo 1.25.0\nrequire (\ngithub.com/DataDog/ddtest/ddto v0.0.0\ngithub.com/stretchr/testify v1.3.0\n)\nreplace github.com/DataDog/ddtest/ddto => %s\n", filepath.ToSlash(root))
	source := `package guard
import (
 "testing"
 "github.com/stretchr/testify/suite"
)
type ExampleSuite struct {suite.Suite}
func (s *ExampleSuite) TestPass() {s.True(true)}
func (s *ExampleSuite) TestSkip() {s.T().Skip("skipped on purpose")}
func TestSuite(t *testing.T) {suite.Run(t, new(ExampleSuite))}
`
	for name, body := range map[string]string{"go.mod": mod, "suite_test.go": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "tidy"); code != 0 {
		t.Fatal(out, stderr)
	}
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-o", bin, ".")
	if code != 0 || strings.Contains(stderr, "is not instrumented") {
		t.Fatalf("build: exit=%d\n%s\n%s", code, out, stderr)
	}
	receiver, run := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestSuite$"}})
	if run.code != 0 {
		t.Fatalf("run: exit=%d\n%s\n%s", run.code, run.out, run.stderr)
	}
	suites := map[string]string{}
	for _, event := range receiver.events {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] != "test" || meta == nil {
			continue
		}
		name, _ := meta["test.name"].(string)
		suite, _ := meta["test.suite"].(string)
		suites[name] = suite
	}
	for _, method := range []string{"TestSuite/TestPass", "TestSuite/TestSkip"} {
		if !strings.HasSuffix(suites[method], "/ExampleSuite") {
			t.Fatalf("%s is not reported with its Testify suite: %v", method, suites)
		}
	}
}
