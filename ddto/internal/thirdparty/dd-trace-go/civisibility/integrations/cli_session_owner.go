package integrations

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// CLISessionEnd describes the end of the go test command whose session ddto
// owns: see utils.CLISession.
type CLISessionEnd struct {
	// Dir is the session directory that ddto handed to the test processes.
	Dir     string
	Session utils.CLISession
	// Start and Finish bound the command; ExitCode is go test's.
	Start, Finish time.Time
	ExitCode      int
	// GoVersion is the test processes' runtime version.
	GoVersion string
	// Coverage is the command's total statement coverage, in percent, from
	// its coverage profile, or nil.
	Coverage *float64
	// GitState is the directory of ddto's Git worker, whose record of the
	// checkout the session reuses, or "".
	GitState string
}

// cliSessionGitTimeout bounds the git commands that the session's CI tags
// run when ddto's Git worker recorded no checkout: go test has exited.
var cliSessionGitTimeout = 10 * time.Second

// CLISessionSummary reports what FinishCLISession did, for ddto's debug log.
type CLISessionSummary struct {
	Sent bool
	// Processes joined the session; Closed of them closed CI Visibility.
	// Crashed left crash output.
	Processes, Closed, Crashed int
	// Recreated counts the modules, suites and tests recreated for the
	// processes that did not close.
	Recreated int
}

// crashTextLimit bounds the crash output that a session or a recreated test
// carries; ddto reads no more of a process's crash output.
const crashTextLimit = 64 << 10

// cliProcess is a test process's record and crash output.
type cliProcess struct {
	stem    string
	lines   []utils.CLISessionRecordLine
	closed  *utils.CLISessionRecordLine
	crash   string
	crashAt time.Time
}

