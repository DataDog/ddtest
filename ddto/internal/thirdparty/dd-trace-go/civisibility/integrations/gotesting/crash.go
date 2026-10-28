package gotesting

import (
	_ "unsafe" // go:linkname

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

// instrumentTestTimeout runs in testing's -timeout alarm just before it
// panics with message: see instrument.TimeoutHook. It returns message for
// that panic.
//
//go:linkname instrumentTestTimeout
func instrumentTestTimeout(message string) string {
	integrations.CloseForCrash("timeout", message, "")
	return message
}
