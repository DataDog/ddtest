package coverage

import (
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// File coverage is ddto's --experimental-file-coverage. Instead of Go's
// coverage counters, packages that ddto instruments keep one mark per source
// file, which a call before every statement raises to fileCoverageEpoch. Each
// test that collects coverage advances the epoch when it starts, and reports
// the files whose mark reached its epoch when its coverage is captured, with
// the files that every test reports. Rows carry file names without line
// bitmaps.
//
// Instrumented packages register their banks while they initialize, before
// this package does: they only read and write these variables through
// linkname, and never call Mini. The variables have no initializer, which
// would run after those registrations.
var (
	fileCoverageEpoch   uint64
	fileCoverageBanksV1 unsafe.Pointer
)

// fileCoverageBankVersion is the layout of fileCoverageBank, which ddto's
// generated code repeats (instrument.FileCoverageBankVersion). The list's
// name carries it too: ddto checks for the declaration before it emits banks
// that this runtime would not read.
const fileCoverageBankVersion = 1

// fileCoverageBank is one package variant's record. marks and names point to
// the first of n entries, always to the first of nAlways.
type fileCoverageBank struct {
	next    unsafe.Pointer
	version uintptr
	marks   *uint64
	names   *string
	n       uintptr
	always  *string
	nAlways uintptr
}

var fileCoverageState struct {
	once      sync.Once
	available bool
}

// fileCoverageAvailable reports registered banks of the known layout, of
// marks or of contexts. A bank of another layout turns file coverage off: its
// files would be missing.
func fileCoverageAvailable() bool {
	return markCoverageAvailable() || contextCoverageAvailable()
}

func markCoverageAvailable() bool {
	fileCoverageState.once.Do(func() {
		banks, files := 0, 0
		for p := atomic.LoadPointer(&fileCoverageBanksV1); p != nil; p = (*fileCoverageBank)(p).next {
			bank := (*fileCoverageBank)(p)
			if bank.version != fileCoverageBankVersion {
				log.Debug("civisibility.cov: file coverage off: bank layout %d, want %d", bank.version, fileCoverageBankVersion)
				return
			}
			banks++
			files += int(bank.n + bank.nAlways)
		}
		fileCoverageState.available = banks > 0
		if banks > 0 {
			log.Debug("civisibility.cov: per-test file coverage: true [banks:%d files:%d]", banks, files)
		}
	})
	return fileCoverageState.available
}

// FileCoverageActive reports whether per-test coverage comes from file marks:
// the build has them, Go's coverage is off, and coverage is uploaded.
func FileCoverageActive() bool {
	return coverageUploadEnabled && mode == "" && fileCoverageAvailable()
}

// FileCoverageParallel reports whether tests keep t.Parallel while file
// coverage is collected: context file coverage tells their files apart. The
// banks of the test binary decide, so a binary built with -c keeps it too.
func FileCoverageParallel() bool {
	return FileCoverageActive() && contextCoverageAvailable()
}

// beginFileCoverage starts a test's window and returns its epoch.
func beginFileCoverage() uint64 {
	if contextCoverageAvailable() {
		return beginContextCoverage()
	}
	return atomic.AddUint64(&fileCoverageEpoch, 1)
}

// scanFileCoverage returns the files whose mark reached epoch and the files
// that every test reports. It runs when the test's coverage is captured: a
// later scan would also see the marks of tests that start afterwards.
func scanFileCoverage(epoch uint64) []string {
	if contextCoverageAvailable() {
		return scanContextCoverage(epoch)
	}
	var names []string
	for p := atomic.LoadPointer(&fileCoverageBanksV1); p != nil; p = (*fileCoverageBank)(p).next {
		bank := (*fileCoverageBank)(p)
		if bank.n > 0 {
			marks := unsafe.Slice(bank.marks, bank.n)
			files := unsafe.Slice(bank.names, bank.n)
			for i := range marks {
				if atomic.LoadUint64(&marks[i]) >= epoch {
					names = append(names, files[i])
				}
			}
		}
		if bank.nAlways > 0 {
			names = append(names, unsafe.Slice(bank.always, bank.nAlways)...)
		}
	}
	return names
}

// fileCoverageFiles returns a test's rows: its own file first, as with Go's
// counters, then the other files in name order, each once, without bitmaps.
func fileCoverageFiles(testFile string, names []string) []coveredFile {
	relative := make([]string, 0, len(names))
	for _, name := range names {
		if name = coverageSourceFile(name); name != testFile {
			relative = append(relative, name)
		}
	}
	slices.Sort(relative)
	relative = slices.Compact(relative)
	files := make([]coveredFile, 0, 1+len(relative))
	files = append(files, coveredFile{name: testFile})
	for _, name := range relative {
		files = append(files, coveredFile{name: name})
	}
	return files
}
