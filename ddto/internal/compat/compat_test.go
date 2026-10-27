package compat

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorMatchingPreservesWrappedAndJoinedErrors(t *testing.T) {
	want := &fixtureError{message: "fixture"}
	for _, err := range []error{want, fmt.Errorf("wrapped: %w", want), errors.Join(errors.New("other"), want)} {
		got, ok := AsType[*fixtureError](err)
		if !ok || got != want {
			t.Fatal("matching error identity changed")
		}
	}
	for _, err := range []error{nil, errors.New("unrelated")} {
		if got, ok := AsType[*fixtureError](err); ok || got != nil {
			t.Fatal("unrelated error matched")
		}
	}
}

type fixtureError struct{ message string }

func (e *fixtureError) Error() string { return e.message }

func TestPointerRetainsValueOwnership(t *testing.T) {
	value := "original"
	copy := Pointer(value)
	value = "changed"
	if *copy != "original" {
		t.Fatal("pointer did not own its value")
	}
}
