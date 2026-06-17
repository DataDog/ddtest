package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/utils"
)

type failingMsgpUnmarshaler struct{}

func (f *failingMsgpUnmarshaler) UnmarshalMsg([]byte) ([]byte, error) {
	return nil, errors.New("msgpack failed")
}

type failingMsgpMarshaler struct{}

func (f failingMsgpMarshaler) MarshalMsg([]byte) ([]byte, error) {
	return nil, errors.New("marshal failed")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestResponseUnmarshalBranches(t *testing.T) {
	var target map[string]string
	if err := (&Response{StatusCode: http.StatusBadRequest}).Unmarshal(&target); err == nil {
		t.Fatal("expected non-unmarshalable response to fail")
	}
	if err := (&Response{CanUnmarshal: true, Format: "xml"}).Unmarshal(&target); err == nil {
		t.Fatal("expected unsupported response format to fail")
	}
	if err := (&Response{CanUnmarshal: true, Format: FormatMessagePack}).Unmarshal(&failingMsgpUnmarshaler{}); err == nil {
		t.Fatal("expected msgpack unmarshal error")
	}
}

func TestRequestHandlerValidationAndRetryBranches(t *testing.T) {
	if NewRequestHandler().Client != defaultHTTPClient {
		t.Fatal("NewRequestHandler should use the default HTTP client")
	}

	handler := NewRequestHandlerWithClient(&http.Client{})
	if _, err := handler.SendRequest(RequestConfig{URL: "http://example.com"}); err == nil {
		t.Fatal("expected missing method error")
	}
	if _, err := handler.SendRequest(RequestConfig{Method: http.MethodGet}); err == nil {
		t.Fatal("expected missing URL error")
	}

	var attempts int
	handler = NewRequestHandlerWithClient(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("network down")
		}),
	})
	response, err := handler.SendRequest(RequestConfig{
		Method:     http.MethodGet,
		URL:        "http://example.com",
		MaxRetries: 1,
		Backoff:    time.Nanosecond,
	})
	if err == nil || response != nil {
		t.Fatalf("expected retry exhaustion error with nil response, got response=%v err=%v", response, err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestRequestHandlerJSONCompressionAndGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test"); got != "custom" {
			t.Fatalf("expected custom header, got %q", got)
		}
		if got := r.Header.Get(HeaderAcceptEncoding); got != ContentEncodingGzip {
			t.Fatalf("expected gzip accept header, got %q", got)
		}
		if got := r.Header.Get(HeaderContentEncoding); got != ContentEncodingGzip {
			t.Fatalf("expected gzip request body, got %q", got)
		}

		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("open gzip request: %v", err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read gzip request: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close gzip request: %v", err)
		}

		var request map[string]string
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["key"] != "value" {
			t.Fatalf("unexpected request body: %#v", request)
		}

		var response bytes.Buffer
		gzipWriter := gzip.NewWriter(&response)
		_, _ = gzipWriter.Write([]byte(`{"ok":"yes"}`))
		_ = gzipWriter.Close()

		w.Header().Set(HeaderContentType, ContentTypeJSONAlternative)
		w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
		_, _ = w.Write(response.Bytes())
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(server.Client()).SendRequest(RequestConfig{
		Method:     http.MethodPost,
		URL:        server.URL,
		Headers:    map[string]string{"X-Test": "custom"},
		Body:       map[string]string{"key": "value"},
		Format:     FormatJSON,
		Compressed: true,
	})
	if err != nil {
		t.Fatalf("SendRequest() returned error: %v", err)
	}
	if !response.Compressed || response.Format != FormatJSON {
		t.Fatalf("unexpected response metadata: %#v", response)
	}
	var decoded map[string]string
	if err := response.Unmarshal(&decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if decoded["ok"] != "yes" {
		t.Fatalf("unexpected response: %#v", decoded)
	}
}

func TestRequestHandlerMultipartCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(HeaderContentEncoding); got != ContentEncodingGzip {
			t.Fatalf("expected gzip multipart body, got %q", got)
		}
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("open gzip body: %v", err)
		}
		defer func() { _ = gzipReader.Close() }()
		r.Body = io.NopCloser(gzipReader)

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		file1, _, err := r.FormFile("file1")
		if err != nil {
			t.Fatalf("file1 missing: %v", err)
		}
		file1Body, _ := io.ReadAll(file1)
		if string(file1Body) != `{"key":"value"}` {
			t.Fatalf("unexpected json file body: %s", string(file1Body))
		}
		file2, _, err := r.FormFile("file2")
		if err != nil {
			t.Fatalf("file2 missing: %v", err)
		}
		file2Body, _ := io.ReadAll(file2)
		if string(file2Body) != "binary" {
			t.Fatalf("unexpected binary file body: %s", string(file2Body))
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(server.Client()).SendRequest(RequestConfig{
		Method: http.MethodPost,
		URL:    server.URL,
		Files: []FormFile{
			{FieldName: "file1", FileName: "file1.json", Content: map[string]string{"key": "value"}, ContentType: ContentTypeJSON},
			{FieldName: "file2", FileName: "file2.bin", Content: strings.NewReader("binary"), ContentType: ContentTypeOctetStream},
		},
		Compressed: true,
	})
	if err != nil {
		t.Fatalf("SendRequest() returned error: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", response.StatusCode)
	}
}

