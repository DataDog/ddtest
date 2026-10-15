//go:build !unix

package integrations

import (
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// exitBySignal ends the process with exit status 1, as the SDK does: Windows
// cannot send a console event to this process alone.
func exitBySignal(sig os.Signal) {
	log.Debug("civisibility: CI Visibility closed; ending the process with exit status 1 for signal %q", sig)
	log.Flush()
	os.Exit(1)
}
