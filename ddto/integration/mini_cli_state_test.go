package integration

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// featureIntake answers, with every feature enabled, the requests that test
// processes make before their first test, and counts them by endpoint and,
// for the settings, by service.
type featureIntake struct {
	coverage bool
	mu       sync.Mutex
	counts   map[string]int
	services map[string]int
}

func (f *featureIntake) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	endpoint := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/setting"):
		endpoint = "settings"
		var request struct {
			Data struct {
				Attributes struct {
					Service string `json:"service"`
				} `json:"attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &request)
		f.mu.Lock()
		if f.services == nil {
			f.services = map[string]int{}
		}
		f.services[request.Data.Attributes.Service]++
		f.mu.Unlock()
		fmt.Fprintf(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{"code_coverage":%t,"tests_skipping":true,"itr_enabled":true,"require_git":false,"known_tests_enabled":true,"impacted_tests_enabled":false,"flaky_test_retries_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":true,"attempt_to_fix_retries":2}}}}`, f.coverage)
	case strings.HasSuffix(r.URL.Path, "/ci/libraries/tests"):
		endpoint = "known"
		fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_libraries_tests","attributes":{"tests":{}}}}`)
	case strings.HasSuffix(r.URL.Path, "/skippable"):
		endpoint = "skippable"
		fmt.Fprint(w, `{"meta":{"correlation_id":"poc"},"data":[]}`)
	case strings.HasSuffix(r.URL.Path, "/test-management/tests"):
		endpoint = "management"
		fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_libraries_tests_request","attributes":{"modules":{}}}}`)
	case strings.HasSuffix(r.URL.Path, "/search_commits"):
		endpoint = "search"
		fmt.Fprint(w, `{"data":[]}`)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
	if endpoint != "" {
		f.mu.Lock()
		if f.counts == nil {
			f.counts = map[string]int{}
		}
		f.counts[endpoint]++
		f.mu.Unlock()
	}
}

// take returns and forgets the counts by endpoint and by service.
func (f *featureIntake) take() (map[string]int, map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts, services := f.counts, f.services
	f.counts, f.services = nil, nil
	return counts, services
}

// wait waits until the intake has received at least want requests by
// endpoint.
func (f *featureIntake) wait(t *testing.T, want map[string]int) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		f.mu.Lock()
		done := true
		for endpoint, n := range want {
			done = done && f.counts[endpoint] >= n
		}
		counts := fmt.Sprint(f.counts)
		f.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("requests %s; want at least %v", counts, want)
		}
	}
}

// featureEndpoints are the requests that a test process makes before its
// first test when the settings enable every feature.
var featureEndpoints = []string{"settings", "known", "skippable", "management"}

// featureEnv isolates the read cache in a new user cache directory and keeps
// Go's build and test cache, which go env reports, warm and shared.
func featureEnv(t *testing.T, url string, extra ...string) []string {
	t.Helper()
	goCache.once.Do(func() {
		out, err := exec.Command("go", "env", "GOCACHE").Output()
		goCache.dir, goCache.err = strings.TrimSpace(string(out)), err
	})
	if goCache.err != nil {
		t.Fatal(goCache.err)
	}
	scratch := t.TempDir()
	return testEnv(append([]string{"DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL=" + url,
		"DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=", "XDG_CACHE_HOME=" + scratch, "GOCACHE=" + goCache.dir}, extra...)...)
}

var goCache struct {
	once sync.Once
	dir  string
	err  error
}