// FinishCLISession sends the session that ddto owns once go test has
// exited, in ddto's process: the session event, with the tags that its test
// processes recorded, and, for a process that ended before it closed CI
// Visibility, the modules and suites that it left open and, when its crash
// output names it, the test that crashed. ddto skips the session when no test
// process joined it and go test succeeded, as when every result came from
// Go's cache. It initializes only the tracer: no settings, Git upload,
// signal handler or logs.
func FinishCLISession(end CLISessionEnd) (CLISessionSummary, error) {
	var summary CLISessionSummary
	// ddto's process has not initialized CI Visibility, which sets the level.
	if infra.BoolEnv("DD_TRACE_DEBUG", false) {
		log.SetLevel(log.LevelDebug)
	}
	processes, err := readCLIProcesses(end.Dir)
	if err != nil {
		return summary, err
	}
	summary.Processes = len(processes)
	var crashes []*cliProcess
	for _, p := range processes {
		if log.DebugEnabled() {
			modules, suites := 0, 0
			for _, line := range p.lines {
				switch line.Kind {
				case "module":
					modules++
				case "suite":
					suites++
				}
			}
			exitCode := "none"
			if p.closed != nil {
				exitCode = strconv.Itoa(p.closed.ExitCode)
			}
			log.Debug("civisibility: test session process %s closed=%t exit_code=%s crash_output_bytes=%d modules=%d suites=%d", filepath.Base(p.stem), p.closed != nil, exitCode, len(p.crash), modules, suites)
		}
		if p.closed != nil {
			summary.Closed++
		}
		if p.crash != "" {
			summary.Crashed++
		}
		// A process that joined and did not close CI Visibility ended
		// abnormally, even before its first module.
		if p.crash != "" || p.closed == nil {
			crashes = append(crashes, p)
		}
	}
	if len(processes) == 0 && end.ExitCode == 0 {
		log.Debug("civisibility: no test process joined ddto's test session and go test succeeded; no session to send")
		return summary, nil
	}
	// The CI tags, Git's included, and the service are the test processes':
	// they run in go test's directory, which -C can move from ddto's.
	if restore, err := chdirForCITags(end.Session.WorkingDirectory); err != nil {
		log.Debug("civisibility: test session CI tags read from ddto's directory: %s", err.Error())
	} else {
		defer restore()
	}
	// The Git worker read the checkout already, without fetching from the
	// network; without its record, the git commands that remain are bounded.
	gitCtx, cancel := context.WithTimeout(context.Background(), cliSessionGitTimeout)
	defer cancel()
	utils.SetGitCommandContext(gitCtx)
	defer utils.SetGitCommandContext(context.Background())
	reused := false
	if end.GitState != "" {
		var record utils.CLIGitRecord
		if err := utils.ReadCLIGitStateFile(end.GitState, utils.CLIGitRecordFile, &record); err == nil {
			utils.UseCLIGitState(end.GitState, record)
			reused = true
		} else {
			log.Debug("civisibility: test session without the Git worker's record: %s", err.Error())
		}
	}
	log.Debug("civisibility: test session CI tags directory=%s git_worker_record=%t git_timeout=%s", end.Session.WorkingDirectory, reused, cliSessionGitTimeout)
	initializeCLISessionOwner(end.GoVersion)
	session := CreateTestSession(
		withTestSessionID(end.Session.ID),
		WithTestSessionWorkingDirectory(utils.GetRelativePathFromCITagsSourceRoot(end.Session.WorkingDirectory)),
		WithTestSessionFramework(cliSessionFramework, end.GoVersion),
		WithTestSessionStartTime(end.Start),
	).(*tslvTestSession)
	merged := mergeCLISessionTags(processes, end.Coverage)
	for key, value := range merged {
		session.SetTag(key, value)
	}
	log.Debug("civisibility: sending ddto's test session id=%d exit_code=%d duration=%s tags=%d coverage=%v crashed_processes=%d", end.Session.ID, end.ExitCode, end.Finish.Sub(end.Start), len(merged), merged[constants.CodeCoveragePercentageOfTotalLines], len(crashes))
	for _, p := range crashes {
		if p.closed == nil {
			summary.Recreated += recreateCLIProcess(session, p, end.Finish)
		}
	}
	var closeOptions []TestSessionCloseOption
	if len(crashes) != 0 {
		closeOptions = append(closeOptions, withTestSessionCrash(crashError(crashes)))
	}
	session.Close(end.ExitCode, append(closeOptions, WithTestSessionFinishTime(end.Finish))...)
	tracer.Stop()
	summary.Sent = true
	return summary, nil
}

// chdirForCITags makes dir the process's directory, as the test processes'
// directory, and returns how to restore the previous one.
func chdirForCITags(dir string) (func(), error) {
	previous, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	pwd, hadPWD := os.LookupEnv("PWD")
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	_ = os.Setenv("PWD", dir)
	return func() {
		_ = os.Chdir(previous)
		if hadPWD {
			_ = os.Setenv("PWD", pwd)
		} else {
			_ = os.Unsetenv("PWD")
		}
	}, nil
}

// cliSessionFramework is gotesting's framework, which test processes report.
const cliSessionFramework = "golang.org/pkg/testing"

// initializeCLISessionOwner initializes CI Visibility in ddto's process only
// as far as its session needs: the CI tags, with the test processes' runtime
// version, and the tracer, with the test processes' service rules. Later
// initializations do nothing.
func initializeCLISessionOwner(goVersion string) {
	ciVisibilityInitializationOnce.Do(func() {
		civisibility.SetState(civisibility.StateInitializing)
		defer civisibility.SetState(civisibility.StateInitialized)
		if infra.BoolEnv("DD_TRACE_DEBUG", false) {
			log.SetLevel(log.LevelDebug)
		}
		utils.AddCITagsMap(libraryCapabilities())
		ciTags := utils.GetCITagsReadOnly()
		if goVersion != "" {
			utils.AddCITagsMap(map[string]string{constants.RuntimeVersion: goVersion})
		}
		_, opts := ciVisibilityService(ciTags)
		tracer.Start(opts...)
	})
}

