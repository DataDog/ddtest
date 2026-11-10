package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonHandWrittenCommandLocalInterpreterSelection(t *testing.T) {
	for _, tc := range []struct {
		name, install, command string
		compatible             bool
	}{
		{"selected pytest", "pip install ddtrace==4.16.0 pytest", "PYENV_VERSION=3.12 pytest --ddtrace tests", false},
		{"second pytest selects interpreter", "pip install ddtrace==4.16.0 pytest", "pytest --ddtrace tests && PYENV_VERSION=3.12 pytest --ddtrace tests", false},
		{"selected installation", "PYENV_VERSION=3.12 pip install ddtrace==4.16.0 pytest", "pytest --ddtrace tests", false},
		{"pytest executable path", "pip install ddtrace==4.16.0 pytest", "PATH=other/bin pytest --ddtrace tests", false},
		{"pytest Python home", "pip install ddtrace==4.16.0 pytest", "PYTHONHOME=other pytest --ddtrace tests", false},
		{"application path", "pip install ddtrace==4.16.0 pytest", "PYTHONPATH=src pytest --ddtrace tests", true},
		{"unrelated command selection is local", "pip install ddtrace==4.16.0 pytest", "PYENV_VERSION=3.12 echo ready && pytest --ddtrace tests", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{tc.install}, tc.command, "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if !tc.compatible {
				for _, finding := range check.Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@")
				}
			}
		})
	}
}

func TestPythonHandWrittenWrappedDependencyOperations(t *testing.T) {
	for _, tc := range []struct {
		name, command, pytest string
		compatible            bool
		tracer                string
	}{
		{"replacement", "cross-env PIP_NO_INPUT=1 pip install ddtrace==4.17.0", "pytest --ddtrace tests", false, "ddtrace@4.17.0"},
		{"shell replacement", "cross-env-shell PIP_NO_INPUT=1 pip install ddtrace==4.17.0", "pytest --ddtrace tests", false, "ddtrace@4.17.0"},
		{"uninstall", "cross-env PIP_NO_INPUT=1 pip uninstall -y ddtrace", "pytest --ddtrace tests", false, ""},
		{"dry run retains environment", "cross-env PIP_DRY_RUN=1 pip install ddtrace==4.17.0", "pytest --ddtrace tests", true, "ddtrace@4.16.0"},
		{"exact install and wrapped test", "cross-env PIP_NO_INPUT=1 pip install ddtrace==4.16.0 pytest", "cross-env MODE=ci python -m pytest --ddtrace tests", true, "ddtrace@4.16.0"},
		{"unreadable inner shell", "cross-env-shell PIP_NO_INPUT=1 'pip install ddtrace==4.17.0'", "pytest --ddtrace tests", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", tc.command}, tc.pytest, "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if tc.tracer != "" {
				require.Equal(t, tc.tracer, check.Jobs[0].Tracer)
			} else {
				for _, finding := range check.Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@")
				}
			}
		})
	}
}

func TestPythonHandWrittenCircleCIConditionalInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, before, condition, command, after string
		compatible                              bool
	}{
		{"unknown install", "", "equal: [ main, << pipeline.git.branch >> ]", "pip install ddtrace==4.16.0 pytest", "", false},
		{"unknown replacement", "      - run: pip install ddtrace==4.16.0 pytest\n", "equal: [ main, << pipeline.git.branch >> ]", "pip install ddtrace==4.17.0", "", false},
		{"true install", "", "true", "pip install ddtrace==4.16.0 pytest", "", true},
		{"false install", "", "false", "pip install ddtrace==4.16.0 pytest", "", false},
		{"unconditional reinstall", "", "equal: [ main, << pipeline.git.branch >> ]", "pip install ddtrace==4.17.0 pytest", "      - run: pip install ddtrace==4.16.0 pytest\n", true},
		{"conditional dry run", "      - run: pip install ddtrace==4.16.0 pytest\n", "equal: [ main, << pipeline.git.branch >> ]", "pip install --dry-run ddtrace==4.17.0", "", true},
		{"conditional unrelated command", "      - run: pip install ddtrace==4.16.0 pytest\n", "equal: [ main, << pipeline.git.branch >> ]", "echo ready", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := handWrittenProviders[1]
			config := provider.config(map[string]string{"DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}, nil, "pytest --ddtrace tests")
			config = strings.Replace(config, "cimg/node:22.11", "cimg/python:3.12", 1)
			conditional := tc.before + "      - when:\n          condition:\n            " + tc.condition + "\n          steps:\n            - run: " + tc.command + "\n" + tc.after
			config = strings.Replace(config, "      - run: pytest", conditional+"      - run: pytest", 1)
			root := unvalidatedRepository(t, provider, config, nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if !tc.compatible {
				for _, finding := range check.Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@")
				}
			}
		})
	}
}

