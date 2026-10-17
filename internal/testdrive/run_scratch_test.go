// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

type runScratchExecutor struct {
	mode    string
	started chan string
	paths   []string
	exits   []int
}

func (e *runScratchExecutor) CombinedOutput(ctx context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	temporary := env["TMPDIR"]
	if temporary == "" || env["TMP"] != temporary || env["TEMP"] != temporary {
		return nil, fmt.Errorf("native child temporary directories disagree: TMPDIR=%q TMP=%q TEMP=%q", temporary, env["TMP"], env["TEMP"])
	}
	e.paths = append(e.paths, temporary)
	if e.started != nil {
		e.started <- temporary
	}
	childEnv := maps.Clone(env)
	childEnv["DDTEST_SCRATCH_CHILD"] = e.mode
	executor := &ext.DefaultCommandExecutor{}
	output, err := executor.CombinedOutput(ctx, os.Args[0], []string{"-test.run=^TestRunScratchChild$"}, childEnv)
	e.exits = append(e.exits, commandExitCode(err))
	return output, err
}

// A real child writes helper scratch without any exit cleanup, like an app
// server terminated during a testdrive. It does not need installed frameworks.
func TestRunScratchChild(t *testing.T) {
	mode := os.Getenv("DDTEST_SCRATCH_CHILD")
	if mode == "" {
		return
	}
	directory, err := os.MkdirTemp("", "helper-copy-")
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(directory, "state"), "private helper state")
	requireWriteFile(t, filepath.Join(os.TempDir(), "ready"), directory)
	if mode == "interrupt" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "failure" {
		os.Exit(7)
	}
}

func TestAllJavaScriptRunsOwnChildScratch(t *testing.T) {
	for _, name := range []string{"jest", "vitest", "playwright", "mocha", "cypress", "cucumber", "webdriverio"} {
		t.Run(name, func(t *testing.T) {
			caller := t.TempDir()
			cache := filepath.Join(caller, "npm-cache")
			require.NoError(t, os.Mkdir(cache, 0700))
			requireWriteFile(t, filepath.Join(caller, "existing-scratch"), "keep")
			requireWriteFile(t, filepath.Join(cache, "existing-cache"), "keep")
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, caller)
			}
			t.Setenv("npm_config_cache", cache)
			drive := preparedTestdrive(t)
			p := platform.NewJavaScript()
			frameworks := map[string]framework.Framework{"jest": framework.NewJest(p), "vitest": framework.NewVitest(p), "playwright": framework.NewPlaywright(p), "mocha": framework.NewMocha(p), "cypress": framework.NewCypress(p), "cucumber": framework.NewCucumber(p), "webdriverio": framework.NewWebdriverIO(p)}
			drive.framework = frameworks[name]
			drive.command, drive.args = "node", nil
			if name == "cucumber" {
				drive.command, drive.args = "npx", []string{"cucumber-js"}
			}
			drive.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }
			executor := &runScratchExecutor{mode: "success"}
			drive.executor = executor
			session, err := NewSession()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			run := drive.runJest
			switch name {
			case "vitest":
				run = drive.runVitest
			case "playwright":
				run = drive.runPlaywright
			case "mocha":
				run = drive.runMocha
			case "cypress":
				run = drive.runCypress
			case "cucumber":
				run = drive.runCucumber
			case "webdriverio":
				run = drive.runWebdriverIO
			}
			seen := map[string]bool{}
			for _, mode := range []string{"baseline", "reporting-only", "probe-pass-control", "baseline"} {
				probe := ""
				if mode == "probe-pass-control" {
					probe = filepath.Join(drive.repositoryRoot, "probe.js")
					requireWriteFile(t, probe, "")
				}
				_, err := run(t.Context(), io.Discard, session, "/trace/ci/init.js", mode, mode != "baseline", intake.Scenario{}, probe, "pass")
				require.NoError(t, err)
				temporary := executor.paths[len(executor.paths)-1]
				require.False(t, seen[temporary], "even a repeated run label needs fresh scratch")
				seen[temporary] = true
				relative, err := filepath.Rel(session.Directory(), temporary)
				require.NoError(t, err)
				require.True(t, filepath.IsLocal(relative), temporary)
				require.FileExists(t, filepath.Join(temporary, "ready"))
				// No state from the baseline/reporting run enters the next child.
				copies, err := filepath.Glob(filepath.Join(temporary, "helper-copy-*"))
				require.NoError(t, err)
				require.Len(t, copies, 1)
			}
			require.NoError(t, session.Close())
			for _, temporary := range executor.paths {
				require.NoDirExists(t, temporary)
			}
			require.FileExists(t, filepath.Join(caller, "existing-scratch"))
			require.FileExists(t, filepath.Join(cache, "existing-cache"))
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				require.Equal(t, caller, os.Getenv(key))
			}
			require.Equal(t, cache, os.Getenv("npm_config_cache"))
		})
	}
}

func TestSessionRemovesChildScratchAfterCommandExit(t *testing.T) {
	for _, mode := range []string{"success", "failure", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			caller := t.TempDir()
			t.Setenv("TMPDIR", caller)
			requireWriteFile(t, filepath.Join(caller, "existing"), "keep")
			cache := t.TempDir()
			t.Setenv("npm_config_cache", cache)
			requireWriteFile(t, filepath.Join(cache, "existing"), "keep cache")
			drive := preparedTestdrive(t)
			drive.projectTracer = "/trace/ci/init.js"
			drive.startIntake = func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }
			executor := &runScratchExecutor{mode: mode, started: make(chan string, 4)}
			drive.executor = executor
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := drive.run(ctx, io.Discard); done <- err }()
			var temporary string
			select {
			case temporary = <-executor.started:
			case <-ctx.Done():
				t.Fatal("child did not start")
			}
			if mode == "interrupt" {
				require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(temporary, "ready")); return err == nil }, 3*time.Second, 10*time.Millisecond)
				cancel()
			}
			runErr := <-done
			switch mode {
			case "interrupt":
				require.ErrorIs(t, runErr, context.Canceled)
			case "failure":
				require.Equal(t, 7, executor.exits[0])
			default:
				require.Equal(t, 0, executor.exits[0])
			}
			require.NoDirExists(t, drive.session.Directory())
			require.NoDirExists(t, temporary)
			require.FileExists(t, filepath.Join(caller, "existing"))
			require.FileExists(t, filepath.Join(cache, "existing"))
			require.Equal(t, caller, os.Getenv("TMPDIR"))
			require.Equal(t, cache, os.Getenv("npm_config_cache"))
		})
	}
}
