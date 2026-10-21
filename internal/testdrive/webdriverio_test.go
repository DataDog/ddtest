package testdrive

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestWebdriverIOPortConflictStopsBeforeTests(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	err = checkWebdriverIOServerPorts([]int{port})
	require.ErrorContains(t, err, "Preserve the existing process")
	require.ErrorContains(t, err, "baseUrl")
	require.NoError(t, listener.Close())
	require.NoError(t, checkWebdriverIOServerPorts([]int{port}))
	require.Error(t, checkWebdriverIOServerPorts([]int{-1}))
}

func TestWebdriverIOHandoffRequiresRerunAfterCIEdit(t *testing.T) {
	result := validationResult{Framework: "webdriverio", Summary: validationSummary{CIConfiguration: "incompatible"}}
	require.Contains(t, validationHandoff(result), "an actual CI run does not update this local report")
	result.Summary.CIConfiguration = "compatible"
	require.NotContains(t, validationHandoff(result), "an actual CI run does not update this local report")
}

func TestFocusedWebdriverIOPassKeepsRepositoryIncomplete(t *testing.T) {
	exit := 0
	result := validationResult{Framework: "webdriverio", FocusedCommand: true, ChecksPassed: true, HTMLReportCurrent: true,
		Compatibility: verdict{Status: "compatible"}, Cleanup: &verdict{Status: "passed"},
		Configurations: []configurationResult{{Status: "passed"}, {Status: "not exercised"}},
		Runs:           []runSummary{{Name: "baseline", Command: "npm run click", ExitCode: &exit}, {Name: "reporting-only", Command: "npm run click", Instrumented: true, ExitCode: &exit}}}
	for _, name := range jestFeatures {
		status := "passed"
		if name == "skipping" {
			status = "not supported"
		}
		result.Features = append(result.Features, featureResult{Name: name, Status: status})
	}
	summary := summarizeValidation(result)
	require.Equal(t, "PASSED", summary.SelectedValidation.Status)
	require.Equal(t, "INCOMPLETE", summary.Status)
	result.Summary = summary
	require.Contains(t, validationHandoff(result), "Selected WebdriverIO testdrive: PASSED")
	result.CheckOnly = true
	require.Equal(t, "NOT EXERCISED", summarizeValidation(result).SelectedValidation.Status)
	result.CheckOnly = false
	result.ChecksPassed = false
	require.Equal(t, "INCOMPLETE", summarizeValidation(result).SelectedValidation.Status)
	result.ChecksPassed = true
	result.Features = result.Features[:len(result.Features)-1]
	require.Equal(t, "INCOMPLETE", summarizeValidation(result).SelectedValidation.Status)
}

func TestWebdriverIORuntimeAndTracerCapabilities(t *testing.T) {
	check := jestPreflight{Version: "9.20.1", Node: "v24.14.1", WebdriverIO: &framework.WebdriverIOConfig{Runner: "local", Framework: "mocha", Files: []string{"spec.js"}, Capabilities: []map[string]string{{"browserName": "chrome"}}}}
	require.Equal(t, "compatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.132.0", Node: ">=18"}).Status)
	require.Equal(t, "incompatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.120.0", Node: ">=18"}).Status)
	check.Node = "v20.19.0"
	require.Equal(t, "incompatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "6.21.0", Node: ">=22"}).Status)
	check.WebdriverIO.Runner = "browser"
	require.Equal(t, "inconclusive", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.132.0", Node: ">=18"}).Status)
	require.True(t, featureSatisfied("webdriverio", featureResult{Name: "skipping", Status: "not supported"}))
	require.False(t, featureSatisfied("webdriverio", featureResult{Name: "auto-retries", Status: "not supported"}))
}
func TestWebdriverIONativeCompletionAndFailures(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"complete":true,"specs":["spec.js"],"browser":"chrome","tests":[{"file":"spec.js","name":"suite case","status":"failed","failure":"assertion failed"}],"errors":[]}`, true},
		{`{"complete":false,"tests":[]}`, false}, {`{"complete":true}`, false}, {`{`, false},
	} {
		root := t.TempDir()
		directory := filepath.Join(root, "native")
		require.NoError(t, os.Mkdir(directory, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "metadata.json"), []byte(`{"selections":[{"browser":"chrome","files":["spec.js"]}]}`), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "worker.json"), []byte(tc.body), 0600))
		run := validationRun{root: root}
		readWebdriverIOResults(directory, &run)
		require.Equal(t, tc.valid, run.ResultError == "")
		if tc.valid {
			require.Len(t, run.Tests, 1)
			require.Equal(t, "assertion failed", run.Tests[0].Failure)
		}
	}
}

func TestWebdriverIOMissingWorkerCannotPass(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "native")
	require.NoError(t, os.Mkdir(directory, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "metadata.json"), []byte(`{"selections":[{"browser":"chrome","files":["one.js","missing.js"]}]}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "worker.json"), []byte(`{"complete":true,"browser":"chrome","specs":["one.js"],"tests":[]}`), 0600))
	run := validationRun{root: root}
	readWebdriverIOResults(directory, &run)
	require.Contains(t, run.ResultError, "selected specs and capabilities")
}
