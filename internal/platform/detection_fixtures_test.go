package platform

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type detectionFixture struct {
	Name      string            `json:"name"`
	Files     map[string]string `json:"files"`
	Framework string            `json:"framework"`
	Language  string            `json:"language"`
	Hint      string            `json:"hint"`
	Error     string            `json:"error"`
}

func TestDetectTestProjectOSSFixtures(t *testing.T) {
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

func TestDetectTestProjectHardFixtures(t *testing.T) {
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
		language, runner, err := DetectTestProject(root, fixture.Hint)
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
