// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const conditionalBootstrap = "${{ matrix.node >= 18 && format('-r {0} --import {1}', env.DD_TRACE_PACKAGE, env.DD_TRACE_ESM_IMPORT) || '' }}"

func TestConditionalJestBootstrapPreservesMatrixCoverage(t *testing.T) {
	for _, guard := range []string{"matrix.node >= 18", "matrix.node >= 22"} {
		workflow := fmt.Sprintf(`jobs:
  test:
    strategy:
      matrix: {node: [14, 16, 18, 20, 22, 24]}
    steps:
      - uses: actions/setup-node@v3
        with: {node-version: '${{ matrix.node }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.node >= 18
        with: {languages: js, js-tracer-version: '5.128.0'}
      - run: npm test
        env:
          NODE_OPTIONS: ${{ %s && format('-r {0} --import {1}', env.DD_TRACE_PACKAGE, env.DD_TRACE_ESM_IMPORT) || '' }}
`, guard)
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.128.0", Node: ">=18"}, nil
		})
		require.Len(t, result.Jobs, 6)
		for i, finding := range result.Jobs {
			want := "compatible"
			switch {
			case i < 2:
				want = "excluded"
			case i < 4 && guard == "matrix.node >= 22":
				want = "inconclusive"
			}
			require.Equal(t, want, finding.Status, finding)
		}
	}
}

func TestBootstrapExpressionsAndScope(t *testing.T) {
	for _, tc := range []struct {
		options string
		valid   bool
	}{
		{conditionalBootstrap, true},
		{"${{ env.DD_TRACE_PACKAGE != '' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", true},
		{"--max-old-space-size=4096 ${{format('--require={0}', env.DD_TRACE_PACKAGE)}}", true},
		{"${{ matrix.node < 18 && '' || format('--require {0}', env.DD_TRACE_PACKAGE) }}", true},
		{"${{format('--require=" + `"{0}"` + "', env.DD_TRACE_PACKAGE)}}", true},
		{"${{format('-r {0}', format('{0}', env.DD_TRACE_PACKAGE))}}", true},
		{"${{ matrix.node < 18 && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", false},
		{"${{format('--import {0}', env.DD_TRACE_ESM_IMPORT)}}", false},
		{"${{format('-r {0}', '__DD_ACTION_PRELOAD__')}}", false},
		{"-r " + actionPreload, false},
		{"${{format('-r {0}', matrix.spoof)}}", false},
		{"${{format('-r {0}', env.CUSTOM_PRELOAD)}}", false},
		{"${{ github.event_name == 'push' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", false},
		{"${{ true || env.UNKNOWN }} -r ${{env.DD_TRACE_PACKAGE}}", false},
		{"${{ env.DD_TRACE_PACKAGE != '/custom/path' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", false},
		{"${{ env.DD_TRACE_PACKAGE < '/custom/path' && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", false},
		{"${{ !startsWith(env.DD_TRACE_PACKAGE, '/custom') && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}", false},
		{"${{join(env.DD_TRACE_PACKAGE)}}", false},
		{"${{format('-r {1}', env.DD_TRACE_PACKAGE)}}", false},
		{"${{format('-r {0}', env.DD_TRACE_PACKAGE)}", false},
		{"-r $DD_TRACE_PACKAGE", false},
		{"-r ${{env.DD_TRACE_PACKAGE}} `custom`", false},
		{strings.Repeat(" ", 4097) + "-r ${{env.DD_TRACE_PACKAGE}}", false},
	} {
		t.Run(tc.options, func(t *testing.T) {
			step := runtimeStep{Env: map[string]string{"NODE_OPTIONS": tc.options}}
			reason := checkJestBootstrap(ciWorkflow{}, runtimeJob{}, step, map[string]any{"node": 22, "spoof": actionPreload})
			require.Equal(t, tc.valid, reason == "", reason)
		})
	}
	for _, scope := range []string{"workflow", "job", "step"} {
		workflow, job := ciWorkflow{}, runtimeJob{}
		step := runtimeStep{Env: map[string]string{"NODE_OPTIONS": conditionalBootstrap}}
		override := map[string]string{"DD_TRACE_PACKAGE": "custom.js"}
		switch scope {
		case "workflow":
			workflow.Env = override
		case "job":
			job.Env = override
		default:
			step.Env["DD_TRACE_PACKAGE"] = "custom.js"
		}
		require.NotEmpty(t, checkJestBootstrap(workflow, job, step, map[string]any{"node": 22}), scope)
	}
}

func TestBootstrapFormatEscapesAndBounds(t *testing.T) {
	for _, tc := range []struct{ expression, want string }{
		{"${{format('{{{0}}}', 'value')}}", "{value}"},
		{"${{format('}} {0} {{', 'it''s')}} trailing", "} it's { trailing"},
		{"${{format('{0}', '{1}')}}", "{1}"},
		{"${{format('{01}/{0}/{1}', 'a', 'b')}}", "b/a/b"},
		{"${{format('literal')}}", "literal"},
	} {
		got, err := bootstrapOptions(tc.expression, nil, nil)
		require.NoError(t, err, tc.expression)
		require.Equal(t, tc.want, got)
	}
	for _, expression := range []string{
		"format()", "format('{')", "format('}')", "format('{0}')", "format('{256}', 'x')",
		"format('{-1}', 'x')", "format('{0:foo}', 'x')", "format('{0}', 42)",
		"format('{0}{0}', matrix.large)", strings.Repeat("format('{0}', ", 33) + "'x'" + strings.Repeat(")", 33),
	} {
		_, err := bootstrapOptions("${{"+expression+"}}", map[string]any{"large": strings.Repeat("x", 3000)}, nil)
		require.Error(t, err, expression)
	}
	_, err := bootstrapOptions("${{ matrix.large }}"+strings.Repeat("x", 2000), map[string]any{"large": strings.Repeat("x", 3000)}, nil)
	require.Error(t, err)
}
