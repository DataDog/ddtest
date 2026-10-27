package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestUnknownCIBuildCanBeResolvedOnlyByExactSetupFacts(t *testing.T) {
	for _, steps := range []string{"      - run: npm run build && npm test\n", "      - run: npm run build\n      - run: npm test\n"} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest","build":"node build.js"}}`)
		requireWriteFile(t, filepath.Join(root, "build.js"), `require('fs').mkdirSync('dist',{recursive:true}); require('fs').writeFileSync('dist/result.js','built')`)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
		requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n"+steps)
		scope, err := onboard.DiscoverWorkflowScope(root, "jest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		require.Len(t, scope.Commands, 1)
		onboard.AssignConfigurationIDs(&scope)
		entry := scope.Commands[0]
		require.True(t, missingPrerequisiteFacts(entry))
		fingerprint, err := InputFingerprint(t.Context(), root)
		require.NoError(t, err)
		facts := setupFactsTemplate(fingerprint, entry)
		require.Len(t, facts.Configurations[0].Prerequisites, 1)
		require.Equal(t, []string{"npm", "run", "build"}, facts.Configurations[0].Prerequisites[0].Argv)
		facts.Configurations[0].Prerequisites[0].ExpectedFiles = []string{"dist/result.js"}
		path := filepath.Join(t.TempDir(), "facts.json")
		data, err := json.Marshal(facts)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
		_, err = applySetupFacts(root, path, fingerprint, &scope)
		require.NoError(t, err)
		require.False(t, missingPrerequisiteFacts(scope.Commands[0]))
		onboard.AssignConfigurationIDs(&scope)
		require.Equal(t, entry.ID, scope.Commands[0].ID)
		require.Equal(t, "npm run build", scope.Commands[0].Prerequisites[0].Command)
		require.NotEmpty(t, scope.Commands[0].Prerequisites[0].FactSource)
		// Discovery of arbitrary executables must stay unresolved, even with setup facts.
		requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: unknown-runner && npm test\n")
		unknown, err := onboard.DiscoverWorkflowScope(root, "jest")
		require.NoError(t, err)
		require.NotEmpty(t, unknown.Unresolved)
	}
}

func TestAcceptedSetupCannotDisappearDuringRediscovery(t *testing.T) {
	old := onboard.ValidationScope{Commands: []onboard.TestCommand{{ID: "original", Command: "jest", Services: []onboard.ServiceCommand{{Argv: []string{"node", "server.js"}}}}}}
	updated := onboard.ValidationScope{Commands: []onboard.TestCommand{{ID: "different", Command: "jest --config other.js"}}}
	require.ErrorContains(t, reattachSetupFacts(&updated, old, true), "original")
}

func TestCheckOnlyPreservesPrivateFullRunReceiptAndTamperingIsRejected(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "source.js"), "original")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	report := completeValidationFixture("jest")
	report.Workflow = &WorkflowEvidence{Fingerprint: fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: "source_preservation", Status: "passed"}}}
	require.NoError(t, finishValidation(io.Discard, root, report))
	var out bytes.Buffer
	require.NoError(t, RenderReport(t.Context(), &out, "json"))
	check := validationResult{Framework: "jest", CheckOnly: true, Compatibility: verdict{Status: "not exercised"}}
	_ = finishValidation(io.Discard, root, check)
	out.Reset()
	require.NoError(t, RenderReport(t.Context(), &out, "json"), out.String())
	var state onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "complete_locally", state.Status)

	original, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	forged, err := readPreviousValidation(root)
	require.NoError(t, err)
	forged.Retained.Result.Summary.FinalResponse = "forged completion"
	data, err := marshalValidation(*forged)
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), data))
	out.Reset()
	require.Error(t, RenderReport(t.Context(), &out, "json"), "public summary edits cannot certify themselves")
	require.NoError(t, writeValidation(validationPath(root), original))
	// Touching a source does not change content; whitespace does.
	now := time.Now()
	require.NoError(t, os.Chtimes("source.js", now, now))
	out.Reset()
	require.NoError(t, RenderReport(t.Context(), &out, "json"))
	requireWriteFile(t, "source.js", "original ")
	current, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	public, err := readPreviousValidation(root)
	require.NoError(t, err)
	public.Retained.Result.Workflow.Fingerprint = current
	data, err = marshalValidation(*public)
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), data))
	out.Reset()
	require.Error(t, RenderReport(t.Context(), &out, "json"))
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "stale_report", state.Code)
	require.Contains(t, state.NextCommand, "--all")
}

