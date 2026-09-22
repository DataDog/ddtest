package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/stretchr/testify/require"
)

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
