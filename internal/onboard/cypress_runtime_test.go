package onboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCypressScenariosFollowDeclaredEvents(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "--initial-branch=juan-fernandez/cypress-ci-context"}, {"remote", "add", "origin", "https://github.com/cypress-io/example.git"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	for event, count := range map[string]int{"push": 2, "pull_request": 3, "workflow_dispatch": 2} {
		jobs, err := cypressContextJobs(root, ciWorkflow{On: event}, runtimeJob{})
		require.NoError(t, err)
		require.Len(t, jobs, count)
	}
	_, err := cypressContextJobs(root, ciWorkflow{On: "pull_request_target"}, runtimeJob{})
	require.ErrorContains(t, err, "workflow event")
	// Repository env cannot impersonate the synthetic github context.
	job := runtimeJob{Context: map[string]string{"github.repository_owner": "cypress-io"}}
	env := conditionEnvironment(ciWorkflow{Env: map[string]string{"github.repository_owner": "different"}}, job, runtimeStep{}, nil)
	active, err := runtimeCondition("github.repository_owner == 'cypress-io'", nil, env)
	require.NoError(t, err)
	require.True(t, active)
}

func TestCypressForkCannotInstallWithUnavailableCredentials(t *testing.T) {
	job := runtimeJob{Context: map[string]string{"scenario": "fork_pull_request / key absent"}}
	job.Steps = []runtimeStep{{Uses: "datadog/test-visibility-github-action@v3", With: map[string]string{"languages": "js", "api_key": "${{ secrets.DD_API_KEY }}"}}}
	findings := checkRuntimeJob(t.Context(), "", ciWorkflow{}, "tests", job, []commandResolution{{}}, nil, nil, nil, "cypress")
	require.Len(t, findings, 1)
	require.Equal(t, "incompatible", findings[0].Status)
	require.Equal(t, "missing_fork_guard", findings[0].Code)
}

func TestCypressRejectsDirectSecretConditionEvenWithKnownContext(t *testing.T) {
	job := runtimeJob{Context: map[string]string{"secrets.DD_API_KEY": "present", "scenario": "push"}}
	job.Steps = []runtimeStep{{Uses: "datadog/test-visibility-github-action@v3", If: "${{ secrets.DD_API_KEY != '' }}", With: map[string]string{"languages": "js"}}}
	findings := checkRuntimeJob(t.Context(), "", ciWorkflow{}, "tests", job, []commandResolution{{}}, nil, nil, nil, "cypress")
	require.Len(t, findings, 1)
	require.Equal(t, StatusInconclusive, findings[0].Status)
	require.Equal(t, CodeUnresolvedCondition, findings[0].Code)
	require.Contains(t, findings[0].Reason, "job-level environment variable")
	for _, expression := range []string{"secrets.DD_API_KEY != ''", "false && secrets.DD_API_KEY != ''"} {
		_, err := runtimeCondition(expression, nil, map[string]string{"\x00secrets.DD_API_KEY": "present"})
		require.ErrorContains(t, err, "secrets context in if")
	}
}

func TestCypressActionRuntimeIsKnownBeforeTracerInstallation(t *testing.T) {
	job := runtimeJob{Steps: []runtimeStep{
		{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "24"}},
		{Uses: "cypress-io/github-action@v6", ActionNode: "20"},
	}}
	resolutions := []commandResolution{{}, {Matched: true, Tests: []TestCommand{{Cypress: &CypressCommand{Action: "cypress-io/github-action@v6"}}}}}
	findings := checkRuntimeJob(t.Context(), "", ciWorkflow{}, "test", job, resolutions, nil, nil, nil, "cypress")
	require.Len(t, findings, 1)
	require.Equal(t, "missing_instrumentation", findings[0].Code)
	require.Equal(t, "20", findings[0].Node, "use the action runtime when choosing a tracer before CI setup")
}

