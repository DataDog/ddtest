package runner

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExclusionRegistrationsKeepPackagesAndDeclarationsSeparate(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"internal_test.go": "package fixture\nimport \"testing\"\n//ddtest:no_auto_retry\nfunc TestInternal(t *testing.T) {}\ntype Suite struct{}\n//ddtest:prevent_failure_suppression\nfunc (s (*Suite)) TestMethod() {}\n//ddtest:no_auto_retry\nfunc (s (Suite)) TestValue() {}\n",
		"external_test.go": "package fixture_test\nimport \"testing\"\n//ddtest:no_auto_retry\n//ddtest:prevent_failure_suppression\nfunc TestExternal(t *testing.T) {}\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := goPackage{Dir: dir, Name: "fixture", TestGoFiles: []string{"internal_test.go"}, XTestGoFiles: []string{"external_test.go"}}
	got, err := exclusionRegistrationSources(p, nil, miniPackage)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got["fixture"], "RegisterTestExclusions((*Suite).TestValue") || !strings.Contains(got["fixture"], "RegisterTestExclusions((*Suite).TestMethod") || strings.Contains(got["fixture"], "TestExternal") || !strings.Contains(got["fixture_test"], "PreventFailureSuppression: true, NoAutoRetry: true") {
		t.Fatalf("registrations: %v", got)
	}
	for _, src := range got {
		if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", src, 0); err != nil {
			t.Fatal(err)
		}
	}
	p.ImportPath = "example.com/runtime.v1"
	boundary, err := exclusionRegistrationSourcesForBoundary(p, nil, miniPackage, true)
	if err != nil || len(boundary) != 1 || !strings.Contains(boundary["fixture_test"], `RegisterTestExclusionsByName("example.com/runtime%2ev1.TestInternal"`) || !strings.Contains(boundary["fixture_test"], `RegisterTestExclusionsByName("example.com/runtime%2ev1.(*Suite).TestMethod"`) || !strings.Contains(boundary["fixture_test"], "RegisterTestExclusions(TestExternal") {
		t.Fatalf("external runtime dependency registrations: %v %v", boundary, err)
	}
	t.Run("generic-receivers", func(t *testing.T) {
		generic := filepath.Join(dir, "generic.go")
		source := "package fixture\ntype Suite[T any] struct{}\n//ddtest:no_auto_retry\nfunc (s (*Suite[T])) TestPointer(){}\n//ddtest:no_auto_retry\nfunc (s (Suite[T])) TestValue(){}\n"
		if err := os.WriteFile(generic, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		external := filepath.Join(dir, "generic_external_test.go")
		if err := os.WriteFile(external, []byte("package fixture_test\ntype Suite[A,B any] struct{}\n//ddtest:no_auto_retry\nfunc (s (*Suite[A,B])) TestPointer(){}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		gp := goPackage{Dir: dir, Name: "fixture", ImportPath: "example.com/generic.v1", GoFiles: []string{"generic.go"}, XTestGoFiles: []string{"generic_external_test.go"}, TestImports: []string{"example.com/helper"}}
		got, err := exclusionRegistrationSources(gp, nil, miniPackage)
		if err != nil {
			t.Fatal(err)
		}
		for pkg, names := range map[string][]string{
			"fixture":      {"example.com/generic%2ev1.(*Suite[...]).TestPointer", "example.com/generic%2ev1.(*Suite[...]).TestValue", "example.com/generic%2ev1.Suite[...].TestValue"},
			"fixture_test": {"example.com/generic%2ev1_test.(*Suite[...]).TestPointer"},
		} {
			for _, name := range names {
				if !strings.Contains(got[pkg], `RegisterTestExclusionsByName("`+name+`"`) {
					t.Fatalf("missing %s: %v", name, got)
				}
			}
		}
		// Importing the generic declaration requires no concrete type argument.
		gp.Module = &struct {
			Path, Version, Dir string
			Main               bool
			Replace            *struct{ Path, Dir, Version string }
		}{Path: "example.com/generic.v1"}
		imported, err := exclusionDependencyRegistrations(goPackage{Imports: []string{gp.ImportPath}}, packageGraph{gp.ImportPath: &gp}, nil, nil)
		if err != nil || !strings.Contains(imported, `RegisterTestExclusionsByName("example.com/generic%2ev1.(*Suite[...]).TestPointer"`) {
			t.Fatalf("generic imported declarations: %s %v", imported, err)
		}
		boundary, err := exclusionRegistrationSourcesForBoundary(gp, nil, miniPackage, true)
		if err != nil || len(boundary) != 1 || !strings.Contains(boundary["fixture_test"], `"example.com/generic%2ev1.(*Suite[...]).TestPointer"`) {
			t.Fatalf("generic runtime dependency boundary: %v %v", boundary, err)
		}
	})
	// An edited overlay and annotation removal replace rather than retain policy.
	overlay := filepath.Join(dir, "replacement.go")
	if err := os.WriteFile(overlay, []byte("package fixture\nfunc TestInternal() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = exclusionRegistrationSources(p, map[string]string{filepath.Join(dir, "internal_test.go"): overlay}, miniPackage)
	if err != nil || got["fixture"] != "" {
		t.Fatalf("removed annotation retained: %v %v", got, err)
	}
}

func TestExclusionRegistrationsRejectNonTestDeclarations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte("package fixture\n//ddtest:no_auto_retry\nfunc helper() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := exclusionRegistrationSources(goPackage{Dir: dir, TestGoFiles: []string{"sample_test.go"}}, nil, miniPackage)
	if err == nil {
		t.Fatal("accepted a non-test annotation")
	}
}

func TestImportedExclusionRegistrationsFollowNativeReachabilityAndOverlay(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"suite.go":           "package helper\ntype Base struct{}\n//ddtest:no_auto_retry\nfunc (*Base) TestCritical(){}\n//ddtest:no_auto_retry\nfunc (Base) TestValue(){}\n",
		"suite_test.go":      "package helper\n//ddtest:no_auto_retry\nfunc TestDependencyOnly(){}\n",
		"overlay.go":         "package helper\ntype Base struct{}\n//ddtest:prevent_failure_suppression\nfunc (Alias) TestCritical(){}\n//ddtest:no_auto_retry\nfunc (ValueAlias) TestValue(){}\n//ddtest:no_auto_retry\nfunc (*Defined) TestDistinct(){}\n",
		"aliases.go":         "package helper\ntype Alias = Wrong\ntype ValueAlias = Wrong\ntype Defined Base\n",
		"aliases_overlay.go": "package helper\ntype Alias = Intermediate\ntype Intermediate = (*Base)\ntype ValueAlias = Base\ntype Defined Base\n",
		"unselected.go":      "package helper\ntype Intermediate = Wrong\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := goPackage{ImportPath: "example.com/root", TestImports: []string{"example.com/helper.v1"}}
	helper := goPackage{Dir: dir, Name: "helper", ImportPath: "example.com/helper.v1", CgoFiles: []string{"suite.go", "aliases.go"}, TestGoFiles: []string{"suite_test.go"}, DepOnly: true}
	helper.Module = &struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
	}{Path: "example.com/helper.v1"}
	requested := []string{}
	graph, err := exclusionDependencyGraph([]goPackage{root}, packageGraph{}, func(paths ...string) ([]goPackage, error) {
		requested = append(requested, paths...)
		return []goPackage{helper}, nil
	})
	if err != nil || strings.Join(requested, " ") != "-deps example.com/helper.v1" {
		t.Fatalf("query=%v err=%v", requested, err)
	}
	overlay := map[string]string{filepath.Join(dir, "suite.go"): filepath.Join(dir, "overlay.go"), filepath.Join(dir, "aliases.go"): filepath.Join(dir, "aliases_overlay.go")}
	got, err := exclusionDependencyRegistrations(root, graph, overlay, nil)
	if err != nil {
		t.Fatal(err)
	}
	// cgo declarations also register inside a local test package, where the
	// compiler itself resolves the receiver aliases in method expressions.
	local, err := exclusionRegistrationSources(helper, overlay, miniPackage)
	if err != nil || !strings.Contains(local["helper"], "RegisterTestExclusions((*Base).TestCritical") || !strings.Contains(local["helper"], "RegisterTestExclusions((*Base).TestValue") {
		t.Fatalf("local cgo declarations: %v %v", local, err)
	}
	for _, wanted := range []string{
		`RegisterTestExclusionsByName("example.com/helper%2ev1.(*Base).TestCritical", __dd_ci_runtime.TestExclusions{PreventFailureSuppression: true, NoAutoRetry: false})`,
		`RegisterTestExclusionsByName("example.com/helper%2ev1.(*Base).TestValue"`,
		`RegisterTestExclusionsByName("example.com/helper%2ev1.Base.TestValue"`,
		`RegisterTestExclusionsByName("example.com/helper%2ev1.(*Defined).TestDistinct"`,
	} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("missing %s: %s", wanted, got)
		}
	}
	if strings.Contains(got, ".(*Alias).") || strings.Contains(got, ".ValueAlias.") || strings.Contains(got, "Wrong") || strings.Contains(got, ".(*Base).TestDistinct") || strings.Contains(got, "helper%2ev1.Base.TestCritical") {
		t.Fatalf("receiver alias identity or defined type was changed: %s", got)
	}
	if strings.Contains(got, "TestDependencyOnly") {
		t.Fatalf("dependency's tests joined consumer: %s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", "package fixture\n"+got, 0); err != nil {
		t.Fatal(err)
	}
	got, err = exclusionDependencyRegistrations(root, graph, overlay, map[string]bool{"example.com/helper.v1": true})
	if err != nil || got != "" {
		t.Fatalf("runtime dependency registered: %s %v", got, err)
	}
	got, err = exclusionDependencyRegistrations(goPackage{ImportPath: "example.com/unrelated"}, graph, overlay, nil)
	if err != nil || got != "" {
		t.Fatalf("unreachable helper registered: %s %v", got, err)
	}
}
