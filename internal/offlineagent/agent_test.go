package offlineagent

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

// testCyclePayload is a small citestcycle payload with a trace ID above 2^53,
// which JSON numbers parsed as doubles would round.
func testCyclePayload() []byte {
	b := msgp.AppendMapHeader(nil, 3)
	b = msgp.AppendString(b, "version")
	b = msgp.AppendInt(b, 1)
	b = msgp.AppendString(b, "metadata")
	b = msgp.AppendMapHeader(b, 1)
	b = msgp.AppendString(b, "*")
	b = msgp.AppendMapHeader(b, 1)
	b = msgp.AppendString(b, "language")
	b = msgp.AppendString(b, "javascript")
	b = msgp.AppendString(b, "events")
	b = msgp.AppendArrayHeader(b, 1)
	b = msgp.AppendMapHeader(b, 2)
	b = msgp.AppendString(b, "type")
	b = msgp.AppendString(b, "test")
	b = msgp.AppendString(b, "content")
	b = msgp.AppendMapHeader(b, 1)
	b = msgp.AppendString(b, "trace_id")
	return msgp.AppendUint64(b, 12345678901234567890)
}

func asJSON(t *testing.T, payload []byte) string {
	t.Helper()
	var converted bytes.Buffer
	_, err := msgp.CopyToJSON(&converted, bytes.NewReader(payload))
	require.NoError(t, err)
	return converted.String()
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return compressed.Bytes()
}

func newAgent(t *testing.T, httpCacheDir string) (*httptest.Server, string) {
	t.Helper()
	payloadsDir := t.TempDir()
	server := httptest.NewServer(NewHandler(Config{PayloadsDir: payloadsDir, HTTPCacheDir: httpCacheDir}))
	t.Cleanup(server.Close)
	return server, payloadsDir
}

func post(t *testing.T, server *httptest.Server, path string, headers map[string]string, body []byte) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(data)
}

// savedFiles returns the payload files of one kind in name order, and fails
// if a temporary file was left behind.
func savedFiles(t *testing.T, payloadsDir string, kind payloadKind) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(payloadsDir, string(kind)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), "."), "temporary file left behind: %s", entry.Name())
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func readSaved(t *testing.T, payloadsDir string, kind payloadKind, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(payloadsDir, string(kind), name))
	require.NoError(t, err)
	return string(data)
}

func TestInfoAdvertisesTheEVPProxyWithGzip(t *testing.T) {
	server, _ := newAgent(t, "")

	response, err := server.Client().Get(server.URL + "/info")
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	var info struct {
		Endpoints []string `json:"endpoints"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&info))

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Contains(t, info.Endpoints, "/evp_proxy/v2/")
	assert.Contains(t, info.Endpoints, "/evp_proxy/v4/")
}

func TestTestCyclePayloadsAreSavedAsJSON(t *testing.T) {
	payload := testCyclePayload()
	for name, request := range map[string]struct {
		path    string
		headers map[string]string
		body    []byte
	}{
		"v2, plain": {"/evp_proxy/v2/api/v2/citestcycle", map[string]string{evpSubdomainHeader: "citestcycle-intake", "Content-Type": "application/msgpack"}, payload},
		"v4, gzip":  {"/evp_proxy/v4/api/v2/citestcycle", map[string]string{evpSubdomainHeader: "citestcycle-intake", "Content-Type": "application/msgpack", "Content-Encoding": "gzip"}, gzipped(t, payload)},
	} {
		t.Run(name, func(t *testing.T) {
			server, payloadsDir := newAgent(t, "")

			status, _ := post(t, server, request.path, request.headers, request.body)

			assert.Equal(t, http.StatusAccepted, status)
			files := savedFiles(t, payloadsDir, kindTests)
			require.Len(t, files, 1)
			assert.Regexp(t, `^tests-\d+-\d+-1\.json$`, files[0])
			saved := readSaved(t, payloadsDir, kindTests, files[0])
			assert.Equal(t, asJSON(t, payload), saved)
			assert.Contains(t, saved, "12345678901234567890")
		})
	}
}

func coverageRequest(t *testing.T, parts []struct{ name, contentType, body string }) (string, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+part.name+`"; filename="`+part.name+`"`)
		header.Set("Content-Type", part.contentType)
		partWriter, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = partWriter.Write([]byte(part.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return writer.FormDataContentType(), body.Bytes()
}

func TestCoveragePartsAreSavedAndThePlaceholderEventIsSkipped(t *testing.T) {
	server, payloadsDir := newAgent(t, "")
	coverage := msgp.AppendMapHeader(nil, 1)
	coverage = msgp.AppendString(coverage, "version")
	coverage = msgp.AppendInt(coverage, 2)
	contentType, body := coverageRequest(t, []struct{ name, contentType, body string }{
		{"event", "application/json", `{"dummy": true}`},
		{"coveragex", "application/msgpack", string(coverage)},
		{"coverage2", "application/json", `{ "version": 2 }`},
	})

	status, _ := post(t, server, "/evp_proxy/v4/api/v2/citestcov", map[string]string{evpSubdomainHeader: "citestcov-intake", "Content-Type": contentType}, body)

	assert.Equal(t, http.StatusAccepted, status)
	files := savedFiles(t, payloadsDir, kindCoverage)
	require.Len(t, files, 2)
	var contents []string
	for _, file := range files {
		contents = append(contents, readSaved(t, payloadsDir, kindCoverage, file))
	}
	assert.ElementsMatch(t, []string{`{"version":2}`, `{"version":2}`}, contents)
	assert.Empty(t, savedFiles(t, payloadsDir, kindTests))
}

func TestCoverageWithoutACoveragePartIsRejected(t *testing.T) {
	server, payloadsDir := newAgent(t, "")
	contentType, body := coverageRequest(t, []struct{ name, contentType, body string }{
		{"event", "application/json", `{"dummy": true}`},
	})

	status, _ := post(t, server, "/evp_proxy/v4/api/v2/citestcov", map[string]string{evpSubdomainHeader: "citestcov-intake", "Content-Type": contentType}, body)

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Empty(t, savedFiles(t, payloadsDir, kindCoverage))
}

func TestInvalidPayloadsAreRejectedWithoutLeavingFiles(t *testing.T) {
	server, payloadsDir := newAgent(t, "")

	status, _ := post(t, server, "/evp_proxy/v2/api/v2/citestcycle", map[string]string{evpSubdomainHeader: "citestcycle-intake"}, testCyclePayload()[:10])

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Empty(t, savedFiles(t, payloadsDir, kindTests))
}

func TestConfigurationRequestsUseThePlanCache(t *testing.T) {
	cacheDir := t.TempDir()
	settings := `{"data":{"attributes":{"tests_skipping":true}}}`
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "settings.json"), []byte(settings), 0o600))
	server, _ := newAgent(t, cacheDir)
	api := map[string]string{evpSubdomainHeader: "api"}

	status, body := post(t, server, "/evp_proxy/v2/api/v2/libraries/tests/services/setting", api, []byte(`{}`))
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, settings, body)

	// No known_tests.json in the cache: the response turns the feature off.
	status, body = post(t, server, "/evp_proxy/v2/api/v2/ci/libraries/tests", api, []byte(`{}`))
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, cachedResponses["/api/v2/ci/libraries/tests"].fallback, body)
}

