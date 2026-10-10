// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/locking"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	runtimeTelemetry "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

const (
	// testFramework represents the name of the testing framework.
	testFramework = "golang.org/pkg/testing"
)

type (
	// TestCoverage is the interface for collecting test coverage.
	TestCoverage interface {
		// CollectCoverageBeforeTestExecution collects coverage before test execution.
		CollectCoverageBeforeTestExecution()
		// CollectCoverageAfterTestExecution collects coverage after test execution.
		CollectCoverageAfterTestExecution()
	}

	// testCoverage holds information about test coverage.
	testCoverage struct {
		sessionID            uint64
		moduleID             uint64
		suiteID              uint64
		testID               uint64
		testFile             string
		preCoverageFilename  string
		postCoverageFilename string
		filesCovered         []coveredFile
	}

	// coverageData holds information about coverage data with their block
	coverageData struct {
		fileName string
		blocks   []coverageBlock
	}

	// coverageBlock holds information about coverage block.
	coverageBlock struct {
		startLine int
		startCol  int
		endLine   int
		endCol    int
		numStmt   int
		count     int
	}

	// coverProfileBlock is one block of a text coverage profile and its file.
	coverProfileBlock struct {
		fileName string
		coverageBlock
	}

	// coverProfile keeps a text profile's blocks in file order.
	coverProfile struct {
		blocks []coverProfileBlock
	}

	// coverageBlockKey is a block's position within its file.
	coverageBlockKey struct {
		startLine int
		startCol  int
		endLine   int
		endCol    int
	}

	coveredFile struct {
		name   string
		bitmap []byte
	}

	// BackfillInput configures backend aggregate coverage that can repair the
	// process-local coverage profile after ITR skips have been applied.
	BackfillInput struct {
		BackendCoverage map[string]*filebitmap.FileBitmap
		ActualSkips     int
	}

	// BackfillResult describes one idempotent coverage backfill finalization.
	BackfillResult struct {
		Applied               bool
		Reason                string
		Coverage              float64
		ProfilePath           string
		MatchedFiles          int
		UnmatchedBackendFiles int
		MatchedBlocks         int
		UpdatedBlocks         int
	}

	runtimeCoverageSnapshot struct {
		path      string
		temporary bool
	}
)

var _ TestCoverage = (*testCoverage)(nil)

var (
	coverageStateMu locking.Mutex

	// Go's coverage emitter mutates process-global state even for separate profiles.
	// Snapshot callers already hold coverageStateMu, so it needs a separate lock.
	runtimeCoverageMu locking.Mutex

	// mode is the coverage mode.
	mode string
	// tearDown is the function to write the coverage counters to the file.
	tearDown func(coverprofile string, gocoverdir string) (string, error)
	// coverageUploadEnabled is true when per-test coverage should be sent to Datadog.
	coverageUploadEnabled bool

	// covWriter is the coverage writer for sending test coverage data to the backend.
	covWriter *coverageWriter

	// runtimeSnapshot is the single runtime coverage snapshot shared by backfill
	// and session coverage calculation.
	runtimeSnapshot *runtimeCoverageSnapshot
	// runtimeSnapshotCleaned prevents duplicate cleanup of the generated temp snapshot.
	runtimeSnapshotCleaned bool
	// backfillInput stores backend aggregate coverage for finalization.
	backfillInput *BackfillInput
	// backfillFinalized records whether FinalizeBackfill has already run.
	backfillFinalized bool
	// backfillResult stores the idempotent finalization result.
	backfillResult BackfillResult

	// temporaryDir is the temporary directory to store coverage files.
	temporaryDir string
	// modulePath is the module path.
	modulePath string
	// moduleDir is the module directory.
	moduleDir string
	// moduleInfoSource records where coverage initialized. Resolving the
	// module starts a go command that most runs never need: only per-test
	// coverage, LCOV reports and profile backfill read modulePath and
	// moduleDir, through resolveModuleInfo. Per-test coverage resolves it
	// during initialization; backfill and LCOV readers run before or after
	// the tests.
	moduleInfoSource struct {
		locking.Mutex
		pending bool
		dir     string
		env     []string
		goPath  string
	}
)

