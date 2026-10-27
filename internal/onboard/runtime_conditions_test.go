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
		{"contains(matrix.node, '20')", true},
		{"contains(matrix.node,20)", true}, {"contains(22.1,'beta')", false},
		{"format('{0}',22.1) == '22.1'", true}, {"startsWith(22.1,'22.')", true},
		{`contains(fromJSON('["18.x", "20.x", "22.x"]'), matrix.node)`, true},
		{`contains(fromJSON('["18.x", "22.x"]'), matrix.node)`, false},
		{`contains(fromJSON('["20.X"]'), matrix.node)`, true},
		{`contains(fromJSON('[]'), matrix.node)`, false},
		{"matrix.missing", false}, {"matrix.missing < 22", true}, {"matrix.missing != ''", false},
		{"matrix.enabled", false}, {"matrix.text", true},
		{"matrix.enabled == false", true}, {"matrix.number == 22", true},
		{"matrix.number == '22'", true}, {"matrix.enabled == 'false'", false},
		{"matrix.enabled < 22", true}, {"matrix.enabled == ''", true},
		{"matrix.number != 'lts'", true}, {"matrix.number == '022'", true},
		{"22.1 != 'lts' && 22.1 != 'beta'", true},
		{"22.1 == '22.1'", true}, {"true == 1", true},
		{"startsWith(matrix.node, '20') && matrix.text != 'FALSE'", false},
		{"'it''s true' == 'IT''S TRUE'", true},
		{"matrix.number >= 22", true}, {"matrix.number > 22", false},
		{"matrix.number <= 22", true}, {"matrix.number < 22", false},
		{"matrix.number >= 18 && matrix.number < 24", true},
		{"matrix.number >= 22 == true", true},
		{"false || matrix.number < 20 && true", false},
		{"'22' >= matrix.number", true}, {"'9' > '22'", true},
		{"'bad' < matrix.number", false}, {"'bad' >= matrix.number", false},
		{"'' < matrix.number", true}, {"'2.2e1' >= matrix.number", true},
		{"'022' >= matrix.number", true}, {"'null' <= matrix.number", false},
		// The null literal is falsy and compares as zero with unlike types.
		{"null", false}, {"!null", true}, {"null == null", true}, {"matrix.missing == null", true},
		{"null == false", true}, {"null == 0", true}, {"null == ''", true}, {"null == 'x'", false},
		{"matrix.number != null", true}, {"null < matrix.number", true},
		// GitHub converts a plain decimal string compared with a number.
		{"matrix.number == '22'", true}, {"matrix.number != '20'", true}, {"'22.0' == matrix.number", true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got, err := runtimeCondition(tc.expression, row)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, expression := range []string{"false && github.event_name == 'push'", "matrix.node == '20.x' ||", "(true", "true garbage"} {
		_, err := runtimeCondition(expression, row)
		require.Error(t, err, expression)
	}
	for _, expression := range []string{"matrix.number >=", "matrix.number >= 22 || github.event_name == 'push'", "matrix.number >= 22 garbage", `contains(fromJSON('[[1]]'), matrix.node)`, `fromJSON('{}')`, `fromJSON('null')`, `fromJSON('false')`, `fromJSON('oops')`, `contains(fromJSON(env.NODES), matrix.node)`, `contains(fromJSON('["20.x"]'), github.event_name)`} {
		_, err := runtimeCondition(expression, row)
		require.Error(t, err, expression)
	}
}

