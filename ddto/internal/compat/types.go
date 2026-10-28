// Package compat supplies the two Go 1.26 helpers used by the port while Mini
// supports Go 1.25. Other code uses the standard library directly.
package compat

import "errors"

// AsType finds a matching error without depending on Go 1.26's generic helper.
func AsType[T error](err error) (T, bool) {
	var target T
	ok := errors.As(err, &target)
	return target, ok
}

// Pointer returns a pointer to a copy of value, including non-type expressions.
func Pointer[T any](value T) *T { return &value }
