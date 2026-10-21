package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// A crash names a test only through a frame of a top-level test function,
// or of a function literal in one, in the crashed goroutine, or else through
// the test function that created that goroutine.
func TestCrashedTestNamesOnlyUnambiguousTests(t *testing.T) {
	for _, tc := range []struct {
		name, crash, module, suite, test string
	}{
		{"closure", "panic: boom\n\ngoroutine 28 [running]:\nexample.com/a.TestRacy.func3()\n\t/src/a/race_test.go:59 +0x7c\ncreated by example.com/a.TestRacy in goroutine 27\n\t/src/a/race_test.go:59 +0x1da\n", "example.com/a", "race_test.go", "TestRacy"},
		{"helper then test", "panic: boom\n\ngoroutine 7 [running]:\nexample.com/a.helper(...)\n\t/src/a/helper_test.go:3\nexample.com/a.TestUses(0xc000123)\n\t/src/a/uses_test.go:9 +0x1\ntesting.tRunner(0xc0, 0x1)\n\t/go/src/testing/testing.go:2193 +0x21c\n", "example.com/a", "uses_test.go", "TestUses"},
		{"created by", "\ngoroutine 61 [running]:\ninternal/runtime/maps.fatal({0xc38474?, 0x0?})\n\t/go/src/runtime/panic.go:1195 +0x18\nexample.com/a.worker()\n\t/src/a/worker.go:5 +0xa9\ncreated by example.com/a.TestMaps in goroutine 56\n\t/src/a/maps_test.go:47 +0x4ee\n", "example.com/a", "maps_test.go", "TestMaps"},
		{"external package trimpath", "panic: x\n\ngoroutine 3 [running]:\nexample.com/a_test.TestExternal(0x1)\n\texample.com/a/ext_test.go:4 +0x1\n", "example.com/a_test", "ext_test.go", "TestExternal"},
		{"benchmark", "panic: x\n\ngoroutine 3 [running]:\nexample.com/a.BenchmarkLoop(0x1)\n\t/src/a/b_test.go:4 +0x1\n", "example.com/a", "b_test.go", "BenchmarkLoop"},
		{"testify method", "panic: x\n\ngoroutine 3 [running]:\nexample.com/a.(*Suite).TestMethod(0x1)\n\t/src/a/suite_test.go:4 +0x1\n", "", "", ""},
		{"TestMain", "panic: x\n\ngoroutine 1 [running]:\nexample.com/a.TestMain(0x1)\n\t/src/a/main_test.go:4 +0x1\n", "", "", ""},
		{"lowercase suffix", "panic: x\n\ngoroutine 1 [running]:\nexample.com/a.Testify(0x1)\n\t/src/a/x_test.go:4 +0x1\n", "", "", ""},
		{"not a test file", "panic: x\n\ngoroutine 1 [running]:\nexample.com/a.TestLike(0x1)\n\t/src/a/like.go:4 +0x1\n", "", "", ""},
		{"no goroutine", "fatal error: out of memory\n", "", "", ""},
		{"later goroutine", "panic: x\n\ngoroutine 1 [running]:\nmain.main()\n\t/src/main.go:1 +0x1\n\ngoroutine 2 [chan receive]:\nexample.com/a.TestLater(0x1)\n\t/src/a/l_test.go:4 +0x1\n", "", "", ""},
	} {
		module, suite, test, ok := crashedTest(tc.crash)
		if ok != (tc.test != "") || module != tc.module || suite != tc.suite || test != tc.test {
			t.Errorf("%s: crashedTest = %q %q %q %t, want %q %q %q", tc.name, module, suite, test, ok, tc.module, tc.suite, tc.test)
		}
	}
}

