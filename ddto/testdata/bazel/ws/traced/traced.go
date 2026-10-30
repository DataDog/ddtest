// Package traced makes manual dd-trace-go spans.
package traced

import (
	"context"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// Work starts a span with a child and calls url inside the child.
func Work(ctx context.Context, url string) error {
	span, ctx := tracer.StartSpanFromContext(ctx, "work.parent", tracer.ResourceName("parent"))
	defer span.Finish()
	child, ctx := tracer.StartSpanFromContext(ctx, "work.child", tracer.ResourceName("child"))
	defer child.Finish()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if err := tracer.Inject(child.Context(), tracer.HTTPHeadersCarrier(req.Header)); err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
