package integration

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gitStateFixture prepares a Mini fixture in a two-commit Git repository with
// a remote and returns its directory, the CLI driver and a compiled binary.
func gitStateFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir, driver := prepareMiniFixture(t)
	prepareParityGit(t, dir)
	if out, stderr, code := command(t, dir, testEnv(), "git", "remote", "add", "origin", "https://example.invalid/acme/fixture.git"); code != 0 {
		t.Fatalf("remote: %s %s", out, stderr)
	}
	bin := filepath.Join(t.TempDir(), executableName("git-state.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	return dir, driver, bin
}

// runGitWorker runs ddto's Git worker in dir and returns its state directory.
// input is what the CLI would write to it before go test exits; empty input
// stops the worker after the Git record.
func runGitWorker(t *testing.T, dir, driver, input string, env ...string) string {
	t.Helper()
	state := t.TempDir()
	if input != "" {
		// As a test process that uses the state marks it.
		if err := os.WriteFile(filepath.Join(state, "used"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(driver, "ci-git-worker", state)
	cmd.Dir = dir
	cmd.Env = append(testEnv(env...), "PWD="+dir)
	if input != "" {
		// go test has exited: the worker finishes its work, then exits.
		cmd.Stdin = strings.NewReader(input + "exited\n")
	} else {
		// As ddto, which closes the input once it has read the record
		// without handing the state over.
		cmd.Stdin = endAfterRecord{state}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worker: %v %s", err, out)
	}
	return state
}

// endAfterRecord ends the worker's input once its record exists.
type endAfterRecord struct{ dir string }

func (r endAfterRecord) Read([]byte) (int, error) {
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(r.dir, "git.json")); err == nil {
			break
		}
	}
	return 0, io.EOF
}

// gitTags collects the Git and workspace tags of every payload.
func gitTags(t *testing.T, capture *miniWireCapture) map[string][]string {
	t.Helper()
	values := map[string]map[string]bool{}
	var visit func(any)
	visit = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, value := range v {
				if text, ok := value.(string); ok && (strings.HasPrefix(key, "git.") || key == "ci.workspace_path") {
					if values[key] == nil {
						values[key] = map[string]bool{}
					}
					values[key][text] = true
				}
				visit(value)
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		}
	}
	for _, payload := range capture.payloads {
		visit(payload)
	}
	tags := map[string][]string{}
	for key, set := range values {
		for value := range set {
			tags[key] = append(tags[key], value)
		}
		sort.Strings(tags[key])
	}
	if len(tags["git.commit.sha"]) != 1 || tags["git.repository_url"] == nil || tags["git.commit.message"] == nil {
		t.Fatalf("missing Git tags: %v", tags)
	}
	return tags
}

// gitCommands returns the Git commands that GIT_TRACE recorded.
func gitCommands(t *testing.T, trace string) string {
	t.Helper()
	data, err := os.ReadFile(trace)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// ddto reads a checkout's Git metadata once and hands it to each test process
// of that checkout, which then reports the same tags without running Git.
func TestMiniGitStateHandoff(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	args := []string{"-test.run=^TestPass$"}
	trace := filepath.Join(t.TempDir(), "git-trace.log")
	// The tags must come from Git, not from testEnv's overrides.
	fromGit := []string{"DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA="}
	env := func(extra ...string) []string { return append(append([]string(nil), fromGit...), extra...) }

	want := gitTags(t, runCIWireCase(t, dir, bin, args, env("GIT_TRACE="+trace), false, false))
	if commands := gitCommands(t, trace); !strings.Contains(commands, "rev-parse --show-toplevel") {
		t.Fatalf("baseline ran no Git metadata command:\n%s", commands)
	}

	state := runGitWorker(t, dir, driver, "", fromGit...)
	handedTrace := filepath.Join(t.TempDir(), "git-trace.log")
	got := gitTags(t, runCIWireCase(t, dir, bin, args, env("GIT_TRACE="+handedTrace, "DDTO_GIT_STATE="+state), false, false))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handed Git tags differ:\n got %v\nwant %v", got, want)
	}
	commands := gitCommands(t, handedTrace)
	for _, metadata := range []string{"rev-parse --show-toplevel", "ls-remote --get-url", "rev-parse --abbrev-ref HEAD", "log -1"} {
		if strings.Contains(commands, metadata) {
			t.Errorf("test process ran git %s with handed state:\n%s", metadata, commands)
		}
	}

	// A state from another checkout is ignored.
	other := t.TempDir()
	for _, gitArgs := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Other", "-c", "user.email=other@example.invalid", "commit", "--allow-empty", "-m", "Other"}} {
		if out, stderr, code := command(t, other, testEnv(), "git", gitArgs...); code != 0 {
			t.Fatalf("other repository: %s %s", out, stderr)
		}
	}
	otherState := runGitWorker(t, other, driver, "", fromGit...)
	otherTrace := filepath.Join(t.TempDir(), "git-trace.log")
	if got := gitTags(t, runCIWireCase(t, dir, bin, args, env("GIT_TRACE="+otherTrace, "DDTO_GIT_STATE="+otherState), false, false)); !reflect.DeepEqual(got, want) {
		t.Fatalf("another checkout's state changed the tags:\n got %v\nwant %v", got, want)
	}
	if commands := gitCommands(t, otherTrace); !strings.Contains(commands, "rev-parse --show-toplevel") {
		t.Fatalf("another checkout's state replaced Git:\n%s", commands)
	}
}

