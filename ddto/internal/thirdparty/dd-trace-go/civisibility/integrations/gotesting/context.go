package gotesting

import (
	"context"
	"sync"
	"testing"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

// Bind before the user body can publish its T/B. Go still owns cancelCtx; the
// wrapper adds values only and leaves deadlines and cleanup cancellation intact.
func bindNativeTestContext(tb testing.TB, test integrations.Test) {
	if !minitracer.SDKMirrorEnabled() {
		return
	}
	layout := getTestingInternalsLayout()
	if layout == nil || !layout.common.ctx.available || !layout.common.mu.available {
		return
	}
	var base unsafe.Pointer
	switch tb := tb.(type) {
	case *testing.T:
		base = commonBaseForTest(tb, layout)
	case *testing.B:
		base = commonBaseForBenchmark(tb, layout)
	}
	if base == nil {
		return
	}
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	ctx := fieldPtr[context.Context](base, layout.common.ctx)
	*ctx = integrations.NativeTestContext(*ctx, test)
	mu.Unlock()
}

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
