// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
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
		{"onboard runtime", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--ci-runtime", "go=1.22"}, `--ci-runtime "go=1.22" names runtime "go"; supported runtimes: node, python, ruby`},
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

// onboard --apply never edits a Jenkinsfile: JavaScript gets the same setup
// handoff Ruby gets, here first the runtime question.
func TestOnboardApplyOnJenkinsHandsOffTheSetup(t *testing.T) {
	ciProviderFixture(t)
	t.Cleanup(func() { _ = onboard.UseCIStatements(nil, nil) })
	require.NoError(t, os.RemoveAll(".github"))
	require.NoError(t, os.WriteFile("Jenkinsfile", []byte("pipeline {\n  agent { label 'linux' }\n  stages { stage('Test') { steps { sh 'npx vitest run' } } }\n}\n"), 0600))
	result, err := runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--yes", "--format", "json")
	require.Error(t, err)
	require.Equal(t, "ci_runtime_required", result.Code)
	result, err = runProtocolCommand(t, newOnboardCommand(nil), "--apply", "--yes", "--format", "json", "--ci-runtime", "node=22")
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Contains(t, result.Instruction, "datadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['JAVASCRIPT']])")
	require.NoDirExists(t, ".testoptimization")
}
