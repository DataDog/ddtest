// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

type workflowCheckExecutor struct{ leak bool }

func (e workflowCheckExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	data, err := e.CombinedOutput(ctx, name, args, env)
	return data, nil, err
}

func (e workflowCheckExecutor) CombinedOutput(_ context.Context, name string, args []string, _ map[string]string) ([]byte, error) {
	switch filepath.Base(name) {
	case "git":
		if args[0] == "check-ignore" {
			return []byte(".testoptimization/testdrive.json\n.testoptimization/report.html\n"), nil
		}
		return nil, nil
	case "npm":
		if e.leak {
			return []byte(`[{"files":[{"path":".testoptimization/report.html"}]}]`), nil
		}
		return []byte(`[{"files":[{"path":"src/index.js"}]}]`), nil
	}
	return nil, errors.New("unexpected command")
}

func TestSharedWorkflowAcrossFrameworksPreservesAssertionsAndPackagingFailures(t *testing.T) {
	frameworks := []framework.Framework{&framework.Jest{}, &framework.Vitest{}, &framework.Playwright{}, &framework.Mocha{}, &framework.Cypress{}, &framework.Cucumber{}, &framework.WebdriverIO{}}
	for _, runner := range frameworks {
		for _, leak := range []bool{false, true} {
			t.Run(runner.Name()+map[bool]string{true: "/package-leak", false: "/pass"}[leak], func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				requireWriteFile(t, filepath.Join(root, "package.json"), `{"name":"fixture"}`)
				drive := &Testdrive{repositoryRoot: root, framework: runner, language: "javascript", tracerVersion: "latest", executor: workflowCheckExecutor{leak}, command: "vitest", nodeVersion: func() string { return "v24.0.0" }}
				calls := 0
				drive.workflowRun = func(ctx context.Context, out io.Writer) error {
					calls++
					r := completeValidationFixture(runner.Name())
					r.Session = "current"
					return finishValidation(out, root, r)
				}
				var output bytes.Buffer
				err := drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"})
				require.Equal(t, 1, calls, "one execution through the existing engine")
				report := readValidationReport(t, root)
				require.Equal(t, 6, len(report.Features), "keep unsupported checks explicitly represented")
				require.Len(t, report.Runs, 2)
				require.Len(t, report.Workflow.Checks, 5)
				if runner.Name() == "playwright" || runner.Name() == "webdriverio" {
					require.Contains(t, report.Workflow.Plan.UnsupportedFeatures, "skipping")
					require.Len(t, report.Workflow.Plan.Features, 5)
				}
				if runner.Name() == "cypress" {
					require.Empty(t, report.Workflow.Plan.UnsupportedFeatures)
					require.Len(t, report.Workflow.Plan.Features, 6)
				}
				var result onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				if leak {
					require.ErrorContains(t, err, "validation_incomplete")
					require.Equal(t, "validation_failed", result.Status)
					require.NotEmpty(t, result.ResumeCommand)
					require.NotContains(t, result.ResumeCommand, "--command", "default recovery must not turn a whole-scope run into an explicit focused run")
					require.Equal(t, "INCOMPLETE", report.Summary.Status)
					require.True(t, report.LocalSuccess, "packaging does not rewrite the observed test comparison")
					require.Contains(t, strings.Join(report.Summary.BlockingChecks, " "), "package_report_exclusions")
				} else {
					require.NoError(t, err)
					require.Equal(t, "complete_locally", result.Status)
					require.Empty(t, result.FinalResponse, "completion is delivered only by the final freshness check")
					require.Contains(t, result.NextCommand, "--report")
					require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status)
				}
			})
		}
	}
}

func TestWorkflowLocksRejectOverlapAndReleaseOnClose(t *testing.T) {
	root := t.TempDir()
	one, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	_, err = acquireWorkflowLock(root)
	require.ErrorIs(t, err, errWorkflowBusy)
	require.NoError(t, one.Close())
	two, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	require.NoError(t, two.Close())
}

