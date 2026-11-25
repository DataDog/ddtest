package offlineagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"
)

// upstreamInfoTimeout bounds the agent's /info answer, which a tracing
// library waits for before it reports anything.
const upstreamInfoTimeout = 5 * time.Second

// upstream is the agent the tests themselves talk to, such as a Datadog
// Agent their CI job runs beside them: the offline agent forwards it every
// request that isn't Test Optimization's, so that the tests' own traces,
// stats, telemetry and configuration requests reach it, and its /info
// answers for it.
type upstream struct {
	proxy  *httputil.ReverseProxy
	client *http.Client
	target *url.URL
}

// ParseUpstream reads an agent URL as tracing libraries take
// DD_TRACE_AGENT_URL: http://host:port, https://host:port, or unix:// and a
// socket's absolute path.
func ParseUpstream(value string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("the upstream agent %q: %w", value, err)
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return nil, fmt.Errorf("the upstream agent %q has no host", value)
		}
	case "unix":
		if !strings.HasPrefix(u.Path, "/") {
			return nil, fmt.Errorf("the upstream agent %q names no socket's absolute path", value)
		}
	default:
		return nil, fmt.Errorf("the upstream agent %q: want http://, https:// or unix://", value)
	}
	return u, nil
}

// newUpstream is the forwarder to target, through its socket for unix://.
func newUpstream(target *url.URL) *upstream {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	base := &url.URL{Scheme: target.Scheme, Host: target.Host}
	if target.Scheme == "unix" {
		socket := target.Path
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		}
		base = &url.URL{Scheme: "http", Host: "localhost"}
	}
	proxy := &httputil.ReverseProxy{
		// The request as the tests would send it to the agent itself: its
		// Host the agent's, and no forwarding headers.
		Rewrite:   func(r *httputil.ProxyRequest) { r.SetURL(base) },
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Debug("Offline agent could not forward a request to the upstream agent", "path", r.URL.Path, "error", err)
			http.Error(w, "the upstream agent is unreachable: "+err.Error(), http.StatusBadGateway)
		},
	}
	return &upstream{proxy: proxy, client: &http.Client{Transport: transport, Timeout: upstreamInfoTimeout}, target: base}
}

// forward sends r to the upstream agent as it came, and its answer back.
func (u *upstream) forward(w http.ResponseWriter, r *http.Request) {
	u.proxy.ServeHTTP(w, r)
}

// forwardBody sends r with body, which the agent has already read, to the
// upstream agent, and its answer back; false when the agent didn't answer.
func (u *upstream) forwardBody(w http.ResponseWriter, r *http.Request, body []byte) bool {
	request, err := http.NewRequestWithContext(r.Context(), r.Method, u.target.String()+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return false
	}
	request.Header = r.Header.Clone()
	response, err := u.client.Do(request)
	if err != nil {
		slog.Debug("Offline agent could not forward a request to the upstream agent", "path", r.URL.Path, "error", err)
		return false
	}
	defer func() { _ = response.Body.Close() }()
	copyHeader(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
	return true
}

// info is the upstream agent's /info, its endpoints with the EVP proxy's
// versions the offline agent handles when the upstream lists neither, so
// that the reporting library still finds where to send its payloads; nil
// when the upstream doesn't answer with a JSON object.
func (u *upstream) info(r *http.Request) (http.Header, []byte) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.target.String()+"/info", nil)
	if err != nil {
		return nil, nil
	}
	request.Header = r.Header.Clone()
	response, err := u.client.Do(request)
	if err != nil {
		slog.Debug("Offline agent could not read the upstream agent's /info", "error", err)
		return nil, nil
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes))
	if err != nil || response.StatusCode != http.StatusOK {
		slog.Debug("Offline agent could not read the upstream agent's /info", "status", response.Status, "error", err)
		return nil, nil
	}
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(body, &answer); err != nil {
		slog.Debug("Offline agent could not read the upstream agent's /info", "error", err)
		return nil, nil
	}
	var endpoints []string
	if raw, ok := answer["endpoints"]; ok {
		_ = json.Unmarshal(raw, &endpoints)
	}
	if !slices.Contains(endpoints, "/evp_proxy/v2/") && !slices.Contains(endpoints, "/evp_proxy/v4/") {
		endpoints = append(endpoints, "/evp_proxy/v2/", "/evp_proxy/v4/")
		data, _ := json.Marshal(endpoints)
		answer["endpoints"] = data
		if body, err = json.Marshal(answer); err != nil {
			return nil, nil
		}
	}
	header := response.Header.Clone()
	header.Del("Content-Length")
	return header, body
}

// copyHeader copies an answer's headers but its length, which the writer
// sets.
func copyHeader(to, from http.Header) {
	for name, values := range from {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		to[name] = slices.Clone(values)
	}
}
