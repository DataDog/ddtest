package runner

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeExclusionFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExclusionRegistrationsKeepPackagesAndDeclarationsSeparate(t *testing.T) {
	dir := t.TempDir()
	writeExclusionFixture(t, dir, map[string]string{
		"suite.go":         "package fixture\ntype Suite struct{}\ntype Alias = Pointer\ntype Pointer = *Suite\n//ddtest:no_auto_retry\nfunc (s (Alias)) TestAliased() {}\n",
		"internal_test.go": "package fixture\nimport \"testing\"\n//ddtest:no_auto_retry\nfunc TestInternal(t *testing.T) {}\n//ddtest:prevent_failure_suppression\nfunc (s (*Suite)) TestMethod() {}\n//ddtest:no_auto_retry\nfunc (s (Suite)) TestValue() {}\nfunc TestPlain(t *testing.T) {}\n",
		"external_test.go": "package fixture_test\nimport \"testing\"\n//ddtest:no_auto_retry\n//ddtest:prevent_failure_suppression\nfunc TestExternal(t *testing.T) {}\n",
	})
	p := goPackage{Dir: dir, Name: "fixture", ImportPath: "example.com/fixture", GoFiles: []string{"suite.go"}, TestGoFiles: []string{"internal_test.go"}, XTestGoFiles: []string{"external_test.go"}}
	got, warnings, err := exclusionRegistrationSources(p, nil, miniPackage, false)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	want := map[string][]string{
		"fixture": {
			"RegisterTestExclusions(Alias.TestAliased, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: false, NoAutoRetry: true})",
			"RegisterTestExclusions(TestInternal, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: false, NoAutoRetry: true})",
			"RegisterTestExclusions((*Suite).TestMethod, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: true, NoAutoRetry: false})",
			"RegisterTestExclusions(Suite.TestValue, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: false, NoAutoRetry: true})",
		},
		"fixture_test": {
			"RegisterTestExclusions(TestExternal, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: true, NoAutoRetry: true})",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("registration packages: %v", got)
	}
	for pkg, calls := range want {
		src := got[pkg]
		if !strings.HasPrefix(src, "package "+pkg+"\n") || strings.Count(src, "RegisterTestExclusions(") != len(calls) {
			t.Fatalf("%s registrations:\n%s", pkg, src)
		}
		for _, call := range calls {
			if !strings.Contains(src, call) {
				t.Fatalf("%s lacks %s:\n%s", pkg, call, src)
			}
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", src, 0); err != nil {
			t.Fatal(err)
		}
	}

	// A package that the runtime imports cannot register in its own files.
	got, warnings, err = exclusionRegistrationSources(p, nil, miniPackage, true)
	if err != nil || len(got) != 1 || got["fixture_test"] == "" {
		t.Fatalf("runtime-imported registrations: %v %v", got, err)
	}
	if want := []string{"example.com/fixture: ddtest annotations ignored: ddtest's runtime imports this package, so only its external test package can register them"}; !slices.Equal(warnings, want) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}

	// An overlay that removes the annotations removes the registrations.
	overlay := filepath.Join(t.TempDir(), "replacement.go")
	if err := os.WriteFile(overlay, []byte("package fixture\nimport \"testing\"\nfunc TestInternal(t *testing.T) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p.GoFiles, p.XTestGoFiles = nil, nil
	got, _, err = exclusionRegistrationSources(p, map[string]string{filepath.Join(dir, "internal_test.go"): overlay}, miniPackage, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("removed annotation retained: %v %v", got, err)
	}
}

// Cgo's Go sources join the scan as ordinary files do.
func TestExclusionRegistrationsReadCgoFiles(t *testing.T) {
	dir := t.TempDir()
	writeExclusionFixture(t, dir, map[string]string{"cgo.go": "package fixture\nimport \"C\"\ntype Suite struct{}\n//ddtest:no_auto_retry\nfunc (s *Suite) TestCgo() {}\n"})
	got, _, err := exclusionRegistrationSources(goPackage{Dir: dir, Name: "fixture", CgoFiles: []string{"cgo.go"}}, nil, miniPackage, false)
	if err != nil || !strings.Contains(got["fixture"], "RegisterTestExclusions((*Suite).TestCgo,") {
		t.Fatalf("cgo registrations: %v %v", got, err)
	}
}

// An annotation on an unsupported declaration must not stop the run: it is
// ignored with a warning naming its position, and the package's supported
// declarations still register.
func TestExclusionRegistrationsIgnoreUnsupportedDeclarations(t *testing.T) {
	dir := t.TempDir()
	source := `package fixture
//ddtest:no_auto_retry
func helper() {}
//ddtest:prevent_failure_suppression
func TestMain(m *testing.M) {}
//ddtest:no_auto_retry
func TestGeneric[T any](t *testing.T) {}
type Suite[T any] struct{}
//ddtest:no_auto_retry
func (s *Suite[T]) TestMethod() {}
//ddtest:no_auto_retry
func TestKept(t *testing.T) {}
`
	writeExclusionFixture(t, dir, map[string]string{"sample_test.go": source})
	got, warnings, err := exclusionRegistrationSources(goPackage{Dir: dir, Name: "fixture", TestGoFiles: []string{"sample_test.go"}}, nil, miniPackage, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got["fixture"], "RegisterTestExclusions(") != 1 || !strings.Contains(got["fixture"], "RegisterTestExclusions(TestKept,") {
		t.Fatalf("registrations:\n%s", got["fixture"])
	}
	file := filepath.Join(dir, "sample_test.go")
	want := []string{
		file + ":3:1: ddtest annotations on helper ignored: they apply only to an individual Test function or Testify Test method",
		file + ":5:1: ddtest annotations on TestMain ignored: they apply only to an individual Test function or Testify Test method",
		file + ":7:1: ddtest annotations on TestGeneric ignored: generic declarations are not supported",
		file + ":10:1: ddtest annotations on TestMethod ignored: generic declarations are not supported",
	}
	if !slices.Equal(warnings, want) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}
}
