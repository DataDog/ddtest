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

	split, lines := splitLongFiles(weights, durations, 40*time.Second)

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

	split, lines := splitLongFiles(map[string]int{"tests/test_two.py": 100_000}, durations, 10*time.Second)

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

func TestSplitLongFilesDoesNothingWithoutAThreshold(t *testing.T) {
	weights := map[string]int{"tests/test_long.py": 90_000}
	durations := testDurationsFile{Files: map[string]map[string]int64{"tests/test_long.py": {"a": 1, "b": 1}}}

	split, lines := splitLongFiles(weights, durations, 0)

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

func TestTestPlanner_Plan_GivesRunnersTheTestsOfSplitFiles(t *testing.T) {
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
	want := []string{"src/long.test.js::quick", "src/long.test.js::slow_one", "src/long.test.js::slow_two", "src/short.test.js"}
	if !slices.Equal(lines, want) {
		t.Errorf("runner lines = %v, want the long file's tests and the short file, each once", lines)
	}
}
