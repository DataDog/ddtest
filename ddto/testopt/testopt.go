// Package testopt provides a native CI Visibility runtime and testing context.
// It imports neither dd-trace-go nor the instrumentator CLI.
package testopt

import (
	"context"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting"
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
