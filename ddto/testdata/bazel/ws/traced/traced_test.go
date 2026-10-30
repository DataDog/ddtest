package traced

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestMain(m *testing.M) {
	_ = tracer.Start(tracer.WithService("traced-app"))
	code := m.Run()
	tracer.Stop()
	os.Exit(code)
}

func TestWork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var opts []tracer.StartSpanOption
		if remote, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header)); err == nil {
			opts = append(opts, tracer.ChildOf(remote))
		}
		span := tracer.StartSpan("server.handle", opts...)
		span.SetTag("origin.header", r.Header.Get("x-datadog-origin"))
		span.Finish()
		w.WriteHeader(204)
	}))
	defer srv.Close()
	if err := Work(t.Context(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestManualSpan(t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(t.Context(), "manual.parent")
	child := tracer.StartSpan("manual.child", tracer.ChildOf(span.Context()))
	child.Finish()
	_ = ctx
	span.Finish()
}
