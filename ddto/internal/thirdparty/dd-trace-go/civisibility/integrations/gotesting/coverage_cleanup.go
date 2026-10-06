package gotesting

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage"
)

// A coverage collector outlives the test body and its execution metadata. Go
// also runs ancestor cleanups when a subtest panics; those snapshots must finish
// before that panic shuts down the writers. Entries exist only for covered
// attempts and are removed by their cleanup, including retry-owned attempts.
var testCoverageShutdown sync.Map // testing/common pointer -> *atomic.Bool

func registerTestCoverageCleanup(t *testing.T, collector coverage.TestCoverage, parentBarrier chan bool) {
	var shutdown atomic.Bool
	ptr := reflect.ValueOf(t).UnsafePointer()
	testCoverageShutdown.Store(ptr, &shutdown)
	t.Cleanup(func() {
		defer testCoverageShutdown.Delete(ptr)
		collector.CollectCoverageAfterTestExecution()
		if parent := getTestParentPrivateFields(t); parent != nil && parent.barrier != nil {
			*parent.barrier = parentBarrier
		}
		if shutdown.Load() {
			integrations.ExitCiVisibility()
		}
	})
}

// requestCoverageShutdown transfers terminal delivery to the nearest coverage
// cleanup. This is used only on the panic path; ordinary tests do no parent walk.
func requestCoverageShutdown(t *testing.T) bool {
	for value := reflect.ValueOf(t); value.IsValid() && !value.IsNil(); {
		if stop, ok := testCoverageShutdown.Load(value.UnsafePointer()); ok {
			stop.(*atomic.Bool).Store(true)
			return true
		}
		value = value.Elem().FieldByName("parent")
	}
	return false
}
