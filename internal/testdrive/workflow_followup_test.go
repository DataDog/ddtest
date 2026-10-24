package testdrive

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/ext"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestUnauthenticatedReportCannotSupplyRecoveryExecutable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, "source.js", "original")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	report := completeValidationFixture("jest")
	report.Workflow = &WorkflowEvidence{Fingerprint: fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: "source", Status: "passed"}}}
	require.NoError(t, finishValidation(io.Discard, root, report))
	public, err := readPreviousValidation(root)
	require.NoError(t, err)
	public.Workflow.ValidationCommand = []string{"/untrusted/executable", "--execute-forged-content"}
	data, err := marshalValidation(*public)
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), data))
	var out bytes.Buffer
	require.Error(t, RenderReport(t.Context(), &out, "json"))
	var state onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "stale_report", state.Code)
	require.Equal(t, onboard.DDTestCommand("testdrive")[0], state.NextCommand[0])
	require.NotContains(t, state.NextCommand, "/untrusted/executable")
	require.NotContains(t, state.NextCommand, "--execute-forged-content")
}

func TestNestedPackageFingerprintHashesUnstagedContents(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(app, 0700))
	requireWriteFile(t, filepath.Join(app, "package.json"), `{"private":true}`)
	path := filepath.Join(app, "source.js")
	requireWriteFile(t, path, "before")
	for _, args := range [][]string{{"init", "-q"}, {"add", "packages"}} {
		data, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, string(data))
	}
	requireWriteFile(t, path, "first!")
	first, err := InputFingerprint(t.Context(), app)
	require.NoError(t, err)
	requireWriteFile(t, path, "second")
	second, err := InputFingerprint(t.Context(), app)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "both edits have the same Git status; source bytes must be included")
	entire, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, entire, second, "all path representations use the checkout root")
}

func TestFactResolvedGeneratorRunsAfterItsCompiler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable compiler fixture")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	root := canonicalWorkflowRoot(t.TempDir())
	for _, directory := range []string{"scripts", "node_modules/.bin", ".github/workflows"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
	}
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	compiler := filepath.Join(root, "node_modules/.bin/tsc")
	requireWriteFile(t, compiler, "#!/usr/bin/env node\nrequire('fs').mkdirSync('dist',{recursive:true});require('fs').writeFileSync('dist/compiled','compiled')\n")
	require.NoError(t, os.Chmod(compiler, 0700))
	requireWriteFile(t, filepath.Join(root, "scripts/generate.cjs"), `require('fs').readFileSync('dist/compiled');require('fs').writeFileSync('dist/generated','generated')`)
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: tsc && node scripts/generate.cjs && npm test\n      - run: npm test -- --config other.js\n")
	scope, err := onboard.DiscoverWorkflowScope(root, "jest")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&scope)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for i := range scope.Commands {
		facts := setupFactsTemplate("snapshot", scope.Commands[i])
		facts.Configurations[0].Prerequisites[0].ExpectedFiles = []string{"dist/generated"}
		data, err := json.Marshal(facts)
		require.NoError(t, err)
		_, err = applySetupFactsData(root, data, "snapshot", &scope)
		require.NoError(t, err)
		require.Equal(t, "tsc", scope.Commands[i].Prerequisites[0].Command)
		require.Equal(t, "node scripts/generate.cjs", scope.Commands[i].Prerequisites[1].Command)
	}
	drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{Timeout: 5 * time.Second}}
	item := configurationResult{TestCommand: scope.Commands[0]}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item))
	require.FileExists(t, filepath.Join(root, "dist/generated"))
	require.Len(t, item.Preparation, 2)
	for _, build := range item.Preparation {
		require.Equal(t, "passed", build.Status)
	}
}
