package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/spf13/viper"
)

func TestSplitLongFilesSplitsOnlyLongFilesWhoseTestsAreListed(t *testing.T) {
	weights := map[string]int{
		"tests/test_long.py":     90_000,
		"tests/test_unlisted.py": 90_000,
		"tests/test_short.py":    10_000,
	}
	durations := testDurationsFile{Files: map[string]map[string]int64{
		"tests/test_long.py": {
			"test_a": int64(40 * time.Second), "test_b": int64(30 * time.Second),
			"test_c": int64(10 * time.Second), "test_d": int64(10 * time.Second),
		},
		"tests/test_short.py": {"test_x": int64(5 * time.Second), "test_y": int64(5 * time.Second)},
	}}

	split, lines, _ := splitLongFiles(weights, durations, 40*time.Second, lastPartListed)

	want := map[string]int{
		"tests/test_long.py[1/3]": 40_000,
		"tests/test_long.py[2/3]": 30_000,
		"tests/test_long.py[3/3]": 20_000,
		"tests/test_unlisted.py":  90_000,
		"tests/test_short.py":     10_000,
	}
	if !reflect.DeepEqual(split, want) {
		t.Errorf("split = %v, want %v", split, want)
	}
	wantLines := map[string][]string{
		"tests/test_long.py[1/3]": {"tests/test_long.py::test_a"},
		"tests/test_long.py[2/3]": {"tests/test_long.py::test_b"},
		"tests/test_long.py[3/3]": {"tests/test_long.py::test_c", "tests/test_long.py::test_d"},
	}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Errorf("lines = %v, want %v", lines, wantLines)
	}
}

func TestSplitLongFilesMakesAtMostOnePartPerTest(t *testing.T) {
	durations := testDurationsFile{Files: map[string]map[string]int64{
		"tests/test_two.py": {"test_a": 0, "test_b": 0},
	}}

	split, lines, _ := splitLongFiles(map[string]int{"tests/test_two.py": 100_000}, durations, 10*time.Second, lastPartListed)

	if len(split) != 2 || split["tests/test_two.py[1/2]"] != 50_000 || split["tests/test_two.py[2/2]"] != 50_000 {
		t.Errorf("split = %v, want two equal parts, dealt by count without durations", split)
	}
	var ids []string
	for _, part := range lines {
		ids = append(ids, part...)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"tests/test_two.py::test_a", "tests/test_two.py::test_b"}) {
		t.Errorf("test IDs = %v, want each test exactly once", ids)
	}
}

func TestSplitLongFilesAddsPartsUntilEachIsUnderTheThreshold(t *testing.T) {
	second := int64(time.Second)
	durations := testDurationsFile{Files: map[string]map[string]int64{
		"tests/test_three.py": {"test_a": 6 * second, "test_b": 6 * second, "test_c": 6 * second},
	}}

	// 18s under 10s makes two parts by count, but three tests of 6s can't
	// be balanced into two parts under 10s.
	split, _, _ := splitLongFiles(map[string]int{"tests/test_three.py": 18_000}, durations, 10*time.Second, lastPartComplement)

	if len(split) != 3 {
		t.Fatalf("split = %v, want three parts of 6s", split)
	}
	for unit, weight := range split {
		if weight > 10_000 {
			t.Errorf("%s weighs %dms, over the 10s threshold", unit, weight)
		}
	}
}

func TestSplitLongFilesLeavesALongTestAPartOfItsOwn(t *testing.T) {
	second := int64(time.Second)
	tests := map[string]int64{"test_long": 60 * second}
	for i := range 100 {
		tests[fmt.Sprintf("test_%03d", i)] = second / 10
	}
	durations := testDurationsFile{Files: map[string]map[string]int64{"tests/test_big.py": tests}}

	// 70s under 30s: the 60s test can't go under it, and doesn't make every
	// other test a part of its own either.
	split, _, _ := splitLongFiles(map[string]int{"tests/test_big.py": 70_000}, durations, 30*time.Second, lastPartComplement)

	if len(split) != 3 {
		t.Errorf("split into %d parts, want 3: the long test alone and the rest in two", len(split))
	}
}

