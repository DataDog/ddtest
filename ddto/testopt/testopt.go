// Package testopt provides a native CI Visibility runtime and testing context.
// It imports neither dd-trace-go nor the instrumentator CLI.
package testopt

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	// Sends the crash output to ddto's test session, if it hands one.
	_ "github.com/tonyredondo/dd-ci-testing-poc/internal/goenv/crashout"
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
type Client struct{ client *minitracer.Client }

// Span represents one CI event; Finish seals it exactly once.
type Span = minitracer.Span

// StartSpanOption specifies an event property.
type StartSpanOption func(*Span)

// FinishOption specifies how an event ends.
type FinishOption = minitracer.FinishOption

// New creates an explicit client without reading credentials from the environment.
func New(c Config) (*Client, error) {
	client, err := minitracer.New(c)
	if err != nil {
		return nil, err
	}
	return &Client{client: client}, nil
}

// StartSpan starts an event, as a child of ctx's event if it has one. Each
// option reserves an entry in the event's metadata map. A nil client starts an
// event that is never sent.
func (c *Client) StartSpan(ctx context.Context, name string, options ...StartSpanOption) (*Span, context.Context) {
	converted := make([]minitracer.StartSpanOption, len(options))
	for i, option := range options {
		converted[i] = minitracer.FuncOption(option)
	}
	var client *minitracer.Client
	if c != nil {
		client = c.client
	}
	return client.StartSpan(ctx, name, converted...)
}

// Flush delivers every buffered event and waits for the result.
func (c *Client) Flush(ctx context.Context) error { return c.client.Flush(ctx) }

// Close delivers the remaining events and releases the client's connections.
func (c *Client) Close(ctx context.Context) error { return c.client.Close(ctx) }

// LastError returns the most recent delivery or rejection error.
func (c *Client) LastError() error { return c.client.LastError() }

// DroppedEvents reports rejected events and events abandoned at final closure.
func (c *Client) DroppedEvents() uint64 { return c.client.DroppedEvents() }

// Context returns the active test context installed by the testing hooks.
// Cancellation follows the native testing lifetime. Uninstrumented tests and
// isolated retry children have no local trace identity.
func Context(tb testing.TB) context.Context { return gotesting.Context(tb) }

// ResourceName sets the event's resource.
func ResourceName(v string) StartSpanOption { return minitracer.ResourceName(v).Func() }

// SpanType sets the native CI event type.
func SpanType(v string) StartSpanOption { return minitracer.SpanType(v).Func() }

// Tag sets metadata or a numeric metric.
func Tag(k string, v any) StartSpanOption { return minitracer.Tag(k, v).Func() }

// StartTime sets the event's start timestamp.
func StartTime(v time.Time) StartSpanOption { return minitracer.StartTime(v).Func() }

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
