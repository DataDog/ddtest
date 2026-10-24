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

func TestBabelNodeSetupRequestsOutputsAndPostTestCopiesStaySeparate(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts/build.js"), []byte("// generates lib/index.js"), 0600))
	writeScripts(t, root, map[string]string{"build": "babel-node scripts/build.js", "test": "jest", "docs": "documentation build src/index.js -f html -o docs && sed -i.bak 's/old/new/g' docs/index.html && rm docs/index.html.bak && rsync -a docs/ site/docs"})
	path := filepath.Join(root, ".github/workflows/test.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    steps:\n      - run: npm run build\n      - run: npm test\n      - run: npm run docs\n"), 0600))
	scope, err := DiscoverWorkflowScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Len(t, scope.Commands[0].Prerequisites, 1)
	require.Equal(t, PreparationNeedsFacts, scope.Commands[0].Prerequisites[0].Kind)
	require.Empty(t, scope.Commands[0].UnvalidatedSetup)
	require.NotEmpty(t, scope.Review)
}
