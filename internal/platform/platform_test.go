package platform

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestPlatformsDetectTheirProjectFiles(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		filename string
	}{
		{name: "ruby", platform: NewRuby(settings.TestSkippingLevelSuite), filename: "Gemfile"},
		{name: "javascript", platform: NewJavaScript(), filename: "package.json"},
		{name: "python pyproject", platform: NewPython(), filename: "pyproject.toml"},
		{name: "python setup", platform: NewPython(), filename: "setup.py"},
		{name: "python requirements", platform: NewPython(), filename: "requirements.txt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryRoot := t.TempDir()
			detected, err := test.platform.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if detected {
				t.Fatal("Detect() = true for an empty repository")
			}

			contents := "{}"
			if test.filename == "pyproject.toml" {
				contents = "[project]\nname = \"example\"\n"
			}
			if err := os.WriteFile(filepath.Join(repositoryRoot, test.filename), []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			detected, err = test.platform.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if !detected {
				t.Fatal("Detect() = false for a matching repository")
			}
		})
	}
}

func TestPlatformSanityChecksPropagateContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "sanity-check")

	tests := []struct {
		name   string
		output []byte
	}{
		{name: "ruby", output: []byte("  * datadog-ci (1.31.0)\n")},
		{name: "python", output: []byte("4.11.0\n")},
		{name: "javascript", output: []byte("v24.0.0\n")},
	}

	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			executor := &mockCommandExecutor{combinedOutput: tests[i].output}
			var check func(context.Context) error
			switch tests[i].name {
			case "ruby":
				platform := NewRuby(settings.TestSkippingLevelTest)
				platform.executor = executor
				check = platform.SanityCheck
			case "python":
				platform := NewPython()
				platform.executor = executor
				check = platform.SanityCheck
			case "javascript":
				platform := NewJavaScript()
				platform.executor = executor
				check = platform.SanityCheck
			}

			if err := check(ctx); err != nil {
				t.Fatalf("SanityCheck() failed: %v", err)
			}
			if len(executor.combinedOutputCtx) == 0 {
				t.Fatal("SanityCheck() did not execute a command")
			}
			for _, got := range executor.combinedOutputCtx {
				if got != ctx {
					t.Fatal("SanityCheck() did not propagate its context")
				}
			}
		})
	}
}

func TestDetectPlatformUnsupported(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM", "node")
	settings.Init()

	_, err := DetectPlatform("", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DetectPlatform() error = %v, want unsupported platform", err)
	}

}

func resetDetectionSettings(t *testing.T) {
	t.Helper()
	cfg := settings.Get()
	old := *cfg
	cfg.Platform = ""
	cfg.Framework = ""
	cfg.Command = ""
	t.Cleanup(func() { *cfg = old })
}

func TestAutomaticPlatformAndFrameworkSelection(t *testing.T) {
	for _, tc := range []struct{ name, file, contents, runner, env string }{
		{"javascript", "package.json", `{"devDependencies":{"jest":"29"}}`, "jest", "NODE_OPTIONS"},
		{"python", "pytest.ini", "[pytest]\n", "pytest", "PYTEST_ADDOPTS"},
		{"ruby", "Gemfile", "gem 'rspec'\n", "rspec", "RUBYOPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDetectionSettings(t)
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(tc.file, []byte(tc.contents), 0644))
			t.Setenv("PATH", t.TempDir()) // Selection must work without any runtime or tracer.
			p, err := DetectPlatform("", "")
			require.NoError(t, err)
			require.Equal(t, tc.name, p.Name())
			fw, err := p.DetectFramework("", "")
			require.NoError(t, err)
			require.Equal(t, tc.runner, fw.Name())
			require.Contains(t, fw.GetPlatformEnv(), tc.env)
			lang, readOnly, err := detectFixture(root, "")
			require.NoError(t, err)
			require.Equal(t, p.Name(), lang)
			require.Equal(t, fw.Name(), readOnly.Name())
		})
	}
}

func TestExplicitSelectionOverridesProjectEvidence(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{"), 0644))
	settings.Get().Framework = "pytest"
	lang, fw, err := detectFixture(root, "")
	require.NoError(t, err)
	require.Equal(t, "python", lang)
	require.Equal(t, "pytest", fw.Name())
	settings.Get().Platform = "javascript"
	_, _, err = detectFixture(root, "")
	require.ErrorContains(t, err, "not supported by platform")
	settings.Get().Framework = "jest"
	lang, fw, err = detectFixture(root, "")
	require.NoError(t, err)
	require.Equal(t, "javascript", lang)
	require.Equal(t, "jest", fw.Name())
}

func TestPlatformHintResolvesPolyglotProject(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"jest":"29"}}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\n"), 0644))
	_, _, err := detectFixture(root, "")
	require.ErrorContains(t, err, "multiple platforms")
	settings.Get().Platform = "python"
	lang, fw, err := detectFixture(root, "")
	require.NoError(t, err)
	require.Equal(t, "python", lang)
	require.Equal(t, "pytest", fw.Name())
}

