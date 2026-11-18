package localtia

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/log"
)

// packageDirectory is the directory that go test started this process in,
// its package's, read as the package initializes: before TestMain, which can
// change directory.
var packageDirectory, _ = os.Getwd()

// process is the test process's local TIA state. It is active when the CLI
// handed this process its Git state with a SelectionFile in it.
var process struct {
	once      sync.Once
	active    bool
	stateDir  string
	selection *PackageSelection // this package's, or nil
	// records names this process's Records file.
	records string

	mu       sync.Mutex
	coverage map[Test][]File
	status   map[Test]string
}

// Active reports whether local TIA runs in this test process.
func Active() bool {
	process.once.Do(load)
	return process.active
}

func load() {
	dir, record := utils.HandedCLIGitState()
	if record == nil {
		return
	}
	var selection Selection
	if err := utils.ReadCLIGitStateFile(dir, SelectionFile, &selection); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Debug("civisibility.tia: local TIA selection unavailable: %s", err.Error())
		}
		return
	}
	process.active, process.stateDir = true, dir
	own := filepath.Clean(packageDirectory)
	for i := range selection.Packages {
		if filepath.Clean(selection.Packages[i].Dir) == own {
			process.selection = &selection.Packages[i]
		}
	}
	skip := 0
	if process.selection != nil {
		skip = len(process.selection.Skip)
	}
	log.Debug("civisibility.tia: local Test Impact Analysis active [skippable:%d]", skip)
	// Until WriteRecords completes it, the file tells the CLI that this
	// process ran and has not finished.
	process.records = fmt.Sprintf("%s%d-%d.json", RecordsFilePrefix, os.Getpid(), time.Now().UnixNano())
	writeRecords(Records{Dir: packageDirectory, Tests: []TestRecord{}})
}

// writeRecords replaces this process's Records file.
func writeRecords(records Records) {
	data, err := json.Marshal(records)
	if err == nil {
		err = utils.WriteCLIStateFileUnlogged(process.stateDir, process.records, data)
	}
	if err != nil {
		log.Debug("civisibility.tia: local TIA records not written: %s", err.Error())
		return
	}
	log.Debug("civisibility.tia: local TIA records written [complete:%t tests:%d]", records.Complete, len(records.Tests))
}

// SkippableTestsResponse returns this package's selection as ITR's skippable
// tests, or nil when local TIA is not active. Each candidate names its
// module, so that a test of another package with the same file base name and
// test name never matches it.
func SkippableTestsResponse() *net.SkippableTestsResponse {
	if !Active() {
		return nil
	}
	response := &net.SkippableTestsResponse{Skippables: map[string]map[string][]net.SkippableResponseDataAttributes{}}
	selected := process.selection
	if selected == nil {
		response.CoverageBackfillReason = "no test to skip"
		return response
	}
	for _, test := range selected.Skip {
		tests := response.Skippables[test.Suite]
		if tests == nil {
			tests = map[string][]net.SkippableResponseDataAttributes{}
			response.Skippables[test.Suite] = tests
		}
		tests[test.Name] = append(tests[test.Name], net.SkippableResponseDataAttributes{Suite: test.Suite, Name: test.Name, Module: test.Module})
	}
	response.ResponseTestsCount = len(selected.Skip)
	if len(selected.Coverage) == 0 {
		response.CoverageBackfillReason = "no line coverage"
		return response
	}
	response.Coverage = make(map[string]*filebitmap.FileBitmap, len(selected.Coverage))
	for file, bitmap := range selected.Coverage {
		response.Coverage[file] = filebitmap.NewFileBitmapFromBytes(bitmap)
	}
	response.CoveragePresent, response.CoverageBackfillSafe = true, true
	return response
}

// RecordCoverage records the files that a top-level test's execution
// covered. With -count, a test runs more than once and keeps every file.
func RecordCoverage(test Test, files []File) {
	if !Active() {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.coverage == nil {
		process.coverage = map[Test][]File{}
	}
	process.coverage[test] = mergeFiles(process.coverage[test], files)
}

// mergeFiles returns the files of both lists, each once, with their bitmaps
// combined.
func mergeFiles(into, files []File) []File {
	if into == nil {
		return files
	}
	for _, file := range files {
		i := slices.IndexFunc(into, func(f File) bool { return f.Path == file.Path })
		switch {
		case i < 0:
			into = append(into, file)
		case len(file.Bitmap) != 0:
			merged := filebitmap.Or(filebitmap.NewFileBitmapFromBytes(into[i].Bitmap), filebitmap.NewFileBitmapFromBytes(file.Bitmap), false)
			into[i].Bitmap = merged.GetBuffer()
		}
	}
	return into
}

// RecordStatus records a test's final status, after its retries. Subtests,
// whose names have a slash, are never skipped and are not recorded. A test
// that -count runs again keeps a failure.
func RecordStatus(test Test, status string) {
	if strings.Contains(test.Name, "/") || !Active() {
		return
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.status == nil {
		process.status = map[Test]string{}
	}
	if process.status[test] != "fail" {
		process.status[test] = status
	}
}

// WriteRecords completes this process's Records file in the CLI's state,
// without recording it in Go's test log, with the tests that finished with
// coverage. It runs when CI Visibility closes: the file of a process that
// crashes before stays incomplete, and the CLI then forgets its package's
// tests, which run again.
func WriteRecords() {
	if !Active() {
		return
	}
	process.mu.Lock()
	records := Records{Dir: packageDirectory, Complete: true, Tests: []TestRecord{}}
	for test, files := range process.coverage {
		if status, ok := process.status[test]; ok {
			records.Tests = append(records.Tests, TestRecord{Test: test, Status: status, Files: files})
		}
	}
	process.mu.Unlock()
	writeRecords(records)
}
