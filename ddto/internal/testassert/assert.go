// Package testassert provides the assertions used by the ported SDK tests.
// It imports only the standard library so dependency tests cannot change a
// consumer's module graph when that consumer runs go mod tidy.
package testassert

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

type TestingT interface{ Errorf(string, ...any) }

func fail(t TestingT, description string, message []any) bool {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}
	if len(message) != 0 {
		if format, ok := message[0].(string); ok && len(message) > 1 {
			description += ": " + fmt.Sprintf(format, message[1:]...)
		} else {
			description += ": " + fmt.Sprint(message...)
		}
	}
	t.Errorf("%s", description)
	return false
}

func Equal(t TestingT, expected, actual any, message ...any) bool {
	if reflect.DeepEqual(expected, actual) {
		return true
	}
	return fail(t, fmt.Sprintf("expected %#v (%T), got %#v (%T)", expected, expected, actual, actual), message)
}
func NotEqual(t TestingT, expected, actual any, message ...any) bool {
	if !reflect.DeepEqual(expected, actual) {
		return true
	}
	return fail(t, "values unexpectedly equal", message)
}
func True(t TestingT, value bool, message ...any) bool {
	if value {
		return true
	}
	return fail(t, "expected true", message)
}
func False(t TestingT, value bool, message ...any) bool {
	if !value {
		return true
	}
	return fail(t, "expected false", message)
}
func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func Nil(t TestingT, value any, message ...any) bool {
	if isNil(value) {
		return true
	}
	return fail(t, fmt.Sprintf("expected nil, got %#v", value), message)
}
func NotNil(t TestingT, value any, message ...any) bool {
	if !isNil(value) {
		return true
	}
	return fail(t, "unexpected nil", message)
}
func Error(t TestingT, value error, message ...any) bool {
	if value != nil {
		return true
	}
	return fail(t, "expected an error", message)
}
func NoError(t TestingT, value error, message ...any) bool {
	if value == nil {
		return true
	}
	return fail(t, fmt.Sprintf("unexpected error: %v", value), message)
}
func EqualError(t TestingT, value error, expected string, message ...any) bool {
	return NotNil(t, value, message...) && Equal(t, expected, value.Error(), message...)
}
func ErrorContains(t TestingT, value error, expected string, message ...any) bool {
	return NotNil(t, value, message...) && Contains(t, value.Error(), expected, message...)
}
func ErrorIs(t TestingT, value, target error, message ...any) bool {
	if errors.Is(value, target) {
		return true
	}
	return fail(t, fmt.Sprintf("error %v does not wrap %v", value, target), message)
}

func length(value any) (int, bool) {
	if value == nil {
		return 0, false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return v.Len(), true
	}
	return 0, false
}
func Len(t TestingT, value any, expected int, message ...any) bool {
	n, ok := length(value)
	if ok && n == expected {
		return true
	}
	return fail(t, fmt.Sprintf("expected length %d, got %d (supported=%t)", expected, n, ok), message)
}
func empty(value any) bool {
	if isNil(value) {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Pointer:
		return empty(v.Elem().Interface())
	}
	return v.IsZero()
}
func Empty(t TestingT, value any, message ...any) bool {
	if empty(value) {
		return true
	}
	return fail(t, fmt.Sprintf("expected empty value, got %#v", value), message)
}
func NotEmpty(t TestingT, value any, message ...any) bool {
	if !empty(value) {
		return true
	}
	return fail(t, "unexpected empty value", message)
}
func Zero(t TestingT, value any, message ...any) bool {
	if value == nil || reflect.ValueOf(value).IsZero() {
		return true
	}
	return fail(t, fmt.Sprintf("expected zero, got %#v", value), message)
}
func NotZero(t TestingT, value any, message ...any) bool {
	if value != nil && !reflect.ValueOf(value).IsZero() {
		return true
	}
	return fail(t, "unexpected zero", message)
}

