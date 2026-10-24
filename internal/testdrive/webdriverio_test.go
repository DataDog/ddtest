package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestWebdriverIOPortConflictStopsBeforeTests(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	err = checkWebdriverIOServerPorts([]int{port})
	var networkError *net.OpError
	require.ErrorAs(t, err, &networkError)
	require.NoError(t, listener.Close())
	require.NoError(t, checkWebdriverIOServerPorts([]int{port}))
	require.Error(t, checkWebdriverIOServerPorts([]int{-1}))
}

func TestWebdriverIOPortHeldOnLoopbackIsDetected(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		err = checkWebdriverIOServerPorts([]int{port})
		require.ErrorContains(t, err, fmt.Sprintf("WDIO static-server port %d is unavailable", port), host)
		require.NoError(t, listener.Close())
	}
}

func TestWebdriverIORuntimeAndTracerCapabilities(t *testing.T) {
	check := jestPreflight{Version: "9.20.1", Node: "v24.14.1", WebdriverIO: &framework.WebdriverIOConfig{Runner: "local", Framework: "mocha", Files: []string{"spec.js"}, Capabilities: []map[string]string{{"browserName": "chrome"}}}}
	require.Equal(t, "compatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.132.0", Node: ">=18"}).Status)
	require.Equal(t, "incompatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.120.0", Node: ">=18"}).Status)
	check.Node = "v20.19.0"
	require.Equal(t, "incompatible", checkWebdriverIOSupport(check, platform.JSSelection{Version: "6.21.0", Node: ">=22"}).Status)
	check.WebdriverIO.Runner = "browser"
	require.Equal(t, "inconclusive", checkWebdriverIOSupport(check, platform.JSSelection{Version: "5.132.0", Node: ">=18"}).Status)
	check.WebdriverIO.Runner = "local"
	check.WebdriverIO.Multiremote, check.WebdriverIO.Capabilities = true, nil
	multiremote := checkWebdriverIOSupport(check, platform.JSSelection{Version: "6.21.0", Node: ">=18"})
	require.Equal(t, "inconclusive", multiremote.Status)
	require.Equal(t, "multiremote_unsupported", multiremote.Code)
	require.Contains(t, multiremote.Reason, "multiremote")
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

func TestWebdriverIORepeatedFeaturesRequireBodyExecutions(t *testing.T) {
	identity := intake.Test{Module: "webdriverio", Suite: "probe.js", SourceFile: "probe.js", Name: probeName}
	for _, feature := range []string{"early-flake-detection", "attempt-to-fix"} {
		first := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "pass", "test.test_management.is_attempt_to_fix": "true"}
		retry := map[string]string{}
		for k, v := range first {
			retry[k] = v
		}
		retry["test.is_retry"] = "true"
		retry["test.retry_reason"] = map[string]string{"early-flake-detection": "early_flake_detection", "attempt-to-fix": "attempt_to_fix"}[feature]
		count := 2
		run := validationRun{framework: "webdriverio", ProbeExecutions: &count, Tests: []jestTest{{Name: probeName, Status: "passed"}},
			Facts: intake.Facts{Events: []intake.Event{{Type: "test", Tags: first}, {Type: "test", Tags: retry}}}}
		require.Equal(t, "passed", evaluateFeature(feature, run, identity).Status)
		count = 1
		require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status, "duplicate telemetry cannot stand in for real body executions")
		run.ProbeExecutions = nil
		require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status)
	}
}

func TestWebdriverIORecommendationUsesWorkerCapabilities(t *testing.T) {
	check := jestPreflight{Version: "9.20.0", Node: "20.19.0", WebdriverIO: &framework.WebdriverIOConfig{Runner: "local", Framework: "mocha", Files: []string{"one.js"}, Capabilities: []map[string]string{{"browserName": "chrome"}}}}
	recommendation, _ := recommendJavaScriptTracer(t.Context(), check, nil, func(_ context.Context, major string) (platform.JSSelection, error) {
		if strings.HasPrefix(major, "6.") {
			return platform.JSSelection{Version: "6.20.1", Node: ">=22"}, nil
		}
		return platform.JSSelection{Version: "5.132.0", Node: ">=18"}, nil
	}, func(context.Context) (map[string]string, error) {
		return map[string]string{"latest": "6.20.1", "latest-node18": "5.132.0"}, nil
	}, "webdriverio")
	require.NotNil(t, recommendation)
	require.Equal(t, "5.132.0", recommendation.Selection.Version)
}

func TestWebdriverIONativeSpecRetriesCompleteSelectionOnce(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "native")
	require.NoError(t, os.Mkdir(directory, 0700))
	requireWriteFile(t, filepath.Join(root, "metadata.json"), `{"selections":[{"capability":"0","browser":"chrome","files":["spec.js"]}]}`)
	for i, status := range []string{"failed", "passed"} {
		body := fmt.Sprintf(`{"complete":true,"retry":%d,"specs":["spec.js"],"browser":"chrome","capability":"0","tests":[{"id":"test-0","file":"spec.js","name":"suite case","status":%q}]}`, i, status)
		requireWriteFile(t, filepath.Join(directory, fmt.Sprintf("attempt-%d.json", i)), body)
	}
	run := validationRun{root: root}
	readWebdriverIOResults(directory, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "passed", run.Tests[0].Status)
	require.Len(t, run.webdriverIOAttempts, 2)
	require.Equal(t, "failed", run.webdriverIOAttempts[0].Status)
}

