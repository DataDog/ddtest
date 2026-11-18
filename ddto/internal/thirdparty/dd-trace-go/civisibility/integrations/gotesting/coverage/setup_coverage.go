package coverage

import (
	"sync"

	"github.com/DataDog/ddtest/ddto/internal/localtia"
)

// Code that runs outside every test's coverage window is in no test's
// coverage, although tests depend on what it did: a package's
// initialization, TestMain before the tests, and work between two windows.
// ddto's local TIA adds its files to every test of the process. The
// collectors report it as follows:
//   - Go's counters in memory: the entries that grew since the previous
//     window, or since the process started, when a window begins;
//   - Go's counters in text profiles: the entries counted when the first
//     window begins;
//   - file marks: the files whose mark the time before a window raised, when
//     the window begins: a scan advances the epoch, so work after it raises
//     marks above what the window reported;
//   - context file coverage: the bits that goroutines set before the first
//     window began.
var outsideWindows struct {
	sync.Mutex
	// after are the in-memory counts when the last window ended, or nil.
	after []uint32
	// baseline is set once text profiles took the first window's baseline.
	baseline bool
	// first is set once context file coverage read the bits set before
	// the first window.
	first bool
}

// recordSetup is localtia.RecordSetup; tests replace it.
var recordSetup = localtia.RecordSetup

// recordOutsideWindows hands files to local TIA as the setup of every test.
func recordOutsideWindows(files []coveredFile) {
	if !localTIAEnabled {
		return
	}
	var setup []localtia.File
	for _, file := range files {
		if file.name != "" {
			setup = append(setup, localtia.File{Path: file.name})
		}
	}
	if len(setup) != 0 {
		recordSetup(setup)
	}
}

// countsOutsideWindows records the in-memory entries that grew since the
// last window ended, given the counts when a window begins.
func countsOutsideWindows(before []uint32) {
	if !localTIAEnabled {
		return
	}
	outsideWindows.Lock()
	var grown []int32
	for i, count := range before {
		if outsideWindows.after == nil && count > 0 || outsideWindows.after != nil && count > outsideWindows.after[i] {
			grown = append(grown, int32(i))
		}
	}
	outsideWindows.Unlock()
	if len(grown) != 0 {
		recordOutsideWindows((&memoryCollector{covered: grown}).files(""))
	}
}

// countsWindowEnded keeps the in-memory counts when a window ends.
func countsWindowEnded(after []uint32) {
	if !localTIAEnabled {
		return
	}
	outsideWindows.Lock()
	outsideWindows.after = after
	outsideWindows.Unlock()
}

// profileOutsideWindows records the entries that the text profile written
// when the first window begins counts.
func profileOutsideWindows(filename string) {
	if !localTIAEnabled {
		return
	}
	outsideWindows.Lock()
	first := !outsideWindows.baseline
	outsideWindows.baseline = true
	outsideWindows.Unlock()
	if !first {
		return
	}
	profile, err := readCoverProfile(filename)
	if err != nil {
		return
	}
	var counted coveredLines
	for i := range profile.blocks {
		if profile.blocks[i].count > 0 {
			counted.add(&profile.blocks[i])
		}
	}
	recordOutsideWindows(counted.files(""))
}

// marksOutsideWindows records the files whose mark reached raised, the value
// that sites set since the last scan, or since the process started, when a
// window begins.
func marksOutsideWindows(raised uint64) {
	if localTIAEnabled {
		recordOutsideWindows(fileCoverageFiles("", markedFiles(raised)))
	}
}