func TestPythonHandWrittenWrapperEnvironmentDoesNotReuseDefaultPin(t *testing.T) {
	for _, command := range []string{
		"uv run --no-sync pytest --ddtrace tests",
		"uv run --no-sync python -m pytest --ddtrace tests",
		"uv run --no-sync --active pytest --ddtrace tests",
		"poetry run pytest --ddtrace tests",
		"poetry run python -m pytest --ddtrace tests",
		"pytest --ddtrace tests && uv run --no-sync pytest --ddtrace tests",
	} {
		t.Run(command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest"}, command, "", nil)
			check := pythonRuntimeCheck(t, root)
			require.NotEqual(t, StatusCompatible, check.Status, "%+v", check)
			setup := handWrittenPythonSetupFromRepository(t, root)
			require.Empty(t, setup.Tracer, "%+v", setup)
			require.False(t, setup.complete(), "%+v", setup)
			for _, finding := range check.Jobs {
				require.NotContains(t, finding.Tracer, "ddtrace@", "%+v", finding)
			}
		})
	}
}

func TestPythonHandWrittenPyenvSelectionInvalidatesInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, selection, command, version string
	}{
		{"global selection", "pyenv global 3.12", "pytest --ddtrace tests", ""},
		{"local selection", "pyenv local 3.12", "pytest --ddtrace tests", ""},
		{"system selection", "pyenv global system", "pytest --ddtrace tests", ""},
		{"unset local", "pyenv local --unset", "pytest --ddtrace tests", ""},
		{"selection in test step", "", "pyenv local 3.12 && pytest --ddtrace tests", ""},
		{"global metadata", "pyenv global", "pytest --ddtrace tests", "4.16.0"},
		{"local metadata", "pyenv local", "pytest --ddtrace tests", "4.16.0"},
		{"selection followed by installation", "pyenv local 3.12 && pip install ddtrace==4.16.0 pytest", "pytest --ddtrace tests", "4.16.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := []string{"pip install ddtrace==4.16.0 pytest"}
			if tc.selection != "" {
				before = append(before, tc.selection)
			}
			root := handWrittenPythonEvidenceRepository(t, before, tc.command, "", nil)
			setup := handWrittenPythonSetupFromRepository(t, root)
			require.Equal(t, tc.version, setup.Tracer, "%+v", setup)
			if tc.version == "" {
				require.False(t, setup.complete(), "%+v", setup)
				check := pythonRuntimeCheck(t, root)
				for _, finding := range check.Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@", "%+v", finding)
				}
			}
		})
	}
}

func handWrittenPythonSetupFromRepository(t *testing.T, root string) *handWrittenSetup {
	t.Helper()
	provider, err := currentCIProvider(root)
	require.NoError(t, err)
	workflows, err := readProviderWorkflows(provider, root)
	require.NoError(t, err)
	workflow := workflows[0]
	job := workflow.Jobs["test"]
	setup := pythonHandWrittenSetup(root, provider, workflow, job, len(job.Steps)-1, "datadoghq.com")
	require.NotNil(t, setup)
	return setup
}

