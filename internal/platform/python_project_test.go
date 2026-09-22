package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonRealProjectConfigurations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pytest bool
	}{
		{"requests-legacy", true}, {"django", false}, {"flask-legacy", true}, {"requests", true}, {"virtualenv", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := detectionFiles(t, filepath.Join("testdata", "detection", tc.name))
			root := t.TempDir()
			t.Setenv("PATH", t.TempDir())
			for name, data := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0644))
			}
			info, err := inspectPythonProject(root)
			require.NoError(t, err)
			require.Equal(t, pythonProject{python: true, pytest: tc.pytest}, info)
			fw, err := NewPython().detectFramework(root, "")
			if tc.pytest {
				require.NoError(t, err)
				require.Equal(t, "pytest", fw.Name())
			} else {
				require.ErrorContains(t, err, "could not detect")
			}
			for name, data := range files {
				t.Run(name, func(t *testing.T) {
					isolated := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(isolated, name), []byte(data), 0644))
					info, err := inspectPythonProject(isolated)
					require.NoError(t, err)
					wantPytest := tc.pytest && (tc.name != "requests-legacy" || name != "setup.cfg")
					require.Equal(t, pythonProject{python: true, pytest: wantPytest}, info)
				})
			}
			require.Equal(t, files, detectionFiles(t, root))
		})
	}
}

func TestPythonConfigurationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, file, contents string
		python, pytest       bool
	}{
		{"unrelated cfg", "setup.cfg", "[server]\nrunner = pytest\n", false, false},
		{"generic metadata", "setup.cfg", "[metadata]\nname = pytest\n", false, false},
		{"commented pytest section", "setup.cfg", "# [tool:pytest]\n[server]\nmessage=pytest\n", false, false},
		{"packaging description", "setup.cfg", "[options]\npackages=find:\n[metadata]\ndescription=pytest integration\n", true, false},
		{"extras dependency", "setup.cfg", "[options.extras_require]\ntest =\n    pytest>=8\n    pytest-cov\n", true, true},
		{"tests_require", "setup.cfg", "[options]\ntests_require =\n    pytest; python_version > '3.9'\n", true, true},
		{"plugin is not runner", "requirements.txt", "pytest-cov\npytest-mock\n", true, false},
		{"requirements comment", "requirements.txt", "# pytest>=8\nrequests # install pytest later\n", true, false},
		{"requirement extras", "requirements-dev.txt", "pytest[testing]>=8\n", true, true},
		{"direct dependency URL", "requirements.txt", "pytest @ https://example.com/pytest.whl\n", true, true},
		{"description is not dependency", "pyproject.toml", "[project]\nname='pytest-helper'\ndescription='pytest'\ndependencies=['pytest-cov']\n", true, false},
		{"comment is not section", "pyproject.toml", "# [tool.pytest.ini_options]\n[tool.ruff]\nline-length=88\n", true, false},
		{"unrelated TOML", "pyproject.toml", "[tool.custom]\nrunner='pytest'\n", false, false},
		{"optional dependencies", "pyproject.toml", "[project.optional-dependencies]\ntest=['pytest>=8']\n", true, true},
		{"dependency groups", "pyproject.toml", "[dependency-groups]\ntest=[{include-group='base'},'pytest>=8']\n", true, true},
		{"group name not dependency", "pyproject.toml", "[dependency-groups]\npytest=['requests']\ntest=[{include-group='pytest'}]\n", true, false},
		{"poetry group", "pyproject.toml", "[tool.poetry.group.test.dependencies]\npytest={version='^8',optional=true}\n", true, true},
		{"pdm dev group", "pyproject.toml", "[tool.pdm.dev-dependencies]\ntest=['pytest>=8']\n", true, true},
		{"native pytest TOML", "pyproject.toml", "[tool.pytest]\nminversion='9.0'\n", true, true},
		{"tox unrelated value", "tox.ini", "[testenv]\ncommands=echo pytest\nsetenv=RUNNER=pytest\n", true, false},
		{"tox module command", "tox.ini", "[testenv:unit]\ncommands=\n    {envpython} -m pytest {posargs}\n", true, true},
		{"tox dependency", "tox.ini", "[testenv]\ndeps=\n    pytest>=8\ncommands=python tests.py\n", true, true},
		{"setup source not executed", "setup.py", "raise RuntimeError('pytest')\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, tc.file), []byte(tc.contents), 0644))
			info, err := inspectPythonProject(root)
			require.NoError(t, err)
			require.Equal(t, pythonProject{python: tc.python, pytest: tc.pytest}, info)
		})
	}
}

func TestPythonMalformedConfiguration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project"), 0644))
	_, err := inspectPythonProject(root)
	require.ErrorContains(t, err, "parse pyproject.toml")
	// Explicit configuration does not require understanding the manifest.
	_, err = NewPython().detectFramework(root, "pytest")
	require.NoError(t, err)
}
