// Package offlineagent stands in for the Datadog Agent while tests run on a
// machine that can't reach Datadog.
//
// Tracing libraries send it their Test Optimization payloads as they would
// send them to an Agent's EVP proxy. It answers the libraries' configuration
// requests from a plan's HTTP cache, or with every feature turned off, and
// writes the payloads as JSON files in the layout of the libraries' Bazel
// payload-files mode (DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES):
//
//	<payloads dir>/tests/tests-<unix nanos>-<pid>-<seq>.json
//	<payloads dir>/coverage/coverage-<unix nanos>-<pid>-<seq>.json
//	<payloads dir>/telemetry/telemetry-<seq, 20 digits>-<pid>.json
//
// Libraries that write payload files themselves and libraries that send them
// here produce the same files, so one uploader handles both.
//
// With an upstream agent (Config.Upstream), the agent the tests themselves
// talk to, it records the Test Optimization requests alone and forwards
// every other one there, the tests' traces, stats and configuration
// requests among them: its /info is the upstream's, with the EVP proxy
// added when the upstream lists none, and the telemetry it records is
// forwarded too. Without one, it answers everything itself and turns down
// what isn't Test Optimization's.
package offlineagent

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxRequestBytes bounds one request body. Tracing libraries split their
// payloads well below it.
const maxRequestBytes = 64 << 20

const evpSubdomainHeader = "X-Datadog-EVP-Subdomain"

// evpPath matches the EVP proxy versions the agent advertises. Version 4 tells
// libraries they may gzip their payloads.
var evpPath = regexp.MustCompile(`^/evp_proxy/v(2|4)(/.*)$`)

// cachedResponses maps API paths to the files of a plan's HTTP cache, and to
// the response used without one, which turns every feature off.
var cachedResponses = map[string]struct {
	file     string
	fallback string
}{
	"/api/v2/libraries/tests/services/setting": {
		file:     "settings.json",
		fallback: `{"data":{"id":"offline","type":"ci_app_tracers_test_service_settings","attributes":{"code_coverage":false,"tests_skipping":false,"itr_enabled":false,"require_git":false,"flaky_test_retries_enabled":false,"known_tests_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false},"impacted_tests_enabled":false}}}`,
	},
	"/api/v2/ci/libraries/tests": {
		file:     "known_tests.json",
		fallback: `{"data":{"id":"offline","type":"ci_app_libraries_tests","attributes":{"tests":{}}}}`,
	},
	"/api/v2/ci/tests/skippable": {
		file:     "skippable_tests.json",
		fallback: `{"meta":{"correlation_id":""},"data":[]}`,
	},
	"/api/v2/test/libraries/test-management/tests": {
		file:     "test_management.json",
		fallback: `{"data":{"id":"offline","type":"ci_app_libraries_tests","attributes":{"modules":{}}}}`,
	},
}

// Config configures the agent.
type Config struct {
	// PayloadsDir receives the payload files.
	PayloadsDir string
	// HTTPCacheDir is a plan's HTTP cache, .testoptimization/cache/http. Empty,
	// or a missing file, means the responses that turn every feature off.
	HTTPCacheDir string
	// Upstream, when set, is the agent the tests talk to (ParseUpstream),
	// which every request that isn't Test Optimization's goes to.
	Upstream *url.URL
}

// NewHandler returns the agent's HTTP handler.
func NewHandler(config Config) http.Handler {
	return buildAgent(config).handler()
}

func buildAgent(config Config) *agent {
	agent := &agent{config: config, files: newPayloadFiles(config.PayloadsDir)}
	if config.Upstream != nil {
		agent.upstream = newUpstream(config.Upstream)
	}
	return agent
}

func (agent *agent) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /info", agent.info)
	mux.HandleFunc("POST /evp_proxy/", agent.evp)
	mux.HandleFunc("POST /telemetry/proxy/api/v2/apmtelemetry", agent.telemetry)
	mux.HandleFunc("/", agent.other)
	return mux
}

type agent struct {
	config   Config
	files    *payloadFiles
	upstream *upstream
}

func (a *agent) info(w http.ResponseWriter, r *http.Request) {
	if a.upstream != nil {
		if header, body := a.upstream.info(r); body != nil {
			copyHeader(w.Header(), header)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		}
		// The library reports through the recorder whether or not the
		// tests' agent answers.
		slog.Warn("Offline agent answers /info itself: the upstream agent didn't", "upstream", a.config.Upstream.Redacted())
	}
	writeJSON(w, http.StatusOK, `{"version":"ddtest-offline-agent","endpoints":["/evp_proxy/v2/","/evp_proxy/v4/","/telemetry/proxy/"]}`)
}

// other is every request that isn't Test Optimization's: the upstream
// agent's, or turned down without one.
func (a *agent) other(w http.ResponseWriter, r *http.Request) {
	if a.upstream != nil {
		a.upstream.forward(w, r)
		return
	}
	slog.Debug("Offline agent ignored a request", "method", r.Method, "path", r.URL.Path)
	http.NotFound(w, r)
}