func TestCompositeEqualityRequiresRuntimeIdentity(t *testing.T) {
	_, err := conditionEqual([]string{"22"}, []string{"22"})
	require.ErrorContains(t, err, "runtime identity")
	equal, err := conditionEqual([]string{"22"}, 22)
	require.NoError(t, err)
	require.False(t, equal)
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

func TestConditionsAndMatrixIncludePreserveCoverage(t *testing.T) {
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
        if: contains(fromJSON('["18.x", "20.x", "22.x"]'), matrix.node-version)
        with: {languages: js, js-tracer-version: '5.128.0'}
      - run: yarn test
        env:
          NODE_OPTIONS: ${{ contains(fromJSON('["18.x", "20.x", "22.x"]'), matrix.node-version) && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}
      - run: yarn build
`, matrix)
		root := newJestRepository(t, workflow)
		writeScripts(t, root, map[string]string{"test": "jest", "build": "yarn clean && tsc", "clean": "rimraf ./dist"})
		result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.128.0", Node: ">=18"}, nil
		})
		require.Equal(t, "compatible", result.Status)
		statuses := map[string]string{}
		for _, finding := range result.Jobs {
			require.Equal(t, "test", finding.Job)
			statuses[finding.Node] = finding.Status
			if finding.Status == StatusExcluded {
				require.Equal(t, CodeExcludedRuntime, finding.Code)
			}
		}
		require.Equal(t, map[string]string{"8.x": "excluded", "10.x": "excluded", "12.x": "excluded", "14.x": "excluded", "16.x": "excluded", "18.x": "compatible", "20.x": "compatible", "22.x": "compatible"}, statuses)

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
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }}"}
`
	result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, nil)
	require.Equal(t, "inconclusive", result.Status)
	require.Equal(t, CodeInvalidMatrix, result.Jobs[0].Code)
}

// A JavaScript matrix mixing numeric Node versions with an alias is compared
// the way GitHub does: numbers against a non-numeric string are unequal, and a
// boolean against the string 'true' compares 1 with NaN.
func TestJavaScriptMatrixGuardsCompareLooselyTypedValues(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node-version: [20, 22, 'lts/*']
        include:
          - {node-version: 22, coverage: true}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node-version }}'}
      - run: npm ci
      - if: matrix.node-version != 'lts/*' && contains(matrix.node-version, 2)
        run: npx jest
      - if: matrix.coverage == 'true'
        run: npx jest --coverage
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "jest", scope.Commands[0].Command, "the coverage step never runs: true == 'true' compares 1 with NaN")
}

// The runner formats numbers with G15, parses strings with
// ExpressionUtility.ParseNumber and searches arrays with loose equality.
func TestConditionsFollowTheRunnersNumberRules(t *testing.T) {
	row := map[string]any{"big": 1000000, "number": 22, "node": "20"}
	for _, test := range []struct {
		condition string
		want      bool
	}{
		{"format('{0}', matrix.big) == '1000000'", true},
		{"contains(matrix.big, '1000000')", true},
		{"startsWith(matrix.big, '1e')", false},
		{"format('{0}', 0.1) == '0.1'", true},
		{"matrix.number == ' 22 '", true},
		{"matrix.number == '+22'", true},
		{"matrix.number == '22.'", true},
		{"matrix.number == '2.2e1'", true},
		{"matrix.number == '0x16'", true},
		{"matrix.number == '0o26'", true},
		{"matrix.number == '0x'", false},
		{"matrix.number == '22px'", false},
		{"matrix.number == '1_000'", false},
		{"'Infinity' > matrix.number", true},
		{"'-Infinity' < matrix.number", true},
		{"'inf' > matrix.number", false},
		{`contains(fromJSON('["20.0"]'), 20)`, true},
		{`contains(fromJSON('[20]'), matrix.node)`, true},
		{`contains(fromJSON('["022", "lts"]'), matrix.number)`, true},
		{`contains(fromJSON('[1]'), matrix.node)`, false},
		{`contains(fromJSON('["lts"]'), matrix.number)`, false},
		{`contains(fromJSON('["20.x"]'), matrix.node)`, false},
		{"-1 == '0xffffffff'", true},
		{"-1 == '0o37777777777'", true},
		{"matrix.number == '0x100000000'", false},
		{"fromJSON('[1]')", true},
		{"fromJSON('[1, \"a\"]') && true", true},
		{"!fromJSON('[]')", false},
		{"!('bad' == 1)", true},
		{"matrix.missing || 'fallback'", true},
	} {
		got, err := runtimeCondition(test.condition, row)
		require.NoError(t, err, test.condition)
		require.Equal(t, test.want, got, test.condition)
	}
}