// InitializeCoverage initializes the runtime coverage.
func InitializeCoverage(m *testing.M, uploadEnabled bool) {
	log.Debug("civisibility.cov: initializing runtime coverage")
	testDep, err := getTestDepsCoverage(m)
	if err != nil {
		log.Debug("civisibility.cov: error initializing runtime coverage: %s", err.Error())
		return
	}
	if testDep == nil {
		log.Debug("civisibility.cov: runtime coverage dependencies are unavailable")
		return
	}

	// initializing runtime coverage
	tMode, tDown, _ := testDep.InitRuntimeCoverage()
	mode = tMode
	tearDown = func(coverprofile string, gocoverdir string) (string, error) {
		runtimeCoverageMu.Lock()
		defer runtimeCoverageMu.Unlock()
		return tDown(coverprofile, gocoverdir)
	}
	coverageUploadEnabled = uploadEnabled
	runtimeSnapshot = nil
	runtimeSnapshotCleaned = false
	backfillInput = nil
	backfillFinalized = false
	backfillResult = BackfillResult{}

	deferModuleInfo()

	// if we cannot collect we bailout early
	if !CanCollectPerTestCoverage() {
		return
	}
	// Per-test coverage reads the module from a worker while tests run, and
	// starting go there reads process state that tests may change, such as
	// time.Local. Resolve it before the tests start.
	resolveModuleInfo()

	// initializing coverage writer
	covWriter = newCoverageWriter()
	integrations.PushCiVisibilityCloseAction(func() {
		covWriter.stop()
	})

	// create a temporary directory to store coverage files
	temporaryDir, err = os.MkdirTemp("", "coverage")
	if err != nil {
		log.Debug("civisibility.cov: error creating temporary directory: %s", err.Error())
	} else {
		log.Debug("civisibility.cov: temporary coverage directory created: %s", temporaryDir)
	}
	integrations.PushCiVisibilityCloseAction(func() {
		_ = os.RemoveAll(temporaryDir)
	})

}

// deferModuleInfo captures the directory, environment and go command that
// resolving the module used at initialization, so a later TestMain chdir or
// environment change does not alter the result.
func deferModuleInfo() {
	dir, _ := os.Getwd()
	goPath, err := exec.LookPath("go")
	if err != nil {
		goPath = "go"
	}
	moduleInfoSource.Lock()
	defer moduleInfoSource.Unlock()
	moduleInfoSource.pending = true
	moduleInfoSource.dir, moduleInfoSource.env, moduleInfoSource.goPath = dir, os.Environ(), goPath
}

// resolveModuleInfo sets modulePath and moduleDir on first use after
// initialization. Readers call it before reading them.
func resolveModuleInfo() {
	moduleInfoSource.Lock()
	defer moduleInfoSource.Unlock()
	if !moduleInfoSource.pending {
		return
	}
	moduleInfoSource.pending = false
	modulePath, moduleDir = listModuleInfo(moduleInfoSource.dir, moduleInfoSource.env, moduleInfoSource.goPath)
}

// listModuleInfo returns the module path and directory that go list reports
// for the package in dir. Tests replace it.
var listModuleInfo = func(dir string, env []string, goPath string) (path, directory string) {
	cmd := exec.Command(goPath, "list", "-f", "{{.Module.Path}};{{.Module.Dir}}")
	cmd.Dir, cmd.Env = dir, env
	stdOut, err := cmd.CombinedOutput()
	if err != nil {
		log.Debug("civisibility.cov: error getting module path and module dir: %s", err.Error())
		return "", ""
	}
	parts := strings.Split(string(stdOut), ";")
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// CanCollect returns whether coverage can be collected.
func CanCollect() bool {
	return mode == "count" || mode == "atomic"
}

// CanComputeCoverageProfile returns whether a runtime coverprofile can be produced.
func CanComputeCoverageProfile() bool {
	return tearDown != nil && (mode == "set" || mode == "count" || mode == "atomic")
}

// CanCollectPerTestCoverage returns whether per-test coverage upload can run.
func CanCollectPerTestCoverage() bool {
	return coverageUploadEnabled && CanCollect()
}

// GetCoverage returns the total coverage percentage for the test package
func GetCoverage() float64 {
	if !CanComputeCoverageProfile() {
		return 0
	}

	snapshot, err := RuntimeCoverageSnapshot()
	if err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		return 0
	}

	totalStatements, coveredStatements, err := getCoverageStatementsInfo(snapshot.path)
	if err != nil {
		log.Debug("civisibility.cov: error parsing coverage file: %s", err.Error())
	}

	if totalStatements == 0 {
		return 0
	}

	return float64(coveredStatements) / float64(totalStatements)
}