func TestWebdriverIOEqualBrowserNamesRetainCapabilityAndTestIdentity(t *testing.T) {
	baseline := []jestTest{{File: "spec.js", Name: "duplicate", Status: "passed", WebdriverIOBrowser: "chrome", WebdriverIOCapability: "0", WebdriverIOTestID: "test-0"}, {File: "spec.js", Name: "duplicate", Status: "failed", WebdriverIOBrowser: "chrome", WebdriverIOCapability: "1", WebdriverIOTestID: "test-0"}}
	changed := slices.Clone(baseline)
	changed[0].Status, changed[1].Status = changed[1].Status, changed[0].Status
	require.NotEqual(t, outcomeKeys(validationRun{Tests: baseline}), outcomeKeys(validationRun{Tests: changed}))
	changed = slices.Clone(baseline)
	changed[1].WebdriverIOCapability = "0"
	changed[1].WebdriverIOTestID = "test-1"
	swapped := slices.Clone(changed)
	swapped[0].Status, swapped[1].Status = swapped[1].Status, swapped[0].Status
	require.NotEqual(t, outcomeKeys(validationRun{Tests: changed}), outcomeKeys(validationRun{Tests: swapped}))
}

func TestWebdriverIODependenciesResolveThroughInstalledPackages(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules/@wdio"), 0700))
	for _, pkg := range []string{"cli", "local-runner"} {
		require.NoError(t, os.Symlink(filepath.Join(modules, "@wdio", pkg), filepath.Join(root, "node_modules/@wdio", pkg)))
	}
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	observer, err := framework.PrepareWebdriverIOObserver(root)
	require.NoError(t, err)
	// Root-level transitive dependencies deliberately do not exist, like pnpm.
	output := filepath.Join(root, "selection.json")
	requireWriteFile(t, filepath.Join(root, "wdio.conf.cjs"), `exports.config={runner:'local',framework:'mocha',specs:[],capabilities:[{browserName:'chrome',specs:['./one.js']},{browserName:'chrome',specs:['./two.js']}]}`)
	requireWriteFile(t, filepath.Join(root, "one.js"), "")
	requireWriteFile(t, filepath.Join(root, "two.js"), "")
	cmd := exec.CommandContext(t.Context(), "node", "--import", observer, filepath.Join(modules, "@wdio/cli/bin/wdio.js"), "run", "wdio.conf.cjs")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DDTEST_WDIO_INSPECT="+output)
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	var config framework.WebdriverIOConfig
	data, err = os.ReadFile(output)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &config))
	require.Len(t, config.Files, 2)
	for _, key := range []string{"DDTEST_WDIO_FILES", "DDTEST_WDIO_PROBE"} {
		meta := filepath.Join(root, "metadata.json")
		require.NoError(t, os.Remove(output))
		parserScript := `import {createRequire} from 'node:module';import {pathToFileURL} from 'node:url';const r=createRequire(process.cwd()+'/package.json');const cli=r.resolve('@wdio/cli');const {resolve}=await import(pathToFileURL(createRequire(cli).resolve('import-meta-resolve')).href);const {installObserver}=await import('./webdriverio_observer.mjs');await installObserver(cli);const {ConfigParser}=await import(resolve('@wdio/config/node',pathToFileURL(cli).href));const parser=new ConfigParser(process.cwd()+'/wdio.conf.cjs');await parser.initialize({logLevel:'silent'});`
		cmd = exec.CommandContext(t.Context(), "node", "--import", observer, "--input-type=module", "-e", parserScript)
		cmd.Dir = root
		value := `["` + filepath.ToSlash(filepath.Join(root, "one.js")) + `"]`
		if key == "DDTEST_WDIO_PROBE" {
			value = filepath.Join(root, "one.js")
		}
		cmd.Env = append(os.Environ(), key+"="+value, "DDTEST_WDIO_METADATA="+meta)
		data, err = cmd.CombinedOutput()
		require.NoError(t, err, string(data))
		data, err = os.ReadFile(meta)
		require.NoError(t, err)
		var metadata struct {
			Selections []struct {
				Capability string
				Files      []string
			}
		}
		require.NoError(t, json.Unmarshal(data, &metadata))
		// A probe runs on every capability; a planned batch keeps each
		// capability's own selection, so only the one selecting one.js remains.
		want := 2
		if key == "DDTEST_WDIO_FILES" {
			want = 1
		}
		require.Len(t, metadata.Selections, want)
		for i, selection := range metadata.Selections {
			require.Equal(t, strconv.Itoa(i), selection.Capability)
			require.Equal(t, []string{filepath.Join(root, "one.js")}, selection.Files)
		}
		require.NoError(t, os.Remove(meta))
		requireWriteFile(t, output, "")
	}
	// Import the reporter from the same non-hoisted root and preserve two tests
	// with equal names while replacing only repeated events for the same uid.
	script := `const {default:Reporter}=await import('./webdriverio_reporter.mjs');const r=new Reporter({stdout:true,writeStream:process.stdout});r.currentSuites=[];r.runner={specs:['one.js']};r.record({uid:'a',title:'same'},'failed');r.record({uid:'b',title:'same'},'passed');r.record({uid:'a',title:'same'},'passed');if(r.results.size!==2||[...r.results.values()].some(t=>t.status!=='passed'))throw Error('native identities lost');`
	cmd = exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	cmd.Dir = root
	data, err = cmd.CombinedOutput()
	require.NoError(t, err, string(data))
}