// readCLIProcesses reads the records and crash outputs in the session
// directory, in name order.
func readCLIProcesses(dir string) ([]*cliProcess, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byStem := map[string]*cliProcess{}
	var stems []string
	process := func(stem string) *cliProcess {
		if p := byStem[stem]; p != nil {
			return p
		}
		p := &cliProcess{stem: stem}
		byStem[stem] = p
		stems = append(stems, stem)
		return p
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "process-") {
			continue
		}
		path := filepath.Join(dir, name)
		switch ext := filepath.Ext(name); ext {
		case utils.CLISessionRecordExt:
			p := process(strings.TrimSuffix(name, ext))
			if p.lines, err = utils.ReadCLISessionRecord(path); err != nil {
				log.Debug("civisibility: test session record %s unreadable: %s", name, err.Error())
			}
			for i := range p.lines {
				if p.lines[i].Kind == "closed" {
					p.closed = &p.lines[i]
				}
			}
		case ".crash":
			text, at, err := readCrashOutput(path)
			if err != nil {
				log.Debug("civisibility: crash output %s unreadable: %s", name, err.Error())
				continue
			}
			if text != "" {
				p := process(strings.TrimSuffix(name, ext))
				p.crash, p.crashAt = text, at
			}
		}
	}
	sort.Strings(stems)
	processes := make([]*cliProcess, 0, len(stems))
	for _, stem := range stems {
		processes = append(processes, byStem[stem])
	}
	return processes, nil
}

// readCrashOutput returns the start of a crash output, at most
// crashTextLimit bytes, and when the runtime last wrote it.
func readCrashOutput(path string) (string, time.Time, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", time.Time{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, crashTextLimit))
	return strings.TrimSpace(string(data)), info.ModTime(), err
}

// mergeCLISessionTags merges the session tags that the closed test
// processes recorded: counts add up, "true" and EFD's "faulty" abort reason
// win, and other values come from the first process that set them. A
// process's coverage covers only its own run: the session takes it only when
// that process is the only one that joined, and otherwise the command's
// coverage profile's.
func mergeCLISessionTags(processes []*cliProcess, coverage *float64) map[string]any {
	merged := map[string]any{}
	coverageReports := 0
	for _, p := range processes {
		if p.closed == nil {
			continue
		}
		keys := make([]string, 0, len(p.closed.Tags))
		for key := range p.closed.Tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := p.closed.Tags[key]
			previous, seen := merged[key]
			switch {
			case key == constants.CodeCoveragePercentageOfTotalLines:
				coverageReports++
				merged[key] = value
			case key == constants.ITRTestsSkippingCount:
				merged[key] = numericTag(previous) + numericTag(value)
			case !seen:
				merged[key] = value
			case value == "true" || key == constants.TestEarlyFlakeDetectionRetryAborted && value == "faulty":
				merged[key] = value
			}
		}
	}
	if coverage != nil {
		merged[constants.CodeCoveragePercentageOfTotalLines] = *coverage
	} else if coverageReports != 0 && len(processes) != 1 {
		log.Debug("civisibility: %d of %d test processes reported their coverage and no coverage profile gives the command's; the session reports none", coverageReports, len(processes))
		delete(merged, constants.CodeCoveragePercentageOfTotalLines)
	}
	return merged
}

// numericTag returns a recorded numeric tag value, which JSON decodes as
// float64.
func numericTag(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}