// RuntimeCoverageSnapshot returns the single coverage profile snapshot used for
// final coverage calculation and ITR backfill.
func RuntimeCoverageSnapshot() (*runtimeCoverageSnapshot, error) {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()
	return runtimeCoverageSnapshotLocked()
}

func runtimeCoverageSnapshotLocked() (*runtimeCoverageSnapshot, error) {
	if runtimeSnapshot != nil {
		return runtimeSnapshot, nil
	}
	if !CanComputeCoverageProfile() {
		return nil, errors.New("runtime coverage unavailable")
	}

	if coverProfile := currentCoverProfilePath(); coverProfile != "" {
		if _, err := os.Stat(coverProfile); err == nil {
			runtimeSnapshot = &runtimeCoverageSnapshot{path: coverProfile}
			return runtimeSnapshot, nil
		}
	}

	if err := ensureTemporaryDirLocked(); err != nil {
		return nil, err
	}
	coverageFile := filepath.Join(temporaryDir, "global_coverage.out")
	if _, err := tearDown(coverageFile, ""); err != nil {
		return nil, err
	}
	runtimeSnapshot = &runtimeCoverageSnapshot{path: coverageFile, temporary: true}
	return runtimeSnapshot, nil
}

func currentCoverProfilePath() string {
	if coverProfileFlag := flag.Lookup("test.coverprofile"); coverProfileFlag != nil {
		return coverProfileFlag.Value.String()
	}
	return ""
}

func ensureTemporaryDirLocked() error {
	if temporaryDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "coverage")
	if err != nil {
		return err
	}
	temporaryDir = dir
	integrations.PushCiVisibilityCloseAction(func() {
		_ = os.RemoveAll(dir)
	})
	return nil
}

// ConfigureBackfill stores backend aggregate coverage for the finalizer.
func ConfigureBackfill(input BackfillInput) {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	copiedCoverage := make(map[string]*filebitmap.FileBitmap, len(input.BackendCoverage))
	maps.Copy(copiedCoverage, input.BackendCoverage)
	backfillInput = &BackfillInput{
		BackendCoverage: copiedCoverage,
		ActualSkips:     input.ActualSkips,
	}
	backfillFinalized = false
	backfillResult = BackfillResult{}
}

// PreflightBackfill validates that runtime coverage can later be backfilled.
// Path matching is deferred to FinalizeBackfill because producing a coverage
// profile before testing.M.Run completes mutates Go's runtime coverage state.
func PreflightBackfill(input BackfillInput) BackfillResult {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	if len(input.BackendCoverage) == 0 {
		return BackfillResult{Reason: "backfill not configured"}
	}
	if !CanComputeCoverageProfile() {
		return BackfillResult{Reason: "runtime coverage unavailable"}
	}
	if !CanCollect() {
		return BackfillResult{Reason: "coverage mode unsupported"}
	}
	result := validateBackendCoverageSourceFiles(input.BackendCoverage)
	if result.unmatchedBackendFiles > 0 {
		return BackfillResult{
			Reason:                "coverage paths unmatched",
			MatchedFiles:          result.matchedFiles,
			UnmatchedBackendFiles: result.unmatchedBackendFiles,
		}
	}
	if result.matchedFiles == 0 {
		return BackfillResult{Reason: "coverage paths unmatched"}
	}
	return BackfillResult{}
}

