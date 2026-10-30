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
			log := filepath.Join(root, "pytest log.txt")
			contents := fmt.Sprintf("[%s]\naddopts=-q\ntestpaths=%q\npythonpath=%q external\ncache_dir=%s\nlog_file=%s\n", section, tests, source, cache, log)
			if name == "pyproject.toml" {
				contents = fmt.Sprintf("[tool.pytest.ini_options]\naddopts=['-q']\ntestpaths=[%q]\npythonpath=[%q, 'external']\ncache_dir=%q\nlog_file=%q\n", tests, source, cache, log)
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0644))
			words := []string{"pytest", "-c", name}
			paths, err := PytestConfigPaths(root, root, "", words)
			require.NoError(t, err)
			require.Equal(t, map[string][]string{"testpaths": {tests}, "pythonpath": {source, "external"}, "cache_dir": {cache}, "log_file": {log}}, paths)
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

func TestPytestNonCollectionOptionValuesDoNotSelectConfiguration(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "legacy")
	require.NoError(t, os.Mkdir(nested, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\naddopts=-q\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "pytest.ini"), []byte("[pytest]\naddopts=-v\n"), 0644))
	for _, option := range []string{"--ignore", "--ignore-glob", "--confcutdir", "--basetemp", "--deselect", "--junitxml", "--junit-xml", "--log-file", "--cov", "--cov-config", "--cov-report"} {
		for _, attached := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/attached=%t", option, attached), func(t *testing.T) {
				value := nested
				if option == "--ignore-glob" {
					value += string(filepath.Separator) + "**"
				}
				if option == "--cov-report" {
					value = "html:" + nested
				}
				args := []string{option, value}
				if attached {
					args = []string{option + "=" + value}
				}
				options, err := PytestConfigOptions(root, root, "", append([]string{"pytest"}, args...))
				require.NoError(t, err)
				require.Equal(t, []string{"-q"}, options)
				_, err = PytestConfigOptions(root, root, "", append(append([]string{"pytest"}, args...), "--", nested))
				require.ErrorContains(t, err, "explicitly with -c", "actual collection paths must still be checked")
			})
		}
	}
}