func TestCypressNodeVersionFileAndPrecedence(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".node-version"), []byte("24\n"), 0600))
	step := runtimeStep{With: map[string]string{"node-version-file": ".node-version"}}
	version, err := setupNodeVersion(root, step, nil)
	require.NoError(t, err)
	require.Equal(t, "24", version)
	step.With["node-version"] = "22"
	version, err = setupNodeVersion(root, step, nil)
	require.NoError(t, err)
	require.Equal(t, "22", version)
	delete(step.With, "node-version")
	step.With["node-version-file"] = "../outside"
	_, err = setupNodeVersion(root, step, nil)
	require.Error(t, err)
	outside := filepath.Join(t.TempDir(), "node")
	require.NoError(t, os.WriteFile(outside, []byte("24"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	step.With["node-version-file"] = "escape"
	_, err = setupNodeVersion(root, step, nil)
	require.ErrorContains(t, err, "escapes")
	require.Equal(t, "24.21.0", cypressContainerNode("cypress/browsers:24.21.0"))
	require.Empty(t, cypressContainerNode("some/image:24.21.0"))
}

func TestCypressCIPathsCheckConditionalInstallationAndActionRuntime(t *testing.T) {
	for _, key := range []string{"present", ""} {
		for _, conditionalPreload := range []bool{true, false} {
			t.Run(key+map[bool]string{true: " conditional", false: " unconditional"}[conditionalPreload], func(t *testing.T) {
				job := runtimeJob{Context: map[string]string{
					"github.repository_owner": "cypress-io", "github.repository": "cypress-io/example",
					"github.event_name": "push", "github.event.pull_request.head.repo.full_name": "cypress-io/example",
					"secrets.DD_API_KEY": key, "scenario": "test",
				}}
				workflow := ciWorkflow{Path: "ci.yml", Env: map[string]string{"DD_API_KEY": "${{ secrets.DD_API_KEY }}"}}
				options := "-r ${{ env.DD_TRACE_PACKAGE }}"
				if conditionalPreload {
					options = "${{ steps.dd.outcome == 'success' && format('-r {0}', env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}"
				}
				job.Steps = []runtimeStep{
					{Uses: "actions/setup-node@v7", With: map[string]string{"node-version": "22"}},
					{ID: "dd", Uses: "datadog/test-visibility-github-action@v3", If: "github.repository_owner == 'cypress-io' && env.DD_API_KEY != ''", With: map[string]string{"languages": "js", "js-tracer-version": "6.21.0"}},
					{Uses: "cypress-io/github-action@v7", ActionNode: "24", Env: map[string]string{"NODE_OPTIONS": options}},
				}
				resolutions := []commandResolution{{}, {}, {Matched: true, Tests: []TestCommand{{Cypress: &CypressCommand{Action: "cypress-io/github-action@v7"}}}}}
				resolve := func(context.Context, string, string) (tracerRequirement, error) {
					return tracerRequirement{Version: "6.21.0", Node: ">=22"}, nil
				}
				findings := checkRuntimeJob(t.Context(), "", workflow, "test", job, resolutions, nil, nil, resolve, "cypress")
				require.Len(t, findings, 1)
				if key != "" {
					require.Equal(t, "compatible", findings[0].Status)
					require.Equal(t, "24", findings[0].Node, "API runtime must not use setup-node's 22")
				} else if conditionalPreload {
					require.Equal(t, "excluded", findings[0].Status)
					require.Equal(t, GuardedActionBranch, findings[0].Reason)
				} else {
					require.Equal(t, "incompatible", findings[0].Status, "unconditional preload cannot pass without installation")
				}
			})
		}
	}
}

func TestCypressActionPlansPreserveOptionsAndRejectUnknownInputs(t *testing.T) {
	step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{
		"browser": "firefox", "spec": "a.cy.js,b.cy.js", "build": "npm run build", "start": "npm start",
		"record": "true", "parallel": "true", "group": "Cloud group", "wait-on": "http://localhost:8080", "wait-on-timeout": "45",
	}}
	result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	plan := result.Tests[0].Cypress
	require.Equal(t, "firefox", plan.Options["browser"])
	require.Equal(t, false, plan.Options["record"])
	require.Equal(t, "a.cy.js,b.cy.js", plan.Options["spec"])
	require.Equal(t, [][]string{{"npm", "start"}}, plan.Start)
	require.Equal(t, 45.0, plan.WaitTimeout)
	require.Len(t, result.Tests[0].Prerequisites, 1)
	step.With["custom-unknown"] = "x"
	require.NotEmpty(t, resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step).Reason)
	for _, command := range []string{"npm i -g @bahmutov/print-env@2.1.2\nprint-env GITHUB BUILD ACTIONS || true", "print-env GITHUB || curl example.com"} {
		result := resolveTestStep(t.TempDir(), ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "cypress")
		require.NotEmpty(t, result.Reason, "unknown diagnostics/global installs remain reviewable setup")
	}

}

