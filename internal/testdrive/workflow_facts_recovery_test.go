package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// factsRecoveryFixture is a Jest repository whose single CI command needs an
// explicit build prerequisite, with facts accepted for the pre-edit snapshot.
func factsRecoveryFixture(t *testing.T) (*Testdrive, string, SetupFacts, *int) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := drive.repositoryRoot
	drive.allConfigurations = true
	drive.nodeVersion = currentNodeVersion
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"jest","build":"node build.js"},"devDependencies":{"jest":"30.2.0"}}`)
	requireWriteFile(t, filepath.Join(root, "build.js"), "// explicit build prerequisite\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n")
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
	}}}
	drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) {
		drive.workflowPlan.TracerNode = ">=22"
		return "6.16.0", nil
	}
	executions := 0
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		executions++
		return finishValidation(out, root, completeValidationFixture("jest"))
	}
	return drive, root, facts, &executions
}

func writeFactsFile(t *testing.T, facts SetupFacts) string {
	t.Helper()
	data, err := json.Marshal(facts)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "facts.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func runFactsWorkflow(t *testing.T, drive *Testdrive, options WorkflowOptions) (onboard.AgentResult, error) {
	t.Helper()
	var output bytes.Buffer
	err := drive.Workflow(t.Context(), &output, options)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	return result, err
}

func TestFailureAfterTheOwnedEditResumesWithTheAcceptedFacts(t *testing.T) {
	drive, root, facts, executions := factsRecoveryFixture(t)
	factsFile := writeFactsFile(t, facts)
	drive.commandTimeout = -1 // Preparation after the owned edit fails.
	result, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile})
	require.ErrorContains(t, err, "validation_preparation")
	require.NotEmpty(t, result.Changes, "the CI edit stays applied")
	current, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, current, result.Fingerprint, "the response names the snapshot the owned edit produced")
	require.Contains(t, result.ResumeCommand, "--resume")
	drive.commandTimeout = 0
	resumed, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: result.RunID})
	require.NoError(t, err, "accepted facts stay bound to ddtest's own edit")
	require.Equal(t, "complete_locally", resumed.Status)
	require.Equal(t, 1, *executions)
}

func TestRejectedFactsReturnATemplateForTheCurrentSnapshot(t *testing.T) {
	drive, root, facts, executions := factsRecoveryFixture(t)
	factsFile := writeFactsFile(t, facts)
	drive.commandTimeout = -1
	failed, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile})
	require.ErrorContains(t, err, "validation_preparation")
	drive.commandTimeout = 0
	// Corrected facts, still written for the pre-edit snapshot, are rejected.
	facts.Configurations[0].Prerequisites[0].ExpectedFiles = []string{"dist/other.js"}
	require.NoError(t, os.WriteFile(factsFile, mustJSON(t, facts), 0600))
	rejected, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: failed.RunID})
	require.ErrorContains(t, err, "facts_invalid")
	current, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, current, rejected.Fingerprint)
	require.NotNil(t, rejected.InputTemplate, "facts_invalid returns a template for the current inputs")
	require.Equal(t, current, rejected.InputTemplate.Fingerprint)
	require.NotEmpty(t, rejected.ResumeCommand)
	// Facts written from that template resume the workflow.
	template := *rejected.InputTemplate
	template.Configurations[0].Prerequisites = facts.Configurations[0].Prerequisites
	require.NoError(t, os.WriteFile(factsFile, mustJSON(t, template), 0600))
	resumed, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile, Resume: rejected.RunID})
	require.NoError(t, err)
	require.Equal(t, "complete_locally", resumed.Status)
	require.Equal(t, 1, *executions)
}

func TestFreshFactsRunAfterTheOwnedEditReusesAcceptedFacts(t *testing.T) {
	drive, _, facts, executions := factsRecoveryFixture(t)
	factsFile := writeFactsFile(t, facts)
	first, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile})
	require.NoError(t, err)
	require.Equal(t, "complete_locally", first.Status)
	require.NotEmpty(t, first.Changes)
	// The onboard hand-out repeats the same facts without --resume.
	again, err := runFactsWorkflow(t, drive, WorkflowOptions{Apply: true, AllowDirty: true, Format: "json", FactsFile: factsFile})
	require.NoError(t, err, "ddtest's own CI edit must not invalidate the facts it accepted")
	require.Equal(t, "complete_locally", again.Status)
	require.Empty(t, again.Changes)
	require.Equal(t, 2, *executions)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
