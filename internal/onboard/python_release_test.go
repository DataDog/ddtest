// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// usePythonRelease makes release the latest ddtrace release for one
// test, counting how often ddtest reads it.
func usePythonRelease(t *testing.T, release string, err error) *int {
	t.Helper()
	reads := 0
	t.Cleanup(SetPythonReleaseResolver(func(context.Context) (string, error) {
		reads++
		return release, err
	}))
	return &reads
}

// The latest release is read once per run; an exact --tracer-version wins
// without reading it; a floating selector is refused; and an unreadable
// PyPI falls back to the release ddtest has validated.
func TestSelectPythonRelease(t *testing.T) {
	reads := usePythonRelease(t, "4.20.2", nil)
	require.Equal(t, "4.20.2", pythonSetupRelease())
	release, err := SelectPythonRelease("latest")
	require.NoError(t, err)
	require.Equal(t, "4.20.2", release)
	require.Equal(t, 1, *reads)

	reads = usePythonRelease(t, "4.20.2", nil)
	release, err = SelectPythonRelease("4.16.0")
	require.NoError(t, err)
	require.Equal(t, "4.16.0", release)
	require.Equal(t, "4.16.0", pythonSetupRelease())
	require.Zero(t, *reads)

	for _, floating := range []string{"4.16", "1.x", "git:main", "4.16.1.dev0"} {
		_, err = SelectPythonRelease(floating)
		require.ErrorContains(t, err, "exact ddtrace release", floating)
	}

	usePythonRelease(t, "", errors.New("offline"))
	require.Equal(t, pythonExampleRelease, pythonSetupRelease())
	usePythonRelease(t, "latest", nil)
	require.Equal(t, pythonExampleRelease, pythonSetupRelease())
}

// Every Python setup ddtest prescribes names the release it selected, never a
// placeholder or one the agent picks: the GitLab script, the CircleCI orb,
// the CircleCI setup without Bash, the Buildkite snippet, the GitHub action
// instructions and the hand-written download it suggests.
func TestPythonSetupsNameTheSelectedRelease(t *testing.T) {
	reads := usePythonRelease(t, "4.20.2", nil)
	for _, provider := range []ciProvider{gitLabCI{}, circleCI{}, buildkite{}, jenkins{}} {
		t.Run(provider.name(), func(t *testing.T) {
			root := copyFixture(t, provider.name()+"-pytest")
			t.Chdir(root)
			useCIProvider(t, provider.name())
			text := providerSetupInstructions(root, provider, "python", "pytest", "datadoghq.eu", true)
			require.NotContains(t, text, "<exact ddtrace release>")
			require.NotContains(t, text, "<resolved-release>")
			if provider.name() != jenkinsProvider {
				require.Contains(t, text, "4.20.2")
				require.Contains(t, text, "the exact release ddtest selected")
			}
		})
	}
	for _, text := range []string{pythonDiscovery{}.agentInstructions("pytest", "datadoghq.eu"), pythonDiscovery{}.instructions("pytest")} {
		require.Contains(t, text, "python-tracer-version: '4.20.2'")
		require.NotContains(t, text, "<resolved-release>")
		require.NotContains(t, text, "<exact ddtrace release>")
	}
	require.Equal(t, 1, *reads)
}

// onboard --agent for Python prints the setup with the release
// --tracer-version names, and refuses a floating one before any setup.
func TestPythonOnboardingHonorsTracerVersion(t *testing.T) {
	usePythonRelease(t, "4.20.2", nil)
	root := gitLabPythonRepository(t, "test:\n  image: python:3.12\n  script:\n    - python -m pytest\n")
	writeFile(t, root, ".gitignore", "/.testoptimization/\n")
	var output bytes.Buffer
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "4.16.0"})
	require.Contains(t, output.String(), `PYTHON_TRACER_VERSION=\"4.16.0\"`, output.String())
	require.NotContains(t, output.String(), "4.20.2")

	usePythonRelease(t, "4.20.2", nil)
	output.Reset()
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "4.16"})
	require.Contains(t, output.String(), `"code":"invalid_options"`)
	require.Contains(t, output.String(), "exact ddtrace release")
}

// An explicit --tracer-version latest selects the exact latest release, as
// no --tracer-version does: the setup pins it, and every command of the
// handoff carries that release, never latest, so the resumed validation
// compares it with the CI pin.
func TestPythonOnboardingResolvesAnExplicitLatest(t *testing.T) {
	usePythonRelease(t, "4.20.2", nil)
	gitLabPythonRepository(t, "test:\n  image: python:3.12\n  script:\n    - python -m pytest\n")
	var output bytes.Buffer
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "latest"})
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Contains(t, output.String(), `PYTHON_TRACER_VERSION=\"4.20.2\"`, output.String())
	commands := [][]string{result.NextCommand, result.ResumeCommand, result.AcceptResumeCommand}
	require.NotEmpty(t, slices.Concat(commands...), output.String())
	for _, command := range commands {
		if i := slices.Index(command, "--tracer-version"); i >= 0 {
			require.Equal(t, "4.20.2", command[i+1], "%v", command)
		}
	}
	require.Contains(t, slices.Concat(commands...), "4.20.2", "the handoff carries the exact release")
	require.NotContains(t, slices.Concat(commands...), "latest")

	selected, err := SelectTracerRelease("python", "latest")
	require.NoError(t, err)
	require.Equal(t, "4.20.2", selected)
	for _, language := range []string{"javascript", "ruby"} {
		selected, err := SelectTracerRelease(language, "latest")
		require.NoError(t, err)
		require.Equal(t, "latest", selected, "%s installs the release its registry tags latest", language)
	}
}