// ddto's Git worker requests, while go test builds, what a test process
// requests before its first test. The test process then finds every response
// in the invocation's read cache: its requests are the worker's, although the
// worker runs another toolchain and has another parent.
func TestMiniCLIStateRequestsAhead(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	intake := &featureIntake{}
	server := newServer(http.HandlerFunc(intake.handler))
	defer server.Close()
	cache := t.TempDir()
	env := featureEnv(t, server.URL, "XDG_CACHE_HOME="+cache)

	// Alone, the test process makes each request.
	if out, stderr, code := command(t, dir, env, bin, "-test.run=^TestPass$"); code != 0 {
		t.Fatalf("fixture: %d %s %s", code, out, stderr)
	}
	counts, _ := intake.take()
	for _, endpoint := range featureEndpoints {
		if counts[endpoint] != 1 {
			t.Fatalf("a test process alone made requests %v", counts)
		}
	}

	state := t.TempDir()
	proceed := startWorker(t, dir, driver, env, state, dir)
	// No test process has started: the worker requests on its own.
	intake.wait(t, map[string]int{"known": 1, "skippable": 1, "management": 1})
	if out, stderr, code := command(t, dir, append(env, "DDTO_GIT_STATE="+state), bin, "-test.run=^TestPass$"); code != 0 {
		t.Fatalf("fixture: %d %s %s", code, out, stderr)
	}
	proceed()
	if _, err := os.Stat(filepath.Join(state, "used")); err != nil {
		t.Fatalf("the test process did not use the state: %v", err)
	}
	counts, _ = intake.take()
	for _, endpoint := range featureEndpoints {
		if counts[endpoint] != 1 {
			t.Fatalf("the worker and the test process made requests %v; want one each, the worker's", counts)
		}
	}

	// With services from CODEOWNERS, the worker requests for each package's.
	addSubPackage(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "CODEOWNERS"), []byte("* @org/root\n/sub/ @org/sub\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env = featureEnv(t, server.URL, "DD_SERVICE=", "DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=true")
	proceed = startWorker(t, dir, driver, env, t.TempDir(), dir, filepath.Join(dir, "sub"))
	intake.wait(t, map[string]int{"known": 2, "skippable": 2, "management": 2})
	proceed()
	if _, services := intake.take(); len(services) != 2 || services["service-root"] != 1 || services["service-sub"] != 1 {
		t.Fatalf("settings by service %v; want one for each package's", services)
	}
}

// startWorker starts ddto's Git worker in dir with the state in state, which
// describes the test packages in dirs, and tells it to proceed; the returned
// function tells it that go test exited and waits for it.
func startWorker(t *testing.T, dir, driver string, env []string, state string, dirs ...string) func() {
	t.Helper()
	goVersion, stderr, code := command(t, dir, testEnv(), "go", "env", "GOVERSION")
	if code != 0 {
		t.Fatalf("go env: %s", stderr)
	}
	// Every test process runs, as with -count=1, so the worker requests
	// while go test builds.
	tests := struct {
		GOARCH        string `json:"goarch"`
		RunsEveryTest bool   `json:"runs_every_test"`
		Packages      []struct {
			Dir string `json:"dir"`
		} `json:"packages"`
	}{GOARCH: runtime.GOARCH, RunsEveryTest: true}
	for _, d := range dirs {
		tests.Packages = append(tests.Packages, struct {
			Dir string `json:"dir"`
		}{d})
	}
	data, err := json.Marshal(tests)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "tests.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	worker := exec.Command(driver, "ci-git-worker", state, "cli-runtime", "1m")
	worker.Dir = dir
	worker.Env = append(env, "PWD="+dir)
	input, err := worker.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var workerErr strings.Builder
	worker.Stderr = &workerErr
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Process.Kill() })
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(state, "git.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker wrote no record: %s", workerErr.String())
		}
	}
	if _, err := io.WriteString(input, "proceed "+strings.TrimSpace(goVersion)+"\n"); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if _, err := io.WriteString(input, "exited\n"); err != nil {
			t.Fatal(err)
		}
		_ = input.Close()
		if err := worker.Wait(); err != nil {
			t.Fatalf("worker: %v %s", err, workerErr.String())
		}
	}
}