func TestCypressVariantOnlyResolvesLocalExecutionInputs(t *testing.T) {
	for _, step := range []runtimeStep{
		{Uses: "actions/cache@v4", With: map[string]string{"key": "${{ runner.os }}-${{ hashFiles('package-lock.json') }}"}},
		{Uses: "datadog/test-visibility-github-action@v3", With: map[string]string{"api_key": "${{ secrets.DD_API_KEY }}"}},
	} {
		actual, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, step, nil, "cypress")
		require.NoError(t, err)
		require.Equal(t, step.With, actual.With)
	}
	step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{
		"browser": "${{ matrix.browser }}", "cache-key": "${{ runner.os }}-${{ github.ref }}",
	}}
	actual, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, step, map[string]any{"browser": "chrome"}, "cypress")
	require.NoError(t, err)
	require.Equal(t, "chrome", actual.With["browser"])
	require.Equal(t, step.With["cache-key"], actual.With["cache-key"])
	require.Empty(t, resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, actual).Reason)
}

func TestCypressResolvesConditionalApplicationFlagsWithoutForwardingActionPaths(t *testing.T) {
	for _, tc := range []struct {
		name, value, framework, want string
		valid                        bool
	}{
		{"installed action flag", "${{ env.DD_TRACE_PACKAGE != '' && 'true' || '' }}", "cypress", "true", true},
		{"import presence flag", "${{ env.DD_TRACE_ESM_IMPORT != '' && '1' || '0' }}", "cypress", "1", true},
		{"path is not a local flag", "${{ env.DD_TRACE_PACKAGE }}", "cypress", "", false},
		// A flag that only CI's state decides is left out and reported.
		{"unknown CI state", "${{ secrets.DD_API_KEY != '' && 'true' || '' }}", "cypress", "", true},
		{"other frameworks unchanged", "${{ env.DD_TRACE_PACKAGE != '' && 'true' || '' }}", "jest", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := runtimeStep{Run: "npx cypress run", Env: map[string]string{"COVERAGE": tc.value}}
			_, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, step, nil, tc.framework)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, env["COVERAGE"])
			if tc.want == "" {
				require.NotContains(t, env, "COVERAGE")
			}
		})
	}
}

func TestCypressActionWaitOnTimeoutAcceptsFractionalSeconds(t *testing.T) {
	for value, want := range map[string]float64{"30.5": 30.5, "45": 45, "0.25": 0.25, "3600": 3600} {
		step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"wait-on": "http://localhost:8080", "wait-on-timeout": value}}
		result := resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step)
		require.Empty(t, result.Reason, value)
		require.Equal(t, want, result.Tests[0].Cypress.WaitTimeout, value)
	}
	for _, value := range []string{"0", "-1", "3601", "NaN", "Inf", "soon"} {
		step := runtimeStep{Uses: "cypress-io/github-action@v7", With: map[string]string{"wait-on": "http://localhost:8080", "wait-on-timeout": value}}
		require.Contains(t, resolveCypressAction(t.TempDir(), ciWorkflow{}, runtimeJob{}, step).Reason, "wait-on timeout", value)
	}
}