func TestReportAndWorkflowUseSameFailedSelectedVerdict(t *testing.T) {
	root := t.TempDir()
	report := focusedValidationFixture("jest")
	report.Workflow = &WorkflowEvidence{Checks: []onboard.WorkflowCheck{{Name: "cleanup", Status: "passed"}}}
	require.NoError(t, finishValidation(io.Discard, root, report))
	current := readValidationReport(t, root)
	require.Equal(t, "selected_passed", workflowVerdict(current))
	current.Workflow.Checks[0].Status = "failed"
	require.Equal(t, "validation_failed", workflowVerdict(current), "a selected suite pass cannot hide failed workflow checks")
}

func BenchmarkIndexedRepositoryFingerprint(b *testing.B) {
	root := b.TempDir()
	require.NoError(b, os.MkdirAll(filepath.Join(root, "files"), 0700))
	body := bytes.Repeat([]byte("x"), 4400)
	for i := range 50000 {
		require.NoError(b, os.WriteFile(filepath.Join(root, "files", fmt.Sprintf("%05d.js", i)), body, 0600))
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "files"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		data, err := command.CombinedOutput()
		require.NoError(b, err, string(data))
	}
	b.ResetTimer()
	for range b.N {
		_, err := InputFingerprint(b.Context(), root)
		require.NoError(b, err)
	}
}

