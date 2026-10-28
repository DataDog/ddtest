//go:build linux || darwin

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestCoordinationFailureKeepsAnExecutableAgentCommand(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, nil, 0600))
	previous := workspace.LockParent
	workspace.LockParent = blocked
	t.Cleanup(func() { workspace.LockParent = previous })
	root := t.TempDir()
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	for _, format := range []string{"agent", "json"} {
		var output bytes.Buffer
		require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: format}), "coordination_unavailable")
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
		require.Equal(t, "request_capability", result.NextAction)
		want := append(onboard.OnboardingCommand("vitest"), "--agent")
		if format == "json" {
			want = append(onboard.OnboardingCommand("vitest"), "--format", "json")
		}
		require.Equal(t, want, result.NextCommand, "the agent retries the same structured onboarding command")
	}
}
