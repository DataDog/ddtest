package instrument

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuiteEntryRetainsLinesAndCoveredLocations(t *testing.T) {
	for _, header := range []string{"", "//line /original/suite.go:1:1\n"} {
		src := header + "package suite\nimport tt \"testing\"\ntype TestingSuite interface{}\nfunc Run(test *tt.T, target TestingSuite){ original() }\nfunc (s TestingSuite) Run() {}\n"
		out, changed, err := TransformTestifyEntry("/backing/suite.go", []byte(src), false)
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
		if _, _, err := TransformTestifyEntry("bad.go", []byte(src), false); err == nil {
			t.Fatal("unsupported entry accepted")
		}
	}
}

func TestTestifyPackageReusesValidationWithoutSkippingFiles(t *testing.T) {
	run := []byte("package suite\nimport tt \"testing\"\ntype TestingSuite interface{}\nfunc Run(test *tt.T,target TestingSuite){ original() }\n")
	helper := []byte("package suite\nconst description=\"__dd_ci_registerTestifySuite\"\n")
	want, _, err := TransformTestifyEntry("suite.go", run, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := TransformTestifyPackage(map[string][]byte{"suite.go": run, "helper.go": helper}, false)
	if err != nil || len(out) != 1 || string(out["suite.go"]) != string(want) {
		t.Fatalf("package transformation differs: %q, %v", out, err)
	}
	for _, bad := range []string{"package suite;var __dd_ci_registerTestifySuite int", "package suite;func broken("} {
		if _, err := TransformTestifyPackage(map[string][]byte{"suite.go": run, "helper.go": []byte(bad)}, false); err == nil {
			t.Fatal("unmodified helper bypassed validation", bad)
		}
	}
	if _, err := TransformTestifyPackage(map[string][]byte{"helper.go": helper}, false); err == nil {
		t.Fatal("missing Run accepted")
	}
}

func TestScopedTestifyEntryPreservesLinesAndUsesCleanupContract(t *testing.T) {
	src := []byte("package suite\nimport \"testing\"\ntype TestingSuite interface{}\nfunc Run(t *testing.T,s TestingSuite){}\n")
	out, changed, err := TransformTestifyEntry("suite.go", src, true)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !strings.Contains(string(out), "defer "+TestifyRegisterName+"(t,s)();") || strings.Count(string(out), "\n") != strings.Count(string(src), "\n") {
		t.Fatal(string(out))
	}
	hook := TestifyEntryHook("fixture.hook", true)
	if !strings.Contains(hook, "(*testing.T, interface{}) func()") {
		t.Fatal(hook)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "hook.go", hook, 0); err != nil {
		t.Fatal(err)
	}
}

// Without Mini, the scoped registration registers nothing and releases
// nothing: the suite runs natively.
func TestTestifyEntryWithoutMiniRunsNatively(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	root := t.TempDir()
	suite := "package suite\nimport \"testing\"\ntype TestingSuite interface{ TestPass(t *testing.T) }\nfunc Run(t *testing.T, suite TestingSuite) {\n\tt.Run(\"TestPass\", suite.TestPass)\n}\n"
	edited, err := TransformTestifyPackage(map[string][]byte{filepath.Join(root, "suite", "suite.go"): []byte(suite)}, true)
	if err != nil || len(edited) != 1 {
		t.Fatal(edited, err)
	}
	for name, data := range map[string]string{
		"go.mod":           "module example.com/testify\n\ngo 1.25.0\n",
		"suite/suite.go":   string(edited[filepath.Join(root, "suite", "suite.go")]),
		"suite/hook.go":    TestifyEntryHook("", true),
		"client/a_test.go": "package client\nimport (\"testing\";\"example.com/testify/suite\")\ntype s struct{}\nfunc (s) TestPass(t *testing.T) { t.Log(\"ran\") }\nfunc TestSuite(t *testing.T) { suite.Run(t, s{}) }\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-v", "-count=1", "./client")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestSuite/TestPass") || !strings.Contains(string(out), "ran") {
		t.Fatalf("the edited suite must link and run without Mini: %v\n%s", err, out)
	}
}
