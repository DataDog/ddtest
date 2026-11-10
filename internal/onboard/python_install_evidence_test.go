package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonHandWrittenPipOperationsReflectInstalledPackages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     []string
		files      map[string]string
		compatible bool
		tracer     string
	}{
		{"dry run installs nothing", []string{"pip install --dry-run ddtrace==4.16.0 pytest"}, nil, false, ""},
		{"dry run preserves earlier install", []string{"pip install ddtrace==4.16.0 pytest", "pip install --dry-run ddtrace==4.17.0"}, nil, true, "ddtrace@4.16.0"},
		{"dry run cannot replace earlier pin", []string{"pip install ddtrace==4.17.0 pytest", "pip install --dry-run ddtrace==4.16.0"}, nil, false, "ddtrace@4.17.0"},
		{"requirements uninstall", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall -y -r removed.txt"}, map[string]string{"removed.txt": "ddtrace\n"}, false, ""},
		{"nested uninstall", []string{"pip install ddtrace==4.16.0 pytest", "python -m pip uninstall --yes --requirement=removed.txt"}, map[string]string{"removed.txt": "-r nested.txt\n", "nested.txt": "ddtrace==4.16.0\n"}, false, ""},
		{"missing uninstall file", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall -y -r absent.txt"}, nil, false, ""},
		{"unrelated uninstall", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall -y -r removed.txt"}, map[string]string{"removed.txt": "requests\n"}, true, "ddtrace@4.16.0"},
		{"reinstall after requirements uninstall", []string{"pip install ddtrace==4.17.0 pytest", "pip uninstall -y -r removed.txt", "pip install ddtrace==4.16.0"}, map[string]string{"removed.txt": "ddtrace\n"}, true, "ddtrace@4.16.0"},
		{"alternate target", []string{"pip install pytest", "pip install --target vendor ddtrace==4.16.0"}, nil, false, ""},
		{"alternate prefix", []string{"pip install pytest", "pip install --prefix=vendor ddtrace==4.16.0"}, nil, false, ""},
		{"dry run target preserves pin", []string{"pip install ddtrace==4.16.0 pytest", "pip install --target vendor --dry-run ddtrace==4.17.0"}, nil, true, "ddtrace@4.16.0"},
		{"option value is not a dry run", []string{"pip install --log --dry-run ddtrace==4.16.0 pytest"}, nil, true, "ddtrace@4.16.0"},
		{"uninstall option value is not a package", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall --log ddtrace -y requests"}, nil, true, "ddtrace@4.16.0"},
		{"dynamic uninstall is unresolved", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall -y '$PACKAGE'"}, nil, false, ""},
		{"compact target", []string{"pip install pytest", "pip install -tvendor ddtrace==4.16.0"}, nil, false, ""},
		{"alternate root", []string{"pip install pytest", "pip install --root vendor ddtrace==4.16.0"}, nil, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, "python -m pytest --ddtrace tests", "", tc.files)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if tc.tracer != "" {
				require.Equal(t, tc.tracer, check.Jobs[0].Tracer)
			}
		})
	}
}

func TestPythonHandWrittenProjectInstallSelectsDependencies(t *testing.T) {
	const project = "[build-system]\nrequires=['hatchling']\nbuild-backend='hatchling.build'\n[project]\nname='app'\nversion='1.0.0'\n"
	for _, tc := range []struct {
		name, command, metadata string
		compatible              bool
	}{
		{"unselected extra", "pip install . pytest", "[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n", false},
		{"selected extra", "pip install '.[tracing]' pytest", "[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n", true},
		{"unselected editable extra", "pip install -e . pytest", "[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n", false},
		{"mandatory dependency", "pip install . pytest", "dependencies=['ddtrace==4.16.0']\n", true},
		{"unselected group", "pip install . pytest", "[dependency-groups]\ntracing=['ddtrace==4.16.0']\n", false},
		{"compact editable extra", "pip install -e'.[tracing]' pytest", "[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n", true},
		{"no dependencies", "pip install --no-deps . pytest", "dependencies=['ddtrace==4.16.0']\n", false},
		{"selected normalized extra", "pip install '.[Trace_Test]' pytest", "[project.optional-dependencies]\ntrace-test=['ddtrace==4.16.0']\n", true},
		{"dynamic dependencies", "pip install . pytest", "dynamic=['dependencies']\n", false},
		{"included group", "pip install --group tracing pytest", "[dependency-groups]\nbase=['ddtrace==4.16.0']\ntracing=[{include-group='base'}]\n", true},
		{"group cycle is unresolved", "pip install --group tracing pytest", "[dependency-groups]\ntracing=[{include-group='tracing'}]\n", false},
		{"explicit group file", "pip install --group=pyproject.toml:tracing pytest", "[dependency-groups]\ntracing=['ddtrace==4.16.0']\n", true},
		{"selected group", "pip install --group tracing pytest", "[dependency-groups]\ntracing=['ddtrace==4.16.0']\n", true},
		{"group does not select project dependencies", "pip install --group tracing pytest", "dependencies=['ddtrace==4.16.0']\n[dependency-groups]\ntracing=['pytest']\n", false},
		{"dry run does not install project declaration", "pip install --dry-run . pytest", "dependencies=['ddtrace==4.16.0']\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{tc.command}, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": project + tc.metadata})
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenDoesNotInferUninstalledDeclarations(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"requirements":   {"requirements.txt": "ddtrace==4.16.0\n"},
		"optional extra": {"pyproject.toml": "[project]\nname='app'\n[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n"},
		"lockfile":       {"uv.lock": "version=1\n[[package]]\nname='ddtrace'\nversion='4.16.0'\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, nil, "python -m pytest --ddtrace tests", "", files)
			check := pythonRuntimeCheck(t, root)
			require.NotEqual(t, StatusCompatible, check.Status, "%+v", check)
			require.NotContains(t, check.Jobs[0].Tracer, "ddtrace@")
			root = handWrittenPythonEvidenceRepository(t, nil, "make test", "--ddtrace", files)
			provider, err := currentCIProvider(root)
			require.NoError(t, err)
			workflows, err := readProviderWorkflows(provider, root)
			require.NoError(t, err)
			workflow := workflows[0]
			job := workflow.Jobs["test"]
			setup := pythonHandWrittenSetup(root, provider, workflow, job, len(job.Steps)-1, "datadoghq.com")
			require.NotNil(t, setup)
			require.Empty(t, setup.Tracer, "a wrapper must not infer a declared tracer either")
		})
	}
}

func TestPythonHandWrittenEnvironmentSwitchInvalidatesInstallation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     []string
		command    string
		compatible bool
	}{
		{"fresh environment", []string{"pip install ddtrace==4.16.0 pytest", "python -m venv venv", "source venv/bin/activate", "pip install pytest"}, "python -m pytest --ddtrace tests", false},
		{"fresh environment same step", []string{"pip install ddtrace==4.16.0 pytest", "python -m venv venv"}, "source venv/bin/activate && pip install pytest && python -m pytest --ddtrace tests", false},
		{"install after activation", []string{"python -m venv venv", "source venv/bin/activate", "pip install ddtrace==4.16.0 pytest"}, "python -m pytest --ddtrace tests", true},
		{"deactivated environment", []string{"python -m venv venv", "source venv/bin/activate", "pip install ddtrace==4.16.0 pytest", "deactivate"}, "python -m pytest --ddtrace tests", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, tc.command, "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenUVSyncNeedsSelectedDependencyEvidence(t *testing.T) {
	files := map[string]string{
		"pyproject.toml": "[project]\nname='app'\nversion='1.0.0'\ndependencies=['pytest']\n[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n",
		"uv.lock":        "version=1\n[[package]]\nname='ddtrace'\nversion='4.16.0'\n",
	}
	for _, tc := range []struct {
		before  []string
		command string
	}{
		{[]string{"uv sync"}, "python -m pytest --ddtrace tests"},
		{nil, "uv run python -m pytest --ddtrace tests"},
		{[]string{"pip install ddtrace==4.16.0 pytest", "uv sync"}, "python -m pytest --ddtrace tests"},
	} {
		t.Run(tc.command+" after "+strings.Join(tc.before, "; "), func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, tc.command, "", files)
			check := pythonRuntimeCheck(t, root)
			require.NotEqual(t, StatusCompatible, check.Status, "%+v", check)
			require.NotContains(t, check.Jobs[0].Tracer, "ddtrace@")
		})
	}
}