// A panic's crash output starts with its value; a fatal error's leaves its
// message out, so the session names its first frame outside the runtime.
func TestCrashMessage(t *testing.T) {
	if got := crashMessage("panic: boom [recovered]\n\ngoroutine 1 [running]:\n"); got != "panic: boom [recovered]" {
		t.Errorf("panic: %q", got)
	}
	fatal := "goroutine 61 [running]:\ninternal/runtime/maps.fatal({0xc38474?, 0x0?})\n\t/go/src/runtime/panic.go:1195 +0x18\nexample.com/a.TestMaps.func2()\n\t/src/a/maps_test.go:50 +0xa9\n"
	if got := crashMessage(fatal); !strings.HasPrefix(got, "fatal error in example.com/a.TestMaps.func2 at maps_test.go:50;") {
		t.Errorf("fatal error: %q", got)
	}
	if got := crashMessage("goroutine 1 [running]:\nruntime.throw()\n\t/go/src/runtime/panic.go:1 +0x1\n"); !strings.HasPrefix(got, "fatal error;") {
		t.Errorf("runtime frames only: %q", got)
	}
}

type panicStringer struct{ calls *int }

func (p panicStringer) String() string { *p.calls++; return "called" }

type namedInt int

// The panic hook renders the value that preprintpanics left without calling
// any method of it.
func TestPanicValueTextCallsNoMethod(t *testing.T) {
	calls := 0
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, "nil"},
		{"boom", "boom"},
		{42, "42"},
		{namedInt(7), "integrations.namedInt(7)"},
		{3.5, "3.5"},
		{true, "true"},
		{panicStringer{&calls}, "(integrations.panicStringer)"},
		{&calls, "(*int)"},
		{errors.New("e"), "(*errors.errorString)"},
	} {
		if got := panicValueText(tc.value); got != tc.want {
			t.Errorf("panicValueText(%T) = %q, want %q", tc.value, got, tc.want)
		}
	}
	if calls != 0 {
		t.Fatalf("panicValueText called String %d times", calls)
	}
}

// Counts add up, true and EFD's faulty win, and a single process's coverage
// is the session's unless the coverage profile gives the command's.
func TestMergeCLISessionTags(t *testing.T) {
	closed := func(tags map[string]any) *cliProcess {
		return &cliProcess{closed: &utils.CLISessionRecordLine{Kind: "closed", Tags: tags}}
	}
	processes := []*cliProcess{
		closed(map[string]any{constants.ITRTestsSkippingCount: 2.0, constants.ITRTestsSkipped: "false", constants.TestEarlyFlakeDetectionRetryAborted: "slow", constants.CodeCoveragePercentageOfTotalLines: 50.0}),
		{lines: []utils.CLISessionRecordLine{{Kind: "module"}}},
		closed(map[string]any{constants.ITRTestsSkippingCount: 3.0, constants.ITRTestsSkipped: "true", constants.TestEarlyFlakeDetectionRetryAborted: "faulty", constants.CodeCoveragePercentageOfTotalLines: 70.0}),
	}
	merged := mergeCLISessionTags(processes, nil)
	if merged[constants.ITRTestsSkippingCount] != 5.0 || merged[constants.ITRTestsSkipped] != "true" || merged[constants.TestEarlyFlakeDetectionRetryAborted] != "faulty" {
		t.Errorf("merged %v", merged)
	}
	if _, ok := merged[constants.CodeCoveragePercentageOfTotalLines]; ok {
		t.Errorf("two processes' coverage became the session's: %v", merged)
	}
	if merged := mergeCLISessionTags(processes[:1], nil); merged[constants.CodeCoveragePercentageOfTotalLines] != 50.0 {
		t.Errorf("a single process's coverage: %v", merged)
	}
	// Another process joined, and crashed before its coverage.
	if merged := mergeCLISessionTags(processes[:2], nil); merged[constants.CodeCoveragePercentageOfTotalLines] != nil {
		t.Errorf("one of two processes' coverage became the session's: %v", merged)
	}
	total := 61.5
	if merged := mergeCLISessionTags(processes, &total); merged[constants.CodeCoveragePercentageOfTotalLines] != 61.5 {
		t.Errorf("the profile's coverage: %v", merged)
	}
}

