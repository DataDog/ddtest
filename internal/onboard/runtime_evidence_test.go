package onboard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func handWrittenPythonEvidenceRepository(t *testing.T, before []string, command, addopts string, files map[string]string) string {
	t.Helper()
	provider := handWrittenProviders[0]
	env := map[string]string{"DD_API_KEY": "$DD_API_KEY", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	if addopts != "" {
		env["PYTEST_ADDOPTS"] = addopts
	}
	config := strings.Replace(provider.config(env, before, command), "node:22", "python:3.12", 1)
	return unvalidatedRepository(t, provider, config, files)
}

func TestPythonHandWrittenEveryInvocation(t *testing.T) {
	for _, tc := range []struct {
		name, command, addopts string
		compatible             bool
	}{
		{"all explicit", "python -m pytest --ddtrace tests/unit && python -m pytest --ddtrace tests/integration", "", true},
		{"second missing", "python -m pytest --ddtrace tests/unit && python -m pytest tests/integration", "", false},
		{"first missing", "python -m pytest tests/unit && python -m pytest --ddtrace tests/integration", "", false},
		{"inline options do not persist", "PYTEST_ADDOPTS=--ddtrace python -m pytest tests/unit && python -m pytest tests/integration", "", false},
		{"job options reach both", "python -m pytest tests/unit && python -m pytest tests/integration", "--ddtrace", true},
		{"inline application path", "PYTHONPATH=src python -m pytest --ddtrace tests", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest"}, tc.command, tc.addopts, nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
			if tc.compatible {
				for _, f := range check.Jobs {
					require.Empty(t, f.pathEdit)
				}
			}
		})
	}
}

func TestPythonHandWrittenInstallationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     []string
		files      map[string]string
		compatible bool
		tracer     string
	}{
		{"constraints alone", []string{"pip install -c constraints.txt pytest"}, map[string]string{"constraints.txt": "ddtrace==4.16.0\n"}, false, ""},
		{"requirements filename used as constraints", []string{"pip install -c requirements.txt pytest"}, map[string]string{"requirements.txt": "ddtrace==4.16.0\n"}, false, ""},
		{"nested constraints alone", []string{"pip install -r requirements.txt"}, map[string]string{"requirements.txt": "-c constraints.txt\npytest\n", "constraints.txt": "ddtrace==4.16.0\n"}, false, ""},
		{"constrained direct install", []string{"pip install -c constraints.txt ddtrace pytest"}, map[string]string{"constraints.txt": "ddtrace==4.16.0\n"}, true, "ddtrace@4.16.0"},
		{"constrained requirement install", []string{"pip install -r requirements.txt"}, map[string]string{"requirements.txt": "-c constraints.txt\nddtrace\npytest\n", "constraints.txt": "ddtrace==4.16.0\n"}, true, "ddtrace@4.16.0"},
		{"later requirements change", []string{"pip install ddtrace==4.16.0 pytest", "pip install -r requirements.txt"}, map[string]string{"requirements.txt": "ddtrace==4.17.0\n"}, false, "ddtrace@4.17.0"},
		{"later direct change", []string{"pip install -r requirements.txt", "pip install ddtrace==4.16.0"}, map[string]string{"requirements.txt": "ddtrace==4.17.0\n"}, true, "ddtrace@4.16.0"},
		{"uninstalled", []string{"pip install ddtrace==4.16.0 pytest", "pip uninstall -y ddtrace"}, map[string]string{"requirements.txt": "ddtrace==4.16.0\n"}, false, ""},
		{"python uninstall", []string{"pip install ddtrace==4.16.0 pytest", "python -m pip uninstall -y ddtrace"}, nil, false, ""},
		{"uv uninstall", []string{"pip install ddtrace==4.16.0 pytest", "uv pip uninstall ddtrace"}, nil, false, ""},
		{"reinstalled", []string{"pip install ddtrace==4.17.0 pytest", "pip uninstall -y ddtrace", "pip install ddtrace==4.16.0"}, nil, true, "ddtrace@4.16.0"},
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

func TestPythonBuildUsesRepositoryDirectory(t *testing.T) {
	root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest", "python -m build"}, "python -m pytest --ddtrace tests", "", map[string]string{"pyproject.toml": "[build-system]\nrequires=['hatchling']\nbuild-backend='hatchling.build'\n"})
	resolution := resolvePythonStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "python -m build"})
	require.True(t, resolution.Review, "%+v", resolution)
	require.False(t, resolution.Matched, "%+v", resolution)
	require.Contains(t, resolution.Reason, "runs no tests")
	require.Equal(t, StatusCompatible, pythonRuntimeCheck(t, root).Status)
}

func TestJavaScriptEffectiveTestSite(t *testing.T) {
	for _, site := range []string{"datadoghq.eu", "${{ vars.DD_SITE }}", ""} {
		t.Run(site, func(t *testing.T) {
			workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "env: {NODE_OPTIONS:", "env: {DD_SITE: '"+site+"', NODE_OPTIONS:", 1)
			require.Contains(t, workflow, "DD_SITE:")
			root := newJestRepository(t, workflow)
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.16.0", Node: ">=18"}, nil
			}).WithSiteCaveats("datadoghq.com")
			require.Equal(t, StatusCompatible, check.Status, "%+v", check)
			if strings.Contains(site, "${{") {
				require.Empty(t, check.Jobs[0].SetupSite)
				require.Contains(t, strings.Join(CISetupCaveats(&check), "\n"), "could not be resolved")
			} else if site != "" {
				require.Equal(t, site, check.Jobs[0].SetupSite)
				require.Contains(t, strings.Join(CISetupCaveats(&check), "\n"), "reports to datadoghq.eu")
			} else {
				require.Equal(t, defaultDatadogSite, check.Jobs[0].SetupSite)
			}
		})
	}
}