func contains(collection, element any) (bool, bool) {
	if collection == nil {
		return false, true
	}
	v := reflect.ValueOf(collection)
	switch v.Kind() {
	case reflect.String:
		text, ok := element.(string)
		if !ok {
			return false, false
		}
		return strings.Contains(v.String(), text), true
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if reflect.DeepEqual(v.Index(i).Interface(), element) {
				return true, true
			}
		}
		return false, true
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if reflect.DeepEqual(key.Interface(), element) {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}
func Contains(t TestingT, collection, element any, message ...any) bool {
	found, ok := contains(collection, element)
	if ok && found {
		return true
	}
	return fail(t, fmt.Sprintf("%#v does not contain %#v", collection, element), message)
}
func NotContains(t TestingT, collection, element any, message ...any) bool {
	found, ok := contains(collection, element)
	if ok && !found {
		return true
	}
	return fail(t, fmt.Sprintf("%#v contains %#v or has no supported containment operation", collection, element), message)
}
func IsType(t TestingT, expected, actual any, message ...any) bool {
	if reflect.TypeOf(expected) == reflect.TypeOf(actual) {
		return true
	}
	return fail(t, fmt.Sprintf("expected type %T, got %T", expected, actual), message)
}
func JSONEq(t TestingT, expected, actual string, message ...any) bool {
	var left, right any
	if err := json.Unmarshal([]byte(expected), &left); err != nil {
		return fail(t, fmt.Sprintf("invalid expected JSON: %v", err), message)
	}
	if err := json.Unmarshal([]byte(actual), &right); err != nil {
		return fail(t, fmt.Sprintf("invalid actual JSON: %v", err), message)
	}
	return Equal(t, left, right, message...)
}
func Regexp(t TestingT, pattern, value any, message ...any) bool {
	var re *regexp.Regexp
	switch p := pattern.(type) {
	case *regexp.Regexp:
		re = p
	case string:
		var err error
		re, err = regexp.Compile(p)
		if err != nil {
			return fail(t, fmt.Sprintf("invalid regexp: %v", err), message)
		}
	default:
		return fail(t, "unsupported regexp type", message)
	}
	if re.MatchString(fmt.Sprint(value)) {
		return true
	}
	return fail(t, "value does not match regexp", message)
}

func compare(left, right any) (int, bool) {
	a, b := reflect.ValueOf(left), reflect.ValueOf(right)
	if !a.IsValid() || !b.IsValid() || a.Kind() != b.Kind() {
		return 0, false
	}
	var less, equal bool
	switch a.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		less, equal = a.Int() < b.Int(), a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		less, equal = a.Uint() < b.Uint(), a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		less, equal = a.Float() < b.Float(), a.Float() == b.Float()
		if !less && !equal && !(a.Float() > b.Float()) {
			return 0, false
		}
	case reflect.String:
		less, equal = a.String() < b.String(), a.String() == b.String()
	default:
		return 0, false
	}
	if less {
		return -1, true
	}
	if equal {
		return 0, true
	}
	return 1, true
}
func Greater(t TestingT, left, right any, message ...any) bool {
	n, ok := compare(left, right)
	if ok && n > 0 {
		return true
	}
	return fail(t, "expected left > right", message)
}
func GreaterOrEqual(t TestingT, left, right any, message ...any) bool {
	n, ok := compare(left, right)
	if ok && n >= 0 {
		return true
	}
	return fail(t, "expected left >= right", message)
}
func LessOrEqual(t TestingT, left, right any, message ...any) bool {
	n, ok := compare(left, right)
	if ok && n <= 0 {
		return true
	}
	return fail(t, "expected left <= right", message)
}
func Positive(t TestingT, value any, message ...any) bool {
	if value == nil {
		return fail(t, "expected positive number", message)
	}
	v := reflect.ValueOf(value)
	return Greater(t, value, reflect.Zero(v.Type()).Interface(), message...)
}
func FileExists(t TestingT, path string, message ...any) bool {
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return true
	}
	return fail(t, fmt.Sprintf("file does not exist: %s", path), message)
}
func panicValue(f func()) (value any, panicked bool) {
	panicked = true
	defer func() {
		if panicked {
			value = recover()
		}
	}()
	f()
	return nil, false
}
func PanicsWithValue(t TestingT, expected any, f func(), message ...any) bool {
	actual, panicked := panicValue(f)
	if !panicked {
		return fail(t, "expected panic", message)
	}
	return Equal(t, expected, actual, message...)
}

func Panics(t TestingT, f func(), message ...any) bool {
	_, panicked := panicValue(f)
	if panicked {
		return true
	}
	return fail(t, "expected panic", message)
}
func NotPanics(t TestingT, f func(), message ...any) bool {
	value, panicked := panicValue(f)
	if !panicked {
		return true
	}
	return fail(t, fmt.Sprintf("unexpected panic: %v", value), message)
}
func samePointer(left, right any) (bool, bool) {
	a, b := reflect.ValueOf(left), reflect.ValueOf(right)
	if !a.IsValid() || !b.IsValid() || a.Kind() != reflect.Pointer || b.Kind() != reflect.Pointer {
		return false, false
	}
	return a.Type() == b.Type() && a.Pointer() == b.Pointer(), true
}
func Same(t TestingT, left, right any, message ...any) bool {
	same, ok := samePointer(left, right)
	if ok && same {
		return true
	}
	return fail(t, "expected identical pointers", message)
}
func NotSame(t TestingT, left, right any, message ...any) bool {
	same, ok := samePointer(left, right)
	if ok && !same {
		return true
	}
	return fail(t, "expected distinct pointers", message)
}