func TestPlatformDetectionSelectsFrameworkWithoutWritingFiles(t *testing.T) {
	resetDetectionSettings(t)
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cucumber", "cypress", "pytest", "rspec", "minitest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			language, filename, contents := "javascript", "package.json", `{"scripts":{"test":"`+name+`"}}`
			switch name {
			case "pytest":
				language, filename, contents = "python", "pytest.ini", "[pytest]\n"
			case "rspec", "minitest":
				language, filename, contents = "ruby", "Gemfile", "gem '"+name+"'\n"
			}
			path := filepath.Join(root, filename)
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
			detected, runner, err := detectFixture(root, "")
			require.NoError(t, err)
			require.Equal(t, language, detected)
			require.Equal(t, name, runner.Name())
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, contents, string(after))
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestPlatformDetectionRequiresUnambiguousSelection(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	_, _, err := detectFixture(root, "")
	require.ErrorContains(t, err, "could not detect")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest run","e2e":"playwright test"}}`), 0644))
	_, _, err = detectFixture(root, "")
	require.ErrorContains(t, err, "--framework")
	language, runner, err := detectFixture(root, "vitest")
	require.NoError(t, err)
	require.Equal(t, "javascript", language)
	require.Equal(t, "vitest", runner.Name())
	_, _, err = detectFixture(root, "unsupported")
	require.ErrorContains(t, err, "unsupported framework")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{"), 0644))
	_, _, err = detectFixture(root, "")
	require.ErrorContains(t, err, "parse")
}

// Exercise the same public selection sequence used by every command.
func detectFixture(root, hint string) (string, framework.Framework, error) {
	p, err := DetectPlatform(root, hint)
	if err != nil {
		return "", nil, err
	}
	fw, err := p.DetectFramework(root, hint)
	if err != nil {
		return "", nil, err
	}
	return p.Name(), fw, nil
}

type detectionFixture struct {
	Name      string            `json:"name"`
	Files     map[string]string `json:"files"`
	Framework string            `json:"framework"`
	Language  string            `json:"language"`
	Hint      string            `json:"hint"`
	Error     string            `json:"error"`
}

func TestPlatformDetectionOSSFixtures(t *testing.T) {
	for _, fixture := range []struct {
		name, language, framework string
		hints                     []string
	}{
		{"class-validator", "javascript", "jest", nil},
		{"compare-versions", "javascript", "mocha", nil},
		{"destr", "javascript", "vitest", nil},
		{"ttvc", "javascript", "", []string{"jest", "playwright"}},
		{"cloudevents", "javascript", "", []string{"mocha", "cucumber"}},
		{"cypress-example-kitchensink", "javascript", "cypress", nil},
		{"itsdangerous", "python", "pytest", nil},
		{"concurrent-ruby", "ruby", "rspec", nil},
		{"i18n", "ruby", "minitest", nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			files := detectionFiles(t, filepath.Join("testdata", "detection", fixture.name))
			testCase := detectionFixture{Name: fixture.name, Files: files, Language: fixture.language, Framework: fixture.framework}
			if len(fixture.hints) > 0 {
				testCase.Error = "multiple test frameworks"
			}
			checkDetectionFixture(t, testCase)
			for _, hint := range fixture.hints {
				t.Run(hint, func(t *testing.T) {
					testCase.Hint = hint
					testCase.Framework = hint
					testCase.Error = ""
					checkDetectionFixture(t, testCase)
				})
			}
		})
	}
}

func TestPlatformDetectionHardFixtures(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "detection", "edge-cases.json"))
	require.NoError(t, err)
	var fixtures []detectionFixture
	require.NoError(t, json.Unmarshal(contents, &fixtures))
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) { checkDetectionFixture(t, fixture) })
	}
}

func checkDetectionFixture(t *testing.T, fixture detectionFixture) {
	t.Helper()
	resetDetectionSettings(t)
	root := filepath.Join(t.TempDir(), "project with spaces")
	require.NoError(t, os.MkdirAll(root, 0755))
	for name, contents := range fixture.Files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	}
	before := detectionFiles(t, root)
	// Detection cannot depend on npm, Python, Ruby, or an installed test runner.
	t.Setenv("PATH", t.TempDir())
	for range 2 {
		language, runner, err := detectFixture(root, fixture.Hint)
		if fixture.Error != "" {
			require.ErrorContains(t, err, fixture.Error)
			require.Nil(t, runner)
		} else {
			require.NoError(t, err)
			require.Equal(t, fixture.Language, language)
			require.Equal(t, fixture.Framework, runner.Name())
		}
		require.Equal(t, before, detectionFiles(t, root), "detection must not write files")
	}
}

func detectionFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = string(contents)
		return nil
	}))
	return files
}
