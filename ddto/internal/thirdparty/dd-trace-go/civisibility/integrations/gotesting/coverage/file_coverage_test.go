package coverage

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

// withFileCoverageBanks registers banks as instrumented packages do, and
// restores the process's banks, epoch and state afterwards.
func withFileCoverageBanks(t *testing.T, banks ...*fileCoverageBank) {
	t.Helper()
	head, epoch := atomic.LoadPointer(&fileCoverageBanksV1), atomic.LoadUint64(&fileCoverageEpoch)
	uploadEnabled, previousMode := coverageUploadEnabled, mode
	t.Cleanup(func() {
		atomic.StorePointer(&fileCoverageBanksV1, head)
		atomic.StoreUint64(&fileCoverageEpoch, epoch)
		coverageUploadEnabled, mode = uploadEnabled, previousMode
		fileCoverageState.once, fileCoverageState.available = sync.Once{}, false
	})
	atomic.StorePointer(&fileCoverageBanksV1, nil)
	for _, bank := range banks {
		bank.next = atomic.LoadPointer(&fileCoverageBanksV1)
		atomic.StorePointer(&fileCoverageBanksV1, unsafe.Pointer(bank))
	}
	fileCoverageState.once, fileCoverageState.available = sync.Once{}, false
	coverageUploadEnabled, mode = true, ""
}

func newFileCoverageBank(marks []uint64, names, always []string) *fileCoverageBank {
	bank := &fileCoverageBank{version: fileCoverageBankVersion, n: uintptr(len(names)), nAlways: uintptr(len(always))}
	if len(names) > 0 {
		bank.marks, bank.names = &marks[0], &names[0]
	}
	if len(always) > 0 {
		bank.always = &always[0]
	}
	return bank
}

func TestFileCoverageWindows(t *testing.T) {
	marksA, marksB := make([]uint64, 2), make([]uint64, 1)
	withFileCoverageBanks(t,
		newFileCoverageBank(marksA, []string{"/repo/a/one.go", "/repo/a/two.go"}, []string{"/repo/a/data.json"}),
		newFileCoverageBank(marksB, []string{"/repo/b/three.go"}, nil),
		newFileCoverageBank(nil, nil, []string{"/repo/c/cgo.go"}),
	)
	if !FileCoverageActive() {
		t.Fatal("file coverage is not active with banks")
	}
	hit := func(m *uint64) { atomic.StoreUint64(m, atomic.LoadUint64(&fileCoverageEpoch)) }

	first := beginFileCoverage()
	hit(&marksA[0])
	hit(&marksB[0])
	got := scanFileCoverage(first)
	slices.Sort(got)
	if want := []string{"/repo/a/data.json", "/repo/a/one.go", "/repo/b/three.go", "/repo/c/cgo.go"}; !slices.Equal(got, want) {
		t.Fatalf("first test: %v, want %v", got, want)
	}

	second := beginFileCoverage()
	hit(&marksA[1])
	got = scanFileCoverage(second)
	slices.Sort(got)
	if want := []string{"/repo/a/data.json", "/repo/a/two.go", "/repo/c/cgo.go"}; !slices.Equal(got, want) {
		t.Fatalf("second test: %v, want %v", got, want)
	}
}

func TestFileCoverageRows(t *testing.T) {
	got := fileCoverageFiles("pkg/x_test.go", []string{"z.go", "a.go", "pkg/x_test.go", "a.go"})
	var names []string
	for _, file := range got {
		if file.bitmap != nil {
			t.Fatalf("%s has a bitmap", file.name)
		}
		names = append(names, file.name)
	}
	if want := []string{"pkg/x_test.go", "a.go", "z.go"}; !slices.Equal(names, want) {
		t.Fatalf("rows %v, want %v", names, want)
	}
}

func TestFileCoverageRefusals(t *testing.T) {
	marks := make([]uint64, 1)
	t.Run("no banks", func(t *testing.T) {
		withFileCoverageBanks(t)
		if FileCoverageActive() {
			t.Fatal("active without banks")
		}
	})
	t.Run("unknown layout", func(t *testing.T) {
		other := newFileCoverageBank(marks, []string{"/repo/a.go"}, nil)
		other.version = fileCoverageBankVersion + 1
		withFileCoverageBanks(t, newFileCoverageBank(marks, []string{"/repo/b.go"}, nil), other)
		if FileCoverageActive() {
			t.Fatal("active with a bank of another layout")
		}
	})
	t.Run("go coverage", func(t *testing.T) {
		withFileCoverageBanks(t, newFileCoverageBank(marks, []string{"/repo/a.go"}, nil))
		mode = "atomic"
		if FileCoverageActive() {
			t.Fatal("active with Go's coverage")
		}
	})
	t.Run("upload off", func(t *testing.T) {
		withFileCoverageBanks(t, newFileCoverageBank(marks, []string{"/repo/a.go"}, nil))
		coverageUploadEnabled = false
		if FileCoverageActive() {
			t.Fatal("active without upload")
		}
	})
}

// A collector with file coverage takes its epoch before the test and its
// files at capture, so a later test's marks do not reach it.
func TestFileCoverageCollector(t *testing.T) {
	marks := make([]uint64, 2)
	withFileCoverageBanks(t, newFileCoverageBank(marks, []string{"/repo/a.go", "/repo/b.go"}, nil))
	collector := &testCoverage{testFile: "a_test.go"}
	if err := collector.collectCoverageBeforeTestExecution(); err != nil || collector.fileEpoch == 0 || collector.memory != nil {
		t.Fatalf("before: err %v, epoch %d, memory %v", err, collector.fileEpoch, collector.memory)
	}
	atomic.StoreUint64(&marks[0], collector.fileEpoch)
	if err := collector.collectCoverageAfterTestExecution(); err != nil {
		t.Fatal(err)
	}
	next := beginFileCoverage()
	atomic.StoreUint64(&marks[1], next)
	if !collector.loadCoverageData() {
		t.Fatal("no coverage data")
	}
	var names []string
	for _, file := range collector.filesCovered {
		names = append(names, file.name)
	}
	// Rows are relative to the CI source root, as the test file is.
	if want := []string{"a_test.go", utils.GetRelativePathFromCITagsSourceRoot("/repo/a.go")}; !slices.Equal(names, want) {
		t.Fatalf("rows %v, want %v", names, want)
	}
}
