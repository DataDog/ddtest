package onboard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnsupportedNodeIsBelowEverySupportedTracer(t *testing.T) {
	for node, unsupported := range map[string]bool{"16": true, "17.9.1": true, "16.x": true, "18": false, "24.14.1": false, "lts/*": false, "": false} {
		require.Equal(t, unsupported, UnsupportedNode(node), node)
	}
}

// Modeled on express: legacy.yml runs only Node 16/17, which no dd-trace
// release supports. ddtest decides on its own not to instrument or try those
// rows; nothing asks the user, and the supported job is still set up.
func TestUnsupportedNodeRowsAreNotTriedWithoutApproval(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '20', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n")
	legacy := filepath.Join(root, ".github/workflows/legacy.yml")
	legacyWorkflow := "jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '17']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm config set loglevel error\n      - run: legacy-only-tool\n      - run: npm test\n"
	require.NoError(t, os.WriteFile(legacy, []byte(legacyWorkflow), 0644))

	scope, err := DiscoverWorkflowScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, scope.Review, NotTriedNote(".github/workflows/legacy.yml / test", []string{"16", "17"}))
	require.Contains(t, scope.Review, NotTriedNote(".github/workflows/test.yml / test", []string{"16"}))
	for _, unresolved := range scope.Unresolved {
		require.NotContains(t, unresolved, "legacy-only-tool", "a row that is never tried can't block")
	}
	require.True(t, slices.ContainsFunc(scope.Commands, func(entry TestCommand) bool { return entry.UnsupportedNode && entry.CIInstrumentationExcluded }), scope.Commands)
	require.True(t, slices.ContainsFunc(scope.Commands, func(entry TestCommand) bool { return !entry.CIInstrumentationExcluded }), "the supported rows stay a ready configuration")

	_, err = ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(legacy)
	require.NoError(t, err)
	require.Equal(t, legacyWorkflow, string(data), "a job that only runs unsupported Node stays unedited")
	data, err = os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	workflow := string(data)
	action := workflow[strings.Index(workflow, "- name: Configure Datadog Test Optimization"):]
	action = action[:strings.Index(action, "uses:")]
	require.Contains(t, action, "if:", "the Node 16 row is guarded out of the inserted action:\n%s", workflow)
	require.Contains(t, action, "matrix.node", workflow)

	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "jest")
	for _, job := range check.Jobs {
		require.NotEqual(t, CodeMissingInstrumentation, job.Code, "%+v", check.Jobs)
		if job.Workflow == ".github/workflows/legacy.yml" {
			require.Equal(t, CodeUnsupportedNode, job.Code, "%+v", job)
			require.Equal(t, StatusExcluded, job.Status)
			require.Equal(t, UnsupportedNodeReason(job.Node), job.Reason)
		}
	}
	require.Equal(t, StatusCompatible, check.Status, check.Jobs)
}

// The Node a test runs on is the one active at its step: a job that tests on
// Node 24 and then switches to Node 16 keeps its Node 24 test validated.
func TestUnsupportedNodeIsDecidedAtEachTestStep(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npx jest --ci\n      - uses: actions/setup-node@v4\n        with: {node-version: '16'}\n      - run: npm test\n")
	scope, err := DiscoverWorkflowScope(root, "jest")
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(scope.Commands, func(entry TestCommand) bool {
		return entry.Command == "jest --ci" && !entry.CIInstrumentationExcluded && !entry.UnsupportedNode
	}), "%+v", scope.Commands)
	require.True(t, slices.ContainsFunc(scope.Commands, func(entry TestCommand) bool {
		return entry.UnsupportedNode && entry.CIInstrumentationExcluded
	}), "%+v", scope.Commands)
	require.Contains(t, scope.Review, NotTriedNote(".github/workflows/test.yml / test", []string{"16"}))
}
