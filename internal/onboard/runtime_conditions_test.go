// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
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
		{`contains(fromJSON('["18.x", "20.x", "22.x"]'), matrix.node)`, true},
		{`contains(fromJSON('["18.x", "22.x"]'), matrix.node)`, false},
		{`contains(fromJSON('["20.X"]'), matrix.node)`, true},
		{`contains(fromJSON('[]'), matrix.node)`, false},
		{"matrix.missing", false}, {"matrix.missing < 22", true}, {"matrix.missing != ''", false},
		{"matrix.enabled", false}, {"matrix.text", true},
		{"matrix.enabled == false", true}, {"matrix.number == 22", true},
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
		{"'022' >= matrix.number", false}, {"'null' <= matrix.number", false},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got, err := runtimeCondition(tc.expression, row)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, expression := range []string{"matrix.enabled == 'false'", "matrix.number == '22'", "false && github.event_name == 'push'", "matrix.node == '20.x' ||", "(true", "true garbage"} {
		_, err := runtimeCondition(expression, row)
		require.Error(t, err, expression)
	}
	for _, expression := range []string{"matrix.enabled < 22", "matrix.number >=", "matrix.number >= 22 || github.event_name == 'push'", "matrix.number >= 22 garbage", `contains(fromJSON('[1]'), matrix.node)`, `fromJSON('{}')`, `fromJSON('null')`, `fromJSON('false')`, `fromJSON('oops')`, `contains(fromJSON(env.NODES), matrix.node)`, `contains(fromJSON('["20.x"]'), github.event_name)`, `contains(matrix.node, 20)`} {
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