// A ddto test run uploads the repository once, from its Git worker, instead
// of once per test process.
func TestMiniGitStateUploadsOnce(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	addSubPackage(t, dir)
	var mu sync.Mutex
	requests := map[string]int{}
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		requests[path.Base(r.URL.Path)]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/setting") {
			fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":false,"impacted_tests_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL,
		"DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=")
	run := func(args ...string) string {
		t.Helper()
		out, stderr, code := command(t, dir, env, driver, append([]string{"test", "--runtime=mini", "-run=^Test(Pass|Sub)$"}, args...)...)
		if code != 0 {
			t.Fatalf("ddto test: %d %s %s", code, out, stderr)
		}
		return out
	}
	run(".", "./sub")
	mu.Lock()
	if requests["search_commits"] != 1 || requests["packfile"] != 1 {
		t.Fatalf("two test processes: %v", requests)
	}
	clear(requests)
	mu.Unlock()
	// With every result cached, no test process uses the state: the worker
	// may search ahead, a read-only request, but packs and uploads nothing.
	// The fixture's own tests are not cacheable; sub's are.
	run("./sub")
	mu.Lock()
	clear(requests)
	mu.Unlock()
	if out := run("./sub"); !strings.Contains(out, "(cached)") {
		t.Fatalf("second run was not cached:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["packfile"] != 0 || requests["search_commits"] > 1 {
		t.Fatalf("cached run: %v", requests)
	}
}

