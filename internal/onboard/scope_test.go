package onboard

import (
	"path/filepath"

	"testing"

	"github.com/stretchr/testify/require"
)

func TestScopeDirectoryChangesAndUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest"})
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "jest run --coverage"})
	for _, tc := range []struct {
		command   string
		count     int
		directory string
	}{
		{"cd app && npm test", 1, "app"},
		{"cd app && cd .. && npm test", 1, "."},
		{"cd missing && npm test", 0, ""},
		{"cd /tmp && npm test", 0, ""},
		{"cd ../ && npm test", 0, ""},
		{"cd $(node setup.js) && npm test", 0, ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			remaining := 256
			result := resolveJestSequence(root, root, tc.command, nil, &remaining, "jest")
			require.Len(t, result.Tests, tc.count)
			if tc.count > 0 {
				require.Empty(t, result.Reason)
				require.Equal(t, tc.directory, result.Tests[0].Directory)
			} else {
				require.NotEmpty(t, result.Reason)
			}
		})
	}
	for _, command := range []string{"yarn --frozen-lockfile", "yarn --non-interactive --frozen-lockfile"} {
		require.Empty(t, resolveJestCommand(root, command, nil).Reason)
	}
	require.NotEmpty(t, resolveJestCommand(root, "yarn --unknown-option", nil).Reason)
	require.Equal(t, "CI=true jest run", ValidationCommands(root, "CI=true npx jest run", "jest")[0].Command, "do not discard shell environment for batch execution")
	require.Equal(t, "CI=true jest", ValidationCommands(root, "CI=true npm test", "jest")[0].Command, "preserve environment through package aliases")
}

func TestPartialShellDiscoveryPreservesStateBoundaries(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest run"})
	for _, command := range []string{
		"pnpm add react@$VERSION && pnpm test",
		"pnpm add react@$(node version.js)\npnpm test",
		"node setup.js > setup.log && pnpm test",
	} {
		remaining := 256
		result := resolveJestSequence(root, root, command, nil, &remaining, "jest")
		require.Len(t, result.Tests, 1, command)
		require.NotEmpty(t, result.Tests[0].UnvalidatedSetup, command)
		require.NotEmpty(t, result.Reason, command)
		require.False(t, result.SingleJest, command)
	}
	for _, command := range []string{
		"cd $DIR && pnpm test", "source $SETUP && pnpm test",
		"export NODE_OPTIONS=$OPTIONS; pnpm test", "eval $SETUP && pnpm test",
		"if true; then cd app; fi; pnpm test", "pnpm add $DEP | pnpm test",
		"pnpm add $DEP & pnpm test", "$COMMAND && pnpm test",
	} {
		remaining := 256
		result := resolveJestSequence(root, root, command, nil, &remaining, "jest")
		require.Empty(t, result.Tests, command)
		require.NotEmpty(t, result.Reason, command)
	}
}
