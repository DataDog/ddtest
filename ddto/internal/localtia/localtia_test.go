package localtia

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
)

func TestRepositoryPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		separator byte
		want      string
		ok        bool
	}{
		{"pkg/a.go", '/', "pkg/a.go", true},
		{"./pkg//b/../a.go", '/', "pkg/a.go", true},
		{"/repo/pkg/a.go", '/', "", false},
		{"../other/a.go", '/', "", false},
		{"..", '/', "", false},
		{".", '/', "", false},
		{"", '/', "", false},
		// Elsewhere a backslash belongs to the file name.
		{`pkg\a.go`, '/', `pkg\a.go`, true},
		{`pkg\sub\a.go`, '\\', "pkg/sub/a.go", true},
		{`..\other\a.go`, '\\', "", false},
		{`C:\repo\a.go`, '\\', "", false},
		{`\repo\a.go`, '\\', "", false},
	} {
		got, ok := repositoryPath(tc.name, tc.separator)
		if got != tc.want || ok != tc.ok {
			t.Errorf("repositoryPath(%q, %q) = %q, %t; want %q, %t", tc.name, tc.separator, got, ok, tc.want, tc.ok)
		}
	}
}

// activate makes this process act as a test process that the CLI handed
// state and selection.
func activate(t *testing.T, selection *PackageSelection) string {
	t.Helper()
	state := t.TempDir()
	process.once.Do(func() {})
	process.active, process.stateDir, process.selection, process.records, process.record = true, state, selection, RecordsFilePrefix+"test.json", true
	process.coverage, process.status = nil, nil
	t.Cleanup(func() {
		process.once = sync.Once{}
		process.active, process.stateDir, process.selection, process.records, process.record = false, "", nil, "", false
		process.coverage, process.status, process.setup, process.failed, process.uncovered = nil, nil, nil, nil, nil
	})
	return state
}

func TestSkippableTestsResponse(t *testing.T) {
	line := filebitmap.FromLineCount(8)
	line.Set(3)
	activate(t, &PackageSelection{
		Dir:            packageDirectory,
		Skip:           []Test{{Module: "example.com/a", Suite: "a_test.go", Name: "TestA"}, {Module: "example.com/a_test", Suite: "x_test.go", Name: "TestX"}},
		Coverage:       map[string][]byte{"a/a.go": line.GetBuffer()},
		NoLineCoverage: []Test{{Module: "example.com/a_test", Suite: "x_test.go", Name: "TestX"}},
	})
	response := SkippableTestsResponse()
	candidates := response.Skippables["a_test.go"]["TestA"]
	if len(candidates) != 1 || candidates[0].Module != "example.com/a" || candidates[0].Name != "TestA" {
		t.Fatalf("TestA candidates %+v", candidates)
	}
	if candidates[0].MissingLineCodeCoverage {
		t.Fatal("TestA has line coverage")
	}
	if got := response.Skippables["x_test.go"]["TestX"]; len(got) != 1 || got[0].Module != "example.com/a_test" || !got[0].MissingLineCodeCoverage {
		t.Fatalf("TestX candidates %+v", got)
	}
	if !response.CoveragePresent || !response.CoverageBackfillSafe || !response.Coverage["a/a.go"].Get(3) {
		t.Fatalf("coverage %+v", response)
	}

	// A package without a selection skips nothing.
	activate(t, nil)
	if response := SkippableTestsResponse(); response == nil || len(response.Skippables) != 0 || response.CoveragePresent {
		t.Fatalf("response without a selection %+v", response)
	}
}