// addSubPackage adds package sub, with TestSub, whose results Go can cache,
// unlike the fixture's.
func addSubPackage(t *testing.T, dir string) {
	t.Helper()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	subTest := filepath.Join(sub, "sub_test.go")
	if err := os.WriteFile(subTest, []byte("package sub\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Go does not cache a result that read a file modified in the last seconds.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(subTest, old, old); err != nil {
		t.Fatal(err)
	}
}

// runImpactedCase runs bin's TestPass against settings that enable impacted
// tests and returns TestPass's test.is_modified tag.
func runImpactedCase(t *testing.T, dir, bin string, env []string) string {
	t.Helper()
	c := &miniWireCapture{}
	c.profile = "impacted"
	server := newServer(http.HandlerFunc(c.handler))
	defer server.Close()
	scratch := t.TempDir()
	env = append(testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL,
		"DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "TMPDIR="+scratch, "XDG_CACHE_HOME="+scratch), env...)
	if out, stderr, code := command(t, dir, env, bin, "-test.run=^TestPass$"); code != 0 {
		t.Fatalf("fixture: %d %s %s", code, out, stderr)
	}
	if len(c.failures) > 0 {
		t.Fatal(c.failures)
	}
	for _, event := range c.events {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] == "test" && meta["test.name"] == "TestPass" {
			modified, _ := meta["test.is_modified"].(string)
			return modified
		}
	}
	t.Fatalf("no TestPass event: %v", c.events)
	return ""
}

// ddto's Git worker discovers the base branch and computes the diff once; a
// test process that receives its state marks the same tests as modified
// without running that Git work.
func TestMiniGitStateImpactedTests(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	remote := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		args = append([]string{"-c", "user.name=Impacted", "-c", "user.email=impacted@example.invalid", "-c", "commit.gpgsign=false"}, args...)
		if out, stderr, code := command(t, dir, testEnv(), "git", args...); code != 0 {
			t.Fatalf("git %v: %s %s", args, out, stderr)
		}
	}
	git(remote, "init", "--bare", "--initial-branch=main")
	git(dir, "remote", "set-url", "origin", remote)
	git(dir, "push", "origin", "main")
	git(dir, "checkout", "-b", "feature")
	sample := filepath.Join(dir, "sample_test.go")
	body, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	marker := "func TestPass(t *testing.T) {"
	if !strings.Contains(string(body), marker) {
		t.Fatalf("TestPass not found in the fixture")
	}
	body = []byte(strings.Replace(string(body), marker, marker+"\n\t_ = \"modified on the feature branch\"", 1))
	if err := os.WriteFile(sample, body, 0600); err != nil {
		t.Fatal(err)
	}
	git(dir, "commit", "-am", "Modify TestPass")
	bin = filepath.Join(t.TempDir(), executableName("impacted.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	fromGit := []string{"DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA="}

	trace := filepath.Join(t.TempDir(), "git-trace.log")
	if modified := runImpactedCase(t, dir, bin, append(fromGit, "GIT_TRACE="+trace)); modified != "true" {
		t.Fatalf("baseline TestPass modified=%q", modified)
	}
	if commands := gitCommands(t, trace); !strings.Contains(commands, " diff ") {
		t.Fatalf("baseline computed no diff:\n%s", commands)
	}

	// The worker needs the same settings as the test process.
	c := &miniWireCapture{}
	c.profile = "impacted"
	server := newServer(http.HandlerFunc(c.handler))
	defer server.Close()
	workerEnv := append(append([]string(nil), fromGit...), "DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true",
		"DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true")
	state := runGitWorker(t, dir, driver, "proceed\n", workerEnv...)
	if _, err := os.Stat(filepath.Join(state, "impacted.json")); err != nil {
		t.Fatalf("worker wrote no impacted-tests state: %v", err)
	}
	handedTrace := filepath.Join(t.TempDir(), "git-trace.log")
	if modified := runImpactedCase(t, dir, bin, append(fromGit, "GIT_TRACE="+handedTrace, "DDTO_GIT_STATE="+state)); modified != "true" {
		t.Fatalf("handed TestPass modified=%q", modified)
	}
	commands := gitCommands(t, handedTrace)
	for _, work := range []string{" diff ", "show-ref", "ls-remote --heads", "merge-base", "pack-objects"} {
		if strings.Contains(commands, work) {
			t.Errorf("test process ran %q with handed state:\n%s", strings.TrimSpace(work), commands)
		}
	}
}

// With deferred delivery, a test process that receives the worker's state
// waits for the worker's upload before it admits tests, as it waits for its
// own upload.
func TestMiniGitStateDeferredWaitsForUpload(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	state := runGitWorker(t, dir, driver, "")
	c := &miniWireCapture{}
	server := newServer(http.HandlerFunc(c.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL,
		"DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_CIVISIBILITY_DEFERRED_DELIVERY=true", "DD_TRACE_DEBUG=true",
		"DDTO_GIT_STATE="+state, "TMPDIR="+t.TempDir())
	// The worker ends its upload after the test process has started.
	go func() {
		time.Sleep(500 * time.Millisecond)
		temp := filepath.Join(state, "upload.json.tmp")
		if err := os.WriteFile(temp, []byte(`{"bytes":7}`), 0600); err == nil {
			_ = os.Rename(temp, filepath.Join(state, "upload.json"))
		}
	}()
	var out bytes.Buffer
	cmd := exec.Command(bin, "-test.v", "-test.run=^TestPass$")
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, out.String())
	}
	text := out.String()
	uploaded, admitted := strings.Index(text, "CLI uploaded 7 bytes"), strings.Index(text, "=== RUN   TestPass")
	if uploaded < 0 || admitted < 0 || uploaded > admitted {
		t.Fatalf("upload at %d, TestPass at %d:\n%s", uploaded, admitted, text)
	}

	// The wait ends after ddto's --git-upload-timeout, which the record hands
	// to test processes, when the upload never ends.
	unfinished := runGitWorker(t, dir, driver, "")
	recordPath := filepath.Join(unfinished, "git.json")
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record["upload_wait_ms"] = 300
	if data, err = json.Marshal(record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	cmd = exec.Command(bin, "-test.v", "-test.run=^TestPass$")
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, append(env, "DDTO_GIT_STATE="+unfinished), &out, &out
	started := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, out.String())
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second || !strings.Contains(out.String(), "CLI repository upload result unavailable") {
		t.Fatalf("waited %s for an unfinished upload:\n%s", elapsed, out.String())
	}
}

// Test processes read the worker's state while tests run without entering
// Go's test log, so results stay cacheable when the state directory lies in
// the module, as with TMPDIR inside it.
func TestMiniGitStateCacheWithTemporaryDirectoryInModule(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	slow := filepath.Join(dir, "slow")
	if err := os.MkdirAll(slow, 0700); err != nil {
		t.Fatal(err)
	}
	slowTest := filepath.Join(slow, "slow_test.go")
	// The test runs while the worker's upload ends.
	if err := os.WriteFile(slowTest, []byte("package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(1500 * time.Millisecond) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(slowTest, old, old); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(dir, ".tmp")
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/setting"):
			fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":false,"impacted_tests_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`)
		case strings.HasSuffix(r.URL.Path, "/packfile"):
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusAccepted)
		default:
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer server.Close()
	// Windows reads TMP and TEMP.
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL,
		"DD_API_KEY=fixture", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=", "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp)
	run := func() string {
		t.Helper()
		out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "./slow")
		if code != 0 {
			t.Fatalf("ddto test: %d %s %s", code, out, stderr)
		}
		return out
	}
	run()
	if out := run(); !strings.Contains(out, "(cached)") {
		t.Fatalf("second run was not cached:\n%s", out)
	}
}

// uploadWaitTest ends once the pack file upload has started.
const uploadWaitTest = `package fixture_test

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestWaitForUpload(t *testing.T) {
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if response, err := http.Get(os.Getenv("POC_UPLOAD_STARTED_URL")); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload did not send its pack file")
		}
	}
}
`

// When the upload outlasts --git-upload-timeout after go test, ddto stops the
// worker: it stops its request, removes its pack files, records the stopped
// upload and closes the telemetry application, and ddto exits with go test's
// result. --keep-ci-state keeps that state for debugging.
func TestMiniGitStateStopsUploadCleanly(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	agent := &telemetryAgent{}
	sending := make(chan struct{}, 1)
	var started atomic.Bool
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/packfile") {
			_, _ = io.Copy(io.Discard, r.Body)
			started.Store(true)
			select {
			case sending <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/upload-started" {
			if !started.Load() {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		agent.handler(w, r)
	}))
	defer server.Close()
	// go test ends only once the upload sends its pack file, however long the
	// upload took to start: only then does --git-upload-timeout begin.
	writeBuildFixture(t, dir, map[string]string{"upload_wait_test.go": uploadWaitTest})
	temp := t.TempDir()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL,
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=",
		"TMPDIR="+temp, "TMP="+temp, "TEMP="+temp, "POC_UPLOAD_STARTED_URL="+server.URL+"/upload-started")
	archive := filepath.Join(t.TempDir(), "ci-state.zip")
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "--git-upload-timeout=1s", "--keep-ci-state="+archive, "-run=^(TestPass|TestWaitForUpload)$", ".")
	if code != 0 {
		t.Fatalf("ddto test: %d %s %s", code, out, stderr)
	}
	select {
	case <-sending:
	default:
		t.Fatal("the upload never sent its pack file")
	}
	if !strings.Contains(stderr, "archived the CI state in "+archive) {
		t.Fatalf("stderr %q", stderr)
	}
	files := map[string][]byte{}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		content, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name], _ = io.ReadAll(content)
		_ = content.Close()
	}
	_ = reader.Close()
	var summary struct {
		Worker     string `json:"worker"`
		StopReason string `json:"stop_reason"`
		Waited     string `json:"waited_after_go_test"`
	}
	if err := json.Unmarshal(files["ddto.json"], &summary); err != nil || summary.Worker != "stopped" || summary.StopReason != "timeout" {
		t.Fatalf("ddto.json %s, %v", files["ddto.json"], err)
	}
	if waited, err := time.ParseDuration(summary.Waited); err != nil || waited > 30*time.Second {
		t.Fatalf("ddto waited %s for a stopped upload: %v", summary.Waited, err)
	}
	var upload struct{ Error string }
	if err := json.Unmarshal(files["upload.json"], &upload); err != nil || upload.Error != "ddto stopped the repository upload: timeout" {
		t.Fatalf("upload.json %s, %v", files["upload.json"], err)
	}
	for _, name := range []string{"git.json", "telemetry.json", "used", "worker.log"} {
		if _, ok := files[name]; !ok {
			t.Errorf("the kept state lacks %s", name)
		}
	}
	for _, place := range []string{temp, filepath.Join(dir, ".git")} {
		entries, err := os.ReadDir(place)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".dd-pack-objects") {
				t.Errorf("the stopped upload left %s in %s", entry.Name(), place)
			}
		}
	}
	s := summarizeTelemetry(t, agent.take())
	if len(s.closings) != 1 || s.logs["civisibility: CLI repository upload stopped|reason:timeout"] == 0 {
		t.Fatalf("telemetry closings %v, logs %v", s.closings, s.logs)
	}

	// Without an archive, ddto keeps the state where it is and says where.
	_, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL,
		"DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=", "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp), driver, "test", "--runtime=mini", "--keep-ci-state", "-run=^TestPass$", ".")
	_, kept, found := strings.Cut(stderr, "kept the CI state in ")
	kept = strings.TrimSpace(strings.SplitN(kept, "\n", 2)[0])
	if code != 0 || !found {
		t.Fatalf("ddto test --keep-ci-state: %d %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(kept, "git.json")); err != nil {
		t.Fatalf("kept state %s: %v", kept, err)
	}
}
