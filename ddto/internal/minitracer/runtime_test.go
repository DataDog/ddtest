package minitracer

import (
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Stop flushes aggregated error logs like dd-trace-go's tracer.Stop, so a
// delivery failure or lost-event report at exit is printed, not discarded.
func TestStopFlushesAggregatedErrorLogs(t *testing.T) {
	recorder := &log.RecordLogger{}
	defer log.UseLogger(recorder)()
	log.Error("mini stop flush check %d", 7)
	Stop()
	for _, line := range recorder.Logs() {
		if strings.Contains(line, "mini stop flush check 7") {
			return
		}
	}
	t.Fatalf("aggregated error not flushed at Stop: %q", recorder.Logs())
}
