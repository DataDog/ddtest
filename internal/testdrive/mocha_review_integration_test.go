package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestMochaProbeCoverageThresholdControls(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES to Mocha, nyc, c8 and dd-trace dependencies")
	}
	for _, tool := range []string{"nyc", "c8"} {
		t.Run(tool, func(t *testing.T) {
			drive := preparedTestdrive(t)
			root := drive.repositoryRoot
			require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0700))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0700))
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"`+tool+` --all --check-coverage --functions=95 -- mocha 'test/*.cjs'"}}`)
			config := `{"all":true,"check-coverage":true,"functions":95,"include":["src/**"],"extension":[".cjs"]}`
			name := ".nycrc.json"
			if tool == "c8" {
				name = ".c8rc.json"
			}
			requireWriteFile(t, filepath.Join(root, name), config)
			requireWriteFile(t, filepath.Join(root, "src/used.cjs"), `exports.used=()=>42`)
			requireWriteFile(t, filepath.Join(root, "test/one.cjs"), `const assert=require('node:assert/strict');describe('source',()=>it('covered',()=>assert.equal(require('../src/used.cjs').used(),42)))`)
			probe := filepath.Join(root, "test/probe.cjs")
			drive.framework = framework.NewMocha(platform.NewJavaScript())
			drive.command, drive.args = "npm", []string{"test"}
			session, err := NewSession()
			require.NoError(t, err)
			defer func() { require.NoError(t, session.Close()) }()
			preload := filepath.Join(modules, "dd-trace/ci/init.js")
			full, err := drive.runMocha(t.Context(), io.Discard, session, preload, runSpec{Name: "baseline", Role: roleBaseline})
			require.NoError(t, err)
			require.Zero(t, full.ExitCode, full.commandOutput)
			requireWriteFile(t, probe, mochaProbeSource)
			for _, mode := range []string{"pass", "fail"} {
				run, err := drive.runMocha(t.Context(), io.Discard, session, preload, runSpec{Name: "probe-" + mode, Role: roleProbeControl, Instrumented: true, ProbePath: probe, ProbeMode: mode})
				require.NoError(t, err)
				require.True(t, run.ProbeCoverageThresholdsDisabled)
				status := "passed"
				if mode == "fail" {
					status = "failed"
				}
				require.True(t, validProbeControl(run, status), "%+v output: %s", run.summary(), run.commandOutput)
			}
			// Full-suite execution must still enforce the customer's threshold.
			require.NoError(t, os.Remove(probe))
			requireWriteFile(t, filepath.Join(root, "src/unused.cjs"), `exports.unused=()=>0`)
			full, err = drive.runMocha(t.Context(), io.Discard, session, preload, runSpec{Name: "threshold-preserved", Role: roleBaseline})
			require.NoError(t, err)
			require.NotZero(t, full.ExitCode, full.commandOutput)
			require.False(t, full.ProbeCoverageThresholdsDisabled)
		})
	}
}

func TestMochaDelayedCommandUsesSharedTimeout(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES")
	}
	drive := preparedTestdrive(t)
	require.NoError(t, os.Symlink(modules, filepath.Join(drive.repositoryRoot, "node_modules")))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, "one.cjs"), `describe('delayed',()=>it('waits',()=>{}));setInterval(()=>{},1000)`)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	drive.command = filepath.Join(drive.repositoryRoot, "node_modules/.bin/mocha")
	drive.args = []string{"--delay", "one.cjs"}
	drive.executor = &ext.DefaultCommandExecutor{Timeout: 500 * time.Millisecond}
	session, err := NewSession()
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	start := time.Now()
	run, err := drive.runMocha(t.Context(), io.Discard, session, "", runSpec{Name: "delayed", Role: roleBaseline})
	require.NoError(t, err)
	require.ErrorIs(t, run.commandError, context.DeadlineExceeded)
	require.Equal(t, "delayed exceeded --timeout 500ms; raise --timeout or fix the hanging step", markTimeout(run).ResultError)
	require.NotZero(t, run.ExitCode)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestMochaDiscoveryFailurePublishesBeforeWorkspaceCopy(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	drive.discoveryError = errors.New("Mocha watch mode is not supported")
	require.NoError(t, os.MkdirAll(filepath.Dir(htmlReportPath(drive.repositoryRoot)), 0700))
	requireWriteFile(t, htmlReportPath(drive.repositoryRoot), "old report")
	var output bytes.Buffer
	require.ErrorContains(t, drive.Run(t.Context(), &output), "watch mode is not supported")
	require.NotContains(t, output.String(), "Isolating configuration")
	report := readValidationReport(t, drive.repositoryRoot)
	require.Contains(t, report.Error, "watch mode is not supported")
	require.Empty(t, report.Runs)
	require.False(t, report.HTMLReportCurrent)
	require.NoFileExists(t, htmlReportPath(drive.repositoryRoot))
}

func TestMochaPreflightResolvesSelectedPackage(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES")
	}
	drive := preparedTestdrive(t)
	root := drive.repositoryRoot
	nested := filepath.Join(root, "packages/unit")
	require.NoError(t, os.MkdirAll(nested, 0700))
	require.NoError(t, os.Symlink(modules, filepath.Join(nested, "node_modules")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules/mocha"), 0700))
	requireWriteFile(t, filepath.Join(root, "node_modules/mocha/package.json"), `{"name":"mocha","version":"0.0.0"}`)
	require.NoError(t, os.Symlink(filepath.Join(modules, "dd-trace"), filepath.Join(root, "node_modules/dd-trace")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cd packages/unit && npm test"}}`)
	requireWriteFile(t, filepath.Join(nested, "package.json"), `{"scripts":{"test":"mocha one.cjs"}}`)
	drive.framework = framework.NewMocha(platform.NewJavaScript())
	drive.platform = platform.NewJavaScript()
	drive.command, drive.args = "npm", []string{"test"}
	result := validationResult{Framework: "mocha", CIRuntime: &onboard.RuntimeCheck{Status: "not applicable"}}
	require.NoError(t, drive.checkMochaPreflight(t.Context(), io.Discard, &result))
	data, err := os.ReadFile(filepath.Join(modules, "mocha/package.json"))
	require.NoError(t, err)
	var manifest struct{ Version string }
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, manifest.Version, result.Preflight.Version)
	require.Equal(t, "compatible", result.Preflight.Verdict.Status)
}
