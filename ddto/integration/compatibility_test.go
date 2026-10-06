package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

const sdkVersion = runner.SDKVersion

const orchestrionVersion = "v1.13.2-0.20260917114356-5c24783fcd76"

// orchestrionPinChecked is what orchestrion go sets for its toolexec children
// after checking that go.mod requires this Orchestrion version.
const orchestrionPinChecked = "DD_ORCHESTRION_IS_GOMOD_VERSION"

// Reference builds run orchestrion go test, as users do: orchestrion go serves
// every toolexec call from one in-process job server. With plain go test
// -toolexec="orchestrion toolexec", the first call starts a daemon whose logs
// stay open in Go's WORK directory; on Windows go then cannot remove WORK and
// exits 1 after a successful build.

type capture struct {
	mu       sync.Mutex
	events   []map[string]any
	failures []string
	retry    bool
	profile  string
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/citestcycle") {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, e := gzip.NewReader(r.Body)
			if e != nil {
				c.fail(e)
				w.WriteHeader(400)
				return
			}
			defer gz.Close()
			reader = gz
		}
		raw, e := io.ReadAll(io.LimitReader(reader, 16<<20))
		if e != nil {
			c.fail(e)
			return
		}
		payload, e := decodeMsgpack(raw)
		if e != nil {
			c.fail(e)
			w.WriteHeader(400)
			return
		}
		obj, ok := payload.(map[string]any)
		if !ok {
			c.fail(fmt.Errorf("non-map payload"))
			return
		}
		events, err := ciMetadataEvents(obj)
		if err != nil {
			c.fail(err)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, event := range events {
			c.events = append(c.events, event)
		}
		w.WriteHeader(202)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if c.profile != "" && c.featureResponse(w, r) {
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/setting"):
		fmt.Fprintf(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{"itr_enabled":false,"tests_skipping":false,"require_git":false,"code_coverage":false,"known_tests_enabled":false,"impacted_tests_enabled":false,"flaky_test_retries_enabled":%t,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`, c.retry)
	case r.URL.Path == "/info":
		fmt.Fprint(w, `{"endpoints":["evp_proxy/v2"]}`)
	default:
		fmt.Fprint(w, `{"data":[]}`)
	}
}
func (c *capture) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = append(c.failures, err.Error())
}

func testEnv(extra ...string) []string {
	// Deliberately avoid inheriting API keys or CI configuration into fixtures.
	names := []string{"PATH", "HOME", "USER", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR", "GOTOOLCHAIN", "CGO_ENABLED", "GOOS", "GOARCH", "GOMAXPROCS", "GOPROXY", "GOSUMDB", "SYSTEMROOT", "SystemRoot", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TEMP", "TMP", "COMSPEC", "PATHEXT"}
	var env []string
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	// configureReferenceFixture checks the Orchestrion pin once. Otherwise every
	// toolexec call runs go list to check it again, and on any failure rewrites
	// orchestrion.tool.go and runs go mod tidy.
	env = append(env, orchestrionPinChecked+"=true")
	env = append(env, "GOFLAGS=", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=false", "DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED=false", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_APPSEC_ENABLED=false", "DD_SERVICE=dd-ci-testing-poc", "DD_ENV=poc", "DD_TEST_SESSION_NAME=poc", "DD_GIT_REPOSITORY_URL=https://github.com/tonyredondo/dd-ci-testing-poc.git", "DD_GIT_COMMIT_SHA=1111111111111111111111111111111111111111", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false")
	return append(env, extra...)
}
func command(t *testing.T, dir string, env []string, name string, args ...string) (string, string, int) {
	t.Helper()
	out, stderr, code, _ := commandWithTiming(t, dir, env, name, args...)
	return out, stderr, code
}

// Measure only the child process. Fixture setup and result comparisons stay outside.
func commandWithTiming(t *testing.T, dir string, env []string, name string, args ...string) (string, string, int, time.Duration) {
	t.Helper()
	out, stderr, code, wall, err := runCommand(dir, env, name, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out, stderr, code, wall
}

// runCommand reports start failures and timeouts as errors instead of through
// testing.T, so independent builds can run in other goroutines.
func runCommand(dir string, env []string, name string, args ...string) (string, string, int, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errout bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errout
	start := time.Now()
	err := cmd.Run()
	wall := time.Since(start)
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			return "", "", 0, wall, fmt.Errorf("%s: %v", name, err)
		}
	}
	if ctx.Err() != nil {
		return "", "", 0, wall, fmt.Errorf("command timed out: %s %v", name, args)
	}
	return out.String(), errout.String(), code, wall, nil
}

// fixtureBuild is one independent compilation of a fixture binary.
type fixtureBuild struct {
	name, dir, tool string
	env, args       []string
}

// buildConcurrently runs independent fixture compilations together. Each one
// writes its own output, and Go's build and module caches are safe for
// concurrent go commands. Builds with -mod=mod run one at a time instead: go
// may rewrite go.mod while another build reads it, and every Orchestrion
// toolexec call re-checks its pin with go list, then rewrites
// orchestrion.tool.go and runs go mod tidy when that check fails. Failures are
// reported from the test goroutine.
func buildConcurrently(t *testing.T, builds ...fixtureBuild) {
	t.Helper()
	type result struct {
		out, stderr string
		code        int
		err         error
	}
	results := make([]result, len(builds))
	run := func(i int) {
		r, build := &results[i], builds[i]
		r.out, r.stderr, r.code, _, r.err = runCommand(build.dir, build.env, build.tool, build.args...)
	}
	check := func(i int) {
		r := results[i]
		if r.err != nil {
			t.Fatalf("%s: %v", builds[i].name, r.err)
		}
		if r.code != 0 {
			t.Fatalf("%s: %s\n%s", builds[i].name, r.out, r.stderr)
		}
	}
	if slices.ContainsFunc(builds, func(b fixtureBuild) bool { return slices.Contains(b.args, "-mod=mod") }) {
		for i := range builds {
			run(i)
			check(i)
		}
		return
	}
	var wg sync.WaitGroup
	for i := range builds {
		wg.Go(func() { run(i) })
	}
	wg.Wait()
	for i := range builds {
		check(i)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(from, path)
		if e != nil {
			return e
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func prepareFixture(t *testing.T, baseline bool) (string, string) {
	t.Helper()
	return prepareFixtureWithTempDir(t, baseline, t.TempDir)
}

// The directory owner must keep the source and driver alive until every child
// process finishes. Most fixtures are test-owned; shared parity builds live
// until TestMain finishes.
func prepareFixtureWithTempDir(t *testing.T, baseline bool, tempDir func() string) (string, string) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tempDir(), "fixture")
	copyTree(t, filepath.Join(root, "testdata/fixture"), dir)
	// Go resolves its working directory without the parent's PWD. On macOS,
	// /var/folders aliases /private/var/folders; overlay keys must use Go's path.
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if baseline {
		configureReferenceFixture(t, dir)
	}
	return dir, sharedDriver(t, root)
}

func normalizedOutput(s string) string {
	s = regexp.MustCompile(`\([0-9]+\.[0-9]+s\)`).ReplaceAllString(s, "(TIME)")
	s = regexp.MustCompile(`\t[0-9]+\.[0-9]+s`).ReplaceAllString(s, "\tTIME")
	// Parallel child completion order is unspecified by native Go. Retain every
	// line and its multiplicity while comparing only that scheduling-independent part.
	var stable, parallel []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "=== CONT  TestParallel/") || strings.HasPrefix(trimmed, "--- PASS: TestParallel/") {
			parallel = append(parallel, line)
		} else {
			stable = append(stable, line)
		}
	}
	sort.Strings(parallel)
	return strings.Join(stable, "\n") + "\n" + strings.Join(parallel, "\n")
}
func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
func normalizedEvents(events []map[string]any) []string {
	var out []string
	for _, event := range events {
		content, ok := event["content"].(map[string]any)
		if !ok {
			continue
		}
		meta, _ := content["meta"].(map[string]any)
		selected := map[string]any{"type": event["type"], "name": content["name"], "resource": content["resource"], "error": content["error"]}
		// Keep semantic test attributes, including source, errors, skips, and retry
		// decisions. Exclude invocation text because the executables have different names.
		for key, value := range meta {
			if strings.HasPrefix(key, "test.") && key != "test.command" && key != "test.execution.order" {
				selected[key] = value
			}
			if key == "error.type" || key == "error.message" || key == "error.stack" {
				selected[key] = value
			}
		}
		metrics, _ := content["metrics"].(map[string]any)
		for _, key := range []string{"test.source.start", "test.source.end"} {
			if value, ok := metrics[key]; ok {
				selected[key] = value
			}
		}
		encoded, _ := json.Marshal(selected)
		out = append(out, string(encoded))
	}
	sort.Strings(out)
	return out
}

type execution struct {
	out, stderr string
	code        int
	events      []string
	wireEvents  []map[string]any
	wall        time.Duration
}

func execute(t *testing.T, dir, bin string, args []string, enabled, retry bool) execution {
	t.Helper()
	return executeProfile(t, dir, bin, args, enabled, retry, "")
}
func executeProfile(t *testing.T, dir, bin string, args []string, enabled, retry bool, profile string) execution {
	t.Helper()
	receiver := &capture{retry: retry, profile: profile}
	server := httptest.NewServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	executionDir := t.TempDir()
	retryPath := filepath.Join(executionDir, "retry-counter")
	env := testEnv(fmt.Sprintf("DD_CIVISIBILITY_ENABLED=%t", enabled), "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "POC_RETRY_COUNTER="+retryPath, "TMPDIR="+executionDir, "TMP="+executionDir, "TEMP="+executionDir, "XDG_CACHE_HOME="+executionDir, fmt.Sprintf("DD_GIT_COMMIT_SHA=%x", sha1.Sum([]byte(dir+"|"+profile))))
	if retry {
		env = append(env, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process")
	}
	if profile != "" {
		env = append(env, "DD_TEST_MANAGEMENT_ENABLED=true", "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED="+fmt.Sprint(profile == "efd"), "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process")
	}
	out, stderr, code := command(t, dir, env, bin, args...)
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.failures) > 0 {
		t.Fatalf("wire protocol: %v", receiver.failures)
	}
	return execution{out: normalizedOutput(out), stderr: stderr, code: code, events: normalizedEvents(receiver.events), wireEvents: receiver.events}
}

func TestTestingCompatibility(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference != "" {
		var err error
		reference, err = filepath.Abs(reference)
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, driver := prepareFixture(t, reference != "")
	variants := []struct {
		name, compiler string
		prefix         []string
	}{{"native", "go", []string{"test"}}, {"overlay", driver, []string{"test"}}}
	if reference != "" {
		variants = append(variants, struct {
			name, compiler string
			prefix         []string
		}{"orchestrion", reference, []string{"go", "test"}})
	}
	var bins []string
	for _, variant := range variants {
		bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
		args := append(append([]string{}, variant.prefix...), "-c", "-o", bin, ".")
		out, e, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), variant.compiler, args...)
		if code != 0 {
			t.Fatalf("compile %s: %s\n%s", variant.name, out, e)
		}
		bins = append(bins, bin)
	}
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"pass", []string{"-test.v", "-test.run=^Test(Pass|Skip|Skipf|SkipNow|Cleanup|Context|Parallel|Nested)$"}, 0},
		{"count-shuffle", []string{"-test.v", "-test.count=2", "-test.shuffle=42", "-test.run=^Test(Pass|Cleanup|Parallel|Context|Nested)$"}, 0},
		{"examples-fuzz-seeds", []string{"-test.v", "-test.run=^(ExampleAdd|FuzzAdd)$"}, 0},
		{"list", []string{"-test.list=TestPass"}, 0},
		{"error", []string{"-test.v", "-test.run=^TestFailures$/^Error$", "-mode=fail"}, 1},
		{"errorf", []string{"-test.v", "-test.run=^TestFailures$/^Errorf$", "-mode=fail"}, 1},
		{"fatal", []string{"-test.v", "-test.run=^TestFailures$/^Fatal$", "-mode=fail"}, 1},
		{"fatalf", []string{"-test.v", "-test.run=^TestFailures$/^Fatalf$", "-mode=fail"}, 1},
		{"fail", []string{"-test.v", "-test.run=^TestFailures$/^Fail$", "-mode=fail"}, 1},
		{"failnow", []string{"-test.v", "-test.run=^TestFailures$/^FailNow$", "-mode=fail"}, 1},
		{"helper", []string{"-test.v", "-test.run=^TestHelper$", "-mode=fail"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			native := execute(t, dir, bins[0], tc.args, false, false)
			if native.code != tc.code {
				t.Fatalf("native exit: %d, %s", native.code, native.out)
			}
			for i := 1; i < len(bins); i++ {
				got := execute(t, dir, bins[i], tc.args, false, false)
				if got.code != native.code || got.out != native.out {
					t.Fatalf("disabled %s differs: exit %d/%d\n%s\nEXPECTED\n%s\nstderr:%s", variants[i].name, got.code, native.code, got.out, native.out, got.stderr)
				}
			}
			overlay := execute(t, dir, bins[1], tc.args, true, false)
			if overlay.code != native.code {
				t.Fatalf("enabled overlay exit %d, expected %d: %s\n%s", overlay.code, native.code, overlay.out, overlay.stderr)
			}
			if tc.name != "list" && tc.name != "examples-fuzz-seeds" && len(overlay.events) == 0 {
				t.Fatalf("no CI events: %s", overlay.stderr)
			}
			if reference != "" {
				original := execute(t, dir, bins[2], tc.args, true, false)
				if original.code != overlay.code || !reflect.DeepEqual(original.events, overlay.events) {
					t.Fatalf("SDK event mismatch\nORCHESTRION %d %v\nOVERLAY %d %v\nORCH stderr %s\nOVERLAY stderr %s", original.code, original.events, overlay.code, overlay.events, original.stderr, overlay.stderr)
				}
			}
		})
	}
	t.Run("process-retry", func(t *testing.T) {
		args := []string{"-test.run=^TestRetry$", "-mode=retry"}
		got := execute(t, dir, bins[1], args, true, true)
		if got.code != 0 || len(got.events) == 0 {
			t.Fatalf("retry failed: %d %s\n%s", got.code, got.out, got.stderr)
		}
		if reference != "" {
			want := execute(t, dir, bins[2], args, true, true)
			if got.code != want.code || !reflect.DeepEqual(got.events, want.events) {
				t.Fatalf("retry mismatch\n%v\n%v\n%s\n%s", got.events, want.events, got.stderr, want.stderr)
			}
		}
	})
	t.Run("panic-goexit-timeout", func(t *testing.T) {
		for _, mode := range []string{"panic", "goexit", "timeout"} {
			args := []string{"-test.run=^Test" + strings.ToUpper(mode[:1]) + mode[1:] + "$", "-mode=" + mode}
			if mode == "goexit" {
				args[0] = "-test.run=^TestGoexit$"
			}
			if mode == "timeout" {
				args = append(args, "-test.timeout=50ms")
			}
			native := execute(t, dir, bins[0], args, false, false)
			marker := map[string]string{"panic": "fixture panic", "goexit": "runtime.Goexit", "timeout": "test timed out"}[mode]
			if native.code == 0 || !strings.Contains(native.stderr, marker) {
				t.Fatalf("native %s failure not exercised: %d %s", mode, native.code, native.stderr)
			}
			for _, enabled := range []bool{false, true} {
				got := execute(t, dir, bins[1], args, enabled, false)
				if got.code != native.code || !strings.Contains(got.stderr, marker) {
					t.Fatalf("%s enabled=%t changed abnormal exit: %d/%d %s", mode, enabled, got.code, native.code, got.stderr)
				}
				if reference != "" {
					want := execute(t, dir, bins[2], args, enabled, false)
					if got.code != want.code || !reflect.DeepEqual(got.events, want.events) {
						t.Fatalf("%s abnormal event mismatch: %v / %v", mode, got.events, want.events)
					}
				}
			}
		}
	})
}

func configureReferenceFixture(t *testing.T, dir string) {
	t.Helper()
	// Both compilers use this SAME temporary module graph, including any MVS
	// upgrades introduced by the reference tool. The SDK version stays fixed.
	if out, e, code := command(t, dir, testEnv(), "go", "get", "github.com/DataDog/orchestrion@"+orchestrionVersion); code != 0 {
		t.Fatalf("prepare common graph: %s\n%s", out, e)
	}
	out, e, code := command(t, dir, testEnv(), "go", "list", "-m", "-json", "github.com/DataDog/dd-trace-go/v2")
	if code != 0 {
		t.Fatalf("SDK: %s", e)
	}
	var module struct{ Dir, Version string }
	if err := json.Unmarshal([]byte(out), &module); err != nil {
		t.Fatal(err)
	}
	if module.Version != sdkVersion {
		t.Fatalf("baseline changed SDK: %s", module.Version)
	}
	yaml, err := os.ReadFile(filepath.Join(module.Dir, "internal/civisibility/integrations/gotesting/orchestrion.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "orchestrion.yml"), yaml, 0644); err != nil {
		t.Fatal(err)
	}
	tool := "//go:build tools\n\npackage fixture\nimport _ \"github.com/DataDog/orchestrion\"\n"
	if err = os.WriteFile(filepath.Join(dir, "orchestrion.tool.go"), []byte(tool), 0644); err != nil {
		t.Fatal(err)
	}
	// Check the pin once, as orchestrion go does before a build; testEnv then
	// skips that check in every toolexec call. A different binary fails here.
	if out, e, code := command(t, dir, testEnv(orchestrionPinChecked+"=false"), os.Getenv("ORCHESTRION_BIN"), "go", "version"); code != 0 || strings.Contains(out+e, "is not present in your go.mod") {
		t.Fatalf("orchestrion pin: %s\n%s", out, e)
	}
}