func TestServiceScriptShorthandsAndExactPortVariable(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"start":"node server.js","dev":"node server.js"}}`)
	requireWriteFile(t, filepath.Join(root, "server.js"), `const port=process.env.PORT_X || 8888`)
	for _, words := range [][]string{{"npm", "start"}, {"yarn", "dev"}, {"pnpm", "start"}, {"pnpm", "dev"}, {"pnpm", "run", "dev"}} {
		service := onboard.ServiceCommand{Argv: words, Directory: ".", ReadinessURL: "http://127.0.0.1:8888", TimeoutSeconds: 2}
		require.NoError(t, validateServiceFact(root, service))
		entry := onboard.TestCommand{Directory: ".", Cypress: &onboard.CypressCommand{Start: [][]string{words}, WaitOn: []string{service.ReadinessURL}}}
		require.ErrorContains(t, applyPortFact(root, &entry, onboard.PortOverrideFact{Environment: "PORT", SourceFile: "server.js"}), "does not declare")
		requireWriteFile(t, filepath.Join(root, "server.js"), `const port=process.env.PORT || 8888`)
		require.NoError(t, applyPortFact(root, &entry, onboard.PortOverrideFact{Environment: "PORT", SourceFile: "server.js"}))
		requireWriteFile(t, filepath.Join(root, "server.js"), `const port=process.env.PORT_X || 8888`)
	}
}

func TestPnpmServiceShorthandMapsOnlyScripts(t *testing.T) {
	require.Equal(t, []string{"pnpm", "run", "start"}, serviceScriptArgv([]string{"pnpm", "start"}))
	require.Equal(t, []string{"pnpm", "run", "dev"}, serviceScriptArgv([]string{"pnpm", "dev"}))
	require.Equal(t, []string{"pnpm", "install"}, serviceScriptArgv([]string{"pnpm", "install"}), "pnpm runs its built-in, not a script")
	require.Equal(t, []string{"pnpm", "--silent"}, serviceScriptArgv([]string{"pnpm", "--silent"}))
}

func TestDependencyHandoffReturnsFreshFactsTemplateInsteadOfDeadEnd(t *testing.T) {
	root := workspace.CanonicalRoot(t.TempDir())
	t.Chdir(root)
	requireWriteFile(t, "package.json", `{"private":true,"scripts":{"test":"cucumber-js features"}}`)
	requireWriteFile(t, "build.js", `// repository setup`)
	require.NoError(t, os.MkdirAll(".github/workflows", 0700))
	requireWriteFile(t, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	scope, err := onboard.DiscoverWorkflowScope(root, "cucumber")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&scope)
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	facts := setupFactsTemplate(fingerprint, scope.Commands[0])
	facts.Configurations[0].Prerequisites[0] = onboard.PrerequisiteFact{Argv: []string{"node", "build.js"}, ExpectedFiles: []string{"dist/result.js"}}
	data, err := json.Marshal(facts)
	require.NoError(t, err)
	factsFile := filepath.Join(t.TempDir(), "facts.json")
	require.NoError(t, os.WriteFile(factsFile, data, 0600))
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Cucumber{}, language: "javascript", tracerVersion: "6.20.1", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
	drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) { return "6.20.1", nil }
	var out bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &out, WorkflowOptions{Apply: true, Format: "json", FactsFile: factsFile}))
	var state onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "setup_dependencies_missing", state.Code)
	require.Contains(t, state.NextCommand, "nyc@17.1.0")
	// Model exactly the manifest/lockfile changes of the declared package-manager
	// handoff; no registry or unrelated repository edit is involved.
	requireWriteFile(t, "package.json", `{"private":true,"scripts":{"test":"cucumber-js features"},"devDependencies":{"nyc":"17.1.0"}}`)
	requireWriteFile(t, "package-lock.json", `{"lockfileVersion":3}`)
	out.Reset()
	require.Error(t, drive.Workflow(t.Context(), &out, WorkflowOptions{Apply: true, Format: "json", FactsFile: factsFile, Resume: state.RunID}))
	state = onboard.AgentResult{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "facts_refresh_required", state.Code)
	require.NotNil(t, state.InputTemplate)
	require.Empty(t, state.NextCommand)
	current, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, current, state.InputTemplate.Fingerprint)
	data, err = json.Marshal(state.InputTemplate)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(factsFile, data, 0600))
	fresh, err := onboard.DiscoverWorkflowScope(root, "cucumber")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&fresh)
	_, err = applySetupFacts(root, factsFile, current, &fresh)
	require.NoError(t, err)
	require.Len(t, fresh.Commands[0].Prerequisites, 1)
}

func TestSetupPreviewHonorsSetupFacts(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest","build":"node build.js"},"devDependencies":{"jest":"30.5.1"}}`)
	requireWriteFile(t, filepath.Join(root, "build.js"), `// repository setup`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm run build\n      - run: npm test\n")
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewJest(platform.NewJavaScript()), language: "javascript"}
	plain, err := onboard.DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.NotEmpty(t, plain.Unresolved, "without facts the preview stops at CI discovery")

	scope, err := onboard.DiscoverWorkflowScope(root, "jest")
	require.NoError(t, err)
	onboard.AssignConfigurationIDs(&scope)
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	facts := setupFactsTemplate(fingerprint, scope.Commands[0])
	facts.Configurations[0].Prerequisites[0].ExpectedFiles = []string{"dist/result.js"}
	data, err := json.Marshal(facts)
	require.NoError(t, err)
	factsFile := filepath.Join(t.TempDir(), "facts.json")
	require.NoError(t, os.WriteFile(factsFile, data, 0600))

	resolved, ctx, err := drive.factsSetupScope(t.Context(), factsFile)
	require.NoError(t, err)
	require.NotEqual(t, t.Context(), ctx, "runtime checks see the facts-backed scope")
	require.Empty(t, resolved.Unresolved)
	require.Len(t, resolved.Commands, 1)
	require.False(t, missingPrerequisiteFacts(resolved.Commands[0]))
	require.Empty(t, resolved.Commands[0].UnvalidatedSetup)

	requireWriteFile(t, factsFile, `{"schema_version":2,"input_fingerprint":"stale"}`)
	_, _, err = drive.factsSetupScope(t.Context(), factsFile)
	require.ErrorContains(t, err, "setup facts")
}