// FinalizeBackfill applies backend coverage to the runtime snapshot once.
func FinalizeBackfill() BackfillResult {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	if backfillFinalized {
		return backfillResult
	}
	backfillFinalized = true

	if backfillInput == nil || len(backfillInput.BackendCoverage) == 0 {
		backfillResult = BackfillResult{Reason: "backfill not configured"}
		return backfillResult
	}
	if backfillInput.ActualSkips <= 0 {
		backfillResult = BackfillResult{Reason: "no actual itr skips"}
		return backfillResult
	}
	if !CanComputeCoverageProfile() {
		backfillResult = BackfillResult{Reason: "runtime coverage unavailable"}
		return backfillResult
	}
	if !CanCollect() {
		backfillResult = BackfillResult{Reason: "coverage mode unsupported"}
		return backfillResult
	}

	snapshot, err := runtimeCoverageSnapshotLocked()
	if err != nil {
		backfillResult = BackfillResult{Reason: "runtime coverage unavailable"}
		return backfillResult
	}

	profile, err := parseOrderedCoverProfile(snapshot.path)
	if err != nil {
		backfillResult = BackfillResult{Reason: "coverage profile invalid", ProfilePath: snapshot.path}
		return backfillResult
	}
	result := profile.applyBackfill(backfillInput.BackendCoverage)
	if result.unmatchedBackendFiles > 0 {
		backfillResult = BackfillResult{
			Reason:                "coverage paths unmatched",
			ProfilePath:           snapshot.path,
			MatchedFiles:          result.matchedFiles,
			UnmatchedBackendFiles: result.unmatchedBackendFiles,
			MatchedBlocks:         result.matchedBlocks,
		}
		return backfillResult
	}
	if result.matchedBlocks == 0 {
		backfillResult = BackfillResult{Reason: "coverage paths unmatched", ProfilePath: snapshot.path, MatchedFiles: result.matchedFiles}
		return backfillResult
	}
	if result.updatedBlocks > 0 {
		if err := profile.writeAtomic(snapshot.path); err != nil {
			backfillResult = BackfillResult{Reason: "runtime coverage unavailable", ProfilePath: snapshot.path}
			return backfillResult
		}
	}

	coverage := 0.0
	if result.totalStatements > 0 {
		coverage = float64(result.coveredStmts) / float64(result.totalStatements)
	}
	backfillResult = BackfillResult{
		Applied:               result.updatedBlocks > 0,
		Coverage:              coverage,
		ProfilePath:           snapshot.path,
		MatchedFiles:          result.matchedFiles,
		UnmatchedBackendFiles: result.unmatchedBackendFiles,
		MatchedBlocks:         result.matchedBlocks,
		UpdatedBlocks:         result.updatedBlocks,
	}
	return backfillResult
}

// CleanupRuntimeCoverageSnapshot removes a generated temp snapshot once.
func CleanupRuntimeCoverageSnapshot() {
	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()
	if runtimeSnapshot == nil || !runtimeSnapshot.temporary || runtimeSnapshotCleaned {
		return
	}
	if err := os.Remove(runtimeSnapshot.path); err != nil {
		log.Debug("civisibility.cov: error removing coverage file: %s", err.Error())
	}
	runtimeSnapshotCleaned = true
}

// ResetForTesting clears package globals used by coverage tests.
func ResetForTesting() {
	processCoverageMu.Lock()
	processAggregateCoverage = nil
	processAggregateCoverageErr = nil
	processCoverageMu.Unlock()

	coverageStateMu.Lock()
	defer coverageStateMu.Unlock()

	mode = ""
	tearDown = nil
	coverageUploadEnabled = false
	covWriter = nil
	runtimeSnapshot = nil
	runtimeSnapshotCleaned = false
	backfillInput = nil
	backfillFinalized = false
	backfillResult = BackfillResult{}
	temporaryDir = ""
	modulePath = ""
	moduleDir = ""
	moduleInfoSource.Lock()
	moduleInfoSource.pending = false
	moduleInfoSource.Unlock()
}