func TestHandWrittenJavaScriptUnknownSiteCaveat(t *testing.T) {
	provider := handWrittenProviders[0]
	env := with(handWrittenAgentless("-r dd-trace/ci/init"), "DD_SITE", "${{ vars.DD_SITE }}")
	root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(env, []string{"npm ci"}, "npx jest"), nil)
	check := handWrittenRuntime(t, root, "jest").WithSiteCaveats("datadoghq.eu")
	require.Equal(t, StatusInconclusive, check.Status)
	require.Empty(t, check.Jobs[0].SetupSite)
	caveats := strings.Join(CISetupCaveats(&check), "\n")
	require.Contains(t, caveats, "could not be resolved")
	require.NotContains(t, caveats, "reports to datadoghq.com")
}

func TestPythonHandWrittenDependencyOrderWithinStep(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		compatible    bool
	}{
		{"install after tests", "python -m pytest --ddtrace && pip install ddtrace==4.16.0", false},
		{"uninstall after tests", "pip install ddtrace==4.16.0 && python -m pytest --ddtrace && pip uninstall -y ddtrace", true},
		{"change between tests", "pip install ddtrace==4.16.0 && python -m pytest --ddtrace && pip install ddtrace==4.17.0 && python -m pytest --ddtrace", false},
		{"install before tests", "pip install ddtrace==4.16.0 && python -m pytest --ddtrace", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := handWrittenPythonEvidenceRepository(t, nil, tc.command, "", nil)
			check := pythonRuntimeCheck(t, root)
			require.Equal(t, tc.compatible, check.Status == StatusCompatible, "%+v", check)
		})
	}
}

func TestPythonHandWrittenEveryInvocationReporting(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, problem string
	}{
		{"agent reporting", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "reports through a Datadog Agent"},
		{"empty key", "DD_API_KEY=", "empty value"},
		{"other site", "DD_SITE=datadoghq.eu", "set DD_SITE=datadoghq.com"},
		{"unknown site", "DD_SITE='$CI_SITE/suffix'", "DD_SITE comes from"},
		{"tracer disabled", "DD_TRACE_ENABLED=false", "turns the tracer off"},
		{"same reporting", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := "python -m pytest --ddtrace tests/unit && " + tc.prefix + " python -m pytest --ddtrace tests/integration"
			root := handWrittenPythonEvidenceRepository(t, []string{"pip install ddtrace==4.16.0 pytest"}, command, "", nil)
			provider, err := currentCIProvider(root)
			require.NoError(t, err)
			workflows, err := readProviderWorkflows(provider, root)
			require.NoError(t, err)
			workflow := workflows[0]
			job := workflow.Jobs["test"]
			setup := pythonHandWrittenSetup(root, provider, workflow, job, len(job.Steps)-1, "datadoghq.com")
			require.NotNil(t, setup)
			if tc.problem == "" {
				require.True(t, setup.complete(), setup.gaps())
			} else {
				require.Contains(t, setup.gaps(), tc.problem)
				require.False(t, setup.complete())
			}
		})
	}
}

func TestGitHubInlineReportingSiteRequiresReview(t *testing.T) {
	for _, command := range []string{
		"DD_SITE=datadoghq.eu npm test",
		"DD_SITE=datadoghq.eu npm run test && DD_SITE=datadoghq.eu npm run test",
		"DD_SITE= npm run test",
		"DD_SITE='${{ vars.DD_SITE }}' npm run test",
		"DD_SITE=datadoghq.eu npm run test && DD_SITE=datadoghq.com npm run test",
		"DD_SITE=datadoghq.eu npm run test && npm run test",
	} {
		t.Run(command, func(t *testing.T) {
			workflow := strings.Replace(fmt.Sprintf(runtimeWorkflow, ""), "run: npm run test", "run: "+command, 1)
			root := newJestRepository(t, workflow)
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.16.0", Node: ">=18"}, nil
			}).WithSiteCaveats("datadoghq.com")
			require.Equal(t, StatusInconclusive, check.Status)
			unresolved := 0
			for _, f := range check.Jobs {
				if f.Code == CodeUnresolvedCommand {
					unresolved++
					if strings.Contains(command, "${{") {
						require.Contains(t, f.Reason, "unresolved CI workflow expression requires review")
					} else {
						require.Contains(t, f.Reason, "command overrides instrumentation or executable selection: DD_SITE=")
					}
					require.Empty(t, f.SetupSite)
					require.Empty(t, f.SetupCaveats)
				} else {
					require.Equal(t, StatusCompatible, f.Status, "%+v", f)
					require.Equal(t, defaultDatadogSite, f.SetupSite)
				}
			}
			require.Equal(t, 4, unresolved, "each matrix row requires review of the inline override")
		})
	}
}
