// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCIStatementFlagsRejectInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func() *cobra.Command
		args    []string
		reason  string
	}{
		{"onboard runtime", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--ci-runtime", "python=3.12"}, `--ci-runtime "python=3.12" names runtime "python"; supported runtimes: node, ruby`},
		{"testdrive version", newTestdriveCommand, []string{"--agent", "--all", "--yes", "--ci-runtime", "node=lts"}, `--ci-runtime "node=lts": the Node version must be numeric, such as 22, 22.11 or 22.11.0`},
		{"same job twice", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--ci-runtime", "test:node=20", "--ci-runtime", ".github/workflows/ci.yml / test:node=22"}, "--ci-runtime test:node=20 and .github/workflows/ci.yml / test:node=22 state different versions for the same job, .github/workflows/ci.yml / test"},
		{"same key twice", newTestdriveCommand, []string{"--agent", "--all", "--yes", "--ci-runtime", "test:node=20", "--ci-runtime", "test:node=22"}, "--ci-runtime test:node=20 and test:node=22 state different versions for the same job"},
		{"undefined fact", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--ci-confirm", "plugin"}, "--ci-confirm plugin: GitHub Actions defines no facts to confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ciProviderFixture(t)
			t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
			result, err := runProtocolCommand(t, tc.command(), tc.args...)
			require.Error(t, err)
			require.Equal(t, "invalid_options", result.Code)
			require.Equal(t, tc.reason, result.Reason)
			require.NoDirExists(t, ".testoptimization")
		})
	}
}

// A statement for a job whose configuration selects another runtime is an
// error before anything runs.
func TestCIRuntimeConflictStopsBeforeDiscovery(t *testing.T) {
	ciProviderFixture(t)
	t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "ci.yml"), []byte("jobs:\n  test:\n    runs-on: self-hosted\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: 20\n      - run: npm test\n"), 0600))
	result, err := runProtocolCommand(t, newOnboardCommand(nil), "--agent", "--ci-runtime", "test:node=22")
	require.Error(t, err)
	require.Equal(t, "ci_runtime_conflict", result.Code)
	require.Contains(t, result.Reason, "--ci-runtime test:node=22 contradicts Node 20, which actions/setup-node selects in .github/workflows/ci.yml / test")
}

// Statements reach every follow-up command exactly once, like --ci-provider.
func TestCIStatementsAreForwardedOnce(t *testing.T) {
	ciProviderFixture(t)
	t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
	args := []string{"--format", "json", "--ci-runtime", "node=22", "--ci-runtime", "test:node=22"}
	result, err := runProtocolCommand(t, newOnboardCommand(nil), args...)
	require.NoError(t, err)
	require.Equal(t, "ready", result.Status)
	command := strings.Join(result.NextCommand, " ")
	require.Equal(t, 1, strings.Count(command, "--ci-runtime node=22"), command)
	require.Equal(t, 1, strings.Count(command, "--ci-runtime test:node=22"), command)

	result, err = runProtocolCommand(t, newTestdriveCommand(), append([]string{"--all"}, args...)...)
	require.Error(t, err)
	require.Equal(t, "consent_required", result.Code)
	command = strings.Join(result.NextCommand, " ")
	require.Equal(t, 2, strings.Count(command, "--ci-runtime"), command)
	require.NotContains(t, command, "[")

	testdrive := newTestdriveCommand()
	require.NoError(t, testdrive.ParseFlags([]string{"--ci-runtime", "node=22", "--ci-runtime", "ruby=3.3", "--all"}))
	require.Equal(t, "ddtest testdrive --all --ci-runtime node=22 --ci-runtime ruby=3.3 --yes", rerunWithYes(testdrive))
}

