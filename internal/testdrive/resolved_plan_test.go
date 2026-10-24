package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestPlanIDsPreserveVariantsAndSurviveLocationAndFactChanges(t *testing.T) {
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{
		{Command: "vitest run", Directory: ".", Environment: map[string]string{"MODE": "development"}},
		{Command: "vitest run", Directory: ".", Environment: map[string]string{"MODE": "production"}},
		{Command: "vitest run", Directory: "packages/app"},
	}}
	onboard.AssignConfigurationIDs(&scope)
	ids := []string{scope.Commands[0].ID, scope.Commands[1].ID, scope.Commands[2].ID}
	require.NotEqual(t, ids[0], ids[1])
	require.NotEqual(t, ids[0], ids[2])
	scope.Commands[0].Locations = []string{"CI step moved"}
	scope.Commands[0].Prerequisites = []onboard.BuildCommand{{Kind: "agent-prerequisite", FactSource: "agent-supplied", Command: "npm run build"}}
	onboard.AssignConfigurationIDs(&scope)
	require.Equal(t, ids[0], scope.Commands[0].ID)
}

func TestConfigurationFactsTargetIDAndCannotChangeScope(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/app"), 0755))
	requireWriteFile(t, filepath.Join(root, "packages/app/package.json"), `{"scripts":{"build":"node build.js"}}`)
	requireWriteFile(t, filepath.Join(root, "packages/app/build.js"), `console.log('build')`)
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "packages/app", Command: "vitest run"}, {Directory: ".", Command: "vitest run"}}, Unresolved: []string{"unresolved CI condition"}}
	onboard.AssignConfigurationIDs(&scope)
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	facts := SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{{ConfigurationID: scope.Commands[0].ID, Directory: "packages/app", Prerequisites: []PrerequisiteFact{{Argv: []string{"npm", "run", "build"}, ExpectedFiles: []string{"packages/app/dist/index.js"}}}}}}
	path := filepath.Join(t.TempDir(), "facts.json")
	write := func() {
		data, err := json.Marshal(facts)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	write()
	_, err = applySetupFacts(root, path, fingerprint, &scope)
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	require.Empty(t, scope.Commands[1].Prerequisites)
	require.Equal(t, []string{"unresolved CI condition"}, scope.Unresolved)
	require.Equal(t, "packages/app", scope.Commands[0].Prerequisites[0].Directory)
	facts.Configurations[0].Directory = "."
	write()
	_, err = applySetupFacts(root, path, fingerprint, &scope)
	require.ErrorContains(t, err, "agree")
	facts.Configurations[0].Directory = "packages/app"
	facts.Configurations[0].ConfigurationID = "invented"
	write()
	_, err = applySetupFacts(root, path, fingerprint, &scope)
	require.ErrorContains(t, err, "unknown configuration_id")
}

func TestCheckpointBindsScopeExpiresAndRemoves(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	c, err := openCheckpoint("", "/repo", "vitest", "", true, true)
	require.NoError(t, err)
	require.NoError(t, c.save())
	_, err = openCheckpoint(c.RunID, "/other", "vitest", "", true, true)
	require.ErrorContains(t, err, "scope")
	_, err = openCheckpoint(c.RunID, "/repo", "jest", "", true, true)
	require.Error(t, err)
	_, err = openCheckpoint("../../file", "/repo", "vitest", "", true, true)
	require.Error(t, err)
	loaded, err := openCheckpoint(c.RunID, "/repo", "vitest", "", true, true)
	require.NoError(t, err)
	require.Equal(t, c.RunID, loaded.RunID)
	c.Expires = time.Now().Add(-time.Second)
	require.NoError(t, c.save())
	_, err = openCheckpoint(c.RunID, "/repo", "vitest", "", true, true)
	require.ErrorContains(t, err, "expired")
	path, err := checkpointPath(c.RunID)
	require.NoError(t, err)
	require.NoFileExists(t, path)
}

func TestPreflightReceiptInvalidatesWhenAnyPreparationInputChanges(t *testing.T) {
	c := workflowCheckpoint{Tracer: "6.16.0", Fingerprint: "source", Node: "v24", FactsSHA256: "facts", Engine: engineIdentity()}
	require.True(t, c.reusable("source", "v24", "facts", "6.16.0", true))
	for _, change := range []struct{ source, node, facts, selector string }{{"changed", "v24", "facts", "6.16.0"}, {"source", "v20", "facts", "6.16.0"}, {"source", "v24", "new facts", "6.16.0"}, {"source", "v24", "facts", "5.0.0"}} {
		require.False(t, c.reusable(change.source, change.node, change.facts, change.selector, true))
	}
	c.Engine = "older binary"
	require.False(t, c.reusable("source", "v24", "facts", "6.16.0", true))
}

func TestInvalidResumeReleasesWorkflowLock(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", Resume: "invalid"}), "resume_invalid")
	lock, err := acquireWorkflowLock(root)
	require.NoError(t, err, "a rejected resume must not block the next workflow")
	require.NoError(t, lock.Close())
}

