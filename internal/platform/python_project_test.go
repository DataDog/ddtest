package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonRealProjectConfigurations(t *testing.T) {
	for _, name := range []string{"requests-legacy", "django", "flask-legacy", "requests", "virtualenv"} {
		t.Run(name, func(t *testing.T) {
			files := detectionFiles(t, filepath.Join("testdata", "detection", name))
			root := t.TempDir()
			t.Setenv("PATH", t.TempDir())
			for name, data := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0644))
			}
			found, err := NewPython().Detect(root)
			require.NoError(t, err)
			require.True(t, found)
			fw, err := NewPython().DetectFramework(root, "")
			require.NoError(t, err)
			require.Equal(t, "pytest", fw.Name())
			for name, data := range files {
				t.Run(name, func(t *testing.T) {
					isolated := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(isolated, name), []byte(data), 0644))
					found, err := NewPython().Detect(isolated)
					require.NoError(t, err)
					require.True(t, found)
				})
			}
			require.Equal(t, files, detectionFiles(t, root))
		})
	}
}

func TestPythonConfigurationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, file, contents string
		python               bool
	}{
		{"unrelated cfg", "setup.cfg", "[server]\nrunner = pytest\n", false},
		{"generic metadata", "setup.cfg", "[metadata]\nname = pytest\n", false},
		{"commented pytest section", "setup.cfg", "# [tool:pytest]\n[server]\nmessage=pytest\n", false},
		{"packaging description", "setup.cfg", "[options]\npackages=find:\n[metadata]\ndescription=pytest integration\n", true},
		{"extras dependency", "setup.cfg", "[options.extras_require]\ntest =\n    pytest>=8\n    pytest-cov\n", true},
		{"tests_require", "setup.cfg", "[options]\ntests_require =\n    pytest; python_version > '3.9'\n", true},
		{"plugin is not runner", "requirements.txt", "pytest-cov\npytest-mock\n", true},
		{"requirements comment", "requirements.txt", "# pytest>=8\nrequests # install pytest later\n", true},
		{"requirement extras", "requirements-dev.txt", "pytest[testing]>=8\n", false},
		{"direct dependency URL", "requirements.txt", "pytest @ https://example.com/pytest.whl\n", true},
		{"description is not dependency", "pyproject.toml", "[project]\nname='pytest-helper'\ndescription='pytest'\ndependencies=['pytest-cov']\n", true},
		{"comment is not section", "pyproject.toml", "# [tool.pytest.ini_options]\n[tool.ruff]\nline-length=88\n", true},
		{"unrelated TOML", "pyproject.toml", "[tool.custom]\nrunner='pytest'\n", true},
		{"optional dependencies", "pyproject.toml", "[project.optional-dependencies]\ntest=['pytest>=8']\n", true},
		{"dependency groups", "pyproject.toml", "[dependency-groups]\ntest=[{include-group='base'},'pytest>=8']\n", true},
		{"group name not dependency", "pyproject.toml", "[dependency-groups]\npytest=['requests']\ntest=[{include-group='pytest'}]\n", true},
		{"poetry group", "pyproject.toml", "[tool.poetry.group.test.dependencies]\npytest={version='^8',optional=true}\n", true},
		{"pdm dev group", "pyproject.toml", "[tool.pdm.dev-dependencies]\ntest=['pytest>=8']\n", true},
		{"native pytest TOML", "pyproject.toml", "[tool.pytest]\nminversion='9.0'\n", true},
		{"tox unrelated value", "tox.ini", "[testenv]\ncommands=echo pytest\nsetenv=RUNNER=pytest\n", true},
		{"tox module command", "tox.ini", "[testenv:unit]\ncommands=\n    {envpython} -m pytest {posargs}\n", true},
		{"tox dependency", "tox.ini", "[testenv]\ndeps=\n    pytest>=8\ncommands=python tests.py\n", true},
		{"setup source not executed", "setup.py", "raise RuntimeError('pytest')\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, tc.file), []byte(tc.contents), 0644))
			found, err := NewPython().Detect(root)
			require.NoError(t, err)
			require.Equal(t, tc.python, found)
			_, err = NewPython().DetectFramework(root, "pytest")
			require.NoError(t, err)
		})
	}
}

func TestPythonDefaultFrameworkDoesNotInspectConfiguration(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project"), 0644))
	found, err := NewPython().Detect(root)
	require.NoError(t, err)
	require.True(t, found)
	fw, err := NewPython().DetectFramework(root, "")
	require.NoError(t, err)
	require.Equal(t, "pytest", fw.Name())
}