func TestConfigurationRequestsTurnEveryFeatureOffWithoutAPlan(t *testing.T) {
	server, _ := newAgent(t, "")

	status, body := post(t, server, "/evp_proxy/v4/api/v2/libraries/tests/services/setting", map[string]string{evpSubdomainHeader: "api"}, []byte(`{}`))

	require.Equal(t, http.StatusOK, status)
	var settings struct {
		Data struct {
			Attributes map[string]any `json:"attributes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &settings))
	require.NotEmpty(t, settings.Data.Attributes)
	for name, value := range settings.Data.Attributes {
		if nested, ok := value.(map[string]any); ok {
			value = nested["enabled"]
		}
		assert.Equal(t, false, value, "setting %s", name)
	}
}

func TestGitUploadsAreAnsweredAsAlreadyKnown(t *testing.T) {
	server, payloadsDir := newAgent(t, "")
	api := map[string]string{evpSubdomainHeader: "api"}
	commits := `[{"id":"0123456789abcdef0123456789abcdef01234567","type":"commit"}]`

	status, body := post(t, server, "/evp_proxy/v2/api/v2/git/repository/search_commits", api, []byte(`{"meta":{"repository_url":"https://example.com/repo"},"data":`+commits+`}`))
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"data":`+commits+`}`, body)

	status, _ = post(t, server, "/evp_proxy/v2/api/v2/git/repository/packfile", api, []byte("pack"))
	assert.Equal(t, http.StatusNoContent, status)

	for _, kind := range []payloadKind{kindTests, kindCoverage, kindTelemetry} {
		assert.Empty(t, savedFiles(t, payloadsDir, kind))
	}
}

func TestTelemetryIsSavedInArrivalOrder(t *testing.T) {
	server, payloadsDir := newAgent(t, "")
	bodies := []string{`{"seq_id":1}`, `{"seq_id":2}`, `{"seq_id":3}`}

	for _, body := range bodies {
		status, _ := post(t, server, "/telemetry/proxy/api/v2/apmtelemetry", nil, []byte(body))
		require.Equal(t, http.StatusAccepted, status)
	}

	files := savedFiles(t, payloadsDir, kindTelemetry)
	require.Len(t, files, 3)
	for i, file := range files {
		assert.Equal(t, bodies[i], readSaved(t, payloadsDir, kindTelemetry, file))
	}
}

func TestUnknownRequestsAreRejected(t *testing.T) {
	server, payloadsDir := newAgent(t, "")

	for _, request := range []struct {
		path      string
		subdomain string
	}{
		{"/v0.4/traces", ""},
		{"/evp_proxy/v3/api/v2/citestcycle", "citestcycle-intake"},
		{"/evp_proxy/v2/api/v2/citestcycle", "llmobs-intake"},
		{"/evp_proxy/v2/api/v2/logs", "api"},
	} {
		status, _ := post(t, server, request.path, map[string]string{evpSubdomainHeader: request.subdomain}, testCyclePayload())
		assert.Equal(t, http.StatusNotFound, status, "%s with subdomain %q", request.path, request.subdomain)
	}
	assert.Empty(t, savedFiles(t, payloadsDir, kindTests))
}
