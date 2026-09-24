// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticConditionTypesAndPrecedence(t *testing.T) {
	row := map[string]any{"node": "20.x", "enabled": false, "text": "false", "number": 22}
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"matrix.node == '18.x' || matrix.node == '20.x' || matrix.node == '22.x'", true},
		{"(matrix.node == '18.x' || matrix.node == '20.x') && !matrix.enabled", true},
		{"false || true && false", false},
		{"(false || true) && false", false},
		{"matrix.enabled", false}, {"matrix.text", true},
		{"matrix.enabled == false", true}, {"matrix.number == 22", true},
		{"startsWith(matrix.node, '20') && matrix.text != 'FALSE'", false},
		{"'it''s true' == 'IT''S TRUE'", true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got, err := runtimeCondition(tc.expression, row)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, expression := range []string{"matrix.enabled == 'false'", "matrix.number == '22'", "false && github.event_name == 'push'", "matrix.node == '20.x' ||", "(true", "true garbage", "matrix.missing", "contains(matrix.node, '20')"} {
		_, err := runtimeCondition(expression, row)
		require.Error(t, err, expression)
	}
}

func TestStaticMatrixIncludesFollowOriginalCombinations(t *testing.T) {
	// GitHub's documented fruit/animal example: later includes can overwrite
	// included values, never original axes, and never augment an appended row.
	rows, err := runtimeMatrix(map[string]any{
		"fruit": []any{"apple", "pear"}, "animal": []any{"cat", "dog"},
		"include": []any{
			map[string]any{"color": "green"}, map[string]any{"color": "pink", "animal": "cat"},
			map[string]any{"fruit": "apple", "shape": "circle"},
			map[string]any{"fruit": "banana"}, map[string]any{"fruit": "banana", "animal": "cat"},
		},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []map[string]any{
		{"fruit": "apple", "animal": "cat", "color": "pink", "shape": "circle"},
		{"fruit": "pear", "animal": "cat", "color": "pink"},
		{"fruit": "apple", "animal": "dog", "color": "green", "shape": "circle"},
		{"fruit": "pear", "animal": "dog", "color": "green"},
		{"fruit": "banana"}, {"fruit": "banana", "animal": "cat"},
	}, rows)
	rows, err = runtimeMatrix(map[string]any{"node": []any{18, 20, 22}, "os": []any{"linux", "windows"}, "exclude": []any{map[string]any{"node": 18}}, "include": []any{map[string]any{"node": 18, "os": "linux", "instrument": false}}})
	require.NoError(t, err)
	require.Len(t, rows, 5)
}

func TestTSyringeConditionsAndIncludePreserveCoverage(t *testing.T) {
	for _, matrix := range []string{
		"node-version: [8.x, 10.x, 12.x, 14.x, 16.x, 18.x, 20.x, 22.x]",
		"include: [{node-version: 8.x}, {node-version: 10.x}, {node-version: 12.x}, {node-version: 14.x}, {node-version: 16.x}, {node-version: 18.x}, {node-version: 20.x}, {node-version: 22.x}]",
	} {
		workflow := fmt.Sprintf(`jobs:
  test:
    strategy:
      matrix:
        %s
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node-version }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.node-version == '18.x' || matrix.node-version == '20.x' || matrix.node-version == '22.x'
        with: {languages: js, js-tracer-version: '5.128.0'}
      - run: yarn test
`, matrix)
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.128.0", Node: ">=18"}, nil
		})
		require.Equal(t, "compatible", result.Status)
		require.Len(t, result.Jobs, 8)
		for i, job := range result.Jobs {
			if i < 5 {
				require.Equal(t, "excluded", job.Status)
			} else {
				require.Equal(t, "compatible", job.Status)
			}
		}
	}
}

func TestEmptyMatrixDoesNotMasqueradeAsNoCI(t *testing.T) {
	workflow := `jobs:
  test:
    strategy:
      matrix:
        node: [22]
        exclude: [{node: 22}]
    steps:
      - run: yarn test
`
	result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil)
	require.Equal(t, "inconclusive", result.Status)
	require.Contains(t, result.Jobs[0].Reason, "no entries")
}