// In a ddto invocation, the worker and the test processes share each
// response: one request per endpoint, or, with services from CODEOWNERS, one
// per service.
func TestMiniCLIStateSharedRequests(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	addSubPackage(t, dir)
	intake := &featureIntake{}
	server := newServer(http.HandlerFunc(intake.handler))
	defer server.Close()
	archive, cache := filepath.Join(t.TempDir(), "state.zip"), t.TempDir()
	runCached := func(env []string, args ...string) (string, map[string]int, map[string]int) {
		t.Helper()
		args = append([]string{"test", "--runtime=mini", "--keep-ci-state=" + archive}, args...)
		out, stderr, code := command(t, dir, env, driver, args...)
		if code != 0 {
			t.Fatalf("ddto test: %d %s %s", code, out, stderr)
		}
		counts, services := intake.take()
		return out, counts, services
	}
	run := func(env []string) (map[string]int, map[string]int) {
		t.Helper()
		_, counts, services := runCached(env, "-count=1", "-run=^Test(Pass|Sub)$", ".", "./sub")
		return counts, services
	}

	// When every result comes from Go's test cache, nothing is requested.
	// The sub package's result can be cached, as in TestMiniCLITelemetry.
	// ddto's tool, which signals a test binary's link, keeps Go's cache.
	cachedEnv := featureEnv(t, server.URL)
	runCached(cachedEnv, "-run=^TestSub$", "./sub")
	if out, counts, _ := runCached(cachedEnv, "-run=^TestSub$", "./sub"); !strings.Contains(out, "(cached)") || len(counts) != 0 || zipFiles(t, archive)["linking"] {
		t.Fatalf("a cached run made requests %v or linked:\n%s", counts, out)
	}
	// So with cacheable test flags after -args, which go test hands to the
	// test binary as they are.
	runCached(cachedEnv, "./sub", "-args", "-test.run=^TestSub$")
	if out, counts, _ := runCached(cachedEnv, "./sub", "-args", "-test.run=^TestSub$"); !strings.Contains(out, "(cached)") || len(counts) != 0 {
		t.Fatalf("a cached run with -args made requests %v:\n%s", counts, out)
	}
	// -o makes go test link the test binary to save it, even for a cached
	// result: the worker waits for a test process, and none runs.
	// A new path makes go test link again.
	saved := t.TempDir()
	runCached(cachedEnv, "-o", filepath.Join(saved, "first.test"), "-run=^TestSub$", "./sub")
	if out, counts, _ := runCached(cachedEnv, "-o", filepath.Join(saved, "second.test"), "-run=^TestSub$", "./sub"); !strings.Contains(out, "(cached)") || len(counts) != 0 || zipFiles(t, archive)["linking"] {
		t.Fatalf("a cached run with -o made requests %v or signalled a link:\n%s", counts, out)
	}
	// With one result outside the cache, go test links that package's test
	// binary, which tells the worker to request; the root package's result is
	// never cached.
	if out, counts, _ := runCached(cachedEnv, "-run=^TestSub$", ".", "./sub"); !strings.Contains(out, "(cached)") || counts["settings"] != 1 || !zipFiles(t, archive)["linking"] {
		t.Fatalf("a partly cached run made requests %v, linked %t:\n%s", counts, zipFiles(t, archive)["linking"], out)
	}

	counts, _ := run(featureEnv(t, server.URL, "XDG_CACHE_HOME="+cache))
	for _, endpoint := range featureEndpoints {
		if counts[endpoint] != 1 {
			t.Fatalf("requests %v; want one each", counts)
		}
	}
	// With -count=1, the worker requests while go test builds, so ddto adds
	// no tool to signal links.
	if zipFiles(t, archive)["linking"] {
		t.Fatal("ddto signalled a link in a run that takes no result from the cache")
	}
	// The invocation's read-cache entries go with it. Only Linux takes the
	// user cache directory from XDG_CACHE_HOME.
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir(filepath.Join(cache, "dd-trace-go", "civisibility-read-cache", "cli"))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("ddto left the invocation's read cache %s", entries[0].Name())
		}
	}
	// ddto described the test processes, with their packages' modules.
	module, stderr, code := command(t, dir, testEnv(), "go", "list", "-f", "{{.Dir}};{{.Module.Path}};{{.Module.Dir}}", ".", "./sub")
	if code != 0 {
		t.Fatalf("go list: %s", stderr)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	var tests struct {
		GOARCH   string `json:"goarch"`
		Packages []struct {
			Dir        string `json:"dir"`
			ModulePath string `json:"module_path"`
			ModuleDir  string `json:"module_dir"`
		} `json:"packages"`
	}
	for _, file := range reader.File {
		if file.Name == "tests.json" {
			content, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			err = json.NewDecoder(content).Decode(&tests)
			_ = content.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = reader.Close()
	var described []string
	for _, p := range tests.Packages {
		described = append(described, p.Dir+";"+p.ModulePath+";"+p.ModuleDir)
	}
	slices.Sort(described)
	listed := strings.Split(strings.TrimSpace(module), "\n")
	slices.Sort(listed)
	if tests.GOARCH != runtime.GOARCH || !slices.Equal(described, listed) {
		t.Fatalf("ddto described %+v; go list reports %v", tests, listed)
	}

	// With experiments, which the test binaries' runtime version includes,
	// the worker still requests as they do.
	counts, _ = run(featureEnv(t, server.URL, "GOEXPERIMENT=cgocheck2"))
	for _, endpoint := range featureEndpoints {
		if counts[endpoint] != 1 {
			t.Fatalf("requests with GOEXPERIMENT %v; want one each", counts)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "CODEOWNERS"), []byte("* @org/root\n/sub/ @org/sub\n"), 0600); err != nil {
		t.Fatal(err)
	}
	counts, services := run(featureEnv(t, server.URL, "DD_SERVICE=", "DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=true"))
	if len(services) != 2 || services["service-root"] != 1 || services["service-sub"] != 1 {
		t.Fatalf("settings by service %v; want one for each package's", services)
	}
	for _, endpoint := range featureEndpoints {
		if counts[endpoint] != 2 {
			t.Fatalf("requests %v; want one per service", counts)
		}
	}
}

// With DD_TRACE_DEBUG, ddto, its Git worker and the test processes log when
// and why the worker requests ahead, and where test processes find the
// responses.
func TestMiniCLIStateDebugLog(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	addSubPackage(t, dir)
	intake := &featureIntake{}
	server := newServer(http.HandlerFunc(intake.handler))
	defer server.Close()
	archive := filepath.Join(t.TempDir(), "state.zip")
	env := featureEnv(t, server.URL, "DD_TRACE_DEBUG=true")
	run := func(args ...string) (string, string) {
		t.Helper()
		args = append([]string{"test", "--runtime=mini", "--keep-ci-state=" + archive}, args...)
		out, stderr, code := command(t, dir, env, driver, args...)
		if code != 0 {
			t.Fatalf("ddto test: %d %s %s", code, out, stderr)
		}
		intake.take()
		return out, stderr
	}
	requireLines := func(log, name string, lines ...string) {
		t.Helper()
		for _, line := range lines {
			if !strings.Contains(log, line) {
				t.Fatalf("%s lacks %q:\n%s", name, line, log)
			}
		}
	}

	// -count=1: the worker requests while go test builds, and the test
	// processes find its responses in the invocation's read cache.
	out, stderr := run("-count=1", "-v", "-run=^Test(Pass|Sub)$", ".", "./sub")
	requireLines(stderr, "ddto's log", `requests_ahead=while_building reason="test binary flag -count"`,
		"git worker test description packages=2", "test runtime version=", "git worker read cache removed")
	requireLines(zipFile(t, archive, "worker.log"), "the worker's log", "civisibility: CLI test description [packages:2",
		"CLI requests ahead started [trigger:runs_every_test", "CLI requests ahead finished [client:0 known_tests:true skippable:true test_management:true")
	requireLines(out, "the test processes' log", "civisibility.read_cache: invocation scope", "scope:cli")
	if !regexp.MustCompile(`civisibility\.read_cache: hit (after wait )?\[endpoint:settings `).MatchString(out) {
		t.Fatalf("no test process found the worker's settings:\n%s", out)
	}

	// Every result from the cache: nothing needed the requests.
	run("-run=^TestSub$", "./sub")
	_, stderr = run("-run=^TestSub$", "./sub")
	requireLines(stderr, "ddto's log", "requests_ahead=at_link")
	worker := zipFile(t, archive, "worker.log")
	requireLines(worker, "the worker's log", "CLI requests ahead not needed")
	if strings.Contains(worker, "CLI requests ahead started") {
		t.Fatalf("the worker requested in a cached run:\n%s", worker)
	}

	// One result outside the cache: go test linked its test binary.
	run("-run=^TestSub$", ".", "./sub")
	requireLines(zipFile(t, archive, "worker.log"), "the worker's log", "CLI requests ahead started [trigger:linking")

	// -o links even cached results: the worker waits for a test process.
	_, stderr = run("-o", filepath.Join(t.TempDir(), "sub.test"), "-run=^TestSub$", "./sub")
	requireLines(stderr, "ddto's log", `requests_ahead=at_first_test reason="-o links test binaries even for cached results"`)
}

// zipFile returns the contents of the file name in archive.
func zipFile(t *testing.T, archive, name string) string {
	t.Helper()
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	file, err := reader.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// zipFiles returns the names of the files in the zip archive.
func zipFiles(t *testing.T, archive string) map[string]bool {
	t.Helper()
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	return names
}