// NewTestCoverage creates a new test coverage.
func NewTestCoverage(sessionID, moduleID, suiteID, testID uint64, testFile string) TestCoverage {
	testFile = utils.GetRelativePathFromCITagsSourceRoot(testFile)
	return &testCoverage{
		sessionID: sessionID,
		moduleID:  moduleID,
		suiteID:   suiteID,
		testID:    testID,
		testFile:  testFile,
	}
}

// CollectCoverageBeforeTestExecution collects coverage before test execution.
func (t *testCoverage) CollectCoverageBeforeTestExecution() {
	if !CanCollectPerTestCoverage() {
		return
	}

	if err := t.collectCoverageBeforeTestExecution(); err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
	} else {
		telemetry.CodeCoverageStarted(testFramework, telemetry.DefaultCoverageLibraryType)
	}
}

func (t *testCoverage) collectCoverageBeforeTestExecution() error {
	if t.preCoverageFilename == "" {
		t.preCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-pre.out", t.moduleID, t.suiteID, t.testID))
	}
	_, err := tearDown(t.preCoverageFilename, "")
	return err
}

// CollectCoverageAfterTestExecution collects coverage after test execution.
func (t *testCoverage) CollectCoverageAfterTestExecution() {
	if !CanCollectPerTestCoverage() {
		return
	}

	if t.getCoverageData() != nil {
		return
	}

	t.scheduleProcessing()
}

// Profiles have already been captured by the test's before/after hooks. Only
// their processing is deferred, so a checkpoint cannot include a later test's
// counters. Completion closes a channel instead of retaining a worker until
// session shutdown receives from it.
func (t *testCoverage) scheduleProcessing() <-chan struct{} {
	done := make(chan struct{})
	integrations.PushCiVisibilityCloseAction(func() {
		<-done
	})
	process := func() {
		defer close(done)
		t.processCoverageData()
	}
	if cidelivery.Enabled() {
		cidelivery.Queue(process)
	} else if !runtimeTelemetry.Disabled() || log.DebugEnabled() {
		// Metric points and debug records need wall-clock timestamps. Finish
		// this work before a following test can change the global local zone.
		process()
	} else {
		go process()
	}
	return done
}

// getCoverageData gets the coverage data.
func (t *testCoverage) getCoverageData() error {
	if !CanCollectPerTestCoverage() {
		return nil
	}

	return t.collectCoverageAfterTestExecution()
}

