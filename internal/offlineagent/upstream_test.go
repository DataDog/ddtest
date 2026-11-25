package offlineagent

import (
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUpstream is the tests' own agent: it answers /info with info and
// every other request with 200 and "upstream", and keeps what it got.
type fakeUpstream struct {
	mu       sync.Mutex
	requests []upstreamRequest
	info     string
	redirect string
}

type upstreamRequest struct {
	method, path, host string
	header             http.Header
	body               []byte
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, upstreamRequest{method: r.Method, path: r.URL.RequestURI(), host: r.Host, header: r.Header.Clone(), body: body})
	f.mu.Unlock()
	if r.URL.Path == "/info" {
		w.Header().Set("Datadog-Agent-State", "state-1")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			// An agent that compresses what a client accepts compressed.
			w.Header().Set("Content-Encoding", "gzip")
			zipped := gzip.NewWriter(w)
			_, _ = io.WriteString(zipped, f.info)
			_ = zipped.Close()
			return
		}
		_, _ = io.WriteString(w, f.info)
		return
	}
	if r.URL.Path == "/telemetry/proxy/api/v2/apmtelemetry" && f.redirect != "" {
		http.Redirect(w, r, f.redirect, http.StatusFound)
		return
	}
	w.Header().Set("X-Upstream", "yes")
	_, _ = io.WriteString(w, "upstream")
}

func (f *fakeUpstream) got() []upstreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamRequest(nil), f.requests...)
}

// newProxyAgent is an agent whose upstream is fake, over TCP.
func newProxyAgent(t *testing.T, info string) (*httptest.Server, string, *fakeUpstream, *url.URL) {
	t.Helper()
	fake := &fakeUpstream{info: info}
	agentServer := httptest.NewServer(fake)
	t.Cleanup(agentServer.Close)
	target, err := ParseUpstream(agentServer.URL)
	require.NoError(t, err)
	payloadsDir := t.TempDir()
	server := httptest.NewServer(NewHandler(Config{PayloadsDir: payloadsDir, Upstream: target}))
	t.Cleanup(server.Close)
	return server, payloadsDir, fake, target
}