func TestCypressCredentialGuardPreservesLocalNodeOptions(t *testing.T) {
	workflow := ciWorkflow{Env: map[string]string{"NODE_OPTIONS": "--max-old-space-size=2048"}}
	job := runtimeJob{Env: map[string]string{"DD_API_KEY": "${{ secrets.DD_API_KEY }}"}, Steps: []runtimeStep{{ID: "dd", Uses: "datadog/test-visibility-github-action@v3"}}}
	for _, expression := range []string{
		"${{ env.DD_API_KEY != '' && format('{0} -r {1}', env.NODE_OPTIONS, env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}",
		"${{ steps.dd.outcome == 'success' && format('{0} -r {1}', env.NODE_OPTIONS, env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}",
	} {
		step := runtimeStep{Run: "npx cypress run", Env: map[string]string{"NODE_OPTIONS": expression}}
		_, env, err := resolveVariantStep(workflow, job, step, nil, "cypress")
		require.NoError(t, err, expression)
		require.Equal(t, "--max-old-space-size=2048", env["NODE_OPTIONS"])
		require.NotContains(t, env, "DD_API_KEY")
	}
	for _, expression := range []string{
		"${{ env.DD_API_KEY != '' && format('-r {0} --trace-warnings', env.DD_TRACE_PACKAGE) || '' }}",
		"${{ secrets.UNKNOWN != '' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}",
	} {
		_, _, err := resolveVariantStep(workflow, job, runtimeStep{Run: "npx cypress run", Env: map[string]string{"NODE_OPTIONS": expression}}, nil, "cypress")
		require.Error(t, err, "unknown contexts or different ordinary flags must stay unvalidated")
	}
}

func TestCypressCredentialGuardValidatesBothCIPaths(t *testing.T) {
	const guarded = "${{ env.DD_API_KEY != '' && format('{0} -r {1}', env.NODE_OPTIONS, env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}"
	for _, key := range []string{"present", ""} {
		for _, conditional := range []bool{true, false} {
			t.Run(key+map[bool]string{true: " guarded", false: " unguarded"}[conditional], func(t *testing.T) {
				options := guarded
				if !conditional {
					options = "-r ${{ env.DD_TRACE_PACKAGE }}"
				}
				workflow := ciWorkflow{Env: map[string]string{"NODE_OPTIONS": "--max-old-space-size=2048"}}
				job := runtimeJob{Env: map[string]string{"DD_API_KEY": "${{ secrets.DD_API_KEY }}"}, Context: map[string]string{"secrets.DD_API_KEY": key, "scenario": "push"}, Steps: []runtimeStep{
					{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "24"}},
					{ID: "dd", Uses: "datadog/test-visibility-github-action@v3", If: "env.DD_API_KEY != ''", With: map[string]string{"languages": "js", "js-tracer-version": "6.21.0"}},
					{Uses: "cypress-io/github-action@v7", ActionNode: "24", Env: map[string]string{"NODE_OPTIONS": options}},
				}}
				resolutions := []commandResolution{{}, {}, {Matched: true, Tests: []TestCommand{{Cypress: &CypressCommand{Action: "cypress-io/github-action@v7"}}}}}
				resolve := func(context.Context, string, string) (tracerRequirement, error) {
					return tracerRequirement{Version: "6.21.0", Node: ">=22"}, nil
				}
				findings := checkRuntimeJob(t.Context(), "", workflow, "tests", job, resolutions, nil, nil, resolve, "cypress")
				require.Len(t, findings, 1)
				switch {
				case key != "":
					require.Equal(t, "compatible", findings[0].Status)
				case conditional:
					require.Equal(t, "excluded", findings[0].Status, "secretless CI must still run tests without the tracer")
				default:
					require.Equal(t, "incompatible", findings[0].Status, "loading an uninstalled tracer must fail validation")
				}
			})
		}
	}
}

