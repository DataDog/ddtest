package api

import (
	"testing"

	"github.com/DataDog/ddtest/ddto/propagation"
	"github.com/DataDog/ddtest/ddto/testopt"
)

func TestContext(t *testing.T) {
	if _, ok := propagation.FromContext(testopt.Context(t)); !ok {
		t.Fatal("the test's context carries no trace identity")
	}
}
