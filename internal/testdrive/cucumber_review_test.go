package testdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestCucumberProbeIsolatesSelectionAndKeepsCoverage(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"nyc --check-coverage -- cucumber-js --tags @smoke --name critical --profile ci features/a.feature"}}`)
	d := &Testdrive{repositoryRoot: root, command: "npm", args: []string{"test"}, cucumberNode: "/node", cucumberExecutable: "/cucumber.js", cucumberCoverage: map[string]string{"nyc": "/nyc.js"}, cucumberSupport: &cucumberSupport{RequirePaths: []string{"/support.cjs"}, ImportPaths: []string{"/steps.mjs"}, RequireModules: []string{"ts-node/register"}}}
	command, args, err := d.cucumberProbeCommand(filepath.Join(root, "probe.feature"))
	require.NoError(t, err)
	require.Equal(t, "/node", command)
	require.Equal(t, []string{"/nyc.js", "--check-coverage=false", "--", "/node", "/cucumber.js", "--config", "probe.feature.config.cjs", "--require", "/support.cjs", "--import", "/steps.mjs", "--require-module", "ts-node/register", filepath.Join(root, "probe.feature"), "--require", filepath.Join(root, "probe.feature.steps.cjs"), "--retry", "0"}, args)
}

func TestCucumberRetryTelemetryChecksEveryNativeAttempt(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "native.ndjson")
	require.NoError(t, os.WriteFile(filename, []byte(cucumberMessages()), 0600))
	run := validationRun{framework: "cucumber", root: root}
	readCucumberResults(filename, root, &run)
	require.Equal(t, []string{"failed", "passed"}, run.Tests[0].CucumberAttempts)
	run.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{{Name: "same", SourceFile: "test/example.feature", Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true}}}}}
	require.Empty(t, verifyCucumberTelemetry(run).Status)
	run.Facts.Tests[0].Attempts[1].RetryReason = "efd"
	require.Equal(t, "unexpected_retry", verifyCucumberTelemetry(run).Code)
}

func TestCucumberLocationsDisambiguateOutlineRowsAndRules(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "native.ndjson")
	messages := `{"gherkinDocument":{"feature":{"children":[{"scenario":{"id":"outline","location":{"line":3},"examples":[{"tableBody":[{"id":"row1","location":{"line":8}},{"id":"row2","location":{"line":9}}]}]}}]}}}
`
	for i, row := range []string{"row1", "row2"} {
		id := string(rune('a' + i))
		messages += `{"pickle":{"id":"` + id + `","uri":"one.feature","name":"same","astNodeIds":["outline","` + row + `"]}}
{"testCase":{"id":"c` + id + `","pickleId":"` + id + `"}}
{"testCaseStarted":{"id":"s` + id + `","testCaseId":"c` + id + `"}}
{"testCaseFinished":{"testCaseStartedId":"s` + id + `"}}
`
	}
	messages += `{"testRunFinished":{"success":true}}`
	require.NoError(t, os.WriteFile(filename, []byte(messages), 0600))
	run := validationRun{framework: "cucumber", root: root}
	readCucumberResults(filename, root, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 2)
	require.Equal(t, 8, run.Tests[0].CucumberLine)
	require.Equal(t, 9, run.Tests[1].CucumberLine)
	require.NotEqual(t, cucumberLocation(run.Tests[0]), cucumberLocation(run.Tests[1]))
	counts := nativeExecutionCounts(run)
	require.NotNil(t, counts.SourceTestIdentities)
	require.Equal(t, 2, *counts.SourceTestIdentities)
}

func TestCucumberPendingAndUndefinedAreNotFailedScenarios(t *testing.T) {
	for _, status := range []string{"PENDING", "UNDEFINED", "AMBIGUOUS"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			filename := filepath.Join(root, "messages")
			data := strings.ReplaceAll(cucumberMessages(), `"FAILED"`, `"`+status+`"`)
			data = strings.ReplaceAll(data, `"willBeRetried":true`, `"willBeRetried":false`)
			require.NoError(t, os.WriteFile(filename, []byte(data), 0600))
			run := validationRun{framework: "cucumber", root: root}
			readCucumberResults(filename, root, &run)
			want := strings.ToLower(status)
			if status == "AMBIGUOUS" {
				want = "failed"
			}
			require.Equal(t, want, run.Tests[0].Status)
		})
	}
}

func TestCucumberLegacyFinalHookErrorsRemainBehavioralEvidence(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "messages")
	data := strings.ReplaceAll(cucumberMessages(), `"testRunFinished":{"success":true}`, `"testRunFinished":{"success":false,"message":"AfterAll failed"}`)
	require.NoError(t, os.WriteFile(filename, []byte(data), 0600))
	run := validationRun{framework: "cucumber", root: root}
	readCucumberResults(filename, root, &run)
	require.Equal(t, []string{"AfterAll failed"}, run.SuiteErrors)
}
func TestCucumberExecutionDoesNotAddACoverageWrapper(t *testing.T) {
	drive := &Testdrive{command: "c8", args: []string{"--", "cucumber-js"}}
	command, args := drive.executionCommand()
	require.Equal(t, "c8", command)
	require.Equal(t, drive.args, args)
}

func TestCucumberProbeKeepsTheOptionsWorldAndHooksNeed(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cucumber-js --world-parameters '{\"baseUrl\":\"http://cli.test\"}' --exit --tags @smoke --profile ci --world-parameters='{\"browser\":\"chrome\"}'"}}`)
	d := &Testdrive{repositoryRoot: root, command: "npm", args: []string{"test"}, cucumberNode: "/node", cucumberExecutable: "/cucumber.js", cucumberSupport: &cucumberSupport{}}
	command, args, err := d.cucumberProbeCommand(filepath.Join(root, "probe.feature"))
	require.NoError(t, err)
	require.Equal(t, "/node", command)
	require.Equal(t, []string{"/cucumber.js", "--config", "probe.feature.config.cjs", "--world-parameters", `{"baseUrl":"http://cli.test"}`, "--exit", "--world-parameters", `{"browser":"chrome"}`, filepath.Join(root, "probe.feature"), "--require", filepath.Join(root, "probe.feature.steps.cjs"), "--retry", "0"}, args)

	// Profile options come from the resolved configuration; selection does not.
	config, err := cucumberProbeConfig(&cucumberSupport{RunOptions: map[string]any{"forceExit": true, "worldParameters": map[string]any{"baseUrl": "http://profile.test"}, "tags": "@smoke"}})
	require.NoError(t, err)
	require.Equal(t, `module.exports={default:{"forceExit":true,"worldParameters":{"baseUrl":"http://profile.test"}}};`+"\n", string(config))
	config, err = cucumberProbeConfig(nil)
	require.NoError(t, err)
	require.Equal(t, "module.exports={default:{}};\n", string(config))
}
