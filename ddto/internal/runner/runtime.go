package runner

import "github.com/DataDog/ddtest/ddto/internal/instrument"

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
		return instrument.MiniHooks(parallelStop, timeout)
	}
	return instrument.Hooks + instrument.FuzzHook
}
