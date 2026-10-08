package testassert_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	assert "github.com/tonyredondo/dd-ci-testing-poc/internal/testassert"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
)

type reporter struct{ failures int }

func (r *reporter) Errorf(string, ...any) { r.failures++ }
func (*reporter) FailNow()                { panic("fatal assertion") }

func TestAssertionResults(t *testing.T) {
	var pointer *int
	value := 3
	sentinel := errors.New("sentinel")
	cases := []struct {
		name string
		want bool
		run  func(*reporter) bool
	}{
		{"deep equality", true, func(r *reporter) bool { return assert.Equal(r, []int{1}, []int{1}) }},
		{"different numeric types", false, func(r *reporter) bool { return assert.Equal(r, int32(1), int64(1)) }},
		{"typed nil", true, func(r *reporter) bool { return assert.Nil(r, pointer) }},
		{"non nil", false, func(r *reporter) bool { return assert.Nil(r, &value) }},
		{"empty map", true, func(r *reporter) bool { return assert.Empty(r, map[string]int{}) }},
		{"nonempty map", false, func(r *reporter) bool { return assert.Empty(r, map[string]int{"x": 1}) }},
		{"map key", true, func(r *reporter) bool { return assert.Contains(r, map[string]int{"x": 1}, "x") }},
		{"wrong container", false, func(r *reporter) bool { return assert.NotContains(r, 1, 2) }},
		{"slice element", true, func(r *reporter) bool { return assert.Contains(r, [][]int{{1}}, []int{1}) }},
		{"length", true, func(r *reporter) bool { return assert.Len(r, "abc", 3) }},
		{"wrong length type", false, func(r *reporter) bool { return assert.Len(r, 0, 0) }},
		{"error identity", true, func(r *reporter) bool { return assert.ErrorIs(r, fmt.Errorf("wrapped: %w", sentinel), sentinel) }},
		{"missing error", false, func(r *reporter) bool { return assert.EqualError(r, nil, "") }},
		{"json order", true, func(r *reporter) bool { return assert.JSONEq(r, `{"a":1,"b":2}`, `{"b":2,"a":1}`) }},
		{"invalid json", false, func(r *reporter) bool { return assert.JSONEq(r, "{", "{") }},
		{"greater", true, func(r *reporter) bool { return assert.Greater(r, 2, 1) }},
		{"incompatible comparison", false, func(r *reporter) bool { return assert.Greater(r, 2, int64(1)) }},
		{"nan", false, func(r *reporter) bool { return assert.GreaterOrEqual(r, math.NaN(), math.NaN()) }},
		{"pointer identity", true, func(r *reporter) bool { return assert.Same(r, &value, &value) }},
		{"pointer required", false, func(r *reporter) bool { return assert.NotSame(r, 1, 2) }},
		{"panic", true, func(r *reporter) bool { return assert.PanicsWithValue(r, "boom", func() { panic("boom") }) }},
		{"missing panic", false, func(r *reporter) bool { return assert.Panics(r, func() {}) }},
		{"unexpected panic", false, func(r *reporter) bool { return assert.NotPanics(r, func() { panic("boom") }) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := new(reporter)
			if got := tc.run(r); got != tc.want || (r.failures == 0) != tc.want {
				t.Fatalf("result=%t failures=%d, want=%t", got, r.failures, tc.want)
			}
		})
	}
}

func TestFatalAssertionStopsExecution(t *testing.T) {
	r := new(reporter)
	continued := false
	func() {
		defer func() {
			if got := recover(); got != "fatal assertion" {
				t.Errorf("unexpected panic: %v", got)
			}
		}()
		require.Equal(r, 1, 2)
		continued = true
	}()
	if continued || r.failures != 1 {
		t.Fatalf("continued=%t failures=%d", continued, r.failures)
	}
	require.Equal(r, 1, 1)
}
