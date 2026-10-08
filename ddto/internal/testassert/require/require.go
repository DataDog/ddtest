// Package require provides fatal versions of the SDK test assertions.
package require

import assert "github.com/tonyredondo/dd-ci-testing-poc/internal/testassert"

type TestingT interface {
	Errorf(string, ...any)
	FailNow()
}

func Equal(t TestingT, expected, actual any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Equal(t, expected, actual, message...) {
		t.FailNow()
	}
}

func NotEqual(t TestingT, expected, actual any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotEqual(t, expected, actual, message...) {
		t.FailNow()
	}
}

func True(t TestingT, value bool, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.True(t, value, message...) {
		t.FailNow()
	}
}

func False(t TestingT, value bool, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.False(t, value, message...) {
		t.FailNow()
	}
}

func Nil(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Nil(t, value, message...) {
		t.FailNow()
	}
}

func NotNil(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotNil(t, value, message...) {
		t.FailNow()
	}
}

func Error(t TestingT, value error, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Error(t, value, message...) {
		t.FailNow()
	}
}

func NoError(t TestingT, value error, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NoError(t, value, message...) {
		t.FailNow()
	}
}

func EqualError(t TestingT, value error, expected string, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.EqualError(t, value, expected, message...) {
		t.FailNow()
	}
}

func ErrorContains(t TestingT, value error, expected string, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.ErrorContains(t, value, expected, message...) {
		t.FailNow()
	}
}

func ErrorIs(t TestingT, value, target error, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.ErrorIs(t, value, target, message...) {
		t.FailNow()
	}
}

func Len(t TestingT, value any, expected int, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Len(t, value, expected, message...) {
		t.FailNow()
	}
}

func Empty(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Empty(t, value, message...) {
		t.FailNow()
	}
}

func NotEmpty(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotEmpty(t, value, message...) {
		t.FailNow()
	}
}

func Zero(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Zero(t, value, message...) {
		t.FailNow()
	}
}

func NotZero(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotZero(t, value, message...) {
		t.FailNow()
	}
}

func Contains(t TestingT, collection, element any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Contains(t, collection, element, message...) {
		t.FailNow()
	}
}

func NotContains(t TestingT, collection, element any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotContains(t, collection, element, message...) {
		t.FailNow()
	}
}

func IsType(t TestingT, expected, actual any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.IsType(t, expected, actual, message...) {
		t.FailNow()
	}
}

func JSONEq(t TestingT, expected, actual string, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.JSONEq(t, expected, actual, message...) {
		t.FailNow()
	}
}

func Regexp(t TestingT, pattern, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Regexp(t, pattern, value, message...) {
		t.FailNow()
	}
}

func Greater(t TestingT, left, right any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Greater(t, left, right, message...) {
		t.FailNow()
	}
}

func GreaterOrEqual(t TestingT, left, right any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.GreaterOrEqual(t, left, right, message...) {
		t.FailNow()
	}
}

func LessOrEqual(t TestingT, left, right any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.LessOrEqual(t, left, right, message...) {
		t.FailNow()
	}
}

func Positive(t TestingT, value any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Positive(t, value, message...) {
		t.FailNow()
	}
}

func FileExists(t TestingT, path string, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.FileExists(t, path, message...) {
		t.FailNow()
	}
}

func PanicsWithValue(t TestingT, expected any, f func(), message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.PanicsWithValue(t, expected, f, message...) {
		t.FailNow()
	}
}

func Panics(t TestingT, f func(), message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Panics(t, f, message...) {
		t.FailNow()
	}
}

func NotPanics(t TestingT, f func(), message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotPanics(t, f, message...) {
		t.FailNow()
	}
}

func Same(t TestingT, left, right any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.Same(t, left, right, message...) {
		t.FailNow()
	}
}

func NotSame(t TestingT, left, right any, message ...any) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	if !assert.NotSame(t, left, right, message...) {
		t.FailNow()
	}
}
