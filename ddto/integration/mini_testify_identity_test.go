package integration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Distinct suites with the same method name must retain the client's identity,
// including Go's duplicate suffixes, nested subtests and repeated executions.
func TestMiniTestifyDuplicateIdentity(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"duplicate_suites_test.go": `package fixture_test
import("testing";"github.com/stretchr/testify/suite")
type FirstSuite struct { suite.Suite }
type SecondSuite struct { suite.Suite }
func(s *FirstSuite) TestShared(){s.Run("nested",func(){s.Equal(1,1)})}
func(s *SecondSuite) TestShared(){s.Run("nested",func(){s.Equal(1,1)})}
func TestRepeatedSuites(t *testing.T){
 for i:=0;i<2;i++ {suite.Run(t,new(FirstSuite));suite.Run(t,new(SecondSuite))}
 t.Run("TestShared",func(t *testing.T){t.Log("ordinary sibling")})
}
`})
	bin := filepath.Join(t.TempDir(), executableName("identity.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-mod=mod", "-race", "-covermode=atomic", "-coverpkg=./...,github.com/stretchr/testify/suite", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
			got, exec := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestRepeatedSuites$", "-test.count=2"}, Env: []string{fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred)}})
			if exec.code != 0 {
				t.Fatal(exec.out, exec.stderr)
			}
			tests := 0
			for _, event := range got.events {
				if event["type"] != "test" {
					continue
				}
				tests++
				meta := event["content"].(map[string]any)["meta"].(map[string]any)
				name := fmt.Sprint(meta["test.name"])
				if !strings.HasPrefix(name, "TestRepeatedSuites/TestShared") {
					continue
				}
				if !strings.Contains(fmt.Sprint(meta["test.module"]), "example.com/dd-ci-testing-fixture") || !strings.HasSuffix(fmt.Sprint(meta["test.source.file"]), "duplicate_suites_test.go") {
					t.Errorf("foreign attribution: %v", meta)
				}
				want := "FirstSuite"
				if strings.Contains(name, "#01") || strings.Contains(name, "#03") {
					want = "SecondSuite"
				}
				if strings.Contains(name, "#04") {
					want = ""
				}
				actual := fmt.Sprint(meta["test.suite"])
				if want != "" && !strings.HasSuffix(actual, "/"+want) || want == "" && actual != "duplicate_suites_test.go" {
					t.Errorf("%s suite=%s want %s", name, actual, want)
				}
			}
			if tests != 20 {
				t.Fatalf("tests=%d want 20", tests)
			}
			if err := validateEventGraph(got.events); err != nil {
				t.Fatal(err)
			}
		})
	}
}
