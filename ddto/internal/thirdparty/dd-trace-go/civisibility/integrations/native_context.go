package integrations

import (
	"context"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
)

// NativeTestContext preserves Go's cancellation and attaches the CI identity
// needed by optional SDK span copies. Process retry children have no local test.
func NativeTestContext(ctx context.Context, test Test) context.Context {
	if native, ok := test.(*tslvTest); ok {
		return minitracer.ContextWithTest(ctx, native.span)
	}
	return ctx
}