func (t *testCoverage) collectCoverageAfterTestExecution() error {
	if t.postCoverageFilename == "" {
		t.postCoverageFilename = filepath.Join(temporaryDir, fmt.Sprintf("%d-%d-%d-post.out", t.moduleID, t.suiteID, t.testID))
	}
	_, err := tearDown(t.postCoverageFilename, "")
	if err != nil {
		log.Debug("civisibility.cov: error getting coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
	}

	return err
}

// processCoverageData processes the coverage data.
func (t *testCoverage) processCoverageData() {
	if !t.loadCoverageData() {
		return
	}
	t.submitCoverageData()
	t.removeCoverageFiles()
}

func (t *testCoverage) loadCoverageData() bool {
	if t.preCoverageFilename == "" ||
		t.postCoverageFilename == "" ||
		t.preCoverageFilename == t.postCoverageFilename {
		log.Debug("civisibility.cov: no coverage data to process")
		telemetry.CodeCoverageErrors()
		return false
	}
	preCoverage, err := readCoverProfile(t.preCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error parsing pre-coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return false
	}
	postCoverage, err := readCoverProfile(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error parsing post-coverage file: %s", err.Error())
		telemetry.CodeCoverageErrors()
		return false
	}

	t.filesCovered = coveredFilesBetween(t.testFile, preCoverage, postCoverage)
	return true
}

func (t *testCoverage) submitCoverageData() {
	telemetry.CodeCoverageFinished(testFramework, telemetry.DefaultCoverageLibraryType)
	if len(t.filesCovered) == 0 {
		telemetry.CodeCoverageIsEmpty()
	}

	covWriter.add(t)
}

func (t *testCoverage) removeCoverageFiles() {
	err := os.Remove(t.preCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error removing pre-coverage file: %s", err.Error())
	}

	err = os.Remove(t.postCoverageFilename)
	if err != nil {
		log.Debug("civisibility.cov: error removing post-coverage file: %s", err.Error())
	}
}

// readCoverProfile parses a text coverage profile and keeps its blocks in
// file order. The file is read at once and lines are parsed in place; only a
// file name that differs from the previous line's is copied. Accepted and
// skipped lines are the same as with the SDK's string-splitting parser, and
// the scanner keeps its line splitting and maximum line length.
func readCoverProfile(filename string) (coverProfile, error) {
	data, err := readCoverProfileFile(filename)
	if err != nil {
		return coverProfile{}, err
	}
	profile := coverProfile{blocks: make([]coverProfileBlock, 0, bytes.Count(data, []byte{'\n'})+1)}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	fileName := ""
	for scanner.Scan() {
		name, block, ok := parseCoverProfileLine(scanner.Bytes())
		if !ok {
			continue
		}
		if string(name) != fileName {
			fileName = string(name)
		}
		profile.blocks = append(profile.blocks, coverProfileBlock{fileName: fileName, coverageBlock: block})
	}
	return profile, scanner.Err()
}

// readCoverProfileFile reads a whole file like os.ReadFile, but sizes the
// buffer with Seek: os.ReadFile's Stat converts timestamps with time.Local,
// which coverage workers must not read while tests may change it.
func readCoverProfileFile(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, 0, size+1) // one more byte to see EOF without growing
	for {
		n, err := file.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err == io.EOF {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
		if len(data) == cap(data) {
			data = append(data, 0)[:len(data)]
		}
	}
}

// parseCoverProfileLine parses one "file:startLine.startCol,endLine.endCol
// numStmt count" line. It skips the same lines as the SDK parser, which split
// with strings.Fields and strings.Split and converted with strconv.Atoi:
// extra fields and separators are ignored, signs are accepted and Unicode
// whitespace separates fields.
func parseCoverProfileLine(line []byte) ([]byte, coverageBlock, bool) {
	if bytes.HasPrefix(line, []byte("mode:")) {
		return nil, coverageBlock{}, false
	}
	separator := bytes.LastIndexByte(line, ':')
	if separator <= 0 || len(bytes.TrimSpace(line[:separator])) == 0 {
		return nil, coverageBlock{}, false
	}
	fields, count := coverProfileFields(line[separator+1:])
	if count < 3 {
		return nil, coverageBlock{}, false
	}
	start, end, ok := cutCoverProfileSeparator(fields[0], ',')
	if !ok {
		return nil, coverageBlock{}, false
	}
	startLine, startCol, okStart := cutCoverProfileSeparator(start, '.')
	endLine, endCol, okEnd := cutCoverProfileSeparator(end, '.')
	if !okStart || !okEnd {
		return nil, coverageBlock{}, false
	}
	var block coverageBlock
	var valid [6]bool
	block.startLine, valid[0] = atoiCoverProfile(startLine)
	block.startCol, valid[1] = atoiCoverProfile(startCol)
	block.endLine, valid[2] = atoiCoverProfile(endLine)
	block.endCol, valid[3] = atoiCoverProfile(endCol)
	block.numStmt, valid[4] = atoiCoverProfile(fields[1])
	block.count, valid[5] = atoiCoverProfile(fields[2])
	if valid != [6]bool{true, true, true, true, true, true} {
		return nil, coverageBlock{}, false
	}
	return line[:separator], block, true
}

// coverProfileFields returns the first three fields of s and how many fields
// there are, counting at most three, with strings.Fields' rules.
func coverProfileFields(s []byte) (fields [3][]byte, count int) {
	start := -1
	for i := 0; i < len(s); {
		r, size := rune(s[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRune(s[i:])
		}
		if unicode.IsSpace(r) {
			if start >= 0 {
				fields[count] = s[start:i]
				if count++; count == len(fields) {
					return fields, count
				}
				start = -1
			}
		} else if start < 0 {
			start = i
		}
		i += size
	}
	if start >= 0 {
		fields[count] = s[start:]
		count++
	}
	return fields, count
}

// cutCoverProfileSeparator returns the first two elements of
// strings.Split(s, separator), and false when there is only one.
func cutCoverProfileSeparator(s []byte, separator byte) ([]byte, []byte, bool) {
	first, rest, found := bytes.Cut(s, []byte{separator})
	if !found {
		return nil, nil, false
	}
	if second, _, more := bytes.Cut(rest, []byte{separator}); more {
		return first, second, true
	}
	return first, rest, true
}

// atoiCoverProfile is strconv.Atoi without converting s to a string on its
// fast path, which accepts the same inputs: an optional sign and decimal digits
// short enough not to overflow. Longer inputs use strconv.Atoi itself.
func atoiCoverProfile(s []byte) (int, bool) {
	const intSize = 32 << (^uint(0) >> 63)
	if n := len(s); n == 0 || intSize == 32 && n >= 10 || intSize == 64 && n >= 19 {
		value, err := strconv.Atoi(string(s))
		return value, err == nil
	}
	digits := s
	if s[0] == '-' || s[0] == '+' {
		if digits = s[1:]; len(digits) == 0 {
			return 0, false
		}
	}
	value := 0
	for _, ch := range digits {
		ch -= '0'
		if ch > 9 {
			return 0, false
		}
		value = value*10 + int(ch)
	}
	if s[0] == '-' {
		value = -value
	}
	return value, true
}

// coveredFilesBetween subtracts the before profile from the after profile and
// returns the test file followed by every file with a block whose count grew,
// sorted by name.
//
// The SDK keyed every block of both profiles with fmt.Sprintf. Two profiles of
// one binary list the same blocks in the same order, so they are compared by
// index when every file name and position matches and no file repeats a
// position. Otherwise blocks are matched by a struct key with the SDK's rules:
// the last duplicate before block wins, and an after block without a before
// block counts when its count is positive.
func coveredFilesBetween(testFile string, before, after coverProfile) []coveredFile {
	var covered coveredLines
	if sameCoverProfileLayout(before, after) {
		for i := range after.blocks {
			if block := &after.blocks[i]; block.count-before.blocks[i].count > 0 {
				covered.add(block)
			}
		}
	} else {
		beforeCounts := make(map[string]map[coverageBlockKey]int)
		for i := range before.blocks {
			block := &before.blocks[i]
			counts := beforeCounts[block.fileName]
			if counts == nil {
				counts = make(map[coverageBlockKey]int)
				beforeCounts[block.fileName] = counts
			}
			counts[block.key()] = block.count
		}
		for i := range after.blocks {
			block := &after.blocks[i]
			if count, found := beforeCounts[block.fileName][block.key()]; found {
				if block.count-count > 0 {
					covered.add(block)
				}
			} else if block.count > 0 {
				covered.add(block)
			}
		}
	}
	return covered.files(testFile)
}

// sameCoverProfileLayout reports whether two profiles list the same blocks in
// the same order, each file in one run of strictly ordered positions.
// testing's text profiles sort blocks this way, and that order rules out a
// position appearing twice in one file.
func sameCoverProfileLayout(before, after coverProfile) bool {
	if len(before.blocks) != len(after.blocks) {
		return false
	}
	var files map[string]struct{}
	for i := range after.blocks {
		block, previous := &after.blocks[i], &before.blocks[i]
		if block.fileName != previous.fileName || block.key() != previous.key() {
			return false
		}
		if i > 0 && after.blocks[i-1].fileName == block.fileName {
			if !after.blocks[i-1].key().before(block.key()) {
				return false
			}
			continue
		}
		if files == nil {
			files = make(map[string]struct{})
		}
		if _, repeated := files[block.fileName]; repeated {
			return false
		}
		files[block.fileName] = struct{}{}
	}
	return true
}

// key returns the block's position.
func (b *coverProfileBlock) key() coverageBlockKey {
	return coverageBlockKey{startLine: b.startLine, startCol: b.startCol, endLine: b.endLine, endCol: b.endCol}
}

// before orders positions as testing sorts profile blocks within a file.
func (k coverageBlockKey) before(other coverageBlockKey) bool {
	return cmp.Or(
		cmp.Compare(k.startLine, other.startLine),
		cmp.Compare(k.endLine, other.endLine),
		cmp.Compare(k.startCol, other.startCol),
		cmp.Compare(k.endCol, other.endCol),
	) < 0
}

// coveredLines collects covered blocks by relative file name. Each file name is
// resolved once, and each bitmap is allocated once at its largest end line.
type coveredLines struct {
	lastFile  string
	lastEntry int
	byFile    map[string]int
	byName    map[string]int
	entries   []coveredLinesEntry
	ranges    []coveredLinesRange
}

type coveredLinesEntry struct {
	name       string
	maxEndLine int
}

type coveredLinesRange struct {
	entry, startLine, endLine int
}

func (c *coveredLines) add(block *coverProfileBlock) {
	if c.byFile == nil || block.fileName != c.lastFile {
		c.lastFile, c.lastEntry = block.fileName, c.entry(block.fileName)
	}
	entry := &c.entries[c.lastEntry]
	entry.maxEndLine = max(entry.maxEndLine, block.endLine)
	c.ranges = append(c.ranges, coveredLinesRange{entry: c.lastEntry, startLine: block.startLine, endLine: block.endLine})
}

func (c *coveredLines) entry(fileName string) int {
	if index, ok := c.byFile[fileName]; ok {
		return index
	}
	if c.byFile == nil {
		c.byFile, c.byName = make(map[string]int), make(map[string]int)
	}
	name := getRelativePathFromCITagsSourceRootForCoverage(fileName)
	index, ok := c.byName[name]
	if !ok {
		index = len(c.entries)
		c.entries = append(c.entries, coveredLinesEntry{name: name, maxEndLine: math.MinInt})
		c.byName[name] = index
	}
	c.byFile[fileName] = index
	return index
}

func (c *coveredLines) files(testFile string) []coveredFile {
	bitmaps := make([]*filebitmap.FileBitmap, len(c.entries))
	for i, entry := range c.entries {
		// A bitmap the size of its largest end line, as the SDK reached by
		// growing it block by block.
		bitmaps[i] = filebitmap.FromLineCount(max(entry.maxEndLine, 0))
	}
	for _, covered := range c.ranges {
		// The SDK panicked on a covered line below 1; it is skipped instead.
		for line := max(covered.startLine, 1); line <= covered.endLine; line++ {
			bitmaps[covered.entry].Set(line)
		}
	}
	order := make([]int, len(c.entries))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return strings.Compare(c.entries[a].name, c.entries[b].name) })
	result := make([]coveredFile, 0, 1+len(order))
	result = append(result, coveredFile{name: testFile})
	for _, i := range order {
		result = append(result, coveredFile{name: c.entries[i].name, bitmap: bitmaps[i].GetBuffer()})
	}
	return result
}