func TestPythonHandWrittenSharedRequirementsAreNotCycles(t *testing.T) {
	for _, tc := range []struct {
		name, dev  string
		compatible bool
	}{
		{"shared include", "-r requirements.txt\npytest\n", true},
		{"recursion", "-r requirements-dev.txt\n", false},
		{"conflicting pin", "-r requirements.txt\nddtrace==4.17.0\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install -r requirements.txt -r requirements-dev.txt"}, "python -m pytest --ddtrace tests", "", map[string]string{"requirements.txt": "ddtrace==4.16.0\n", "requirements-dev.txt": tc.dev})
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenLaterInstallsCannotPreserveUnresolvedDependencies(t *testing.T) {
	for _, tc := range []struct {
		command    string
		compatible bool
	}{
		{"pip install some-package", false},
		{"pip install -r later.txt", false},
		{"pip install --no-deps some-package", true},
		{"pip install --no-deps -r later.txt", true},
		{"pip install --dry-run some-package", true},
		{"pip --version", true},
		{"PIP_TARGET=vendor pip --version", true},
		{"pip --python other-venv install --dry-run ddtrace==4.17.0", true},
		{"pip install ddtrace==4.16.0 some-package", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", tc.command}, "python -m pytest --ddtrace tests", "", map[string]string{"later.txt": "some-package\n"})
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenPipScopeMustMatchPytest(t *testing.T) {
	for _, tc := range []struct {
		command    string
		compatible bool
	}{
		{"pip --python other-venv install ddtrace==4.16.0", false},
		{"pip --python=other-venv install ddtrace==4.16.0", false},
		{"python -m pip --python other-venv install ddtrace==4.16.0", false},
		{"other-venv/bin/pip install ddtrace==4.16.0", false},
		{"pip3.11 install ddtrace==4.16.0", false},
		{"python3.11 -m pip install ddtrace==4.16.0", false},
		{"uv pip install ddtrace==4.16.0", false},
		{"other-venv/bin/python -m pip install ddtrace==4.16.0", false},
		{"PIP_TARGET=vendor pip install ddtrace==4.16.0", false},
		{"PIP_PREFIX=vendor pip install ddtrace==4.16.0", false},
		{"PIP_ROOT=vendor pip install ddtrace==4.16.0", false},
		{"PIP_USER=1 pip install ddtrace==4.16.0", false},
		{"PIP_USER=0 pip install ddtrace==4.16.0", true},
		{"PIP_PYTHON=other-venv pip install ddtrace==4.16.0", false},
		{"pip install ddtrace==4.16.0", true},
		{"python3 -m pip install ddtrace==4.16.0", true},
		{"pip3 install ddtrace==4.16.0", true},
		{"PIP_TARGET= pip install ddtrace==4.16.0", true},
		{"PIP_NO_INPUT=1 pip install ddtrace==4.16.0", true},
		{"pip --log --python install ddtrace==4.16.0", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install pytest", tc.command}, "python -m pytest --ddtrace tests", "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenPipEnvironmentOptions(t *testing.T) {
	const project = "[build-system]\nrequires=['hatchling']\nbuild-backend='hatchling.build'\n[project]\nname='app'\nversion='1.0.0'\ndependencies=['ddtrace==4.16.0']\n"
	for _, tc := range []struct {
		name       string
		before     []string
		compatible bool
	}{
		{"dry run installs nothing", []string{"PIP_DRY_RUN=1 pip install ddtrace==4.16.0 pytest"}, false},
		{"dry run preserves pin", []string{"pip install ddtrace==4.16.0 pytest", "PIP_DRY_RUN=yes pip install ddtrace==4.17.0"}, true},
		{"false dry run installs", []string{"PIP_DRY_RUN=false pip install ddtrace==4.16.0 pytest"}, true},
		{"empty dry run installs", []string{"PIP_DRY_RUN= pip install ddtrace==4.16.0 pytest"}, true},
		{"global isolated ignores dry run", []string{"pip install ddtrace==4.16.0 pytest", "PIP_DRY_RUN=1 pip --isolated install ddtrace==4.17.0"}, false},
		{"command isolated ignores dry run", []string{"pip install ddtrace==4.16.0 pytest", "PIP_DRY_RUN=1 pip install --isolated ddtrace==4.17.0"}, false},
		{"unknown dry run", []string{"PIP_DRY_RUN='$DRY_RUN' pip install ddtrace==4.16.0 pytest"}, false},
		{"no project dependencies", []string{"PIP_NO_DEPS=1 pip install . pytest"}, false},
		{"no selected extra dependencies", []string{"PIP_NO_DEPS=on pip install '.[tracing]' pytest"}, false},
		{"false no deps installs project dependencies", []string{"PIP_NO_DEPS=off pip install . pytest"}, true},
		{"no deps direct pin", []string{"PIP_NO_DEPS=true pip install ddtrace==4.16.0 pytest"}, true},
		{"no deps preserves unrelated named install", []string{"pip install ddtrace==4.16.0 pytest", "PIP_NO_DEPS=1 pip install some-package"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": project + "[project.optional-dependencies]\ntracing=['ddtrace==4.16.0']\n"})
			require.Equal(t, tc.compatible, pythonRuntimeCheck(t, root).Status == StatusCompatible)
		})
	}
	for _, name := range []string{"PIP_DRY_RUN", "PIP_NO_DEPS"} {
		t.Run("job "+name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install . pytest"}, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": project})
			file := ".gitlab-ci.yml"
			data, err := os.ReadFile(filepath.Join(root, file))
			require.NoError(t, err)
			writeFile(t, root, file, strings.Replace(string(data), "variables:", "variables:\n    "+name+": '1'", 1))
			require.NotEqual(t, StatusCompatible, pythonRuntimeCheck(t, root).Status)
		})
	}
}

func TestPythonHandWrittenUnknownArtifactsInvalidatePins(t *testing.T) {
	for _, command := range []string{
		"pip install --no-deps ddtrace-4.17.0-py3-none-any.whl",
		"pip install --no-deps ./ddtrace-4.17.0-py3-none-any.whl",
		"pip install --no-deps https://example.test/ddtrace-4.17.0-py3-none-any.whl",
		"pip install --no-deps -r later.txt",
		"PIP_NO_DEPS=1 pip install ddtrace-4.17.0-py3-none-any.whl",
	} {
		t.Run(command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", command}, "python -m pytest --ddtrace tests", "", map[string]string{"later.txt": "ddtrace-4.17.0-py3-none-any.whl\n"})
			check := pythonRuntimeCheck(t, root)
			require.NotEqual(t, StatusCompatible, check.Status, "%+v", check)
			require.NotContains(t, check.Jobs[0].Tracer, "ddtrace@")
		})
	}
}

func TestPythonGitHubHandWrittenRequiresProviderSetup(t *testing.T) {
	for _, tc := range []struct {
		name, condition, reinstall string
	}{
		{"changed interpreter", "", ""},
		{"install after selection", "", "      - run: pip install ddtrace==4.16.0 pytest\n"},
		{"skipped selection", "        if: false\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonWorkflow(t, `jobs:
  test:
    runs-on: ubuntu-latest
    env:
      DD_API_KEY: ${{ secrets.DD_API_KEY }}
      DD_CIVISIBILITY_AGENTLESS_ENABLED: 'true'
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.11'}
      - run: pip install ddtrace==4.16.0 pytest
      - uses: actions/setup-python@v6
`+tc.condition+`        with: {python-version: '3.12'}
`+tc.reinstall+`      - run: python -m pytest --ddtrace
`)
			t.Chdir(root)
			useCIProvider(t, "github")
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, StatusIncompatible, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Equal(t, CodeMissingInstrumentation, check.Jobs[0].Code)
			require.Contains(t, check.Jobs[0].Reason, "No Datadog Python action")
		})
	}
}

func TestPythonHandWrittenUnparsedInstallCannotPreservePin(t *testing.T) {
	project := "[build-system]\nrequires=['hatchling']\nbuild-backend='hatchling.build'\n[project]\nname='app'\nversion='1.0.0'\n[project.optional-dependencies]\ntracing=['ddtrace==4.17.0']\n"
	for _, tc := range []struct {
		command string
		tracer  string
	}{
		{"pip install .[tracing]", "ddtrace@4.17.0"},
		{"python -m pip install .[tracing]", "ddtrace@4.17.0"},
		{"pip --no-input install .[tracing]", ""},
		{"pip install dist/*.whl", ""},
		{"if true; then pip install ddtrace==4.17.0; fi", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", tc.command}, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": project})
			check := pythonRuntimeCheck(t, root)
			require.NotEqual(t, StatusCompatible, check.Status, "%+v", check)
			if tc.tracer != "" {
				require.Equal(t, tc.tracer, check.Jobs[0].Tracer)
			} else {
				for _, finding := range check.Jobs {
					require.NotContains(t, finding.Tracer, "ddtrace@")
				}
			}
		})
	}
	// The same parser must accept an extras installation before pytest in
	// a single step, and retain the observed replacement release.
	root := handWrittenPythonEvidenceRepository(t, nil, "pip install .[tracing] && python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": strings.ReplaceAll(project, "4.17.0", "4.16.0")})
	require.Equal(t, StatusCompatible, pythonRuntimeCheck(t, root).Status)
}

func TestPythonHandWrittenPipDependencyEnvironmentDoesNotCertifyInstallation(t *testing.T) {
	const project = "[project]\nname='app'\nversion='1.0.0'\ndependencies=['ddtrace==4.16.0']\n[dependency-groups]\ntracing=['ddtrace==4.17.0']\n"
	for _, tc := range []struct {
		name           string
		before         []string
		jobEnvironment string
		compatible     bool
	}{
		{"dependency alias skips project tracer", []string{"PIP_NO_DEPENDENCIES=1 pip install . pytest"}, "", false},
		{"job dependency alias skips project tracer", []string{"pip install . pytest"}, "PIP_NO_DEPENDENCIES: '1'", false},
		{"group can replace earlier tracer", []string{"pip install ddtrace==4.16.0 pytest", "PIP_GROUP=tracing pip install --no-deps requests"}, "", false},
		{"job group can replace earlier tracer", []string{"pip install ddtrace==4.16.0 pytest", "pip install --no-deps requests"}, "PIP_GROUP: tracing", false},
		{"empty dependency alias", []string{"PIP_NO_DEPENDENCIES= pip install . pytest"}, "", true},
		{"empty group preserves pin", []string{"pip install ddtrace==4.16.0 pytest", "PIP_GROUP= pip install --no-deps requests"}, "", true},
		{"dry run alias preserves pin", []string{"pip install ddtrace==4.16.0 pytest", "PIP_NO_DEPENDENCIES=1 pip install --dry-run ."}, "", true},
		{"environment dry run group preserves pin", []string{"pip install ddtrace==4.16.0 pytest", "PIP_GROUP=tracing PIP_DRY_RUN=1 pip install --no-deps requests"}, "", true},
		{"global isolation ignores dependency alias", []string{"PIP_NO_DEPENDENCIES=1 pip --isolated install . pytest"}, "", true},
		{"command isolation ignores dependency alias", []string{"PIP_NO_DEPENDENCIES=1 pip install --isolated . pytest"}, "", true},
		{"global isolation ignores group", []string{"pip install ddtrace==4.16.0 pytest", "PIP_GROUP=tracing pip --isolated install --no-deps requests"}, "", true},
		{"command isolation ignores group", []string{"pip install ddtrace==4.16.0 pytest", "PIP_GROUP=tracing pip install --isolated --no-deps requests"}, "", true},
		{"exact reinstall after group", []string{"PIP_GROUP=tracing pip install --no-deps requests", "pip install ddtrace==4.16.0 pytest"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": project})
			if tc.jobEnvironment != "" {
				data, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
				require.NoError(t, err)
				writeFile(t, root, ".gitlab-ci.yml", strings.Replace(string(data), "variables:", "variables:\n    "+tc.jobEnvironment, 1))
			}
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if tc.compatible {
				require.Equal(t, "ddtrace@4.16.0", check.Jobs[0].Tracer)
			} else {
				require.NotContains(t, check.Jobs[0].Tracer, "ddtrace@")
			}
		})
	}
}

func TestPythonHandWrittenPipEditableEnvironmentCannotPreservePin(t *testing.T) {
	for _, tc := range []struct {
		command    string
		compatible bool
	}{
		{"PIP_EDITABLE=./ddtrace-fork pip install --no-deps requests", false},
		{"PIP_EDITABLE=./ddtrace-fork pip --isolated install --no-deps requests", true},
		{"PIP_EDITABLE=./ddtrace-fork pip install --isolated --no-deps requests", true},
		{"PIP_EDITABLE= pip install --no-deps requests", true},
		{"PIP_EDITABLE=./ddtrace-fork pip install --dry-run --no-deps requests", true},
		{"PIP_EDITABLE=./ddtrace-fork PIP_DRY_RUN=1 pip install --no-deps requests", true},
		{"PIP_EDITABLE=./ddtrace-fork pip install --no-deps requests && pip install ddtrace==4.16.0", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			files := map[string]string{"ddtrace-fork/pyproject.toml": "[project]\nname='ddtrace'\nversion='4.17.0'\n"}
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", tc.command}, "python -m pytest --ddtrace tests", "", files)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if !tc.compatible {
				require.NotContains(t, check.Jobs[0].Tracer, "ddtrace@")
			}
		})
	}
}

func TestPythonHandWrittenPipRequirementsEnvironmentCannotPreservePin(t *testing.T) {
	for _, tc := range []struct {
		command    string
		compatible bool
	}{
		{"PIP_REQUIREMENT=later.txt pip install --no-deps requests", false},
		{"PIP_REQUIREMENT=later.txt pip uninstall -y requests", false},
		{"PIP_REQUIREMENT=later.txt PIP_DRY_RUN=1 pip install requests", true},
		{"PIP_REQUIREMENT=later.txt pip install --dry-run requests", true},
		{"PIP_REQUIREMENT=later.txt pip --isolated install --no-deps requests", true},
		{"PIP_REQUIREMENT=later.txt pip install --isolated --no-deps requests", true},
		{"PIP_REQUIREMENT=later.txt pip --isolated uninstall -y requests", true},
		{"PIP_REQUIREMENT=later.txt pip uninstall --isolated -y requests", true},
		{"PIP_REQUIREMENT= pip install --no-deps requests", true},
		{"PIP_REQUIREMENT=later.txt pip --version", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", tc.command}, "python -m pytest --ddtrace tests", "", map[string]string{"later.txt": "ddtrace==4.17.0\n"})
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenUVPipSyncInvalidatesInstallation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     []string
		compatible bool
	}{
		{"system synchronization", []string{"pip install ddtrace==4.16.0 pytest", "uv pip sync --system later.txt"}, false},
		{"activated environment synchronization", []string{"python -m venv venv", "source venv/bin/activate", "pip install ddtrace==4.16.0 pytest", "uv pip sync later.txt"}, false},
		{"install after synchronization", []string{"pip install ddtrace==4.16.0 pytest", "uv pip sync --system later.txt", "pip install ddtrace==4.16.0 pytest"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, tc.before, "python -m pytest --ddtrace tests", "", map[string]string{"later.txt": "pytest\n"})
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