func TestSplitLongFilesFindsThePartCountQuickly(t *testing.T) {
	// 10,000 tests of 60ms under 100ms: every count below 10,000 pairs two
	// tests somewhere, so a part per test is the answer.
	tests := make(map[string]int64, 10_000)
	for i := range 10_000 {
		tests[fmt.Sprintf("test_%05d", i)] = int64(60 * time.Millisecond)
	}
	durations := testDurationsFile{Files: map[string]map[string]int64{"tests/test_many.py": tests}}
	start := time.Now()

	split, _, _ := splitLongFiles(map[string]int{"tests/test_many.py": 600_000}, durations, 100*time.Millisecond, lastPartComplement)

	if len(split) != 10_000 {
		t.Errorf("split into %d parts, want 10000", len(split))
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("splitting took %s", elapsed)
	}
}

func TestSplitLongFilesDoesNothingWithoutAThreshold(t *testing.T) {
	weights := map[string]int{"tests/test_long.py": 90_000}
	durations := testDurationsFile{Files: map[string]map[string]int64{"tests/test_long.py": {"a": 1, "b": 1}}}

	split, lines, _ := splitLongFiles(weights, durations, 0, lastPartListed)

	if !reflect.DeepEqual(split, weights) || lines != nil {
		t.Errorf("split = %v, lines = %v, want the weights as they are", split, lines)
	}
}

func TestExpandSplitUnitsGivesRunnersTheTestIDsOfTheirParts(t *testing.T) {
	distribution := [][]string{{"tests/test_long.py[1/2]", "tests/test_other.py"}, {"tests/test_long.py[2/2]"}}
	lines := map[string][]string{
		"tests/test_long.py[1/2]": {"tests/test_long.py::test_a"},
		"tests/test_long.py[2/2]": {"tests/test_long.py::test_b", "tests/test_long.py::test_c"},
	}

	got := expandSplitUnits(distribution, lines)

	want := [][]string{
		{"tests/test_long.py::test_a", "tests/test_other.py"},
		{"tests/test_long.py::test_b", "tests/test_long.py::test_c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expanded = %v, want %v", got, want)
	}
}

