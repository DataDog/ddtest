package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testifySuitePackage(t *testing.T, version, source string) *goPackage {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "suite.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := &goPackage{Dir: dir, ImportPath: "github.com/stretchr/testify/suite", GoFiles: []string{"suite.go"}}
	p.Module = &struct {
		Path, Version string
		Main          bool
		Replace       *struct{ Dir, Version string }
	}{Path: "github.com/stretchr/testify", Version: version}
	return p
}

const testifyRunSource = "package suite\n\nimport \"testing\"\n\ntype TestingSuite interface{}\n\nfunc Run(t *testing.T, suite TestingSuite) {}\n"

func TestUnsupportedTestifyWarnsInsteadOfFailing(t *testing.T) {
	for _, tc := range []struct {
		name, version, source, warning string
	}{
		{"old version", "v1.3.0", testifyRunSource, "requires v1.4.0 or a later v1 release"},
		{"other major", "v2.0.0", testifyRunSource, "requires v1.4.0 or a later v1 release"},
		{"unknown version", "", testifyRunSource, "(unknown version)"},
		{"changed entry", "v1.11.1", "package suite\n\nimport \"testing\"\n\ntype TestingSuite interface{}\n\nfunc Run(t *testing.T, suite TestingSuite, options ...int) {}\n", "unsupported library API"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, warning, err := prepareTestifyPackage(testifySuitePackage(t, tc.version, tc.source), map[string]string{}, Mini, t.TempDir())
			if err != nil || entry != nil || !strings.Contains(warning, tc.warning) || !strings.Contains(warning, "ordinary tests") {
				t.Fatalf("entry=%v warning=%q err=%v", entry, warning, err)
			}
		})
	}
	for _, version := range []string{"v1.4.0", "v1.10.0", "v1.12.1"} {
		entry, warning, err := prepareTestifyPackage(testifySuitePackage(t, version, testifyRunSource), map[string]string{}, Mini, t.TempDir())
		if err != nil || entry == nil || warning != "" {
			t.Fatalf("%s: entry=%v warning=%q err=%v", version, entry, warning, err)
		}
	}
	// A missing source is an environment failure, not an unsupported library.
	missing := testifySuitePackage(t, "v1.11.1", testifyRunSource)
	missing.GoFiles = []string{"missing.go"}
	if _, _, err := prepareTestifyPackage(missing, map[string]string{}, Mini, t.TempDir()); err == nil {
		t.Fatal("unreadable Testify source was ignored")
	}
}

func TestUnsupportedGoleakWarnsInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	source := "package goleak\n\ntype Option interface{}\n\nfunc Find(options ...Option) error { return nil }\n"
	if err := os.WriteFile(filepath.Join(dir, "leaks.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := &goPackage{Dir: dir, ImportPath: "go.uber.org/goleak", GoFiles: []string{"leaks.go"}}
	pkg.Module = &struct {
		Path, Version string
		Main          bool
		Replace       *struct{ Dir, Version string }
	}{Path: "go.uber.org/goleak", Version: "v1.2.1"}
	entry, warning, err := prepareGoleak(pkg, map[string]string{}, t.TempDir())
	if err != nil || entry != nil || !strings.Contains(warning, "goleak v1.2.1 is not instrumented") {
		t.Fatalf("entry=%v warning=%q err=%v", entry, warning, err)
	}
	pkg.Module.Version = "v1.3.0"
	if entry, warning, err = prepareGoleak(pkg, map[string]string{}, t.TempDir()); err != nil || entry == nil || warning != "" {
		t.Fatalf("supported goleak: entry=%v warning=%q err=%v", entry, warning, err)
	}
}
