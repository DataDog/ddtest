package onboard

import (
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
