package onboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHumanOnboardingPointsAtThePreviewFirst(t *testing.T) {
	text := humanOnboardingHandoff("jest", []string{"/usr/local/bin/ddtest", "onboard", "--framework", "jest", "--apply", "--yes"})
	preview := strings.Index(text, "/usr/local/bin/ddtest onboard --framework jest --apply\n")
	apply := strings.Index(text, "/usr/local/bin/ddtest onboard --framework jest --apply --yes\n")
	require.Positive(t, preview, text)
	require.Greater(t, apply, preview, "the preview comes before the command that writes")
	require.Contains(t, text, "this writes nothing")
}

func TestVitestInstructionsShareTheAgentParagraph(t *testing.T) {
	jest := instructions("javascript", "jest")
	vitest := instructions("javascript", "vitest")
	for _, text := range []string{jest, vitest} {
		require.Contains(t, text, "preview the recognized edits with `ddtest onboard --apply`, which writes nothing")
		require.Contains(t, text, "--agent --all --yes")
	}
	require.True(t, strings.HasPrefix(vitest, "# Enable Datadog Test Optimization for Vitest"))
	require.Contains(t, vitest, "ddtest testdrive --framework vitest --agent --all --yes")
}

func TestExecutableFallbackNoticeIsEmptyForThePersistentExecutable(t *testing.T) {
	// Test binaries aren't go run executables, so the running one is used.
	executable, fromPATH := ddtestExecutable()
	require.NotEmpty(t, executable)
	require.False(t, fromPATH)
	require.Empty(t, ExecutableFallbackNotice())
}

func TestSelfPathNamesTheWrapperInReturnedCommands(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "ddtest")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv(SelfPathVariable, wrapper)

	executable, fromPATH := ddtestExecutable()
	require.Equal(t, wrapper, executable)
	require.False(t, fromPATH, "a chosen path is not a PATH fallback")
	require.Empty(t, ExecutableFallbackNotice())
	command := DDTestCommand("testdrive", "--report")
	require.Equal(t, []string{wrapper, "testdrive", "--report"}, command[:3])
}

func TestSelfPathIsIgnoredUnlessItIsAnAbsoluteExecutableFile(t *testing.T) {
	running, err := os.Executable()
	require.NoError(t, err)
	directory := t.TempDir()
	plain := filepath.Join(directory, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("data"), 0o644))
	executable := filepath.Join(directory, "ddtest")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755))
	relative, err := filepath.Rel(mustGetwd(t), executable)
	require.NoError(t, err)

	cases := map[string]string{
		"empty":      "",
		"relative":   relative,
		"missing":    filepath.Join(directory, "missing"),
		"directory":  directory,
		"executable": executable,
	}
	if runtime.GOOS != "windows" {
		cases["not executable"] = plain
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(SelfPathVariable, value)
			path, fromPATH := ddtestExecutable()
			require.False(t, fromPATH)
			if name == "executable" {
				require.Equal(t, executable, path)
				return
			}
			require.Equal(t, running, path, "an unusable value falls back to the running executable")
		})
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	require.NoError(t, err)
	return directory
}
