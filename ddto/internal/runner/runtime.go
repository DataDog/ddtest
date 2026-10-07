package runner

import (
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Runtime selects the test event implementation. Mini is the default.
type Runtime string

const (
	SDK  Runtime = "sdk"
	Mini Runtime = "mini"
)

// hooksForRuntime returns testing's hooks. parallelStop reports that testing
// declares the counter Mini's in-process retries must balance.
func hooksForRuntime(runtime Runtime, parallelStop bool) string {
	if runtime == Mini {
		hooks := strings.ReplaceAll(instrument.Hooks, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/")
		hooks += instrument.FuzzHook
		if parallelStop {
			hooks += instrument.ParallelStopHook
		}
		return hooks
	}
	return instrument.Hooks
}
