// CI test-cycle wire schema derived from dd-trace-go; source base and adaptations
// are recorded in ../thirdparty/dd-trace-go/README.md (Apache-2.0).
//
//go:generate go run ../../scripts/msgpackgen -file=events.go -unexported -marshal=false -o=events_msgp.go -tests=false
package minitracer

import "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"

type testCyclePayload struct {
	Version  int32                        `msg:"version"`
	Metadata map[string]map[string]string `msg:"metadata"`
	Events   msgp.Raw                     `msg:"events"`
}

// testCycleBatch encodes events directly into the envelope, without an intermediate array buffer.
type testCycleBatch struct {
	Version  int32                        `msg:"version"`
	Metadata map[string]map[string]string `msg:"metadata"`
	Events   ciEvents                     `msg:"events"`
}
type testCyclePayloads []*testCyclePayload
type ciEvents []*ciEvent
type ciEvent struct {
	Type    string       `msg:"type"`
	Version int32        `msg:"version"`
	Content eventContent `msg:"content"`
	common  *CommonTags  `msg:"-"` // Immutable defaults, projected into envelope or event at delivery.
}
type eventContent struct {
	SessionID     uint64             `msg:"test_session_id,omitempty"`    // identifier of this session
	ModuleID      uint64             `msg:"test_module_id,omitempty"`     // identifier of this module
	SuiteID       uint64             `msg:"test_suite_id,omitempty"`      // identifier of this suite
	CorrelationID string             `msg:"itr_correlation_id,omitempty"` // Correlation Id for Intelligent Test Runner transactions
	Name          string             `msg:"name"`                         // operation name
	Service       string             `msg:"service"`                      // configured test service
	Resource      string             `msg:"resource"`                     // test or CI hierarchy resource name
	Type          string             `msg:"type"`                         // test or CI hierarchy event type
	Start         int64              `msg:"start"`                        // span start time expressed in nanoseconds since epoch
	Duration      int64              `msg:"duration"`                     // duration of the span expressed in nanoseconds
	SpanID        uint64             `msg:"span_id,omitempty"`            // identifier of this span
	TraceID       uint64             `msg:"trace_id,omitempty"`           // lower 64-bits of the root span identifier
	ParentID      uint64             `msg:"parent_id,omitempty"`          // identifier of the span's direct parent
	Error         int32              `msg:"error"`                        // error status of the span; 0 means no errors
	Meta          map[string]string  `msg:"meta,omitempty"`               // arbitrary map of metadata
	Metrics       map[string]float64 `msg:"metrics,omitempty"`            // arbitrary map of numeric metrics
}
