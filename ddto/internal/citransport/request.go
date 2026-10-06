package citransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"

	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
)

// Each retry has its own reader over the same sealed payload. GetBody participates
// in the same ownership boundary as the initial body.
func (t *Transport) newRequest(ctx context.Context, body []byte, owner *bodyOwner) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.config.Endpoint, owner.reader(body))
	if err != nil {
		return nil, errors.New("cannot create CI Visibility request")
	}
	req.ContentLength = int64(len(body))
	if len(body) == 0 {
		req.Body = http.NoBody
	}
	req.GetBody = func() (io.ReadCloser, error) { return owner.reader(body), nil }
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("Datadog-Meta-Lang", "go")
	req.Header.Set("Datadog-Meta-Lang-Version", strings.TrimPrefix(runtime.Version(), "go"))
	req.Header.Set("Datadog-Meta-Lang-Interpreter", runtime.Compiler+"-"+runtime.GOARCH+"-"+runtime.GOOS)
	req.Header.Set("Datadog-Meta-Tracer-Version", t.config.Version)
	if id := infra.ContainerID(); id != "" {
		req.Header.Set("Datadog-Container-ID", id)
	}
	if id := infra.EntityID(); id != "" {
		req.Header.Set("Datadog-Entity-ID", id)
	}
	if t.config.Agentless {
		req.Header.Set("dd-api-key", t.config.APIKey)
		req.Header.Set("Content-Encoding", "gzip")
	} else {
		req.Header.Set("X-Datadog-EVP-Subdomain", "citestcycle-intake")
	}
	return req, nil
}
