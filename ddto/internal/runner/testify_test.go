package runner

import (
	"encoding/json"
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
		Replace       *struct{ Path, Dir, Version string }
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
		Replace       *struct{ Path, Dir, Version string }
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

// The fork's pseudo-version predates its actual upstream API. Feed replacement
// metadata through JSON, just as go list does, so its module path participates.
func TestTestifyDataDogReplacement(t *testing.T) {
	for _, selected := range []Runtime{Mini, SDK} {
		t.Run(string(selected), func(t *testing.T) {
			pkg := testifySuitePackage(t, "v1.12.1", testifyRunSource)
			metadata := `{"Path":"github.com/stretchr/testify","Version":"v1.12.1","Replace":{"Path":"github.com/DataDog/testify","Version":"v1.1.5-0.20250616071259-629a0cde43ec"}}`
			if err := json.Unmarshal([]byte(metadata), &pkg.Module); err != nil {
				t.Fatal(err)
			}
			entry, warning, err := prepareTestifyPackage(pkg, nil, selected, t.TempDir())
			if err != nil || entry == nil || warning != "" {
				t.Fatalf("DataDog fork was not instrumented: entry=%v warning=%q err=%v", entry, warning, err)
			}
		})
	}
}

func TestTestifyForkReplacementGuards(t *testing.T) {
	const forkVersion = "v1.1.5-0.20250616071259-629a0cde43ec"
	const incompatible = "package suite\nimport \"testing\"\ntype TestingSuite interface{}\nfunc Run(t *testing.T, s TestingSuite, extra bool) {}\n"
	for _, selected := range []Runtime{Mini, SDK} {
		for _, tc := range []struct {
			name, source string
			overlay      bool
		}{
			{"changed API", incompatible, false},
			{"missing entry", "package suite\n", false},
			{"overlay changes API", incompatible, true},
			{"overlay removes entry", "package suite\n", true},
		} {
			t.Run(string(selected)+"/"+tc.name, func(t *testing.T) {
				source := tc.source
				if tc.overlay {
					source = testifyRunSource
				}
				pkg := testifySuitePackage(t, "v1.12.1", source)
				pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: "github.com/DataDog/testify", Version: forkVersion}
				var replacements map[string]string
				if tc.overlay {
					backing := filepath.Join(t.TempDir(), "suite.go")
					if err := os.WriteFile(backing, []byte(tc.source), 0600); err != nil {
						t.Fatal(err)
					}
					replacements = map[string]string{filepath.Join(pkg.Dir, "suite.go"): backing}
				}
				entry, warning, err := prepareTestifyPackage(pkg, replacements, selected, t.TempDir())
				if err != nil || entry != nil || !strings.Contains(warning, "unsupported library API") {
					t.Fatalf("API validation was bypassed: entry=%v warning=%q err=%v", entry, warning, err)
				}
				if !strings.Contains(warning, "Testify v1.12.1 is not instrumented") || !strings.Contains(warning, "replacement github.com/DataDog/testify "+forkVersion) {
					t.Fatalf("warning omits original version or replacement identity: %s", warning)
				}
			})
		}
	}
}

func TestTestifyReplacementVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, required, path, version string
		want                          bool
	}{
		{"fork independent pseudo-version", "v1.12.1", "github.com/DataDog/testify", "v1.1.5-0.20250616071259-000000000000", true},
		{"other fork independent version", "v1.12.1", "example.com/testify", "v0.1.0", true},
		{"fork independent major", "v1.12.1", "example.com/testify/v2", "v2.0.0", true},
		{"fork original too old", "v1.3.0", "github.com/DataDog/testify", "v1.10.0", false},
		{"fork unknown original", "", "github.com/DataDog/testify", "v1.10.0", false},
		{"fork unsupported original major", "v2.0.0", "github.com/DataDog/testify", "v1.10.0", false},
		{"same module downgrade", "v1.12.1", "github.com/stretchr/testify", "v1.3.0", false},
		{"same module upgrade", "v1.3.0", "github.com/stretchr/testify", "v1.10.0", true},
		{"local replacement", "v1.12.1", "../testify", "", true},
		{"local old original", "v1.3.0", "../testify", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := testifySuitePackage(t, tc.required, testifyRunSource)
			pkg.Module.Replace = &struct{ Path, Dir, Version string }{Path: tc.path, Version: tc.version}
			entry, warning, err := prepareTestifyPackage(pkg, nil, Mini, t.TempDir())
			if err != nil || (entry != nil) != tc.want || (warning == "") != tc.want {
				t.Fatalf("want supported=%t: entry=%v warning=%q err=%v", tc.want, entry, warning, err)
			}
		})
	}
}
