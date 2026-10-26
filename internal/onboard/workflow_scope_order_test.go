package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFactPrerequisitesPreserveCompilerAndGeneratorOrder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/generate.cjs"), []byte("// consumes compiler output"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	original := resolveJestCommand(root, "tsc && node scripts/generate.cjs && npm test", nil)
	require.NotEmpty(t, original.Reason)
	result := classifyFactPrerequisites(root, root, "tsc && node scripts/generate.cjs && npm test", "jest", original)
	require.Empty(t, result.Reason)
	require.Len(t, result.Tests, 1)
	for _, builds := range [][]BuildCommand{result.Builds, result.Tests[0].Prerequisites} {
		require.Len(t, builds, 2)
		require.Equal(t, "tsc", builds[0].Command)
		require.Equal(t, "node scripts/generate.cjs", builds[1].Command)
	}
}

func TestShellScriptBuildStepsCanBeDeclaredAsFacts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/build.sh"), []byte("#!/bin/sh\necho built\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/plain.sh"), []byte("echo not executable\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	for _, build := range []string{"./scripts/build.sh", "bash scripts/build.sh", "sh scripts/plain.sh"} {
		command := build + " && npm test"
		original := resolveJestCommand(root, command, nil)
		require.NotEmpty(t, original.Reason, build)
		result := classifyFactPrerequisites(root, root, command, "jest", original)
		require.Empty(t, result.Reason, build)
		require.Len(t, result.Tests, 1)
		require.Len(t, result.Tests[0].Prerequisites, 1)
		require.Equal(t, build, result.Tests[0].Prerequisites[0].Command)
		require.Equal(t, PreparationNeedsFacts, result.Tests[0].Prerequisites[0].Kind)
	}
	for _, build := range []string{"./scripts/plain.sh", "./scripts/missing.sh", "bash /etc/profile"} {
		command := build + " && npm test"
		result := classifyFactPrerequisites(root, root, command, "jest", resolveJestCommand(root, command, nil))
		require.NotEmpty(t, result.Reason, "%s stays unresolved: not an executable repository script", build)
	}
}