// recreateCLIProcess recreates, failed, the modules and suites that a test
// process left open when it ended without closing CI Visibility, and the test
// that its crash output names in one of those suites. It returns how many
// events it recreated.
func recreateCLIProcess(session *tslvTestSession, p *cliProcess, fallback time.Time) int {
	errType, message, stack := processEndError(p)
	finish := p.crashAt
	if finish.IsZero() {
		finish = fallback
	}
	type openSuite struct {
		line  utils.CLISessionRecordLine
		suite TestSuite
	}
	modules := map[uint64]utils.CLISessionRecordLine{}
	suites := map[uint64]*openSuite{}
	var order []uint64
	for _, line := range p.lines {
		switch line.Kind {
		case "module":
			modules[line.ID] = line
		case "module_end":
			delete(modules, line.ID)
		case "suite":
			suites[line.ID] = &openSuite{line: line}
			order = append(order, line.ID)
		case "suite_end":
			delete(suites, line.ID)
		}
	}
	recreated := 0
	created := map[uint64]*tslvTestModule{}
	module := func(id uint64) *tslvTestModule {
		if m := created[id]; m != nil {
			return m
		}
		line, open := modules[id]
		if !open {
			return nil
		}
		// By identity: another process can have had a module of that name.
		m := createTestModule(session, line.Name, &tslvTestModuleStartOptions{framework: session.framework, frameworkVersion: session.frameworkVersion, startTime: line.Start, id: line.ID, service: line.Service}).(*tslvTestModule)
		failRecreated(m, errType, message, stack)
		created[id] = m
		recreated++
		return m
	}
	for _, id := range order {
		s := suites[id]
		if s == nil {
			continue
		}
		m := module(s.line.Module)
		if m == nil {
			continue
		}
		s.suite = createTestSuite(m, s.line.Name, &tslvTestSuiteStartOptions{startTime: s.line.Start, id: s.line.ID, service: s.line.Service})
		failRecreated(s.suite, errType, message, stack)
		recreated++
	}
	for id := range modules {
		module(id)
	}
	if moduleName, suiteName, testName, ok := crashedTest(p.crash); ok {
		for _, id := range order {
			s := suites[id]
			if s == nil || s.suite == nil || s.line.Name != suiteName || modules[s.line.Module].Name != moduleName {
				continue
			}
			test := s.suite.CreateTest(testName, WithTestStartTime(finish))
			test.SetError(WithErrorInfo(errType, message, stack))
			test.SetTag(constants.TestFinalStatus, constants.TestStatusFail)
			test.Close(ResultStatusFail, WithTestFinishTime(finish))
			recreated++
			log.Debug("civisibility: recreated the crashed test name=%s suite=%s module=%s", testName, suiteName, moduleName)
			break
		}
	}
	for _, id := range order {
		if s := suites[id]; s != nil && s.suite != nil {
			s.suite.Close(WithTestSuiteFinishTime(finish))
		}
	}
	for _, m := range created {
		m.Close(WithTestModuleFinishTime(finish))
	}
	log.Debug("civisibility: test process %s ended without closing CI Visibility (%s); recreated %d events", filepath.Base(p.stem), message, recreated)
	return recreated
}

// failRecreated marks a recreated module or suite failed with the process's
// end.
func failRecreated(event ddTslvEvent, errType, message, stack string) {
	event.SetError(WithErrorInfo(errType, message, stack))
	event.SetTag(constants.TestStatus, constants.TestStatusFail)
}

// processEndError describes how a test process that did not close CI
// Visibility ended: its crash output's first line and the output, or, without
// output, a process that something stopped from outside Go, such as a kill
// or the race detector with GORACE=halt_on_error=1.
func processEndError(p *cliProcess) (errType, message, stack string) {
	if p.crash == "" {
		return "crash", "the test process ended before it closed CI Visibility, without Go crash output: it was killed, or a sanitizer such as the race detector with GORACE=halt_on_error=1 stopped it", ""
	}
	return "crash", crashMessage(p.crash), p.crash
}

