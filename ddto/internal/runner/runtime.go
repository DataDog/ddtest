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
// declares the counter Mini's in-process retries must balance, and timeout
// that its -timeout alarm calls Mini's.
func hooksForRuntime(runtime Runtime, parallelStop, timeout bool) string {
	if runtime == Mini {
		hooks := strings.ReplaceAll(instrument.Hooks+instrument.FuzzHook, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/")
		hooks = strings.Replace(hooks, instrument.OrchestrionMarkCall, instrument.MiniOrchestrionMark, 1)
		hooks = strings.Replace(hooks, "import _ \"unsafe\"\n", "import (\n\t\"sync/atomic\"\n\t_ \"unsafe\"\n)\n", 1)
		if parallelStop {
			hooks += instrument.ParallelStopHook
		}
		if timeout {
			hooks += instrument.TimeoutHook
		}
		return hooks
	}
	return instrument.Hooks + instrument.FuzzHook
}
