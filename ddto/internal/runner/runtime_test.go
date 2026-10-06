package runner

import (
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Only Mini links the parallel-stop hook, and only when testing declares the
// counter it increments; the SDK runtime has no registration target.
func TestParallelStopHookOnlyForMini(t *testing.T) {
	if !strings.HasSuffix(hooksForRuntime(Mini, true), instrument.ParallelStopHook) {
		t.Fatal("Mini hooks lack the parallel-stop hook")
	}
	if strings.Contains(hooksForRuntime(Mini, false), "registerTestingParallelStop") {
		t.Fatal("hook added although testing lacks the counter")
	}
	if strings.Contains(hooksForRuntime(SDK, true), "registerTestingParallelStop") {
		t.Fatal("SDK hooks link a Mini-only target")
	}
}
