package gotesting

import (
	"context"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
	"testing"
)

// Context combines the native testing lifetime with the current CI identity.
// Process retry children follow the original SDK contract and have no local
// event identity: their CI events are reconstructed by the controlling parent.
func Context(tb testing.TB) context.Context {
	native := tb.Context()
	if identity, ok := propagation.FromContext(getTestOptimizationContext(tb)); ok {
		return propagation.WithContext(native, identity)
	}
	return native
}
