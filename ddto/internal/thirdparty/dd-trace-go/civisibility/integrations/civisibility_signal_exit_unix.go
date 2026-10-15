//go:build unix

package integrations

import (
	"os"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// exitBySignal ends the process with sig, as Go's runtime does for a signal
// that no channel wants, so go test reports it as without CI Visibility. If
// another channel registered for sig in the meantime, that channel receives
// it and the process goes on.
func exitBySignal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	log.Debug("civisibility: CI Visibility closed; ending the process with signal %q, as without CI Visibility", sig)
	// The final flush printed nothing more; errors otherwise wait a minute.
	log.Flush()
	if err := syscall.Kill(syscall.Getpid(), s); err != nil {
		log.Error("civisibility: could not end the process with signal %q: %s", sig, err)
	}
}
