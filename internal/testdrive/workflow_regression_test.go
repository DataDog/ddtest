// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type packageCheckExecutor struct {
	workflowCheckExecutor
	stdout, stderr string
	err            error
	called         bool
}

func (e *packageCheckExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	if name == "npm" {
		e.called = true
		return []byte(e.stderr + e.stdout), e.err
	}
	return e.workflowCheckExecutor.CombinedOutput(ctx, name, args, env)
}

func (e *packageCheckExecutor) Output(_ context.Context, _ string, _ []string, _ map[string]string) ([]byte, []byte, error) {
	e.called = true
	return []byte(e.stdout), []byte(e.stderr), e.err
}

func TestWorkflowPackageCheckUsesPublishabilityAndSeparateOutput(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, stdout, stderr, status string
		err                                    error
		called                                 bool
	}{
		{"private unnamed root", `{"private":true,"workspaces":["packages/*"]}`, "", "", "not applicable", nil, false},
		{"private named root", `{"name":"private-app","private":true}`, "", "", "not applicable", nil, false},
		{"public package with warning", `{"name":"fixture","version":"1.0.0"}`, `[{"files":[{"path":"src/index.js"}]}]`, "npm warn Unknown project config\n", "passed", nil, true},
		{"public package leaks report", `{"name":"fixture","version":"1.0.0"}`, `[{"files":[{"path":".testoptimization/report.html"}]}]`, "npm warn No .npmignore file found\n", "failed", nil, true},
		{"invalid stdout", `{"name":"fixture","version":"1.0.0"}`, "not JSON", "npm warn config\n", "failed", nil, true},
		{"nonzero exit", `{"name":"fixture","version":"1.0.0"}`, `[{"files":[]}]`, "npm error permission denied\n", "failed", errors.New("exit status 1"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), tc.manifest)
			executor := &packageCheckExecutor{stdout: tc.stdout, stderr: tc.stderr, err: tc.err}
			drive := &Testdrive{repositoryRoot: root, executor: configurationExecutor{commandExecutor: executor, environment: map[string]string{"EXISTING_CI_ENV": "kept"}}}
			checks := drive.verifyReportExclusions(t.Context())
			require.Equal(t, tc.status, checks[1].Status, checks[1].Reason)
			require.Equal(t, tc.called, executor.called)
			if tc.err != nil {
				require.Contains(t, checks[1].Reason, "permission denied")
			}
		})
	}
}

type workflowPreflightExecutor struct{ root, runner string }

func (e workflowPreflightExecutor) CombinedOutput(_ context.Context, name string, _ []string, env map[string]string) ([]byte, error) {
	if name != filepath.Join(e.root, "node_modules", ".bin", e.runner) {
		return nil, errors.New("framework executable is not on the caller PATH")
	}
	if e.runner == "playwright" {
		return nil, os.WriteFile(env["DDTEST_PLAYWRIGHT_REPORT"], []byte(`{"node":"v24.0.0","version":"1.51.0","projects":[{"name":"chromium","root":"."}],"tests":[{"file":"test.js","name":"works","status":"passed","project":"chromium","browser":"chromium"}],"errors":[]}`), 0600)
	}
	if path := env["DDTEST_JEST_PREFLIGHT"]; path != "" {
		if err := os.WriteFile(path, []byte(`{"node":"v24.0.0","executable":"node","arguments":["test"]}`), 0600); err != nil {
			return nil, err
		}
	}
	return []byte(`{"version":"29.4.3","configs":[{"testRunner":"jest-circus/runner","rootDir":"."}]}`), nil
}

func TestWorkflowPreflightResolvesLocalFrameworkExecutables(t *testing.T) {
	for _, runner := range []framework.Framework{&framework.Jest{}, &framework.Playwright{}} {
		t.Run(runner.Name(), func(t *testing.T) {
			drive := preflightFixture(t, "29.4.3", "jest-circus/runner.js")
			drive.framework = runner
			drive.executor = workflowPreflightExecutor{root: drive.repositoryRoot, runner: runner.Name()}
			require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0755))
			requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: '24'\n      - run: npx "+runner.Name()+" test\n")
			version, err := drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: runner.Name() + " test"}}}, false)
			require.NoError(t, err)
			require.Equal(t, "6.16.0", version)
			drive.workflowPlan = &onboard.ExecutionPlan{}
			_, err = drive.selectWorkflowTracer(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: runner.Name() + " test"}, {Directory: ".", Command: "unknown-command"}}}, false)
			require.Error(t, err)
			require.Equal(t, "6.16.0", drive.workflowPlan.Tracer, "a later blocker must retain an already resolved release")
		})
	}
}
