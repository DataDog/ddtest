package testdrive

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

// webdriverIOInspectExecutor answers the preflight's config inspection with a
// supported local Mocha config; other commands (tracer metadata) succeed empty.
type webdriverIOInspectExecutor struct{ ports []int }

func (e *webdriverIOInspectExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	return e.CombinedOutputInDirectory(ctx, "", name, args, env)
}

func (e *webdriverIOInspectExecutor) CombinedOutputInDirectory(_ context.Context, _ string, _ string, _ []string, env map[string]string) ([]byte, error) {
	if path := env["DDTEST_WDIO_INSPECT"]; path != "" {
		ports := "[]"
		if len(e.ports) > 0 {
			ports = "[" + strconv.Itoa(e.ports[0]) + "]"
		}
		return nil, os.WriteFile(path, []byte(`{"node":"24.14.1","version":"9.20.0","runner":"local","framework":"mocha","files":["test/a.js"],"static_server_ports":`+ports+`,"capabilities":[{"browserName":"chrome"}]}`), 0600)
	}
	return nil, nil
}

func webdriverIOPreflightDrive(t *testing.T) *Testdrive {
	t.Helper()
	drive := preparedTestdrive(t)
	drive.framework = framework.NewWebdriverIO(platform.NewJavaScript())
	drive.platform = platform.NewJavaScript()
	writeMochaManifest(t, drive.repositoryRoot, `{"scripts":{"test":"wdio run wdio.conf.cjs"},"devDependencies":{"@wdio/cli":"9.20.0"}}`)
	drive.command, drive.args = "npm", []string{"test"}
	drive.executor = &webdriverIOInspectExecutor{}
	return drive
}

func TestWebdriverIOPreflightSelectsTheTracerOfTheCIEnvironment(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	drive := webdriverIOPreflightDrive(t)
	writeFakeTracer(t, filepath.Join(drive.repositoryRoot, "node_modules"), "5.132.0")
	// The developer's shell preloads another tracer; the CI step clears NODE_OPTIONS.
	t.Setenv("NODE_OPTIONS", "--require "+writeFakeTracer(t, filepath.Join(t.TempDir(), "node_modules"), "6.21.0"))
	drive.configurationEnvironment = map[string]string{"NODE_OPTIONS": ""}
	result := validationResult{Framework: "webdriverio", CIRuntime: &onboard.RuntimeCheck{}}
	_ = drive.checkWebdriverIOPreflight(t.Context(), io.Discard, &result)
	require.NotNil(t, result.Selection)
	require.Equal(t, "project", result.Selection.Source)
	require.Equal(t, "5.132.0", result.Selection.Version)
}

func TestWebdriverIOPreflightDefersGitTracersToInstallation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	for _, checkOnly := range []bool{false, true} {
		drive := webdriverIOPreflightDrive(t)
		drive.tracerVersion, drive.checkOnly = "git:https://github.com/DataDog/dd-trace-js.git#master", checkOnly
		result := validationResult{Framework: "webdriverio", CIRuntime: &onboard.RuntimeCheck{}}
		err := drive.checkWebdriverIOPreflight(t.Context(), io.Discard, &result)
		require.Empty(t, result.Selection.Version, "a git: fallback has no version before installation")
		require.Equal(t, webdriverIOTracerUnresolved, result.Preflight.Verdict.Code)
		if checkOnly {
			require.ErrorContains(t, err, "webdriverio preflight", "check-only cannot install, so it stays inconclusive")
		} else {
			require.NoError(t, err, "full runs check the installed build after installation")
		}
	}
}

func TestWebdriverIOGitDeferralKeepsOtherBlockers(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	drive := webdriverIOPreflightDrive(t)
	drive.executor = &webdriverIOInspectExecutor{ports: []int{listener.Addr().(*net.TCPAddr).Port}}
	drive.tracerVersion = "git:https://github.com/DataDog/dd-trace-js.git#master"
	result := validationResult{Framework: "webdriverio", CIRuntime: &onboard.RuntimeCheck{}}
	err = drive.checkWebdriverIOPreflight(t.Context(), io.Discard, &result)
	require.ErrorContains(t, err, "is unavailable", "a deferred tracer check must not hide a busy server port")
	// Known incompatibilities still block a git: tracer before installation.
	check := jestPreflight{Version: "8.40.0", WebdriverIO: &framework.WebdriverIOConfig{Runner: "local", Framework: "mocha", FileCount: 1, Capabilities: []map[string]string{{"browserName": "chrome"}}}}
	require.Equal(t, "incompatible", checkWebdriverIOSupport(check, platform.JSSelection{Requested: "git:https://example.test/dd-trace.git"}).Status)
}
