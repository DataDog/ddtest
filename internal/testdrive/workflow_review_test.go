package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestOnboardFactsSurviveOwnedEditsAndResume(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	drive.allConfigurations = true
	drive.nodeVersion = currentNodeVersion
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"jest","build":"node build.js"},"devDependencies":{"jest":"30.2.0"}}`)
	requireWriteFile(t, filepath.Join(root, "build.js"), "// explicit build prerequisite\n")
	requireWriteFile(t, filepath.Join(root, "server.js"), "// explicit application service\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n")
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	data, err := command.CombinedOutput()
	require.NoError(t, err, string(data))
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	scope, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&scope)
	require.Len(t, scope.Commands, 1)
	facts := SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{{ConfigurationID: scope.Commands[0].ID,
		Prerequisites: []PrerequisiteFact{{Argv: []string{"npm", "run", "build"}, ExpectedFiles: []string{"dist/built.js"}}},
		Services:      []onboard.ServiceCommand{{Argv: []string{"node", "server.js"}, Directory: ".", ReadinessURL: "http://127.0.0.1:3000", TimeoutSeconds: 1}},
	}}}
	factsFile := filepath.Join(t.TempDir(), "facts.json")
	data, err = json.Marshal(facts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(factsFile, data, 0600))
	selections, executions := 0, 0
	drive.workflowSelect = func(_ context.Context, _ onboard.ValidationScope, _ bool) (string, error) {
		selections++
		drive.workflowPlan.TracerNode = ">=22"
		return "6.16.0", nil
	}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		executions++
		current := drive.workflowPlan.Scope.Commands
		require.Len(t, current, 2)
		for _, entry := range current {
			require.Len(t, entry.Services, 1, "accepted services cannot disappear during instrumentation")
			require.Len(t, entry.Prerequisites, 1, "accepted build facts cannot disappear during instrumentation")
			require.Equal(t, []string{"dist/built.js"}, entry.Prerequisites[0].ExpectedFiles)
		}
		require.Equal(t, ">=22", drive.workflowPlan.TracerNode)
		r := completeValidationFixture("jest")
		r.Features[0].Status = "failed"
		r.Features[0].Reason = "deliberate control failure keeps the workflow resumable"
		return finishValidation(out, root, r)
	}
	resume := ""
	for range 2 {
		var output bytes.Buffer
		err = drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: resume})
		require.ErrorContains(t, err, "validation_incomplete", output.String())
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result))
		require.Contains(t, result.ResumeCommand, "--format")
		require.Contains(t, result.ResumeCommand, "json")
		resume = result.RunID
		checkpoint, err := readCheckpoint(resume)
		require.NoError(t, err)
		require.Equal(t, fingerprint, checkpoint.FactsFingerprint)
		require.NotEqual(t, fingerprint, checkpoint.Fingerprint, "owned edits rebind accepted facts only to their resulting snapshot")
	}
	require.Equal(t, 1, selections, "resume restores the tracer runtime requirement from preparation")
	require.Equal(t, 2, executions, "resume must execute fresh validation")
	// Neither a changed facts document nor a customer source edit can reuse the binding.
	require.NoError(t, os.WriteFile(factsFile, append(data, '\n'), 0600))
	var changedFacts bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &changedFacts, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: resume}), "facts_invalid")
	require.Equal(t, 2, executions)
	var rejected onboard.AgentResult
	require.NoError(t, json.Unmarshal(changedFacts.Bytes(), &rejected))
	resume = rejected.RunID
	require.NoError(t, os.WriteFile(factsFile, []byte("{malformed"), 0600))
	changedFacts.Reset()
	require.ErrorContains(t, drive.Workflow(t.Context(), &changedFacts, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: resume}), "facts_invalid")
	rejected = onboard.AgentResult{}
	require.NoError(t, json.Unmarshal(changedFacts.Bytes(), &rejected))
	resume = rejected.RunID
	require.NoError(t, os.WriteFile(factsFile, data, 0600))

	var restored bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &restored, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: resume}), "validation_incomplete")
	var restoredState onboard.AgentResult
	require.NoError(t, json.Unmarshal(restored.Bytes(), &restoredState))
	resume = restoredState.RunID
	require.Equal(t, 3, executions, "restoring the accepted facts must restore their services and prerequisites")
	require.Equal(t, 1, selections, "rejected replacement facts must not destroy accepted preparation")
	requireWriteFile(t, filepath.Join(root, "build.js"), "// changed by the customer\n")
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: resume}), "facts_invalid")
	require.Equal(t, 3, executions)
}

func TestSavedReportInvalidatesReplacedIgnoredTracer(t *testing.T) {
	for _, directory := range []string{".", "packages/app"} {
		t.Run(directory, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.MkdirAll(filepath.Join(root, directory, "node_modules/dd-trace"), 0700))
			requireWriteFile(t, filepath.Join(root, directory, "package.json"), `{"private":true}`)
			path := filepath.Join(root, directory, "node_modules/dd-trace/package.json")
			requireWriteFile(t, path, `{"version":"6.20.1"}`)
			requireWriteFile(t, filepath.Join(root, ".gitignore"), "node_modules/\n.testoptimization/\n")
			command := exec.Command("git", "init", "-q")
			command.Dir = root
			data, err := command.CombinedOutput()
			require.NoError(t, err, string(data))
			fingerprint, err := InputFingerprint(t.Context(), root)
			require.NoError(t, err)
			report := completeValidationFixture("jest")
			report.Workflow = &WorkflowEvidence{Fingerprint: fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: "current", Status: "passed"}}}
			require.NoError(t, finishValidation(io.Discard, root, report))
			var output bytes.Buffer
			require.NoError(t, RenderReport(t.Context(), &output, "json"))
			before, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			requireWriteFile(t, path, `{"version":"6.21.0"}`)
			output.Reset()
			require.ErrorContains(t, RenderReport(t.Context(), &output, "json"), "current source/runtime")
			require.NotContains(t, output.String(), "complete_locally")
			after, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			require.Equal(t, before, after, "staleness detection preserves the prior execution report")
		})
	}
}

func TestJSONWorkflowFollowupsPreserveFormat(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root = drive.repositoryRoot
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"devDependencies":{"jest":"30.2.0"}}`)
	drive.executor = workflowCheckExecutor{}
	drive.nodeVersion = currentNodeVersion
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		return finishValidation(out, root, completeValidationFixture("jest"))
	}
	var output bytes.Buffer
	require.NoError(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, []string{"testdrive", "--report", "--format", "json"}, result.NextCommand[1:])
	output.Reset()
	require.NoError(t, RenderReport(t.Context(), &output, "json"))
	require.True(t, json.Valid(output.Bytes()))
	require.NotContains(t, strings.TrimSpace(output.String()), "\nFINAL RESULT")
}

