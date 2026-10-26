package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTracerSkipsConfigurationsSetupLeavesReported(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	drive.executor = workflowPreflightExecutor{root: root, runner: "jest"}
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: npm test\n")
	active := onboard.TestCommand{Directory: ".", Command: "jest"}
	// A partial run keeps this entry unedited and reported; preflighting its
	// unknown command must not stop setup of the active configuration.
	reported := onboard.TestCommand{Directory: ".", Command: "npm run test:sauce", UnvalidatedSetup: []string{"CI environment variable SAUCE_ACCESS_KEY requires review"}}
	selected, err := drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{reported, active}}, false)
	require.NoError(t, err)
	require.Equal(t, "6.16.0", selected)
}

func TestCloudGridAndMultiremoteConfigurationsAreReportOnly(t *testing.T) {
	grid := jestPreflight{WebdriverIO: &framework.WebdriverIOConfig{CloudGrid: []string{"cloud provider service"}}}
	require.Contains(t, localOnlyUnsupported("webdriverio", grid), "Cloud grids are not supported locally")
	multiremote := jestPreflight{WebdriverIO: &framework.WebdriverIOConfig{Multiremote: true}}
	require.Contains(t, localOnlyUnsupported("webdriverio", multiremote), "multiremote")
	local := jestPreflight{Version: "9.32.0", WebdriverIO: &framework.WebdriverIOConfig{Runner: "local", Framework: "jasmine"}}
	require.Empty(t, localOnlyUnsupported("webdriverio", local), "a local configuration still constrains the tracer")
	require.Empty(t, localOnlyUnsupported("jest", grid))
	require.ErrorContains(t, noRunnableConfiguration([]string{"npm run test:sauce: Cloud grids are not supported locally"}), "no configuration can be set up and validated locally")
}

func TestTracerMajorsMessageNamesDifferentMajorsOnlyWhenNodeRequiresThem(t *testing.T) {
	ci := onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{
		{Workflow: ".github/workflows/ci.yaml", Job: "build", Step: 4, Node: "20.18.2", Status: onboard.StatusCompatible},
		{Workflow: ".github/workflows/sauce.yaml", Job: "build", Step: 4, Node: "20.18.2", Status: onboard.StatusCompatible},
	}}
	latest := platform.JSSelection{Version: "6.21.0", Node: ">=22"}
	covering := &tracerRecommendation{Selection: platform.JSSelection{Version: "5.132.0", Node: ">=18"}, Compatible: []string{"20.18.2"}}
	err := ciTracerMajorsError("webdriverio", ci, latest, covering)
	require.NotContains(t, err.Error(), "different dd-trace majors", "one release supports every Node 20 row")
	require.Contains(t, err.Error(), "dd-trace@5.132.0 supports every instrumented CI row")
	var majors *ciTracerMajors
	require.ErrorAs(t, err, &majors)
	require.Equal(t, "5.132.0", majors.Recommended)
	require.Empty(t, majors.Exclusions, "only Mocha has an opt-out edit")

	partial := &tracerRecommendation{Selection: covering.Selection, Compatible: []string{"20.18.2"}, Unsupported: []string{"16.20.2"}}
	require.Contains(t, ciTracerMajorsError("webdriverio", ci, latest, partial).Error(), "CI rows need different dd-trace majors")

	unknown := ciTracerMajorsError("webdriverio", ci, latest, nil)
	require.NotContains(t, unknown.Error(), "different dd-trace majors")
	require.Contains(t, unknown.Error(), "No verified dd-trace release was resolved")
}

func TestPreflightResumePinsTheRecommendedTracer(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"vitest run"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '20.18.2'}\n      - run: npm test\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "latest", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
	drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) {
		return "", &ciTracerMajors{message: "dd-trace@6.21.0 requires Node >=22", Recommended: "5.132.0"}
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "preflight_unresolved", result.Code)
	i := slices.Index(result.ResumeCommand, "--tracer-version")
	require.GreaterOrEqual(t, i, 0, result.ResumeCommand)
	require.Equal(t, "5.132.0", result.ResumeCommand[i+1])
	require.Contains(t, result.ResumeCommand, "--resume")
	require.NotContains(t, result.Needs, "opt-out the user approves", "no opt-out applies outside Mocha")
	require.Contains(t, result.Needs, "dd-trace@5.132.0")
}