// getRelativePathFromCITagsSourceRootForCoverage returns the relative path from the CI tags source root for coverage
// by converting a module path to a module directory.
func getRelativePathFromCITagsSourceRootForCoverage(filePath string) string {
	resolveModuleInfo()
	return utils.GetRelativePathFromCITagsSourceRoot(strings.ReplaceAll(filePath, modulePath, moduleDir))
}

// getCoverageStatementsInfo parses the coverage profile data and returns the total statements and covered statements
func getCoverageStatementsInfo(filename string) (totalStatements, coveredStatements int, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip the header
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		_, blockInfo, err := splitCoverageProfileLine(line)
		if err != nil {
			continue
		}

		// Split the block info by space
		infoParts := strings.Fields(blockInfo)
		if len(infoParts) < 3 {
			continue
		}

		// Convert the number of statements and hit count
		numStmt, err1 := strconv.Atoi(infoParts[1])
		count, err2 := strconv.Atoi(infoParts[2])

		// Skip if any conversion failed
		if err1 != nil || err2 != nil {
			continue
		}

		// Update total statements
		totalStatements += numStmt

		// Update covered statements if the hit count is greater than 0
		if count > 0 {
			coveredStatements += numStmt
		}
	}

	return totalStatements, coveredStatements, scanner.Err()
}
