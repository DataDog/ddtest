package integration

import (
	"bytes"
	"compress/gzip"
	"context"
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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const orchestrionVersion = "v1.13.2-0.20260917114356-5c24783fcd76"

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
		events, ok := obj["events"].([]any)
		if !ok {
			c.fail(fmt.Errorf("missing events: %v", obj))
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, event := range events {
			obj, ok := event.(map[string]any)
			if !ok {
				c.failures = append(c.failures, "invalid event")
				continue
			}
			c.events = append(c.events, obj)
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
	env = append(env, "GOFLAGS=", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=false", "DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED=false", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_APPSEC_ENABLED=false", "DD_SERVICE=dd-ci-testing-poc", "DD_ENV=poc", "DD_TEST_SESSION_NAME=poc", "DD_GIT_REPOSITORY_URL=https://github.com/tonyredondo/dd-ci-testing-poc.git", "DD_GIT_COMMIT_SHA=1111111111111111111111111111111111111111", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false")
	return append(env, extra...)
}
func command(t *testing.T, dir string, env []string, name string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errout bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errout
	err := cmd.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("command timed out: %s %v", name, args)
	}
	return out.String(), errout.String(), code
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
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "fixture")
	copyTree(t, filepath.Join(root, "testdata/fixture"), dir)
	// Go resolves its working directory without the parent's PWD. On macOS,
	// /var/folders aliases /private/var/folders; overlay keys must use Go's path.
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if baseline {
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
		if err = json.Unmarshal([]byte(out), &module); err != nil {
			t.Fatal(err)
		}
		if module.Version != "v2.11.0-rc.1" {
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
	}
	bin := filepath.Join(t.TempDir(), executableName("ddtest"))
	out, e, code := command(t, root, testEnv(), "go", "build", "-o", bin, "./cmd/ddtest")
	if code != 0 {
		t.Fatalf("build driver: %s\n%s", out, e)
	}
	return dir, bin
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
	retryPath := filepath.Join(t.TempDir(), "retry-counter")
	env := testEnv(fmt.Sprintf("DD_CIVISIBILITY_ENABLED=%t", enabled), "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "POC_RETRY_COUNTER="+retryPath)
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
	return execution{normalizedOutput(out), stderr, code, normalizedEvents(receiver.events)}
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
		}{"orchestrion", "go", []string{"test", "-toolexec=" + reference + " toolexec"}})
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
