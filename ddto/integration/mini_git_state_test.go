package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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
// input is what the CLI would write to it; empty input stops the worker after
// the Git record.
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
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worker: %v %s", err, out)
	}
	return state
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
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// runImpactedCase runs bin's TestPass against settings that enable impacted
// tests and returns TestPass's test.is_modified tag.
func runImpactedCase(t *testing.T, dir, bin string, env []string) string {
	t.Helper()
	c := &miniWireCapture{}
	c.profile = "impacted"
	server := httptest.NewServer(http.HandlerFunc(c.handler))
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
	server := httptest.NewServer(http.HandlerFunc(c.handler))
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
