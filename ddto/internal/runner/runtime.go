package runner

import (
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Runtime selects the test event implementation. SDK remains the default.
type Runtime string

const (
	SDK  Runtime = "sdk"
	Mini Runtime = "mini"
)

func hooksForRuntime(runtime Runtime) string {
	if runtime == Mini {
		return strings.ReplaceAll(instrument.Hooks, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/")
	}
	return instrument.Hooks
}
