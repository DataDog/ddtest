package onboard

import (
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