func TestPythonHandWrittenCondaMutationsInvalidateInstallation(t *testing.T) {
	for _, manager := range []string{"conda", "mamba", "micromamba"} {
		for _, command := range []string{"install conda-forge::ddtrace=4.14.0", "update --all", "remove ddtrace", "uninstall ddtrace", "env update -f environment.yml", "--json install ddtrace=4.14.0"} {
			t.Run(manager+" "+command, func(t *testing.T) {
				root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", manager + " " + command}, "pytest --ddtrace tests", "", nil)
				setup := handWrittenPythonSetupFromRepository(t, root)
				require.Empty(t, setup.Tracer, "%+v", setup)
				require.False(t, setup.complete(), "%+v", setup)
				for _, finding := range pythonRuntimeCheck(t, root).Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@", "%+v", finding)
				}
			})
		}
		for _, command := range []string{"list", "info", "--version", "env list", "install --dry-run ddtrace=4.14.0", "install --help", "install ddtrace=4.14.0 && pip install ddtrace==4.16.0 pytest"} {
			t.Run(manager+" "+command, func(t *testing.T) {
				root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", manager + " " + command}, "pytest --ddtrace tests", "", nil)
				setup := handWrittenPythonSetupFromRepository(t, root)
				require.Equal(t, "4.16.0", setup.Tracer, "%+v", setup)
				require.True(t, setup.complete(), "%+v", setup)
			})
		}
	}
}

func TestPythonHandWrittenEnvironmentRecreation(t *testing.T) {
	for _, tc := range []struct {
		name, recreate, after string
		compatible            bool
	}{
		{"cleared venv", "python -m venv --clear venv", "pip install --no-deps pytest", false},
		{"cleared Python3 venv", "python3 -m venv --clear venv", "pip install --no-deps pytest", false},
		{"cleared virtualenv", "python -m virtualenv --clear venv", "pip install --no-deps pytest", false},
		{"cleared standalone virtualenv", "virtualenv --clear venv", "pip install --no-deps pytest", false},
		{"cleared uv venv", "uv venv --clear venv", "pip install --no-deps pytest", false},
		{"exact reinstall", "python -m venv --clear venv", "pip install ddtrace==4.16.0 pytest", true},
		{"reuse without clear", "python -m venv venv", "pip install --no-deps pytest", true},
		{"help does not clear", "python -m venv --help --clear venv", "pip install --no-deps pytest", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := []string{"python -m venv venv", ". venv/bin/activate", "pip install ddtrace==4.16.0 pytest", tc.recreate, tc.after}
			root := handWrittenPythonEvidenceRepository(t, before, "python -m pytest --ddtrace tests", "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if !tc.compatible {
				for _, f := range check.Jobs {
					require.NotContains(t, f.Tracer, "ddtrace@")
				}
			}
		})
	}
}

