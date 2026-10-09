// Package testopt provides a native CI Visibility runtime and testing context.
// It imports neither dd-trace-go nor the instrumentator CLI.
package testopt

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	// Restores the caller's Go settings before any package that imports os.
	_ "github.com/tonyredondo/dd-ci-testing-poc/internal/goenv/restore"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

// Config controls the native event client.
type Config = minitracer.Config

// TransportConfig configures delivery of native CI payloads.
type TransportConfig = citransport.Config

// Client owns a bounded batch of native CI events.
type Client = minitracer.Client

// Span represents one CI event; Finish seals it exactly once.
type Span = minitracer.Span

// StartSpanOption specifies an event property.
type StartSpanOption = minitracer.StartSpanOption

// FinishOption specifies how an event ends.
type FinishOption = minitracer.FinishOption

// New creates an explicit client without reading credentials from the environment.
func New(c Config) (*Client, error) { return minitracer.New(c) }

// Context returns the active test context installed by the testing hooks.
// Cancellation follows the native testing lifetime. Uninstrumented tests and
// isolated retry children have no local trace identity.
func Context(tb testing.TB) context.Context { return gotesting.Context(tb) }

// ResourceName sets the event's resource.
func ResourceName(v string) StartSpanOption { return minitracer.ResourceName(v) }

// SpanType sets the native CI event type.
func SpanType(v string) StartSpanOption { return minitracer.SpanType(v) }

// Tag sets metadata or a numeric metric.
func Tag(k string, v any) StartSpanOption { return minitracer.Tag(k, v) }

// StartTime sets the event's start timestamp.
func StartTime(v time.Time) StartSpanOption { return minitracer.StartTime(v) }

// FinishTime sets the event's end timestamp.
func FinishTime(v time.Time) FinishOption { return minitracer.FinishTime(v) }

// F adapts testing.F for manual fuzz instrumentation. With ddto's automatic
// hooks, the adapter delegates to them and does not produce duplicate events.
type F = gotesting.F

// GetFuzz preserves the original testing.F and the fuzz callback's exact type.
func GetFuzz(f *testing.F) *F { return gotesting.GetFuzz(f) }

// RunM instruments a TestMain entrypoint without the CLI. Tests retain Go's
// exit code; ddto's automatic M.Run hook owns instrumented builds.
func RunM(m *testing.M) int {
	registerTestPackage(1)
	return gotesting.RunM(m)
}

// callerEnvironment holds ddto's fingerprint of the caller's Go settings.
var callerEnvironment string

// RegisterTestEnvironment keeps ddto's fingerprint of the caller's Go
// settings in the test binary. Go keys cached test results on the binary and
// on its own environment, which ddto replaces with a temporary workspace;
// the fingerprint makes cached results follow the caller's values instead.
func RegisterTestEnvironment(fingerprint string) { callerEnvironment = fingerprint }

// RegisterTestPackage records the caller's source directory for opt-in
// CODEOWNERS services. ddto calls it from its generated test-package init;
// ordinary imports and disabled configuration perform no source lookup.
func RegisterTestPackage() { registerTestPackage(1) }

func registerTestPackage(skip int) {
	if !infra.BoolEnv(utils.ServiceFromCodeOwnersEnv, false) {
		return
	}
	if _, file, _, ok := runtime.Caller(skip + 1); ok {
		utils.RegisterTestPackageSource(file)
	}
}