// Only top-level tests that finished with coverage are written, with every
// file of each of their executions and a failure that -count repeats.
func TestWriteRecords(t *testing.T) {
	state := activate(t, nil)
	first, second := filebitmap.FromLineCount(8), filebitmap.FromLineCount(8)
	first.Set(1)
	second.Set(5)
	passed := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestA"}
	flaky := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestFlaky"}
	RecordCoverage(passed, []File{{Path: "a/a_test.go"}, {Path: "a/a.go", Bitmap: first.GetBuffer()}})
	RecordCoverage(passed, []File{{Path: "a/a.go", Bitmap: second.GetBuffer()}, {Path: "a/b.go"}})
	RecordStatus(passed, "pass")
	RecordCoverage(flaky, []File{{Path: "a/a_test.go"}})
	RecordStatus(flaky, "fail")
	RecordStatus(flaky, "pass")
	RecordStatus(Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestA/sub"}, "pass")
	RecordStatus(Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestNoCoverage"}, "pass")
	WriteRecords()

	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), RecordsFilePrefix) {
		t.Fatalf("state %v: %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(state, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var records Records
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if records.Dir != packageDirectory || !records.Complete || len(records.Tests) != 2 {
		t.Fatalf("records %+v", records)
	}
	slices.SortFunc(records.Tests, func(a, b TestRecord) int { return strings.Compare(a.Name, b.Name) })
	a, f := records.Tests[0], records.Tests[1]
	if a.Test != passed || a.Status != "pass" || len(a.Files) != 3 {
		t.Fatalf("TestA %+v", a)
	}
	merged := filebitmap.NewFileBitmapFromBytes(a.Files[1].Bitmap)
	if a.Files[1].Path != "a/a.go" || !merged.Get(1) || !merged.Get(5) {
		t.Fatalf("TestA's bitmaps were not combined: %+v", a.Files)
	}
	if f.Test != flaky || f.Status != "fail" {
		t.Fatalf("TestFlaky %+v", f)
	}
}

// A process that only skips records nothing, and still skips.
func TestReadOnlyRecordsNothing(t *testing.T) {
	state := activate(t, &PackageSelection{Dir: packageDirectory, Skip: []Test{{Module: "example.com/a", Suite: "a_test.go", Name: "TestA"}}})
	process.record = false
	if Recording() {
		t.Fatal("recording")
	}
	test := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestB"}
	RecordCoverage(test, []File{{Path: "a/a.go"}})
	RecordStatus(test, "pass")
	WriteRecords()
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 0 {
		t.Fatalf("state %v: %v", entries, err)
	}
	if process.coverage != nil || process.status != nil {
		t.Fatalf("recorded %v %v", process.coverage, process.status)
	}
	if response := SkippableTestsResponse(); len(response.Skippables["a_test.go"]["TestA"]) != 1 {
		t.Fatalf("response %+v", response)
	}
}

// The files of code that ran outside every test's window join every test.
func TestRecordSetup(t *testing.T) {
	state := activate(t, nil)
	test := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestA"}
	RecordCoverage(test, []File{{Path: "a/a_test.go"}})
	RecordStatus(test, "pass")
	RecordSetup([]File{{Path: "lib/init.go"}})
	RecordSetup([]File{{Path: "lib/init.go"}, {Path: "lib/main.go"}})
	WriteRecords()
	data, err := os.ReadFile(filepath.Join(state, process.records))
	if err != nil {
		t.Fatal(err)
	}
	var records Records
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range records.Tests[0].Files {
		paths = append(paths, file.Path)
	}
	if !slices.Equal(paths, []string{"a/a_test.go", "lib/init.go", "lib/main.go"}) {
		t.Fatalf("files %q", paths)
	}
}

// A test that passed after an execution that failed records as flaky.
// A test with an execution whose coverage was not collected, such as an
// Early Flake Detection one, is recorded as repeated, and not as a pass;
// a failed execution still makes it flaky, and a failure stays one.
func TestRecordUncovered(t *testing.T) {
	state := activate(t, nil)
	repeated := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestRepeated"}
	flaky := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestFlaky"}
	failed := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestFailed"}
	once := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestOnce"}
	for _, test := range []Test{repeated, flaky, failed, once} {
		RecordCoverage(test, []File{{Path: "a/a_test.go"}})
	}
	for _, test := range []Test{repeated, flaky, failed} {
		RecordUncovered(test)
	}
	RecordUncovered(Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestOnce/sub"})
	RecordFailure(flaky)
	RecordStatus(repeated, "pass")
	RecordStatus(flaky, "pass")
	RecordStatus(failed, "fail")
	RecordStatus(once, "pass")
	WriteRecords()
	data, err := os.ReadFile(filepath.Join(state, process.records))
	if err != nil {
		t.Fatal(err)
	}
	var records Records
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, record := range records.Tests {
		got[record.Test.Name] = record.Status
	}
	want := map[string]string{"TestRepeated": "repeated", "TestFlaky": "flaky", "TestFailed": "fail", "TestOnce": "pass"}
	if !maps.Equal(got, want) {
		t.Fatalf("statuses %v, want %v", got, want)
	}
}

func TestRecordFailure(t *testing.T) {
	state := activate(t, nil)
	test := Test{Module: "example.com/a", Suite: "a_test.go", Name: "TestRetried"}
	RecordCoverage(test, []File{{Path: "a/a_test.go"}})
	RecordFailure(test)
	RecordStatus(test, "pass")
	WriteRecords()
	data, err := os.ReadFile(filepath.Join(state, process.records))
	if err != nil {
		t.Fatal(err)
	}
	var records Records
	if err := json.Unmarshal(data, &records); err != nil || records.Tests[0].Status != "flaky" {
		t.Fatalf("records %+v: %v", records, err)
	}
}