func TestBuildFactExecutesInDeclaredPackageAndRequiresItsOutput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	root := canonicalWorkflowRoot(t.TempDir())
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/app"), 0755))
	requireWriteFile(t, filepath.Join(root, "packages/app/build.cjs"), `require('fs').writeFileSync('built.js','ok')`)
	drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
	item := configurationResult{TestCommand: onboard.TestCommand{Directory: "packages/app", Prerequisites: []onboard.BuildCommand{{Kind: "agent-prerequisite", Directory: "packages/app", Command: "node build.cjs", ExpectedFiles: []string{"packages/app/built.js"}}}}}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item))
	require.FileExists(t, filepath.Join(root, "packages/app/built.js"))
	require.NoFileExists(t, filepath.Join(root, "built.js"))
	item.Preparation = nil
	item.Prerequisites[0].ExpectedFiles = []string{"packages/app/missing.js"}
	require.ErrorContains(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item), "Declared build output")
}

func TestResumeAlwaysExecutesNewPairAndStopsIdenticalFailures(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"name":"fixture"}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "latest", executor: workflowCheckExecutor{true}, command: "vitest", nodeVersion: func() string { return "v24.0.0" }}
	calls := 0
	drive.workflowRun = func(ctx context.Context, out io.Writer) error {
		calls++
		return finishValidation(out, root, completeValidationFixture("vitest"))
	}
	resume := ""
	for attempt := 1; attempt <= 3; attempt++ {
		var output bytes.Buffer
		require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", Resume: resume}))
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result))
		require.NotNil(t, result.PlanSummary)
		resume = result.RunID
		if attempt == 3 {
			require.Equal(t, "report_result", result.NextAction)
			require.Empty(t, result.ResumeCommand)
		} else {
			require.Contains(t, result.ResumeCommand, "--resume")
		}
	}
	require.Equal(t, 3, calls, "no test verdict can be reused")
	drive.executor = workflowCheckExecutor{}
	var output bytes.Buffer
	require.NoError(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", Resume: resume}))
	require.Equal(t, 4, calls)
	path, err := checkpointPath(resume)
	require.NoError(t, err)
	require.NoFileExists(t, path)
}

func TestApplicationServiceUsesDeclaredPortOverrideAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "server.cjs"), `require('http').createServer((q,s)=>s.end('ready')).listen(Number(process.env.APP_PORT),'127.0.0.1')`)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = occupied.Close() }()
	address := "http://" + occupied.Addr().String()
	service := onboard.ServiceCommand{Argv: []string{"node", "server.cjs"}, Directory: ".", ReadinessURL: address, TimeoutSeconds: 5}
	drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
	item := configurationResult{TestCommand: onboard.TestCommand{Services: []onboard.ServiceCommand{service}}}
	_, stop, err := drive.startSetupServices(t.Context(), io.Discard, &item)
	require.ErrorContains(t, err, "preserve")
	require.NoError(t, stop())
	item.Services[0].PortEnvironment = "APP_PORT"
	ctx, stop, err := drive.startSetupServices(t.Context(), io.Discard, &item)
	require.NoError(t, err)
	selected := "127.0.0.1:" + item.Environment["APP_PORT"]
	require.NotEqual(t, occupied.Addr().String(), selected)
	response, err := http.Get("http://" + selected)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.NoError(t, ctx.Err())
	require.NoError(t, stop())
	connection, err := net.DialTimeout("tcp", selected, time.Second)
	if err == nil {
		_ = connection.Close()
	}
	require.Error(t, err, "owned service must stop")
	connection, err = net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
	require.NoError(t, err, "unrelated listener must survive")
	_ = connection.Close()
}

func TestApplicationServiceFailureAndCancellationAreBounded(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	for _, script := range []string{`process.exit(2)`, `setInterval(()=>{},100)`} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "server.cjs"), script)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		address := fmt.Sprint("http://", listener.Addr())
		_ = listener.Close()
		drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
		item := configurationResult{TestCommand: onboard.TestCommand{Services: []onboard.ServiceCommand{{Argv: []string{"node", "server.cjs"}, Directory: ".", ReadinessURL: address, TimeoutSeconds: 1}}}}
		started := time.Now()
		_, stop, err := drive.startSetupServices(t.Context(), io.Discard, &item)
		require.ErrorContains(t, err, "readiness")
		_ = stop()
		require.Less(t, time.Since(started), 5*time.Second)
	}
}

func TestCypressSelectionUsesBothLocalAndActionRuntime(t *testing.T) {
	check := jestPreflight{Version: "15.4.0", Node: "20.20.2"}
	ci := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Node: "20"}}}
	resolver := func(_ context.Context, major string) (platform.JSSelection, error) {
		if strings.HasPrefix(major, "6.") {
			return platform.JSSelection{Version: "6.21.0", Node: ">=22"}, nil
		}
		return platform.JSSelection{Version: "5.132.0", Node: ">=18"}, nil
	}
	selected, _ := recommendJavaScriptTracer(t.Context(), check, ci, resolver, func(context.Context) (map[string]string, error) {
		return map[string]string{"latest": "6.21.0", "latest-node18": "5.132.0"}, nil
	}, "cypress")
	require.NotNil(t, selected)
	require.Equal(t, "5.132.0", selected.Selection.Version)
	require.Empty(t, selected.Unsupported)
}