func TestPythonHandWrittenPipConfigurationDestinations(t *testing.T) {
	for _, tc := range []struct {
		name, config, install string
		compatible            bool
	}{
		{"target", "pip config set global.target vendor", "pip install ddtrace==4.16.0", false},
		{"install target", "python -m pip config --site set install.target vendor", "pip install ddtrace==4.16.0", false},
		{"prefix", "pip config --user set global.prefix other", "pip install ddtrace==4.16.0", false},
		{"user site", "pip config set install.user true", "pip install ddtrace==4.16.0", false},
		{"edit", "pip config edit", "pip install ddtrace==4.16.0", false},
		{"isolated still reads site config", "pip config --site set install.target vendor", "pip --isolated install ddtrace==4.16.0", false},
		{"configuration disabled", "pip config set global.target vendor", "PIP_CONFIG_FILE=/dev/null pip install ddtrace==4.16.0", true},
		{"read get", "pip config get global.target", "pip install ddtrace==4.16.0", true},
		{"read list", "pip config list", "pip install ddtrace==4.16.0", true},
		{"read debug", "pip config debug", "pip install ddtrace==4.16.0", true},
		{"unrelated setting", "pip config set global.timeout 60", "pip install ddtrace==4.16.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install pytest", tc.config, tc.install}, "python -m pytest --ddtrace tests", "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
	root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", "pip config set global.target vendor"}, "python -m pytest --ddtrace tests", "", nil)
	require.Equal(t, StatusCompatible, pythonRuntimeCheck(t, root).Status, "configuration writes do not remove already installed packages")
}

func TestPythonHandWrittenPipConfigurationInstallationOptions(t *testing.T) {
	for _, tc := range []struct {
		name, config, install string
		compatible            bool
	}{
		{"dry run", "pip config set global.dry-run true", "pip install ddtrace==4.16.0", false},
		{"install dry run", "pip config --site set install.dry-run true", "pip install ddtrace==4.16.0", false},
		{"project dependencies disabled", "pip config set global.no-deps true", "pip install .", false},
		{"dependency alias", "pip config set global.no-dependencies true", "pip install .", false},
		{"extra requirements", "pip config set global.requirement later.txt", "pip install ddtrace==4.16.0", false},
		{"extra constraints", "pip config set global.constraint later.txt", "pip install ddtrace==4.16.0", false},
		{"editable selection", "pip config set install.editable other", "pip install ddtrace==4.16.0", false},
		{"group selection", "pip config set install.group tracing", "pip install ddtrace==4.16.0", false},
		{"configuration disabled", "pip config set global.dry-run true", "PIP_CONFIG_FILE=/dev/null pip install ddtrace==4.16.0", true},
		{"query", "pip config get global.dry-run", "pip install ddtrace==4.16.0", true},
		{"unrelated timeout", "pip config set global.timeout 60", "pip install ddtrace==4.16.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"pyproject.toml": "[project]\nname='example'\nversion='1.0'\ndependencies=['ddtrace==4.16.0']\n[build-system]\nrequires=['hatchling']\nbuild-backend='hatchling.build'\n", "later.txt": "ddtrace==4.17.0\n"}
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install pytest", tc.config, tc.install}, "python -m pytest --ddtrace tests", "", files)
			require.Equal(t, tc.compatible, pythonRuntimeCheck(t, root).Status == StatusCompatible)
		})
	}
}

func TestPythonHandWrittenPipSubcommandIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, install string
		compatible    bool
	}{
		{"target ignored", "PIP_TARGET=vendor pip install --isolated ddtrace==4.16.0 pytest", true},
		{"prefix ignored", "PIP_PREFIX=other python -m pip install --isolated ddtrace==4.16.0 pytest", true},
		{"root ignored", "PIP_ROOT=other pip install ddtrace==4.16.0 pytest --isolated", true},
		{"user ignored", "PIP_USER=1 pip install --isolated ddtrace==4.16.0 pytest", true},
		{"interpreter selected before subcommand", "PIP_PYTHON=other pip install --isolated ddtrace==4.16.0 pytest", false},
		{"requirements before isolation", "PIP_TARGET=vendor pip install -r requirements.txt --isolated", true},
		{"log before isolation", "PIP_TARGET=vendor pip install --log install.log --isolated ddtrace==4.16.0", true},
		{"log value is not a flag", "PIP_TARGET=vendor pip install --log --isolated ddtrace==4.16.0", false},
		{"assigned log value is not a flag", "PIP_TARGET=vendor pip install --log=--isolated ddtrace==4.16.0", false},
		{"requirement value is not a flag", "PIP_TARGET=vendor pip install -r --isolated", false},
		{"explicit target persists", "PIP_TARGET=vendor pip install --isolated --target elsewhere ddtrace==4.16.0", false},
		{"custom configuration persists", "PIP_CONFIG_FILE=custom.conf pip install --isolated ddtrace==4.16.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"requirements.txt": "ddtrace==4.16.0\npytest\n", "--isolated": "ddtrace==4.16.0\npytest\n"}
			root := handWrittenPythonEvidenceRepository(t, []string{tc.install}, "python -m pytest --ddtrace tests", "", files)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}
