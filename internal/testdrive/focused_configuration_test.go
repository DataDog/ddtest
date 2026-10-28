// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// A focused command that is exactly one CI configuration, as written in CI
// (npx --no-install nyc -- npm test) or as discovered (nyc -- npm test),
// runs that configuration's preparation, so it can be credited.
func TestFocusedCommandAdoptsItsConfigurationPreparation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts": {"test": "mocha"}, "devDependencies": {"mocha": "11.0.0", "nyc": "17.0.0"}}`), 0o644))
	preparation := []onboard.BuildCommand{{Directory: ".", Command: "cp config.example.json config.json", Kind: onboard.PreparationPatch}}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "nyc -- npm test", Prerequisites: preparation}}}
	for _, command := range []string{"npx --no-install nyc -- npm test", "nyc -- npm test"} {
		entry, ok := matchingConfigurationPreparation(scope, nil, root, "mocha", command)
		require.True(t, ok, command)
		require.Equal(t, preparation, entry.Prerequisites)
		require.True(t, sameConfiguration(entry, scope.Commands[0]))
	}
	_, ok := matchingConfigurationPreparation(scope, nil, root, "mocha", "npm test")
	require.False(t, ok, "another command is never credited")
	scope.Commands = append(scope.Commands, onboard.TestCommand{Directory: ".", Command: "nyc -- npm test", Prerequisites: []onboard.BuildCommand{{Directory: ".", Command: "cp other.json config.json", Kind: onboard.PreparationPatch}}})
	_, ok = matchingConfigurationPreparation(scope, nil, root, "mocha", "nyc -- npm test")
	require.False(t, ok, "variants with different preparation are never guessed between")
}

// Reports never show the disposable copy's temporary path.
func TestCopiedRunCommandsStayRelative(t *testing.T) {
	copyRoot := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, os.MkdirAll(copyRoot, 0o755))
	result := validationResult{Preflight: &jestPreflight{Command: filepath.Join(copyRoot, "node_modules", ".bin", "nyc") + " -- npm test"}, Runs: []runSummary{{Command: filepath.Join(copyRoot, "node_modules", ".bin", "nyc") + " -- npm test"}}}
	scrubCopyPaths(&result, copyRoot)
	require.Equal(t, "node_modules/.bin/nyc -- npm test", result.Preflight.Command)
	require.Equal(t, "node_modules/.bin/nyc -- npm test", result.Runs[0].Command)
}

// CI copies a config file from its example before the tests; local
// validation replays it in the configuration copy.
func TestLiteralCopyPreparation(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.example.json"), []byte(`{"a": 1}`), 0o644))
	require.NoError(t, applyLiteralPatch(root, root, []string{"cp", "config.example.json", "config.json"}))
	data, err := os.ReadFile(filepath.Join(root, "config.json"))
	require.NoError(t, err)
	require.Equal(t, `{"a": 1}`, string(data))
	require.Error(t, applyLiteralPatch(root, root, []string{"cp", "config.example.json", "../outside.json"}))
	require.Error(t, applyLiteralPatch(root, root, []string{"cp", "missing.json", "config.json"}))
}

// Discovery's caveats (jobs outside validation, GitLab's non-test
// components) reach the final response without blocking.
func TestScopeCaveatsReachTheFinalResponse(t *testing.T) {
	result := validationResult{Scope: &onboard.ValidationScope{Caveats: []string{".gitlab-ci.yml / sentry: CI job without the Datadog setup that never runs mocha; it is outside this validation, and any tests it runs are not reported to Datadog."}}}
	caveats, blockers := ciFidelity(&result)
	require.Empty(t, blockers)
	require.Equal(t, result.Scope.Caveats, caveats)
}

// A run that opted in to reviewed preparation says so in its final
// response, once per command; preparation that needed no review is not
// listed.
func TestReviewedPreparationIsReported(t *testing.T) {
	result := pairedReport("reviewed", "passed")
	result.Configurations = []configurationResult{{TestCommand: onboard.TestCommand{Command: "npm test"}, Status: "passed", Preparation: []buildResult{
		{BuildCommand: onboard.BuildCommand{Command: "cp config.example.json config.json", Kind: onboard.PreparationPatch}, Status: "passed"},
		{BuildCommand: onboard.BuildCommand{Command: "npm run trans:compile", Kind: onboard.PreparationPackageBuild}, Status: "passed", Reviewed: true},
		{BuildCommand: onboard.BuildCommand{Command: "npm run trans:check", Kind: onboard.PreparationPackageBuild}, Status: "passed", Reviewed: true},
	}}, {TestCommand: onboard.TestCommand{Command: "npm test", Directory: "other"}, Status: "passed", Preparation: []buildResult{
		{BuildCommand: onboard.BuildCommand{Command: "npm run trans:compile", Kind: onboard.PreparationPackageBuild}, Status: "passed", Reviewed: true},
	}}}
	result.Summary = summarizeValidation(result)
	require.Contains(t, validationHandoff(result), "Reviewed preparation run (opted in with --allow-reviewed-builds): npm run trans:compile; npm run trans:check\n")
	result.Configurations[0].Preparation[1].Reviewed, result.Configurations[0].Preparation[2].Reviewed, result.Configurations[1].Preparation[0].Reviewed = false, false, false
	result.Summary = summarizeValidation(result)
	require.NotContains(t, validationHandoff(result), "Reviewed preparation run")
}

// A plain Vitest CLI run that adopted CI preparation runs it in a prepared
// copy before the tests; without preparation it runs as before.
func TestAdoptedPreparationRunsBeforeCredit(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies": {"vitest": "3.2.0"}}`), 0o644))
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", command: "npx", args: []string{"vitest", "run"}}
	require.False(t, drive.runsInPreparedCopy())
	drive.selectedPlan.Prerequisites = []onboard.BuildCommand{{Directory: ".", Command: "cp config.example.json config.json", Kind: onboard.PreparationPatch}}
	require.True(t, drive.runsInPreparedCopy())
}

// The adopted configuration's environment comes with its preparation;
// variants that differ only by environment are never guessed between.
func TestAdoptedPreparationKeepsItsEnvironment(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts": {"test": "mocha"}, "devDependencies": {"mocha": "11.0.0"}}`), 0o644))
	preparation := []onboard.BuildCommand{{Directory: ".", Command: "cp config.example.json config.json", Kind: onboard.PreparationPatch}}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "npm test", Prerequisites: preparation, Environment: map[string]string{"TZ": "UTC"}}}}
	entry, ok := matchingConfigurationPreparation(scope, nil, root, "mocha", "npm test")
	require.True(t, ok)
	require.Equal(t, map[string]string{"TZ": "UTC"}, entry.Environment)
	scope.Commands = append(scope.Commands, onboard.TestCommand{Directory: ".", Command: "npm test", Prerequisites: preparation, Environment: map[string]string{"TZ": "Europe/Madrid"}})
	_, ok = matchingConfigurationPreparation(scope, nil, root, "mocha", "npm test")
	require.False(t, ok)
}
