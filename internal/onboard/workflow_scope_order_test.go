package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestPostTestLookaheadResolvesLaterStepsForTheRow(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "tsc", "test": "jest", "test:built": "jest --selectProjects built"})
	path := filepath.Join(root, ".github/workflows/test.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	write := func(steps string) ValidationScope {
		require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    strategy:\n      matrix:\n        script: ['test:built']\n    steps:\n      - run: npm test\n      - run: npm run build\n"+steps), 0600))
		scope, err := DiscoverWorkflowScope(root, "jest")
		require.NoError(t, err)
		return scope
	}
	// The later test is only recognizable after matrix substitution.
	scope := write("      - run: npm run ${{ matrix.script }}\n")
	require.Len(t, scope.Commands, 2)
	require.Empty(t, scope.Commands[0].Prerequisites)
	require.Len(t, scope.Commands[1].Prerequisites, 1, "the build precedes the matrix-selected test")
	require.Equal(t, "tsc", scope.Commands[1].Prerequisites[0].Command)
	// A later test skipped for this row does not keep post-test work.
	scope = write("      - if: matrix.script == 'other'\n        run: npm test\n")
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].Prerequisites)
	require.Contains(t, fmt.Sprint(scope.Review), "Post-test preparation requires separate review")
}

func TestFactBackedRuntimeRequiresFactsOnlyForActiveConsumers(t *testing.T) {
	for _, framework := range []string{"jest", "vitest"} {
		for _, scenario := range []string{"excluded separate setup", "excluded inline setup", "shared setup without facts", "shared setup with facts", "unknown excluded setup"} {
			t.Run(framework+"/"+scenario, func(t *testing.T) {
				setup := "      - if: matrix.enabled == false\n        run: node setup.js\n"
				tests := "      - run: npm test\n"
				switch scenario {
				case "excluded inline setup":
					setup = "      - if: matrix.enabled == false\n        run: node setup.js && npm test\n        env: {NODE_OPTIONS: '', DD_CIVISIBILITY_ENABLED: 'false'}\n"
					tests = "      - if: matrix.enabled\n        run: npm test\n"
				case "shared setup without facts", "shared setup with facts":
					setup = "      - run: node setup.js\n"
				case "unknown excluded setup":
					setup = "      - if: matrix.enabled == false\n        run: unknown-setup-tool\n"
				}
				options := "${{ matrix.enabled && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}"
				if framework == "vitest" {
					options = "${{ matrix.enabled && format('-r {0} --import {1}', env.DD_TRACE_PACKAGE, env.DD_TRACE_ESM_IMPORT) || '' }}"
				}
				root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        include:
          - {node: '18', enabled: false}
          - {node: '24', enabled: true}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.enabled
        with: {languages: js, js-tracer-version: '5.132.0'}
`+setup+tests+`        env:
          NODE_OPTIONS: `+options+`
          DD_CIVISIBILITY_ENABLED: ${{ matrix.enabled && 'true' || 'false' }}
`)
				command := "jest"
				if framework == "vitest" {
					command = "vitest run"
				}
				writeScripts(t, root, map[string]string{"test": command})
				require.NoError(t, os.WriteFile(filepath.Join(root, "setup.js"), []byte(`require('fs').writeFileSync('must-not-execute', 'executed')`), 0600))
				scope, err := DiscoverWorkflowScope(root, framework)
				require.NoError(t, err)
				if scenario == "shared setup with facts" {
					for i := range scope.Commands {
						for j := range scope.Commands[i].Prerequisites {
							scope.Commands[i].Prerequisites[j].FactSource = "accepted setup facts"
							scope.Commands[i].Prerequisites[j].ExpectedFiles = []string{"generated.js"}
						}
					}
				}
				ctx := WithWorkflowSetupScope(t.Context(), root, scope)
				result := checkCIRuntimes(ctx, root, nil, func(context.Context, string, string) (tracerRequirement, error) {
					return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
				}, framework)
				blocked := scenario == "shared setup without facts" || scenario == "unknown excluded setup"
				if blocked {
					require.NotEqual(t, StatusCompatible, result.Status)
					require.True(t, slices.ContainsFunc(result.Jobs, func(f RuntimeFinding) bool { return f.Code == CodeUnresolvedCommand }), result.Jobs)
				} else {
					require.Equal(t, StatusCompatible, result.Status, result.Jobs)
					require.Len(t, result.Jobs, 2)
					require.True(t, slices.ContainsFunc(result.Jobs, func(f RuntimeFinding) bool { return f.Status == StatusExcluded }), result.Jobs)
					require.True(t, slices.ContainsFunc(result.Jobs, func(f RuntimeFinding) bool { return f.Status == StatusCompatible }), result.Jobs)
				}
				require.NoFileExists(t, filepath.Join(root, "must-not-execute"), "runtime analysis must never execute setup")
			})
		}
	}
}