// onboard --apply enables the Datadog Jenkins plugin: JavaScript gets the
// automatic setup, here first the runtime question, then the preview's consent.
func TestOnboardApplyOnJenkinsRunsTheAutomaticSetup(t *testing.T) {
	ciProviderFixture(t)
	t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
	require.NoError(t, os.RemoveAll(".github"))
	jenkinsfile := "pipeline {\n  agent { label 'linux' }\n  stages { stage('Test') { steps { sh 'npx vitest run' } } }\n}\n"
	require.NoError(t, os.WriteFile("Jenkinsfile", []byte(jenkinsfile), 0600))
	result, err := runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--yes", "--format", "json")
	require.Error(t, err)
	require.Equal(t, "ci_runtime_required", result.Code)
	result, err = runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--format", "json", "--ci-runtime", "node=22")
	require.Error(t, err)
	require.Equal(t, "consent_required", result.Code)
	require.Contains(t, result.NextCommand, "--yes")
	data, err := os.ReadFile("Jenkinsfile")
	require.NoError(t, err)
	require.Equal(t, jenkinsfile, string(data))
	require.NoDirExists(t, ".testoptimization")
}

// Ruby's only setup ddtest writes on Jenkins is the plugin's: onboard --apply
// previews the Jenkinsfile edit, --yes writes it and returns the rest of the
// setup (the datadog-ci pin), and applying again changes nothing.
func TestOnboardApplyOnJenkinsWritesThePluginSetupForRuby(t *testing.T) {
	ciProviderFixture(t)
	t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
	require.NoError(t, os.RemoveAll(".github"))
	require.NoError(t, os.Remove("package.json"))
	require.NoError(t, os.WriteFile("Gemfile", []byte("source 'https://rubygems.org'\ngem 'rspec'\n"), 0600))
	jenkinsfile := "node {\n  stage('Test') {\n    sh 'bundle exec rspec'\n  }\n}\n"
	require.NoError(t, os.WriteFile("Jenkinsfile", []byte(jenkinsfile), 0600))

	command := newOnboardCommand(nil)
	command.SetArgs([]string{"--apply", "--ci-runtime", "ruby=3.3"})
	var preview bytes.Buffer
	command.SetOut(&preview)
	command.SetErr(io.Discard)
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Contains(t, preview.String(), "+datadog(testOptimization: [enabled: true, serviceName: '")
	require.Contains(t, preview.String(), "Preview only; no source files changed and no tests executed. Review this diff, then pass --apply --yes to apply it.")
	data, err := os.ReadFile("Jenkinsfile")
	require.NoError(t, err)
	require.Equal(t, jenkinsfile, string(data), "a preview writes nothing")

	result, err := runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--format", "json", "--ci-runtime", "ruby=3.3")
	require.Error(t, err)
	require.Equal(t, "consent_required", result.Code)

	result, err = runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--yes", "--format", "json", "--ci-runtime", "ruby=3.3")
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Len(t, result.Changes, 1)
	require.Equal(t, "Jenkinsfile", result.Changes[0].Path)
	require.Contains(t, result.SetupDiff, "+datadog(testOptimization:")
	require.Contains(t, result.UndoInstructions, "git apply -R")
	require.Contains(t, result.Reason, "onboard --apply wrote the Datadog Jenkins plugin")
	require.Contains(t, result.Instruction, `Pin datadog-ci in the bundle: add gem "datadog-ci", "<exact datadog-ci release>"`)
	require.Equal(t, []string{"testdrive", "--framework", "rspec", "--all", "--agent", "--yes"}, result.ResumeCommand[1:7])
	data, err = os.ReadFile("Jenkinsfile")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(data), "datadog(testOptimization: [enabled: true, serviceName: '"), string(data))
	require.Contains(t, string(data), "\n  node {\n    stage('Test') {\n      sh 'bundle exec rspec'\n    }\n  }\n}\n")

	result, err = runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--yes", "--format", "json", "--ci-runtime", "ruby=3.3")
	require.Error(t, err)
	require.Empty(t, result.Changes, "applying again changes nothing")
	require.Equal(t, "ci_setup_confirmation_required", result.Code)
	again, err := os.ReadFile("Jenkinsfile")
	require.NoError(t, err)
	require.Equal(t, string(data), string(again))

	command = newOnboardCommand(nil)
	command.SetArgs([]string{"--apply", "--ci-runtime", "ruby=3.3"})
	preview.Reset()
	command.SetOut(&preview)
	_ = command.ExecuteContext(t.Context())
	require.True(t, strings.HasPrefix(preview.String(), "RSpec CI setup is already configured; no edits.\n"), preview.String())
}