func TestCustomHookHandoffContainsResolvedTracerAndResumableScope(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run"},"devDependencies":{"cypress":"15.4.0"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npm test\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Cypress{}, language: "javascript", tracerVersion: "latest", allConfigurations: true, nodeVersion: func() string { return "20.20.2" }}
	selections := 0
	drive.workflowSelect = func(_ context.Context, scope onboard.ValidationScope, _ bool) (string, error) {
		selections++
		require.Len(t, scope.Commands, 1)
		return "5.132.0", nil
	}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "ci_edit_unsupported")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Equal(t, 1, selections)
	require.Equal(t, "5.132.0", result.PlanSummary.Tracer)
	require.Contains(t, result.ResumeCommand, "--tracer-version")
	require.Contains(t, result.ResumeCommand, "5.132.0")
	require.Contains(t, result.ResumeCommand, "--resume")
	_, err := openCheckpoint(result.RunID, canonicalWorkflowRoot(root), "cypress", "", false, true)
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(root, ".gitignore"), "handoff does not guess unsupported CI hooks")
}

func TestMetadataTransportFailureRequestsCapabilityNotRuntimeChange(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", allConfigurations: true, nodeVersion: func() string { return "24.14.1" }}
	drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) {
		return "", &url.Error{Op: "Get", URL: "https://raw.githubusercontent.com/vendor/action/ref/action.yml", Err: &net.DNSError{Name: "raw.githubusercontent.com", Err: "no such host"}}
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "blocked_environment", result.Status)
	require.Equal(t, "metadata_network", result.Code)
	require.Equal(t, []string{"network access to raw.githubusercontent.com"}, result.Capabilities)
	require.Empty(t, result.RequiredInput)
	require.Contains(t, result.ResumeCommand, "--resume")
}

func TestCypressManagedStartupRejectsOccupiedPortBeforeTests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	plan := &onboard.CypressCommand{Start: [][]string{{"npm", "run", "start"}}, WaitOn: []string{"http://" + listener.Addr().String()}}
	require.ErrorContains(t, checkCypressStartupPorts(plan), "Preserve that listener")
	plan.Start = nil
	require.NoError(t, checkCypressStartupPorts(plan), "externally managed services are not ours to reject or stop")
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	_ = connection.Close()
}

func TestCypressPortFactsPreserveCIAndActionOptions(t *testing.T) {
	root := canonicalWorkflowRoot(t.TempDir())
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"start":"node serve.js"}}`)
	requireWriteFile(t, filepath.Join(root, "serve.js"), `const port=process.env.PORT || 8888`)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	original := &onboard.CypressCommand{Action: "cypress-io/github-action@v6", Start: [][]string{{"npm", "run", "start"}}, WaitOn: []string{"http://" + listener.Addr().String()}, Options: map[string]any{"record": false, "spec": "app.cy.js"}}
	entry := onboard.TestCommand{Directory: ".", Cypress: original, Environment: map[string]string{"COVERAGE": "true"}}
	require.NoError(t, applyPortFact(root, &entry, onboard.PortOverrideFact{Environment: "PORT", SourceFile: "serve.js"}))
	require.Same(t, original, entry.Cypress, "planning must not reserve a port or modify the action")
	require.NotNil(t, entry.PortOverride)
	require.Empty(t, entry.Environment["PORT"])
	require.NoError(t, configurePortFact(root, &entry, *entry.PortOverride, true))
	require.NotSame(t, original, entry.Cypress)
	require.Equal(t, "http://"+listener.Addr().String(), original.WaitOn[0])
	require.Equal(t, original.Start, entry.Cypress.Start)
	require.Equal(t, "app.cy.js", entry.Cypress.Options["spec"])
	require.Equal(t, "true", entry.Environment["COVERAGE"])
	require.NotEmpty(t, entry.Environment["PORT"])
	config := entry.Cypress.Options["config"].(map[string]any)
	require.Equal(t, entry.Cypress.WaitOn[0], config["baseUrl"])
	require.Contains(t, entry.Cypress.WaitOn[0], ":"+entry.Environment["PORT"])
	require.NoError(t, checkCypressStartupPorts(entry.Cypress))
	require.NotEmpty(t, entry.LocalOverrides)
	require.ErrorContains(t, applyPortFact(root, &entry, onboard.PortOverrideFact{Environment: "OTHER_PORT", SourceFile: "serve.js"}), "does not declare")
	original.Options["config"] = "viewportWidth=1000"
	entry.Cypress = original
	require.ErrorContains(t, applyPortFact(root, &entry, onboard.PortOverrideFact{Environment: "PORT", SourceFile: "serve.js"}), "custom Cypress config string")
}