// ddto's session carries the closed processes' tags and, for a process that
// crashed before it closed, the modules and suites that it left open, with
// their IDs, and the test that its crash output names; not the suite that
// had ended.
func TestFinishCLISessionRecreatesOnlyOpenContainers(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	var mu sync.Mutex
	var events []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/citestcycle") {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			events = append(events, decodeTestCycle(t, body)...)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "false")
	t.Setenv("DD_TRACE_AGENT_URL", server.URL)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_SERVICE", "svc")

	dir := t.TempDir()
	start := time.Now().Add(-time.Minute)
	writeRecord(t, filepath.Join(dir, "process-a.jsonl"),
		utils.CLISessionRecordLine{Kind: "module", ID: 11, Name: "example.com/a", Service: "svc", Start: start},
		utils.CLISessionRecordLine{Kind: "suite", ID: 21, Module: 11, Name: "done_test.go", Service: "svc", Start: start},
		utils.CLISessionRecordLine{Kind: "suite_end", ID: 21},
		utils.CLISessionRecordLine{Kind: "suite", ID: 22, Module: 11, Name: "race_test.go", Service: "svc", Start: start},
	)
	crash := "panic: boom\n\ngoroutine 9 [running]:\nexample.com/a.TestRacy.func1()\n\t/src/a/race_test.go:5 +0x1\n"
	if err := os.WriteFile(filepath.Join(dir, "process-a.crash"), []byte(crash), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, filepath.Join(dir, "process-b.jsonl"),
		utils.CLISessionRecordLine{Kind: "module", ID: 12, Name: "example.com/b", Start: start},
		utils.CLISessionRecordLine{Kind: "module_end", ID: 12},
		utils.CLISessionRecordLine{Kind: "closed", ExitCode: 0, Tags: map[string]any{constants.ITRTestsSkippingCount: 4, constants.TestManagementEnabled: "true"}},
	)
	if err := os.WriteFile(filepath.Join(dir, "process-b.crash"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Another killed process had a module of the same name, as for subtests
	// of a shared helper package.
	writeRecord(t, filepath.Join(dir, "process-c.jsonl"),
		utils.CLISessionRecordLine{Kind: "module", ID: 13, Name: "example.com/a", Service: "svc", Start: start},
		utils.CLISessionRecordLine{Kind: "suite", ID: 23, Module: 13, Name: "race_test.go", Service: "svc", Start: start},
	)
	summary, err := FinishCLISession(CLISessionEnd{Dir: dir, Session: utils.CLISession{ID: 99, Command: "ddto test ./...", WorkingDirectory: dir}, Start: start, Finish: time.Now(), ExitCode: 2, GoVersion: runtime.Version()})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Sent || summary.Processes != 3 || summary.Closed != 1 || summary.Crashed != 1 || summary.Recreated != 5 {
		t.Fatalf("summary %+v", summary)
	}
	mu.Lock()
	defer mu.Unlock()
	var got []string
	for _, event := range events {
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		metrics, _ := content["metrics"].(map[string]any)
		id := func(key string) any { return content[key] }
		switch event["type"] {
		case "test_session_end":
			got = append(got, fmt.Sprintf("session %v %v %v %v %v", id("test_session_id"), meta["test.status"], meta["error.message"], metrics[constants.ITRTestsSkippingCount], meta[constants.TestManagementEnabled]))
		case "test_module_end":
			got = append(got, fmt.Sprintf("module %v %v %v %v", id("test_session_id"), id("test_module_id"), meta["test.module"], meta["test.status"]))
		case "test_suite_end":
			got = append(got, fmt.Sprintf("suite %v %v %v %v", id("test_module_id"), id("test_suite_id"), meta["test.suite"], meta["test.status"]))
		case "test":
			got = append(got, fmt.Sprintf("test %v %v %v %v", id("test_suite_id"), meta["test.name"], meta["test.status"], meta["error.message"]))
		}
	}
	want := map[string]bool{
		"session 99 fail panic: boom (and 1 more test processes) 4 true": true,
		"module 99 11 example.com/a fail":                                true,
		"suite 11 22 race_test.go fail":                                  true,
		"test 22 TestRacy fail panic: boom":                              true,
		"module 99 13 example.com/a fail":                                true,
		"suite 13 23 race_test.go fail":                                  true,
	}
	if len(got) != len(want) {
		t.Fatalf("events %v", got)
	}
	for _, line := range got {
		if !want[line] {
			t.Errorf("unexpected event %q among %v", line, got)
		}
	}
}

// Without a test process and with go test's success, as when every result
// came from Go's cache, ddto sends no session.
func TestFinishCLISessionSkipsAnEmptySuccess(t *testing.T) {
	summary, err := FinishCLISession(CLISessionEnd{Dir: t.TempDir(), Session: utils.CLISession{ID: 1}, ExitCode: 0})
	if err != nil || summary.Sent {
		t.Fatalf("summary %+v, %v", summary, err)
	}
}

// ddto's session is skipped as empty, as dd-trace-go #5697 reports a test
// session that ran no tests, only when go test succeeded and every test
// process that joined it ran nothing.
func TestFinishCLISessionReportsAnEmptySession(t *testing.T) {
	empty := map[string]any{constants.TestSessionEmptyReason: constants.TestSessionEmptyReasonZeroTests}
	for _, tc := range []struct {
		name       string
		exitCode   int
		tags       []map[string]any
		wantStatus string
		wantEmpty  bool
	}{
		{name: "every process empty", tags: []map[string]any{empty, empty}, wantStatus: constants.TestStatusSkip, wantEmpty: true},
		{name: "one process ran tests", tags: []map[string]any{empty, {}}, wantStatus: constants.TestStatusPass},
		{name: "go test failed", exitCode: 1, tags: []map[string]any{empty, empty}, wantStatus: constants.TestStatusFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetCIVisibilityBootstrapStateForTesting()
			t.Cleanup(restoreCIVisibilityBootstrapForTesting)
			var mu sync.Mutex
			var events []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/citestcycle") {
					body, _ := io.ReadAll(r.Body)
					mu.Lock()
					events = append(events, decodeTestCycle(t, body)...)
					mu.Unlock()
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			t.Cleanup(server.Close)
			t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "false")
			t.Setenv("DD_TRACE_AGENT_URL", server.URL)
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
			t.Setenv("DD_SERVICE", "svc")

			dir := t.TempDir()
			for i, tags := range tc.tags {
				writeRecord(t, filepath.Join(dir, fmt.Sprintf("process-%d.jsonl", i)), utils.CLISessionRecordLine{Kind: "closed", Tags: tags})
			}
			start := time.Now().Add(-time.Minute)
			summary, err := FinishCLISession(CLISessionEnd{Dir: dir, Session: utils.CLISession{ID: 7, WorkingDirectory: dir}, Start: start, Finish: time.Now(), ExitCode: tc.exitCode, GoVersion: runtime.Version()})
			if err != nil || !summary.Sent {
				t.Fatalf("summary %+v, %v", summary, err)
			}
			mu.Lock()
			defer mu.Unlock()
			var session map[string]any
			for _, event := range events {
				if event["type"] == "test_session_end" {
					session, _ = event["content"].(map[string]any)["meta"].(map[string]any)
				}
			}
			if session == nil {
				t.Fatalf("no session event among %v", events)
			}
			if session[constants.TestStatus] != tc.wantStatus {
				t.Fatalf("status %v, want %s", session[constants.TestStatus], tc.wantStatus)
			}
			_, hasReason := session[constants.TestSessionEmptyReason]
			_, hasSkipReason := session[constants.TestSkipReason]
			if hasReason != tc.wantEmpty || hasSkipReason != tc.wantEmpty {
				t.Fatalf("empty reason %v, skip reason %v; want them only for an empty session", session[constants.TestSessionEmptyReason], session[constants.TestSkipReason])
			}
		})
	}
}