func TestFactsAreBoundToInputsAndCannotReplaceCommands(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"prepare:test":"node build.js"}}`)
	requireWriteFile(t, filepath.Join(root, "build.js"), "// build fixtures\n")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "vitest run"}}, Unresolved: []string{"unknown CI wrapper"}}
	facts := SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Prerequisites: []PrerequisiteFact{{ForCommand: "vitest run", Argv: []string{"npm", "run", "prepare:test"}, ExpectedFiles: []string{"dist/index.js"}}}}
	file := filepath.Join(t.TempDir(), "facts.json")
	data, err := json.Marshal(facts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, data, 0600))
	_, err = applySetupFacts(root, file, fingerprint, &scope)
	require.NoError(t, err)
	require.Equal(t, "vitest run", scope.Commands[0].Command)
	require.Equal(t, []string{"unknown CI wrapper"}, scope.Unresolved)
	require.Equal(t, []string{"dist/index.js"}, scope.Commands[0].Prerequisites[0].ExpectedFiles)
	requireWriteFile(t, filepath.Join(root, "build.js"), "// changed build\n")
	changed, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, fingerprint, changed)
	_, err = applySetupFacts(root, file, changed, &scope)
	require.ErrorContains(t, err, "fingerprint")
	facts.Prerequisites[0].ForCommand = "an easier test suite"
	data, err = json.Marshal(facts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, data, 0600))
	_, err = applySetupFacts(root, file, fingerprint, &scope)
	require.ErrorContains(t, err, "does not match")
}

func TestPrerequisiteOutputContractMustHoldBeforeTests(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	drive := &Testdrive{repositoryRoot: root, executor: workflowCheckExecutor{}}
	item := configurationResult{TestCommand: onboard.TestCommand{Prerequisites: []onboard.BuildCommand{{Kind: "agent-prerequisite", Directory: ".", Command: "git status", ExpectedFiles: []string{"dist/index.js"}}}}}
	require.ErrorContains(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item), "Declared build output")
	require.Equal(t, "failed", item.Preparation[0].Status)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dist"), 0755))
	requireWriteFile(t, filepath.Join(root, "dist/index.js"), "export default 1")
	item.Preparation = nil
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item))
}

func TestSavedReportRejectsChangedInputsWithoutOverwritingEvidence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "source.js"), "initial")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	r := completeValidationFixture("vitest")
	r.Workflow = &WorkflowEvidence{Fingerprint: fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: "check", Status: "passed"}}}
	require.NoError(t, finishValidation(io.Discard, root, r))
	var output bytes.Buffer
	require.NoError(t, RenderReport(t.Context(), &output, "json"))
	require.Contains(t, output.String(), "complete_locally")
	before, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(root, "source.js"), "changed")
	output.Reset()
	require.ErrorContains(t, RenderReport(t.Context(), &output, "json"), "current source/runtime")
	require.NotContains(t, output.String(), "COMPLETE LOCALLY")
	after, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestPreflightRetryIsBoundedAndNeverRetriesPermissionOrTests(t *testing.T) {
	for _, code := range []string{"ECONNRESET", "ENOTFOUND", "EACCES"} {
		t.Run(code, func(t *testing.T) {
			count := 0
			failure := &platform.MetadataError{Code: code, Err: errors.New(code)}
			err := workflowPreflight(t.Context(), func() error { count++; return failure })
			require.ErrorIs(t, err, failure)
			expected := 1
			if code == "ECONNRESET" {
				expected = 2
			}
			require.Equal(t, expected, count)
		})
	}
	count := 0
	err := workflowPreflight(t.Context(), func() error { count++; return errors.New("feature failed") })
	require.Error(t, err)
	require.Equal(t, 1, count)
}

func TestPrerequisiteFactRunsRealLocalBuildAndChecksItsOutput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "build.js"), "require('fs').mkdirSync('dist',{recursive:true});require('fs').writeFileSync('dist/index.js','module.exports=42')")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	facts := SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Prerequisites: []PrerequisiteFact{{ForCommand: "vitest run", Argv: []string{"node", "build.js"}, ExpectedFiles: []string{"dist/index.js"}}}}
	path := filepath.Join(t.TempDir(), "facts.json")
	data, err := json.Marshal(facts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "vitest run"}}}
	_, err = applySetupFacts(root, path, fingerprint, &scope)
	require.NoError(t, err)
	drive := &Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
	item := configurationResult{TestCommand: scope.Commands[0]}
	require.NoError(t, drive.runBuildPrerequisites(t.Context(), io.Discard, &item))
	require.FileExists(t, filepath.Join(root, "dist/index.js"))
	require.Equal(t, "passed", item.Preparation[0].Status)
}

func TestStoppedWorkflowCannotLeaveEarlierSuccessAsCurrent(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: custom-setup && npx vitest run\n")
	previous := completeValidationFixture("vitest")
	previous.Session = "earlier-pass"
	require.NoError(t, finishValidation(io.Discard, root, previous))
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "scope_unresolved")
	current := readValidationReport(t, root)
	require.Equal(t, "INCOMPLETE", current.Summary.Status)
	require.NotNil(t, current.Retained)
	require.NotNil(t, current.LastExecution)
	require.False(t, current.LastExecution.Current)
	require.False(t, current.Success)
	require.Empty(t, current.Runs)
}

func TestRubyWorkflowPreservesNativeValidationPath(t *testing.T) {
	for _, runner := range []framework.Framework{framework.NewRSpec(platform.NewRuby("")), framework.NewMinitest(platform.NewRuby(""))} {
		t.Run(runner.Name(), func(t *testing.T) {
			root := t.TempDir()
			preparation := errors.New("Ruby command requires preparation")
			drive := &Testdrive{repositoryRoot: root, framework: runner, language: "ruby", discoveryError: preparation}
			var output bytes.Buffer
			require.ErrorIs(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "verbose"}), preparation)
			require.NotContains(t, output.String(), "framework_unsupported")
			report := readValidationReport(t, root)
			require.Equal(t, runner.Name(), report.Framework)
			require.Equal(t, preparation.Error(), report.Error)
			require.Equal(t, "INCOMPLETE", report.Summary.Status)
		})
	}
}

func TestRubyWorkflowAgentModesReportMissingBundles(t *testing.T) {
	for _, runner := range []framework.Framework{framework.NewRSpec(platform.NewRuby("")), framework.NewMinitest(platform.NewRuby(""))} {
		for _, format := range []string{"agent", "json"} {
			t.Run(runner.Name()+"/"+format, func(t *testing.T) {
				root := t.TempDir()
				previous := []byte(`{"success":false}`)
				require.NoError(t, writeValidation(validationPath(root), previous))
				executor := &fakeTestdriveExecutor{}
				drive := &Testdrive{repositoryRoot: root, framework: runner, language: "ruby", executor: executor, rubyVersion: func() string { return "ruby 3.3.11 arm64" }}
				var output bytes.Buffer
				require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: format}), "ruby_dependencies_missing")
				var result onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.Equal(t, "needs_input", result.Status)
				require.Equal(t, []string{"bundle", "install", "--gemfile", "Gemfile"}, result.NextCommand)
				require.Len(t, result.SetupCommands, 1)
				require.Empty(t, executor.command)
				current, err := os.ReadFile(validationPath(root))
				require.NoError(t, err)
				require.NotEmpty(t, current)
				require.NotContains(t, string(current), "COMPLETE LOCALLY")
			})
		}
	}
}
