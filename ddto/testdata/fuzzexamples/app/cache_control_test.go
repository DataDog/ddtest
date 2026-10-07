package app

import (
	"os"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

// SDK read-cache hooks are process-local. Apply the intake-owned root before
// TestMain's early child/worker branches; parent Go globals are not inherited.
func init() {
	if root := os.Getenv("DD_FUZZ_EXAMPLE_CACHE_ROOT"); root != "" {
		net.SetReadCacheHooksForTesting(root, nil, nil, nil, nil)
	}
}
