package tracedv1

import (
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestMain(m *testing.M) {
	tracer.Start(tracer.WithService("tracedv1-app"))
	code := m.Run()
	tracer.Stop()
	os.Exit(code)
}

func TestManualSpanV1(t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(t.Context(), "manual.v1.parent")
	child, _ := tracer.StartSpanFromContext(ctx, "manual.v1.child")
	child.Finish()
	span.Finish()
}