func TestRequestHandlerStatusRetryAndRateLimitBranches(t *testing.T) {
	t.Run("server error retries", func(t *testing.T) {
		var attempts int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		response, err := NewRequestHandlerWithClient(server.Client()).SendRequest(RequestConfig{
			Method:     http.MethodGet,
			URL:        server.URL,
			MaxRetries: 1,
			Backoff:    time.Nanosecond,
		})
		if err == nil || response != nil {
			t.Fatalf("expected retry exhaustion, got response=%v err=%v", response, err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("rate limit reset header", func(t *testing.T) {
		var attempts int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			w.Header().Set(HeaderRateLimitReset, "0")
			http.Error(w, "limited", HTTPStatusTooManyRequests)
		}))
		defer server.Close()

		response, err := NewRequestHandlerWithClient(server.Client()).SendRequest(RequestConfig{
			Method:     http.MethodGet,
			URL:        server.URL,
			MaxRetries: 1,
			Backoff:    time.Nanosecond,
		})
		if err == nil || response != nil {
			t.Fatalf("expected rate-limit retry exhaustion, got response=%v err=%v", response, err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("rate limit fallback backoff", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "limited", HTTPStatusTooManyRequests)
		}))
		defer server.Close()

		response, err := NewRequestHandlerWithClient(server.Client()).SendRequest(RequestConfig{
			Method:     http.MethodGet,
			URL:        server.URL,
			MaxRetries: 0,
			Backoff:    time.Nanosecond,
		})
		if err == nil || response != nil {
			t.Fatalf("expected rate-limit fallback exhaustion, got response=%v err=%v", response, err)
		}
	})
}

func TestHTTPSerializationHelpersErrorBranches(t *testing.T) {
	if _, err := compressData(nil); err == nil {
		t.Fatal("expected nil compression error")
	}
	if _, err := decompressData([]byte("not-gzip")); err == nil {
		t.Fatal("expected gzip decompression error")
	}
	if _, err := serializeData("value", "unknown"); err == nil {
		t.Fatal("expected unsupported serialization format")
	}
	if _, err := serializeData(failingMsgpMarshaler{}, FormatMessagePack); err == nil {
		t.Fatal("expected msgpack marshal error")
	}
	if _, err := prepareContent("not-bytes", ContentTypeOctetStream); err == nil {
		t.Fatal("expected octet-stream content error")
	}
	if _, err := prepareContent("value", "application/unknown"); err == nil {
		t.Fatal("expected unsupported content type error")
	}
	if _, _, err := createMultipartFormData([]FormFile{{
		FieldName:   "bad",
		Content:     "value",
		ContentType: "application/unknown",
	}}, false); err == nil {
		t.Fatal("expected multipart unsupported content error")
	}
	if delay := getExponentialBackoffDuration(20, time.Second); delay != 10*time.Second {
		t.Fatalf("expected max backoff cap, got %s", delay)
	}
}