func TestReadTestDurationsFileRejectsAFileWithoutFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durations.json")
	if err := os.WriteFile(path, []byte(`{"test_suites": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readTestDurationsFile(path); err == nil || !strings.Contains(err.Error(), "no files") {
		t.Errorf("error = %v, want the missing files", err)
	}
}

func TestTestPlanner_Plan_SplitsOnlyPytestFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	durationsPath := filepath.Join(tempDir, "test-durations.json")
	durations := `{"files": {"src/long.test.js": {"slow one": 60000000000, "slow two": 50000000000, "quick": 10000000000}}}`
	if err := os.WriteFile(durationsPath, []byte(durations), 0o644); err != nil {
		t.Fatal(err)
	}
	// settings.Init pins the durations it parses with viper.Set, which then
	// shadow the environment in later calls of the same process.
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "3")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "3")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TEST_DURATIONS_FILE", durationsPath)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_SPLIT_FILES_OVER", "60s")
	settings.Init()
	mockFramework := &MockFramework{
		FrameworkName:            "jest",
		TestFiles:                []string{"src/long.test.js", "src/short.test.js"},
		FullDiscoveryUnsupported: true,
	}
	mockPlatform := &MockPlatform{
		PlatformName: "javascript",
		Tags:         map[string]string{"language": "javascript"},
		Framework:    mockFramework,
		TestLevel:    settings.TestSkippingLevelSuite,
	}
	client := &MockTestOptimizationClient{
		Settings:   testOptimizationSettings(true, true, false),
		Skippables: api.Skippables{Suites: api.SkippableSuites{}},
		Durations: map[string]map[string]api.TestSuiteDurationInfo{
			"jest": {
				"src/long.test.js":  {SourceFile: "src/long.test.js", Duration: api.DurationPercentiles{P50: "120000000000"}},
				"src/short.test.js": {SourceFile: "src/short.test.js", Duration: api.DurationPercentiles{P50: "1000000000"}},
			},
		},
	}
	planner := NewWithDependencies(mockPlatform, mockFramework, client, newDefaultMockCIProviderDetector())

	if err := planner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	assertFileContent(t, constants.TestFilesOutputPath, "src/long.test.js\nsrc/short.test.js\n")
	var lines []string
	for runner := range 3 {
		data, err := os.ReadFile(filepath.Join(constants.TestsSplitDir, fmt.Sprintf("runner-%d", runner)))
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Fields(strings.ReplaceAll(string(data), " ", "_"))...)
	}
	slices.Sort(lines)
	want := []string{"src/long.test.js", "src/short.test.js"}
	if !slices.Equal(lines, want) {
		t.Errorf("runner lines = %v, want whole files: Jest's runner can't take a part's test IDs", lines)
	}
}

func TestSplitLongFilesMakesTheLastPartTheRestOfTheFile(t *testing.T) {
	weights := map[string]int{"tests/test_long.py": 90_000, "tests/test_short.py": 10_000}
	durations := testDurationsFile{Files: map[string]map[string]int64{
		"tests/test_long.py": {
			"test_a": int64(40 * time.Second), "test_b": int64(30 * time.Second),
			"test_c": int64(10 * time.Second), "test_d": int64(10 * time.Second),
		},
	}}

	split, lines, listed := splitLongFiles(weights, durations, 40*time.Second, lastPartComplement)

	want := map[string]int{
		"tests/test_long.py[1/3]": 40_000,
		"tests/test_long.py[2/3]": 30_000,
		"tests/test_long.py[3/3]": 20_000,
		"tests/test_short.py":     10_000,
	}
	if !reflect.DeepEqual(split, want) {
		t.Errorf("split = %v, want %v", split, want)
	}
	wantLines := map[string][]string{
		"tests/test_long.py[1/3]": {"tests/test_long.py::test_a"},
		"tests/test_long.py[2/3]": {"tests/test_long.py::test_b"},
		"tests/test_long.py[3/3]": {"tests/test_long.py"},
	}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Errorf("lines = %v, want %v", lines, wantLines)
	}
	wantListed := map[string][]string{"tests/test_long.py": {"tests/test_long.py::test_a", "tests/test_long.py::test_b"}}
	if !reflect.DeepEqual(listed, wantListed) {
		t.Errorf("listed = %v, want the tests of the other parts %v", listed, wantListed)
	}
}

func TestTestAncestors(t *testing.T) {
	for name, want := range map[string][]string{
		"test_f":               nil,
		"test_f[1]":            {"test_f"},
		"TestC::test_m":        {"TestC"},
		"TestC::test_m[a::b]":  {"TestC", "TestC::test_m"},
		"TestA::TestB::test_m": {"TestA", "TestA::TestB"},
		"test_f[x[1]]":         {"test_f"},
		"[odd]":                nil,
	} {
		if got := testAncestors(name); !slices.Equal(got, want) {
			t.Errorf("testAncestors(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCheckOneGranularity(t *testing.T) {
	for _, tt := range []struct {
		tests   map[string]int64
		wantErr string
	}{
		{tests: map[string]int64{"test_f": 1, "test_fa": 1, "TestC::test_m": 1, "TestC::test_m2": 1, "test_g[1]": 1, "test_g[2]": 1}},
		{tests: map[string]int64{"test_f": 1, "test_f[1]": 1}, wantErr: `as "test_f" and as "test_f[1]"`},
		{tests: map[string]int64{"TestC": 1, "TestC::test_m": 1}, wantErr: `as "TestC" and as "TestC::test_m"`},
		{tests: map[string]int64{"TestC::test_m": 1, "TestC::test_m[x]": 1}, wantErr: `as "TestC::test_m" and as "TestC::test_m[x]"`},
	} {
		err := checkOneGranularity(testDurationsFile{Files: map[string]map[string]int64{"tests/test_x.py": tt.tests}})
		if tt.wantErr == "" && err != nil {
			t.Errorf("tests %v: error = %v, want none", tt.tests, err)
		}
		if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("tests %v: error = %v, want one naming %s", tt.tests, err, tt.wantErr)
		}
	}
}

// planPytestWithSplitFiles plans a pytest suite with a long file for runners
// and returns their lines.
func planPytestWithSplitFiles(t *testing.T, splitFilesOver string, runners int, durations string) ([]string, error) {
	t.Helper()
	tempDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	durationsPath := filepath.Join(tempDir, "test-durations.json")
	if err := os.WriteFile(durationsPath, []byte(durations), 0o644); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", fmt.Sprint(runners))
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", fmt.Sprint(runners))
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TEST_DURATIONS_FILE", durationsPath)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_SPLIT_FILES_OVER", splitFilesOver)
	settings.Init()
	mockFramework := &MockFramework{
		FrameworkName:            "pytest",
		TestFiles:                []string{"tests/test_long.py", "tests/test_short.py"},
		FullDiscoveryUnsupported: true,
	}
	mockPlatform := &MockPlatform{
		PlatformName: "python",
		Tags:         map[string]string{"language": "python"},
		Framework:    mockFramework,
		TestLevel:    settings.TestSkippingLevelSuite,
	}
	client := &MockTestOptimizationClient{
		Settings:   testOptimizationSettings(true, true, false),
		Skippables: api.Skippables{Suites: api.SkippableSuites{}},
		Durations: map[string]map[string]api.TestSuiteDurationInfo{
			"pytest": {
				"tests/test_long.py":  {SourceFile: "tests/test_long.py", Duration: api.DurationPercentiles{P50: "120000000000"}},
				"tests/test_short.py": {SourceFile: "tests/test_short.py", Duration: api.DurationPercentiles{P50: "1000000000"}},
			},
		},
	}
	planner := NewWithDependencies(mockPlatform, mockFramework, client, newDefaultMockCIProviderDetector())
	if err := planner.Plan(context.Background()); err != nil {
		return nil, err
	}
	var lines []string
	for runner := range runners {
		data, err := os.ReadFile(filepath.Join(constants.TestsSplitDir, fmt.Sprintf("runner-%d", runner)))
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Fields(string(data))...)
	}
	slices.Sort(lines)
	return lines, nil
}

const pytestLongFileDurations = `{"files": {"tests/test_long.py": {"test_slow_one": 60000000000, "test_slow_two": 50000000000, "test_quick": 10000000000}}}`

func TestTestPlanner_Plan_GivesPytestTheRestOfSplitFiles(t *testing.T) {
	t.Chdir(t.TempDir())

	lines, err := planPytestWithSplitFiles(t, "60s", 3, pytestLongFileDurations)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	want := []string{"tests/test_long.py", "tests/test_long.py::test_slow_one", "tests/test_short.py"}
	if !slices.Equal(lines, want) {
		t.Errorf("runner lines = %v, want a part by test, the rest of the file and the short file", lines)
	}
	data, err := os.ReadFile(constants.SplitFileTestsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"tests/test_long.py":["tests/test_long.py::test_slow_one"]}`; got != want {
		t.Errorf("split file tests = %s, want %s", got, want)
	}

	// A later plan in the same directory that splits nothing removes them.
	if _, err := planPytestWithSplitFiles(t, "0s", 3, pytestLongFileDurations); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if _, err := os.Stat(constants.SplitFileTestsPath); !os.IsNotExist(err) {
		t.Errorf("split file tests of an earlier plan remain: %v", err)
	}
}

func TestTestPlanner_Plan_GivesASinglePytestRunnerWholeFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := planPytestWithSplitFiles(t, "60s", 3, pytestLongFileDurations); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// The runner reads a split file's path as its last part; a single runner
	// gets whole files, so a later plan must not leave the parts' tests.
	lines, err := planPytestWithSplitFiles(t, "60s", 1, pytestLongFileDurations)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if want := []string{"tests/test_long.py", "tests/test_short.py"}; !slices.Equal(lines, want) {
		t.Errorf("runner lines = %v, want %v", lines, want)
	}
	if _, err := os.Stat(constants.SplitFileTestsPath); !os.IsNotExist(err) {
		t.Errorf("a single runner's plan has split file tests: %v", err)
	}
}

func TestTestPlanner_Plan_RefusesPytestDurationsOfTwoGranularities(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := planPytestWithSplitFiles(t, "60s", 3, `{"files": {"tests/test_long.py": {"test_slow_one": 60000000000, "test_slow_one[2]": 50000000000}}}`)

	if err == nil || !strings.Contains(err.Error(), `as "test_slow_one" and as "test_slow_one[2]"`) {
		t.Fatalf("Plan() error = %v, want the two granularities", err)
	}
	if code := errcode.CodeOf(err); code != errcode.PlanTestDurationsFileInvalid {
		t.Errorf("error code = %v, want %v", code, errcode.PlanTestDurationsFileInvalid)
	}
}

func TestSplitPartFileReadsTheFileOfAPart(t *testing.T) {
	for unit, want := range map[string]string{
		splitPartName("tests/test_a.py", 2, 3): "tests/test_a.py",
		"tests/test_a.py":                      "tests/test_a.py",
		"tests/test_a.py[x/3]":                 "tests/test_a.py[x/3]",
		"tests/test_a.py[]":                    "tests/test_a.py[]",
	} {
		if got := splitPartFile(unit); got != want {
			t.Errorf("splitPartFile(%q) = %q, want %q", unit, got, want)
		}
	}
}