func (a *agent) evp(w http.ResponseWriter, r *http.Request) {
	match := evpPath.FindStringSubmatch(r.URL.Path)
	if match == nil {
		a.other(w, r)
		return
	}
	path := match[2]
	switch subdomain := r.Header.Get(evpSubdomainHeader); {
	case subdomain == "citestcycle-intake" && path == "/api/v2/citestcycle":
		a.store(w, r, func(body []byte) error {
			return a.files.writeMsgpack(kindTests, body)
		})
	case subdomain == "citestcov-intake" && path == "/api/v2/citestcov":
		a.store(w, r, func(body []byte) error {
			return a.writeCoverage(r.Header.Get("Content-Type"), body)
		})
	case subdomain == "api" && a.isTestOptimizationAPI(path):
		a.api(w, r, path)
	default:
		if a.upstream != nil {
			a.upstream.forward(w, r)
			return
		}
		slog.Debug("Offline agent ignored an EVP request", "subdomain", subdomain, "path", path)
		http.NotFound(w, r)
	}
}

// isTestOptimizationAPI says whether path is an API request of Test
// Optimization's, which the agent answers itself: the libraries'
// configuration and their git uploads. Without an upstream agent, every
// API request is, the unknown ones turned down.
func (a *agent) isTestOptimizationAPI(path string) bool {
	if a.upstream == nil {
		return true
	}
	_, cached := cachedResponses[path]
	return cached || path == searchCommitsPath || path == packfilePath
}

// The git uploads' API paths.
const (
	searchCommitsPath = "/api/v2/git/repository/search_commits"
	packfilePath      = "/api/v2/git/repository/packfile"
)

func (a *agent) api(w http.ResponseWriter, r *http.Request, path string) {
	if cached, ok := cachedResponses[path]; ok {
		writeJSON(w, http.StatusOK, a.cachedResponse(cached.file, cached.fallback))
		return
	}
	switch path {
	case searchCommitsPath:
		// Answer that Datadog already has every commit, so libraries upload
		// no git data from here.
		body, err := readBody(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &request); err != nil || len(request.Data) == 0 {
			http.Error(w, "search_commits needs a JSON body with data", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, fmt.Sprintf(`{"data":%s}`, request.Data))
	case packfilePath:
		w.WriteHeader(http.StatusNoContent)
	default:
		slog.Debug("Offline agent ignored an API request", "path", path)
		http.NotFound(w, r)
	}
}

// telemetry records the libraries' telemetry, and with an upstream agent
// also forwards it there, answering as the upstream does: the reporting
// library's and the tests' own telemetry come the same way, and the agent
// can't tell them apart.
func (a *agent) telemetry(w http.ResponseWriter, r *http.Request) {
	if a.upstream == nil {
		a.store(w, r, func(body []byte) error {
			return a.files.writeTelemetry(body)
		})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	var body []byte
	if err != nil {
		err = fmt.Errorf("read request body: %w", err)
	} else {
		body, err = decodeBody(r.Header, raw)
	}
	if err == nil {
		err = a.files.writeTelemetry(body)
	}
	if err != nil {
		slog.Error("Offline agent could not save a payload", "path", r.URL.Path, "error", err)
		a.files.lose(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !a.upstream.forwardBody(w, r, raw) {
		writeJSON(w, http.StatusAccepted, `{}`)
	}
}

// store reads a payload request and answers as the intake does once write
// has saved it.
func (a *agent) store(w http.ResponseWriter, r *http.Request, write func([]byte) error) {
	body, err := readBody(w, r)
	if err == nil {
		err = write(body)
	}
	if err != nil {
		slog.Error("Offline agent could not save a payload", "path", r.URL.Path, "error", err)
		a.files.lose(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, `{}`)
}

// writeCoverage saves every coverage part of a multipart request. Libraries
// also send a placeholder "event" part, which carries no data.
func (a *agent) writeCoverage(contentType string, body []byte) error {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return fmt.Errorf("coverage needs a multipart body: %q", contentType)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	saved := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read coverage part: %w", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return fmt.Errorf("read coverage part %q: %w", part.FormName(), err)
		}
		if part.FormName() == "event" {
			continue
		}
		switch partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type")); partType {
		case "application/msgpack":
			err = a.files.writeMsgpack(kindCoverage, data)
		case "application/json":
			err = a.files.writeJSON(kindCoverage, data)
		default:
			err = fmt.Errorf("coverage part %q has unsupported content type %q", part.FormName(), partType)
		}
		if err != nil {
			return err
		}
		saved++
	}
	if saved == 0 {
		return errors.New("coverage request has no coverage part")
	}
	return nil
}

func (a *agent) cachedResponse(file, fallback string) string {
	if a.config.HTTPCacheDir == "" {
		return fallback
	}
	data, err := os.ReadFile(filepath.Join(a.config.HTTPCacheDir, file))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Offline agent could not read the plan's HTTP cache; answering with features off", "file", file, "error", err)
		}
		return fallback
	}
	return string(data)
}

// readBody reads a request body, gunzipping it when the library compressed
// it.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return decodeBody(r.Header, raw)
}

// decodeBody is a request body as read, gunzipped when the library
// compressed it.
func decodeBody(header http.Header, raw []byte) ([]byte, error) {
	if !strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
		return raw, nil
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("gunzip request body: %w", err)
	}
	defer func() { _ = unzipped.Close() }()
	data, err := io.ReadAll(io.LimitReader(unzipped, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("request body is larger than %d bytes", maxRequestBytes)
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