// A test process records that it ran nothing only when testing.M.Run also
// succeeded: a TestMain can exit 0 after Run fails, as with -parallel=0.
func TestCLISessionRecordsAnEmptyCloseOnlyOnSuccess(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	for _, exitCode := range []int{0, 2} {
		dir := t.TempDir()
		file, err := os.Create(filepath.Join(dir, "process-a.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		session := CreateTestSession().(*tslvTestSession)
		session.cliRecord, session.cliTags = utils.NewCLISessionRecordForTesting(file), map[string]any{}
		session.Close(exitCode, WithTestSessionSkipIfEmpty())
		_ = file.Close()
		processes, err := readCLIProcesses(dir)
		if err != nil || len(processes) != 1 || processes[0].closed == nil {
			t.Fatalf("processes %v, %v", processes, err)
		}
		_, empty := processes[0].closed.Tags[constants.TestSessionEmptyReason]
		if empty != (exitCode == 0) {
			t.Fatalf("exit code %d: empty reason %v", exitCode, processes[0].closed.Tags[constants.TestSessionEmptyReason])
		}
	}
}

func writeRecord(t *testing.T, path string, lines ...utils.CLISessionRecordLine) {
	t.Helper()
	var data []byte
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, encoded...), '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// decodeTestCycle returns the events of a test-cycle payload.
func decodeTestCycle(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	payload, _, err := msgp.ReadIntfBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	raw := payload.(map[string]any)["events"]
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case []byte:
		decoded, _, err := msgp.ReadIntfBytes(v)
		if err != nil {
			t.Fatal(err)
		}
		list = decoded.([]any)
	}
	var events []map[string]any
	for _, event := range list {
		events = append(events, event.(map[string]any))
	}
	return events
}

// go test has exited, so the session's git commands, without the Git
// worker's record, have a deadline: a git that hangs, as a fetch can, does
// not keep ddto from exiting.
func TestFinishCLISessionBoundsGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hanging git is a shell script")
	}
	resetCIVisibilityBootstrapStateForTesting()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	utils.ResetCITags()
	utils.ResetHandedCLIGitStateForTesting()
	t.Cleanup(func() {
		utils.ResetCITags()
		utils.ResetHandedCLIGitStateForTesting()
	})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := cliSessionGitTimeout
	cliSessionGitTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cliSessionGitTimeout = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	t.Cleanup(server.Close)
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "false")
	t.Setenv("DD_TRACE_AGENT_URL", server.URL)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	dir := t.TempDir()
	writeRecord(t, filepath.Join(dir, "process-a.jsonl"), utils.CLISessionRecordLine{Kind: "closed"})
	started := time.Now()
	summary, err := FinishCLISession(CLISessionEnd{Dir: dir, Session: utils.CLISession{ID: 7, WorkingDirectory: dir}, Start: started, Finish: started, ExitCode: 1, GoVersion: runtime.Version()})
	if err != nil || !summary.Sent {
		t.Fatalf("summary %+v, %v", summary, err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("the session waited %s for git", elapsed)
	}
}