// crashMessage returns a crash output's message: its first line for a panic,
// whose value the output prints first. The runtime prints a fatal error's
// message before it copies its output to the crash output, which starts with
// the goroutine instead: the message then names the error's first frame
// outside the runtime.
func crashMessage(crash string) string {
	first := firstLine(crash)
	if !strings.HasPrefix(first, "goroutine ") {
		return first
	}
	lines := strings.Split(crash, "\n")
	for i := 1; i+1 < len(lines) && strings.TrimSpace(lines[i]) != ""; i += 2 {
		function := strings.TrimPrefix(lines[i], "created by ")
		if at := strings.LastIndexByte(function, '('); at > 0 && strings.HasSuffix(function, ")") {
			function = function[:at]
		}
		if strings.HasPrefix(function, "runtime.") || strings.HasPrefix(function, "internal/") || strings.HasPrefix(function, "sync.") {
			continue
		}
		file := strings.TrimSpace(lines[i+1])
		if at := strings.LastIndexByte(file, ' '); at > 0 {
			file = file[:at]
		}
		return "fatal error in " + function + " at " + filepath.Base(filepath.FromSlash(file)) + "; the test output shows the error, which Go's crash output leaves out"
	}
	return "fatal error; the test output shows it, which Go's crash output leaves out"
}

// crashError is the session's error for the processes that crashed or ended
// without closing CI Visibility.
func crashError(crashes []*cliProcess) (errType, message, stack string) {
	errType, message, stack = processEndError(crashes[0])
	if len(crashes) > 1 {
		message += fmt.Sprintf(" (and %d more test processes)", len(crashes)-1)
	}
	return errType, message, stack
}

// crashedTest returns the test that a Go crash output names in its first
// goroutine, the one that crashed: the first frame of a test function, or of
// a function literal in one, and otherwise the test function that created
// the goroutine. module and suite are the test function's package path and
// file name, as gotesting names them. A frame that names no test function
// unambiguously, such as a method of a testify suite, TestMain or a helper,
// names none.
func crashedTest(crash string) (module, suite, name string, ok bool) {
	lines := strings.Split(crash, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, ":") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", "", "", false
	}
	var created [3]string
	for i := start; i+1 < len(lines) && strings.TrimSpace(lines[i]) != ""; i += 2 {
		function := lines[i]
		creator := false
		if rest, found := strings.CutPrefix(function, "created by "); found {
			function, creator = rest, true
			if at := strings.Index(function, " in goroutine "); at >= 0 {
				function = function[:at]
			}
		} else if at := strings.LastIndexByte(function, '('); at > 0 && strings.HasSuffix(function, ")") {
			function = function[:at]
		}
		file := strings.TrimSpace(lines[i+1])
		if at := strings.LastIndexByte(file, ':'); at >= 0 {
			file = file[:at]
		}
		m, n, isTest := testFunction(function)
		if !isTest || !strings.HasSuffix(file, "_test.go") {
			continue
		}
		s := filepath.Base(filepath.FromSlash(file))
		if !creator {
			return m, s, n, true
		}
		if created[0] == "" {
			created = [3]string{m, s, n}
		}
	}
	if created[0] != "" {
		return created[0], created[1], created[2], true
	}
	return "", "", "", false
}

// testFunction splits a function's full name, as a Go stack prints it, into
// its package path and the test function that declares it, if it is a test
// function or a function literal in one.
func testFunction(full string) (module, name string, ok bool) {
	lastSlash := max(strings.LastIndexByte(full, '/'), 0)
	dot := strings.IndexByte(full[lastSlash:], '.')
	if dot < 0 {
		return "", "", false
	}
	module, rest := full[:lastSlash+dot], full[lastSlash+dot+1:]
	name, _, _ = strings.Cut(rest, ".")
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		suffix, found := strings.CutPrefix(name, prefix)
		if !found || name == "TestMain" {
			continue
		}
		// As go test: the prefix alone, or followed by a non-lowercase rune.
		if r, _ := utf8.DecodeRuneInString(suffix); suffix == "" || !unicode.IsLower(r) {
			return module, name, true
		}
	}
	return "", "", false
}