func TestMochaSetupEditsPreserveAdditionalPrerequisites(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.0.0","nyc":"17.1.0"}}`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	before, err := onboard.DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&before)
	require.Len(t, before.Commands, 1)
	before.Commands[0].Prerequisites = []onboard.BuildCommand{{Command: "npm run build", FactSource: "accepted fact", ExpectedFiles: []string{"dist/index.js"}}}
	_, err = onboard.ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", "")
	require.NoError(t, err)
	after, err := onboard.DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&after)
	require.Len(t, after.Commands, 1)
	require.NoError(t, reattachSetupFacts(&after, before, true))
	require.Len(t, after.Commands[0].Prerequisites, 1, "before: %s; after: %s", before.Commands[0].Command, after.Commands[0].Command)
}

func TestWorkflowReturnsSetupRepairsWithoutWeakeningFeatureFailures(t *testing.T) {
	for _, scenario := range []string{"build setup", "service port", "source mutation", "Cypress documentation mutation", "feature failure"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			runner := framework.Framework(&framework.Vitest{})
			testCommand := "vitest run"
			if scenario == "service port" || scenario == "Cypress documentation mutation" {
				runner = &framework.Cypress{}
				testCommand = "cypress run"
			}
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"`+testCommand+`","start":"node server.js"}}`)
			requireWriteFile(t, filepath.Join(root, "source.js"), "original\n")
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n")
			drive := &Testdrive{repositoryRoot: root, framework: runner, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
			drive.workflowRun = func(_ context.Context, out io.Writer) error {
				result := completeValidationFixture(runner.Name())
				switch scenario {
				case "source mutation":
					requireWriteFile(t, filepath.Join(root, "source.js"), "changed during execution\n")
				case "Cypress documentation mutation":
					require.NotNil(t, drive.workflowSourceCheck)
					require.NoError(t, drive.workflowSourceCheck(t.Context()))
					requireWriteFile(t, filepath.Join(root, "README.md"), "edited while a browser was running\n")
					next := &Testdrive{workflowSourceCheck: drive.workflowSourceCheck,
						startIntake: func(string, intake.Scenario) (localIntake, error) {
							t.Fatal("source changes must stop the next browser run before intake/process startup")
							return nil, nil
						}}
					session := planSession()
					defer func() { require.NoError(t, session.Close()) }()
					_, err := next.runCypress(t.Context(), out, session, "", runSpec{Name: "reporting-only", Role: roleReporting})
					require.ErrorContains(t, err, "remaining browser runs stopped")
					require.ErrorContains(t, err, "documentation and formatting")
					result.Error = err.Error()
					result.Compatibility.Status = "inconclusive"
				case "feature failure":
					result.Features[0].Status = "failed"
				default:
					entry := onboard.TestCommand{ID: "configuration-repair", Directory: ".", Command: testCommand}
					item := configurationResult{TestCommand: entry, Status: "failed", Error: "preparation failed"}
					if scenario == "service port" {
						item.Cypress = &onboard.CypressCommand{Start: [][]string{{"npm", "run", "start"}}}
						item.Error = "cypress startup port is occupied"
						result.Error = item.Error
					}
					result.Configurations = []configurationResult{item}
					result.Error = item.Error
				}
				return finishValidation(out, root, result)
			}
			var output bytes.Buffer
			require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			switch scenario {
			case "build setup":
				require.Equal(t, "configuration_setup_failed", result.Code)
				require.Contains(t, result.Reason, "setup did not complete: preparation failed", "the reason names why setup stopped")
				require.NotNil(t, result.InputTemplate)
				require.Equal(t, "configuration-repair", result.InputTemplate.Configurations[0].ConfigurationID)
				require.Empty(t, result.InputTemplate.Configurations[0].Prerequisites[0].Argv, "ask for a real prerequisite, never fabricate one")
			case "service port":
				require.Equal(t, "service_setup_unresolved", result.Code)
				require.NotNil(t, result.InputTemplate)
				require.Equal(t, "server.js", result.InputTemplate.Configurations[0].PortOverride.SourceFile)
				require.Equal(t, "PORT", result.InputTemplate.Configurations[0].PortOverride.Environment)
			case "source mutation", "Cypress documentation mutation":
				require.Equal(t, "validation_incomplete", result.Code)
				require.Contains(t, strings.Join(result.RequiredInput, " "), "source_preservation")
			case "feature failure":
				require.Equal(t, "validation_incomplete", result.Code)
				require.Nil(t, result.InputTemplate, "a failed control is not a setup repair recipe")
			}
			require.Contains(t, result.ResumeCommand, "--format")
			require.Contains(t, result.ResumeCommand, "json")
			require.Empty(t, result.FinalResponse)
		})
	}
}

func TestMochaWorkflowRequiresSetupOnlyForInstrumentedCIConfigurations(t *testing.T) {
	for _, scenario := range []string{"excluded legacy setup", "active legacy setup", "unknown excluded consumer"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"mocha test.js"}}`)
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			legacyEnabled, extra := "false", ""
			if scenario == "active legacy setup" {
				legacyEnabled = "true"
			}
			if scenario == "unknown excluded consumer" {
				extra = "      - if: matrix.datadog == false\n        run: unknown-test-wrapper\n"
			}
			workflow := `jobs:
  test:
    strategy:
      matrix:
        include:
          - {node: '8', datadog: LEGACY}
          - {node: '24', datadog: true}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.datadog
        with: {languages: js}
      - run: |
          if [[ "${{ matrix.node }}" -lt 10 ]]; then
            xargs -n1 npm rm --silent --save-dev
          fi
EXTRA      - run: npm test
        env:
          NODE_OPTIONS: ${{ matrix.datadog && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}
`
			workflow = strings.ReplaceAll(strings.ReplaceAll(workflow, "LEGACY", legacyEnabled), "EXTRA", extra)
			requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), workflow)
			drive := &Testdrive{repositoryRoot: root, framework: &framework.Mocha{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
			executions := 0
			drive.workflowRun = func(_ context.Context, out io.Writer) error {
				executions++
				require.Len(t, drive.workflowScope.Commands, 2, "excluded configurations remain in the evidence scope")
				template := scopeFactsTemplate("snapshot", *drive.workflowScope)
				require.Len(t, template.Configurations, 1, "excluded configurations must not request setup facts")
				return finishValidation(out, root, completeValidationFixture("mocha"))
			}
			var output bytes.Buffer
			err := drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", ExplicitVersion: true})
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			switch scenario {
			case "excluded legacy setup":
				require.NoError(t, err)
				require.Equal(t, "complete_locally", result.Status)
				require.Equal(t, 1, executions)
			case "active legacy setup":
				require.Error(t, err)
				require.Equal(t, "scope_unresolved", result.Code)
				require.Contains(t, result.Reason, "xargs")
				require.Zero(t, executions)
			case "unknown excluded consumer":
				require.Error(t, err)
				require.Equal(t, "scope_unresolved", result.Code)
				require.Zero(t, executions)
			}
			require.Equal(t, workflow, readApplyFile(t, root, ".github/workflows/test.yml"), "testdrive must preserve the legacy matrix and setup")
		})
	}
}
