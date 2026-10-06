package instrument

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestSuiteEntryRetainsLinesAndCoveredLocations(t *testing.T) {
	for _, header := range []string{"", "//line /original/suite.go:1:1\n"} {
		src := header + "package suite\nimport tt \"testing\"\ntype TestingSuite interface{}\nfunc Run(test *tt.T, target TestingSuite){ original() }\nfunc (s TestingSuite) Run() {}\n"
		out, changed, err := TransformTestifyEntry("/backing/suite.go", []byte(src))
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		if strings.Count(string(out), "\n") != strings.Count(src, "\n") || !strings.Contains(string(out), TestifyRegisterName+"(test,target); original()") {
			t.Fatal(string(out))
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "new.go", out, 0); err != nil {
			t.Fatal(err)
		}
		if header != "" && strings.Contains(string(out), "/*line /backing/") {
			t.Fatal("covered line mapping overridden")
		}
	}
	for _, src := range []string{`package suite;func Run(){}`, `package suite; import "testing";type TestingSuite interface{};func Run(_ *testing.T,s TestingSuite){}`} {
		if _, _, err := TransformTestifyEntry("bad.go", []byte(src)); err == nil {
			t.Fatal("unsupported entry accepted")
		}
	}
}

func TestTestifyPackageReusesValidationWithoutSkippingFiles(t *testing.T) {
	run := []byte("package suite\nimport tt \"testing\"\ntype TestingSuite interface{}\nfunc Run(test *tt.T,target TestingSuite){ original() }\n")
	helper := []byte("package suite\nconst description=\"__dd_ci_registerTestifySuite\"\n")
	want, _, err := TransformTestifyEntry("suite.go", run)
	if err != nil {
		t.Fatal(err)
	}
	out, err := TransformTestifyPackage(map[string][]byte{"suite.go": run, "helper.go": helper})
	if err != nil || len(out) != 1 || string(out["suite.go"]) != string(want) {
		t.Fatalf("package transformation differs: %q, %v", out, err)
	}
	for _, bad := range []string{"package suite;var __dd_ci_registerTestifySuite int", "package suite;func broken("} {
		if _, err := TransformTestifyPackage(map[string][]byte{"suite.go": run, "helper.go": []byte(bad)}); err == nil {
			t.Fatal("unmodified helper bypassed validation", bad)
		}
	}
	if _, err := TransformTestifyPackage(map[string][]byte{"helper.go": helper}); err == nil {
		t.Fatal("missing Run accepted")
	}
}
