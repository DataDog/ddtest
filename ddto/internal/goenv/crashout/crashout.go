// Package crashout sends a test process's crash output, which
// runtime/debug.SetCrashOutput describes, to a file in the test session
// directory that ddto hands to the process, so that ddto can report a crash
// that ends the process before CI Visibility sends its events. It does so
// while the process initializes, as soon as os and runtime/debug have, before
// any package that imports testing. Without a handed directory, as in a
// binary started outside ddto, it changes nothing.
//
// Only the test runtime imports it, after goenv/restore, which receives the
// directory.
package crashout

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
)

var (
	stem    string
	failure error
)

func init() {
	dir, ok := goenv.TestSession()
	if !ok {
		return
	}
	// Go's test log does not record files yet: testing starts it in M.Run.
	file, err := os.CreateTemp(dir, "process-*.crash")
	if err != nil {
		failure = err
		return
	}
	defer file.Close()
	// The runtime writes to its own duplicate of the descriptor.
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		failure = err
		_ = os.Remove(file.Name())
		return
	}
	stem = strings.TrimSuffix(file.Name(), ".crash")
}

// Stem returns the path of this process's crash output file without its
// .crash extension, or "" and why there is none when ddto handed a session.
func Stem() (string, error) { return stem, failure }
