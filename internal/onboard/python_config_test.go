package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPytestConfigPathsPreserveListsAndScalarCacheDirectory(t *testing.T) {
	for _, name := range []string{"pytest.ini", "tox.ini", "setup.cfg", "pyproject.toml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			tests, source, cache := filepath.Join(root, "tests"), filepath.Join(root, "src dir"), filepath.Join(root, "cache dir")
			section := "pytest"
			if name == "setup.cfg" {
				section = "tool:pytest"
			}
			contents := fmt.Sprintf("[%s]\naddopts=-q\ntestpaths=%q\npythonpath=%q external\ncache_dir=%s\n", section, tests, source, cache)
			if name == "pyproject.toml" {
				contents = fmt.Sprintf("[tool.pytest.ini_options]\naddopts=['-q']\ntestpaths=[%q]\npythonpath=[%q, 'external']\ncache_dir=%q\n", tests, source, cache)
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0644))
			words := []string{"pytest", "-c", name}
			paths, err := PytestConfigPaths(root, root, "", words)
			require.NoError(t, err)
			require.Equal(t, map[string][]string{"testpaths": {tests}, "pythonpath": {source, "external"}, "cache_dir": {cache}}, paths)
			options, err := PytestConfigOptions(root, root, "", words)
			require.NoError(t, err)
			require.Equal(t, []string{"-q"}, options)
		})
	}
}

func TestPytestConfigSelectionStaysInsideRepository(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.Mkdir(root, 0755))
	outside := filepath.Join(parent, "outside.ini")
	require.NoError(t, os.WriteFile(outside, []byte("[pytest]\naddopts = -p no:ddtrace\n"), 0644))
	_, err := PytestConfigOptions(root, root, "", []string{"pytest", "-c", "../outside.ini"})
	require.ErrorContains(t, err, "leaves the repository")
	if err := os.Symlink(outside, filepath.Join(root, "pytest.ini")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err = PytestConfigOptions(root, root, "", []string{"pytest"})
	require.ErrorContains(t, err, "leaves the repository")
}
