package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestWebdriverIOBrowserCacheOutlivesTheSession(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	env := map[string]string{"TMPDIR": filepath.Join(temporary, "session", "tmp-run")}
	webdriverIOCacheEnvironment(env)
	// WebdriverIO's own default, not the session folder each run removes.
	require.Equal(t, temporary, env["DDTEST_WDIO_CACHE"])
	require.NotContains(t, env, "PUPPETEER_CACHE_DIR")
}

func TestWebdriverIOIdentityCountsKeepCapabilitiesAndDuplicateTitles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "test", "one.js")
	run := validationRun{framework: "webdriverio", root: root, Tests: []jestTest{
		{File: file, Name: "same", Status: "passed", WebdriverIOBrowser: "chrome", WebdriverIOCapability: "0", WebdriverIOTestID: "0-0"},
		{File: file, Name: "same", Status: "passed", WebdriverIOBrowser: "chrome", WebdriverIOCapability: "0", WebdriverIOTestID: "0-1"},
		{File: file, Name: "same", Status: "passed", WebdriverIOBrowser: "firefox", WebdriverIOCapability: "1", WebdriverIOTestID: "0-0"},
	}}
	counts := nativeExecutionCounts(run)
	require.Empty(t, counts.Reason)
	require.NotNil(t, counts.SourceTestIdentities)
	require.Equal(t, 3, *counts.SourceTestIdentities)
}

func TestWebdriverIODifferencesUseReportedNumberingAndRepositoryPaths(t *testing.T) {
	root := t.TempDir()
	run := validationRun{framework: "webdriverio", root: root, Tests: []jestTest{{File: filepath.Join(root, "test", "one.js"), Name: "works", Status: "failed", WebdriverIOCapability: "0", WebdriverIOTestID: "0-0"}}}
	keys := strings.Join(outcomeKeys(run), "\n")
	require.Contains(t, keys, "test/one.js › works [capability 1, test 0-0]: failed")
	require.NotContains(t, keys, root)
}

func TestWebdriverIOMissingNativeReportNamesTheWorkerEvidence(t *testing.T) {
	var baseline validationRun
	readWebdriverIOResults(filepath.Join(t.TempDir(), "native"), &baseline)
	require.Contains(t, baseline.ResultError, "WebdriverIO native worker evidence is missing")
	require.Contains(t, compareJest(baseline, validationRun{}).Reason, "WebdriverIO native worker evidence is missing")
}

func TestWebdriverIOPublishedReportRedactsGridKeys(t *testing.T) {
	result := validationResult{Framework: "webdriverio", Runs: []runSummary{{Command: "npx wdio run wdio.conf.js --user fake-user --key fake-long-key"}},
		Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "npx wdio run wdio.conf.js -k fake-short-key"}, Status: "passed"}}}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"fake-long-key", "fake-short-key"} {
		require.NotContains(t, string(data), secret)
	}
	require.Contains(t, string(data), "--key [REDACTED]")
}

func TestWebdriverIOPreviewRedactsGridKeys(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"@wdio/cli":"9.20.0"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: npx wdio run wdio.conf.js --key fake-ci-key\n")
	t.Chdir(root)
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	for _, all := range []bool{false, true} {
		// --command and --all are exclusive: preview the override, then the CI scope.
		viper.Reset()
		viper.Set("framework", "webdriverio")
		if !all {
			viper.Set("command", "npx wdio run wdio.conf.js --key fake-key-123")
		}
		settings.Init()
		drive, err := Prepare(PrepareOptions{TracerVersion: "6.21.0", All: all})
		require.NoError(t, err)
		var output bytes.Buffer
		drive.Preview(&output)
		require.NotContains(t, output.String(), "fake-key-123")
		require.NotContains(t, output.String(), "fake-ci-key")
		require.Contains(t, output.String(), "--key [REDACTED]")
	}
}

func TestWebdriverIOReporterUsesTheCLIThatStartedTheRun(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	// The current package can resolve the runner but not @wdio/cli, as in a
	// workspace whose CLI lives in another package.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "@wdio"), 0700))
	require.NoError(t, os.Symlink(filepath.Join(modules, "@wdio", "local-runner"), filepath.Join(root, "node_modules", "@wdio", "local-runner")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	_, err = framework.PrepareWebdriverIOObserver(root)
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `await import('./webdriverio_reporter.mjs')`)
	cmd.Dir = root
	// pnpm links @wdio/cli into node_modules; its dependencies sit next to its
	// real location, not the link.
	linked := filepath.Join(root, "linked-cli")
	require.NoError(t, os.Symlink(filepath.Join(modules, "@wdio", "cli"), linked))
	cmd.Env = append(os.Environ(), "DDTEST_WDIO_CLI="+filepath.Join(linked, "build", "index.js"))
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
}
