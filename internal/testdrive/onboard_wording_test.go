package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestSetupUnresolvedNamesThePendingPrerequisite(t *testing.T) {
	entry := onboard.TestCommand{ID: "jest-1", Command: "jest", Directory: ".", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "npm run build", Kind: onboard.PreparationNeedsFacts}}}
	require.Equal(t, []string{"prerequisite npm run build needs expected_files"}, pendingSetup(entry))
	template := setupFactsTemplate("fingerprint", entry)
	require.Equal(t, "jest", template.Configurations[0].Prerequisites[0].ForCommand)
	empty := setupFactsTemplate("fingerprint", onboard.TestCommand{ID: "jest-1", Command: "jest", Directory: "."})
	require.Equal(t, "jest", empty.Configurations[0].Prerequisites[0].ForCommand)
}

func TestPrerequisiteFactErrorsNameWhatIsWrong(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"build":"node build.js"}}`)
	for _, tc := range []struct {
		argv, files []string
		want        string
	}{
		{[]string{"evil.sh"}, []string{"dist/out.js"}, "must use an installed package script, or node, sh, bash or ./ with a local script"},
		{[]string{"./missing.sh"}, []string{"dist/out.js"}, "no such file"},
		{[]string{"node"}, []string{"dist/out.js"}, "prerequisite script must be repository-local"},
		{[]string{"npm", "run", "build"}, nil, "expected repository files"},
		{nil, []string{"dist/out.js"}, "prerequisite requires an argv"},
	} {
		err := validatePrerequisiteFact(root, root, PrerequisiteFact{Argv: tc.argv, ExpectedFiles: tc.files})
		require.ErrorContains(t, err, tc.want, tc.argv)
		if len(tc.argv) > 0 && len(tc.files) > 0 {
			require.NotContains(t, err.Error(), "requires an argv and expected", "both were supplied")
		}
	}
}

func TestBlockerCountMatchesTheListedItems(t *testing.T) {
	result := focusedValidationFixture("jest")
	result.Summary = summarizeValidation(result)
	result.Summary.BlockingChecks = []string{"first blocker", "second blocker", "third blocker"}
	require.Contains(t, validationHandoff(result), "Blockers: 3 — first blocker; second blocker; and 1 more.")
}

func TestUnreadableReportAndFinishedResumeHaveCodes(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0700))
	requireWriteFile(t, validationPath(root), "not json")
	_, err := readPreviousValidation(root)
	require.ErrorIs(t, err, errReportUnreadable)
	var output bytes.Buffer
	require.Error(t, renderReportFailure(&output, "json", err))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "report_unreadable", result.Code)
	require.NotContains(t, result.Reason, "invalid character", "no raw parser error")

	t.Setenv("TMPDIR", t.TempDir())
	drive := &Testdrive{repositoryRoot: workspace.CanonicalRoot(t.TempDir()), framework: &framework.Vitest{}, language: "javascript"}
	output.Reset()
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", Resume: strings.Repeat("a", 32)}))
	result = onboard.AgentResult{}
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "resume_not_found", result.Code)
	require.NotContains(t, result.Reason, "lstat")
}

func TestFrameworkListsFollowThePolicyTable(t *testing.T) {
	all := frameworkNamesWith(func(policy frameworkPolicy) bool { return policy.Configurations })
	for _, name := range []string{"Cucumber", "Cypress", "Jest", "Mocha", "Playwright", "Vitest", "WebdriverIO"} {
		require.Contains(t, all, name)
	}
}

func TestJestPreflightKeepsHelperTemporaryFilesInOwnedStorage(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	run.checkOnly = true
	require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
	executor := run.executor.(*preflightExecutor)
	temporary := executor.environment["TMPDIR"]
	require.NotEmpty(t, temporary, "Jest's haste cache and npm's compile cache must not land in the user's TMPDIR")
	require.NotEqual(t, filepath.Clean(os.TempDir()), filepath.Clean(temporary))
	require.NoDirExists(t, temporary, "the owned directory is removed after preflight")
}

func TestPrerequisiteFactsAcceptTheShellScriptsDiscoveryOffers(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/build.sh"), []byte("#!/bin/sh\necho built\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/plain.sh"), []byte("echo plain\n"), 0600))
	for _, argv := range [][]string{{"./scripts/build.sh"}, {"bash", "scripts/build.sh"}, {"sh", "scripts/plain.sh"}} {
		require.NoError(t, validatePrerequisiteFact(root, root, PrerequisiteFact{Argv: argv, ExpectedFiles: []string{"dist/out.js"}}), argv)
	}
	require.ErrorContains(t, validatePrerequisiteFact(root, root, PrerequisiteFact{Argv: []string{"./scripts/plain.sh"}, ExpectedFiles: []string{"dist/out.js"}}), "not executable")
	outside := filepath.Join(t.TempDir(), "outside.sh")
	require.NoError(t, os.WriteFile(outside, []byte("echo outside\n"), 0700))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "scripts/link.sh")))
	require.ErrorContains(t, validatePrerequisiteFact(root, root, PrerequisiteFact{Argv: []string{"bash", "scripts/link.sh"}, ExpectedFiles: []string{"dist/out.js"}}), "escapes the repository")
	require.ErrorContains(t, validatePrerequisiteFact(root, root, PrerequisiteFact{Argv: []string{"./scripts"}, ExpectedFiles: []string{"dist/out.js"}}), "not a regular file")
}

func TestUnreadableReportDoesNotBlockCIPreservation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0700))
	requireWriteFile(t, validationPath(root), "not json")
	result := validationResult{Framework: "mocha"}
	require.NoError(t, checkCIPreservation(root, &result), "fresh results must replace an unreadable report")
	require.NotNil(t, result.CIPreservation)
}

func TestCheckOnlyWorkflowRejectsFactsWithoutReplacingTheReport(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := workspace.CanonicalRoot(t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0700))
	requireWriteFile(t, validationPath(root), `{"schema_version":1}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", checkOnly: true}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", FactsFile: filepath.Join(t.TempDir(), "missing.json")}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "invalid_options", result.Code)
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.JSONEq(t, `{"schema_version":1}`, string(data), "rejected input must leave the saved report untouched")
}

func TestCIPreservationFailureIsABlockingCheck(t *testing.T) {
	result := validationResult{Framework: "mocha", CIPreservation: &verdict{Status: "failed", Reason: "baseline matrix row 'node 20' is missing"}}
	require.Contains(t, strings.Join(summarizeValidation(result).BlockingChecks, "\n"), "CI preservation: failed — baseline matrix row 'node 20' is missing")
}
