package gotesting

import "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"

// isAbnormalTestingMExit reports whether M.Run's finalizer runs because a
// panic leaves M.Run, and records that in the debug log: the panic then ends
// the process after CI Visibility closes. It lives in its own file so that
// testing.go keeps its lines, which stack comparisons rely on.
func isAbnormalTestingMExit(exitCode int) bool {
	if exitCode != testingMAbnormalExitCode {
		return false
	}
	log.Debug("instrumentTestingM: a panic leaves testing.M.Run and will end the process; closing CI Visibility first")
	return true
}
