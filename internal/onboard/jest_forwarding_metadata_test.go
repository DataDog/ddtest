package onboard

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestJestForwardingReturnsStructuredProbeLintAndStartupOverrides(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"eslint . && jest"}}`), 0600))
	plan, err := ForwardJestExecution(root, "npm test")
	require.NoError(t, err)
	require.True(t, plan.ProbeLintSkipped, "resolution: %+v; plan: %+v", resolveJestCommand(root, "npm test", nil), plan)
	require.Empty(t, plan.ReviewReason)
	legacy, err := ForwardJestCommand(root, "npm test")
	require.NoError(t, err)
	require.Equal(t, plan.Command, legacy)
	plain, err := ForwardJestExecution(root, "jest --runInBand")
	require.NoError(t, err)
	require.False(t, plain.ProbeLintSkipped)
	require.Empty(t, plain.ReviewReason)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"NODE_OPTIONS=--experimental-vm-modules jest"}}`), 0600))
	override, err := ForwardJestExecution(root, "npm test")
	require.NoError(t, err)
	require.Contains(t, override.ReviewReason, "NODE_OPTIONS")
}