// reentrantLogger runs onLog for each message, as a client's logger can.
type reentrantLogger struct{ onLog func() }

func (l reentrantLogger) Log(string) { l.onLog() }

// A process in ddto's test session logs nothing while it holds a module's,
// suite's or session's lock: a client's logger that calls back into them,
// here closing the suite on every message, does not deadlock.
func TestCLISessionLogsOutsideLifecycleLocks(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	level := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	t.Cleanup(func() { log.SetLevel(level) })

	file, err := os.Create(filepath.Join(t.TempDir(), "process-a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	record := utils.NewCLISessionRecordForTesting(file)
	session := CreateTestSession().(*tslvTestSession)
	session.cliRecord, session.cliTags = record, map[string]any{}
	var suite atomic.Pointer[TestSuite]
	t.Cleanup(log.UseLogger(reentrantLogger{onLog: func() {
		if s := suite.Load(); s != nil {
			(*s).Close()
		}
	}}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		module := session.GetOrCreateModule("m")
		s := module.GetOrCreateSuite("s")
		suite.Store(&s)
		s.CreateTest("T").Close(ResultStatusPass)
		s.Close()
		module.Close()
		session.Close(0)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a log line under a lifecycle lock deadlocked with the client's logger")
	}
	if lines, failures, _ := record.Stats(); lines != 5 || failures != 0 {
		t.Fatalf("record lines %d, failures %d; want module, suite, their ends and the close", lines, failures)
	}
}