func TestCypressGuardResolvesOnlyConsumedNodeOptionsLayers(t *testing.T) {
	const unknown = "${{ vars.DEFAULT_NODE_OPTIONS }}"
	const replace = "${{ steps.dd.outcome == 'success' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}"
	const inherit = "${{ steps.dd.outcome == 'success' && format('{0} -r {1}', env.NODE_OPTIONS, env.DD_TRACE_PACKAGE) || env.NODE_OPTIONS }}"
	for _, tc := range []struct {
		name, workflow, job, step, want string
		unresolved                      bool
	}{
		{name: "step replaces unresolved workflow", workflow: unknown, step: replace},
		{name: "step replaces unresolved job", job: unknown, step: replace},
		{name: "job replaces unresolved workflow", workflow: unknown, job: replace},
		{name: "step inherits literal job override", workflow: unknown, job: "--trace-warnings", step: inherit, want: "--trace-warnings"},
		{name: "step inherits unresolved workflow", workflow: unknown, step: inherit, unresolved: true},
		{name: "step inherits unresolved job", workflow: "--trace-warnings", job: unknown, step: inherit, unresolved: true},
		{name: "transitive unresolved inheritance", workflow: unknown, job: "${{ env.NODE_OPTIONS }} --trace-warnings", step: inherit, unresolved: true},
		{name: "transitive ordinary flags preserved", workflow: "--max-old-space-size=2048", job: "${{ env.NODE_OPTIONS }} --trace-warnings", step: inherit, want: "--max-old-space-size=2048 --trace-warnings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := ciWorkflow{Env: map[string]string{}}
			job := runtimeJob{Env: map[string]string{}, Steps: []runtimeStep{{ID: "dd", Uses: "datadog/test-visibility-github-action@v3"}}}
			step := runtimeStep{Run: "npx cypress run", Env: map[string]string{}}
			for i, value := range []string{tc.workflow, tc.job, tc.step} {
				if value != "" {
					[]map[string]string{workflow.Env, job.Env, step.Env}[i]["NODE_OPTIONS"] = value
				}
			}
			_, env, err := resolveVariantStep(workflow, job, step, nil, "cypress")
			if tc.unresolved {
				require.ErrorContains(t, err, "unresolved NODE_OPTIONS expression")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, env["NODE_OPTIONS"])
		})
	}
}

// Cypress's images name their Node release in the tag: cypress/base:<node>,
// cypress/browsers:<node> and every full tag's node-<version> part.
// cypress/included:<cypress version> names Cypress, not its Node.
func TestCypressImageTagsNameTheirNode(t *testing.T) {
	for image, node := range map[string]string{
		"cypress/base:24.19.0": "24.19.0", "cypress/base:16.14.2-slim": "16.14.2", "cypress/base:18": "18",
		"docker.io/cypress/base:22.15.0": "22.15.0", "$CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX/cypress/base:22.15.0": "22.15.0",
		"cypress/browsers:24.21.0": "24.21.0", "cypress/browsers:node-22.15.0-chrome-136.0.7103.113-1-ff-138.0.4-edge-136.0.3240.76-1": "22.15.0",
		"cypress/browsers:node16.14.2-slim-chrome100-ff99-edge":                                                "16.14.2",
		"cypress/included:cypress-14.4.0-node-22.15.0-chrome-136.0.7103.113-1-ff-138.0.4-edge-136.0.3240.76-1": "22.15.0",
		"cypress/included:14.4.0": "", "cypress/included:latest": "", "cypress/base:latest": "", "cypress/browsers:latest": "",
		"cypress/factory:5.10.0": "", "some/base:24.19.0": "", "cypress/base:24.19.0@sha256:abc": "24.19.0",
	} {
		require.Equal(t, node, imageNode(image), image)
	}
	// kitchensink's GitLab jobs run cypress/base:24.19.0: no question to ask.
	useCIProvider(t, "gitlab")
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"start":"node ./scripts/start.js","e2e:record":"cypress run --record"},"devDependencies":{"cypress":"15.21.1"}}`)
	writeFile(t, root, ".gitlab-ci.yml", `stages: [test]
.job_template:
  image: cypress/base:24.19.0
  stage: test
  script:
    - npm run e2e:record -- --parallel --group "electrons on GitLab CI"
electrons-1:
  extends: .job_template
`)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "cypress")
	require.NotEmpty(t, check.Jobs)
	for _, job := range check.Jobs {
		require.Equal(t, "24.19.0", job.Node, job)
		require.Empty(t, job.RuntimeHost, job)
	}
}