func TestSerializeDataReaderAndBytes(t *testing.T) {
	bytesData, err := serializeData([]byte("bytes"), FormatJSON)
	if err != nil || string(bytesData) != "bytes" {
		t.Fatalf("serialize bytes = %q, %v", string(bytesData), err)
	}
	readerData, err := serializeData(strings.NewReader("reader"), FormatJSON)
	if err != nil || string(readerData) != "reader" {
		t.Fatalf("serialize reader = %q, %v", string(readerData), err)
	}
	content, err := prepareContent(strings.NewReader("reader"), ContentTypeOctetStream)
	if err != nil || string(content) != "reader" {
		t.Fatalf("prepare reader content = %q, %v", string(content), err)
	}
}

func TestCreateMultipartFormDataWithoutFileName(t *testing.T) {
	body, contentType, err := createMultipartFormData([]FormFile{{
		FieldName:   "field",
		Content:     []byte("value"),
		ContentType: ContentTypeOctetStream,
	}}, false)
	if err != nil {
		t.Fatalf("createMultipartFormData() returned error: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read part: %v", err)
	}
	if part.FileName() != "" {
		t.Fatalf("expected empty filename, got %q", part.FileName())
	}
}

func TestTransportConstructorAgentlessAndURLBranches(t *testing.T) {
	utils.ResetCITags()
	t.Cleanup(utils.ResetCITags)
	utils.AddCITagsMap(map[string]string{
		git.GitRepositoryURL:     "https://github.com/DataDog/ddtest.git/",
		git.GitCommitSHA:         "sha",
		git.GitBranch:            "",
		git.GitTag:               "v1.2.3",
		constants.OSPlatform:     "linux",
		constants.OSArchitecture: "amd64",
		constants.OSVersion:      "ubuntu",
		constants.RuntimeName:    "ruby",
		constants.RuntimeVersion: "3.3.0",
	})
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
	t.Setenv("DD_SITE", "datadoghq.eu")
	t.Setenv("DD_ENV", "ci")
	t.Setenv("DD_TAGS", "test.configuration.flavor:unit,regular:ignored")

	created, ok := NewTransportWithServiceNameAndSubdomain("", "citestcycle").(*transport)
	if !ok {
		t.Fatalf("expected *transport")
	}
	if !created.agentless || created.baseURL != "https://citestcycle.datadoghq.eu" {
		t.Fatalf("unexpected agentless transport: agentless=%t baseURL=%q", created.agentless, created.baseURL)
	}
	if created.serviceName != "ddtest" {
		t.Fatalf("service name = %q, want ddtest", created.serviceName)
	}
	if created.environment != "ci" || created.branchName != "v1.2.3" {
		t.Fatalf("unexpected env/branch: env=%q branch=%q", created.environment, created.branchName)
	}
	if created.testConfigurations.Custom["flavor"] != "unit" {
		t.Fatalf("custom test configuration missing: %#v", created.testConfigurations.Custom)
	}
	if created.headers["dd-api-key"] != "api-key" || created.headers["trace_id"] == "" || created.headers["parent_id"] == "" {
		t.Fatalf("missing agentless headers: %#v", created.headers)
	}

	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "https://custom.example")
	custom, ok := NewTransportWithServiceNameAndSubdomain("explicit-service", "api").(*transport)
	if !ok {
		t.Fatalf("expected *transport")
	}
	if custom.baseURL != "https://custom.example" || custom.serviceName != "explicit-service" {
		t.Fatalf("unexpected custom agentless transport: baseURL=%q service=%q", custom.baseURL, custom.serviceName)
	}
}

func TestTransportGetURLPathForEVPProxy(t *testing.T) {
	transport := &transport{baseURL: "http://localhost:8126"}
	if got, want := transport.getURLPath("api/v2/test"), "http://localhost:8126/evp_proxy/v2/api/v2/test"; got != want {
		t.Fatalf("getURLPath() = %q, want %q", got, want)
	}
}