func TestTheUpstreamGetsEveryRequestButTestOptimizations(t *testing.T) {
	server, payloadsDir, fake, target := newProxyAgent(t, `{"version":"7.40.1","endpoints":["/v0.4/traces","/evp_proxy/v2/"]}`)

	// The tests' own traces and other EVP and API requests reach their agent as they were sent.
	for _, request := range []struct {
		path, subdomain string
	}{
		{"/v0.4/traces", ""},
		{"/evp_proxy/v2/api/v2/llmobs", "llmobs-intake"},
		{"/evp_proxy/v3/api/v2/citestcycle", "citestcycle-intake"},
		{"/evp_proxy/v2/api/v2/logs", "api"},
	} {
		status, body := post(t, server, request.path, map[string]string{evpSubdomainHeader: request.subdomain, "X-Datadog-Trace-Count": "1"}, []byte("payload"))
		assert.Equal(t, http.StatusOK, status, request.path)
		assert.Equal(t, "upstream", body, request.path)
	}
	response, err := server.Client().Get(server.URL + "/v0.7/config?x=1")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, "yes", response.Header.Get("X-Upstream"))

	// Test Optimization's own: recorded or answered here, never forwarded.
	status, _ := post(t, server, "/evp_proxy/v4/api/v2/citestcycle", map[string]string{evpSubdomainHeader: "citestcycle-intake"}, testCyclePayload())
	assert.Equal(t, http.StatusAccepted, status)
	status, body := post(t, server, "/evp_proxy/v2/api/v2/libraries/tests/services/setting", map[string]string{evpSubdomainHeader: "api"}, []byte(`{}`))
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"itr_enabled":false`)

	got := fake.got()
	require.Len(t, got, 5)
	for i, path := range []string{"/v0.4/traces", "/evp_proxy/v2/api/v2/llmobs", "/evp_proxy/v3/api/v2/citestcycle", "/evp_proxy/v2/api/v2/logs", "/v0.7/config?x=1"} {
		assert.Equal(t, path, got[i].path)
		assert.Equal(t, target.Host, got[i].host, "the request's Host is the agent's, as the tests would send it")
	}
	assert.Equal(t, []byte("payload"), got[0].body)
	assert.Equal(t, "1", got[0].header.Get("X-Datadog-Trace-Count"))
	assert.Empty(t, got[0].header.Get("X-Forwarded-For"))
	assert.Len(t, savedFiles(t, payloadsDir, kindTests), 1)
}

func TestInfoIsTheUpstreamsWithTheEVPProxyItHandles(t *testing.T) {
	// An agent that lists no EVP proxy version of the recorder's gets both added; its other fields and headers stay.
	server, _, _, _ := newProxyAgent(t, `{"version":"7.40.1","endpoints":["/v0.4/traces","/evp_proxy/v1/"],"config":{"statsd_port":8125}}`)
	response, err := server.Client().Get(server.URL + "/info")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.JSONEq(t, `{"version":"7.40.1","endpoints":["/v0.4/traces","/evp_proxy/v1/","/evp_proxy/v2/","/evp_proxy/v4/"],"config":{"statsd_port":8125}}`, string(body))
	assert.Equal(t, "state-1", response.Header.Get("Datadog-Agent-State"))

	// One that lists one of them is answered as it is, byte for byte.
	own := `{"version":"7.60.0","endpoints":["/evp_proxy/v4/"]}`
	server, _, _, _ = newProxyAgent(t, own)
	response, err = server.Client().Get(server.URL + "/info")
	require.NoError(t, err)
	body, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, own, string(body))
}

func TestInfoOfAnAgentThatCompressesOrAnswersNull(t *testing.T) {
	// The library accepts gzip: the merge still reads the agent's answer, and gives it uncompressed.
	server, _, _, _ := newProxyAgent(t, `{"version":"7.40.1","endpoints":["/v0.4/traces"]}`)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/info", nil)
	require.NoError(t, err)
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Empty(t, response.Header.Get("Content-Encoding"))
	assert.JSONEq(t, `{"version":"7.40.1","endpoints":["/v0.4/traces","/evp_proxy/v2/","/evp_proxy/v4/"]}`, string(body))

	// An answer that isn't a JSON object, null here, gets the recorder's own.
	server, _, _, _ = newProxyAgent(t, `null`)
	response, err = server.Client().Get(server.URL + "/info")
	require.NoError(t, err)
	body, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Contains(t, string(body), `"version":"ddtest-offline-agent"`)
}

func TestTelemetryGetsTheUpstreamsRedirectAsItIs(t *testing.T) {
	server, payloadsDir, fake, _ := newProxyAgent(t, `{}`)
	fake.redirect = "/elsewhere"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Post(server.URL+"/telemetry/proxy/api/v2/apmtelemetry", "application/json", strings.NewReader(`{"seq_id":1}`))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	assert.Equal(t, http.StatusFound, response.StatusCode)
	assert.Equal(t, "/elsewhere", response.Header.Get("Location"))
	require.Len(t, fake.got(), 1, "the recorder didn't follow the redirect itself")
	assert.Len(t, savedFiles(t, payloadsDir, kindTelemetry), 1)
}

func TestTelemetryIsRecordedAndForwarded(t *testing.T) {
	server, payloadsDir, fake, _ := newProxyAgent(t, `{}`)
	compressed := gzipped(t, []byte(`{"seq_id":1}`))

	status, body := post(t, server, "/telemetry/proxy/api/v2/apmtelemetry", map[string]string{"Content-Encoding": "gzip"}, compressed)

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "upstream", body, "the upstream's answer")
	files := savedFiles(t, payloadsDir, kindTelemetry)
	require.Len(t, files, 1)
	assert.Equal(t, `{"seq_id":1}`, readSaved(t, payloadsDir, kindTelemetry, files[0]))
	got := fake.got()
	require.Len(t, got, 1)
	assert.Equal(t, compressed, got[0].body, "the request as the library sent it")
	assert.Equal(t, "gzip", got[0].header.Get("Content-Encoding"))
}

func TestAnUpstreamThatDoesntAnswer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gone := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	target, err := ParseUpstream(gone)
	require.NoError(t, err)
	payloadsDir := t.TempDir()
	server := httptest.NewServer(NewHandler(Config{PayloadsDir: payloadsDir, Upstream: target}))
	t.Cleanup(server.Close)

	// The library still finds the recorder; the tests' requests fail as without an agent; the telemetry is kept.
	response, err := server.Client().Get(server.URL + "/info")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Contains(t, string(body), `"version":"ddtest-offline-agent"`)
	status, _ := post(t, server, "/v0.4/traces", nil, []byte("payload"))
	assert.Equal(t, http.StatusBadGateway, status)
	status, _ = post(t, server, "/telemetry/proxy/api/v2/apmtelemetry", nil, []byte(`{"seq_id":1}`))
	assert.Equal(t, http.StatusAccepted, status)
	assert.Len(t, savedFiles(t, payloadsDir, kindTelemetry), 1)
}

func TestAnUpstreamOnAUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ddtest-upstream-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	fake := &fakeUpstream{info: `{}`}
	agentServer := &http.Server{Handler: fake}
	go func() { _ = agentServer.Serve(listener) }()
	t.Cleanup(func() { _ = agentServer.Shutdown(context.Background()) })
	target, err := ParseUpstream("unix://" + socket)
	require.NoError(t, err)
	server := httptest.NewServer(NewHandler(Config{PayloadsDir: t.TempDir(), Upstream: target}))
	t.Cleanup(server.Close)

	status, body := post(t, server, "/v0.4/traces", nil, []byte("payload"))

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "upstream", body)
	require.Len(t, fake.got(), 1)
}

func TestParseUpstream(t *testing.T) {
	for _, value := range []string{"http://localhost:8126", "https://agent:8126", "unix:///var/run/datadog/apm.socket", " http://[::1]:8126 "} {
		_, err := ParseUpstream(value)
		assert.NoError(t, err, value)
	}
	for _, value := range []string{"", "localhost:8126", "ftp://agent", "http://", "unix://relative.sock", "unix:",
		"http://gateway:8126/agent", "http://agent:8126?x=1", "http://agent:8126#f"} {
		_, err := ParseUpstream(value)
		assert.Error(t, err, value)
	}
	_, err := ParseUpstream("http://localhost:8126/")
	assert.NoError(t, err, "a trailing slash")
	_, err = ParseUpstream("http://alice:secret@agent:8126")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret", "the error repeats no credential")
}
