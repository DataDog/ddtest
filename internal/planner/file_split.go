package planner

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/errcode"
	"github.com/DataDog/ddtest/internal/settings"
)

// testDurationsFile is the --test-durations-file shape: the duration of every
// test of each file it names, in nanoseconds, by test name as the framework
// selects it after the file path and "::", such as a pytest function name.
type testDurationsFile struct {
	Files map[string]map[string]int64 `json:"files"`
}

func readTestDurationsFile(path string) (testDurationsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return testDurationsFile{}, fmt.Errorf("read test durations file: %w", err)
	}
	var durations testDurationsFile
	if err := json.Unmarshal(data, &durations); err != nil {
		return testDurationsFile{}, fmt.Errorf("parse test durations file %s: %w", path, err)
	}
	if durations.Files == nil {
		return testDurationsFile{}, fmt.Errorf("test durations file %s has no files", path)
	}
	return durations, nil
}

// splitLongFiles replaces each file expected to take longer than threshold,
// and whose tests durations lists, with parts of its tests: as many as it
// takes to bring each under threshold, at most one per test, balanced by the
// tests' durations. A part is a unit of the split with the file's name and
// its number, such as "tests/test_a.py[2/3]", weighted with its share of the
// file's weight, and lines maps it to the test IDs a runner passes the
// framework instead of the file. Splitting relies on durations listing every
// test of the file: a test it leaves out runs nowhere, so the caller lists
// only files whose tests it knows, such as files unchanged since it
// discovered them.
func splitLongFiles(weights map[string]int, durations testDurationsFile, threshold time.Duration) (map[string]int, map[string][]string) {
	thresholdMillis := int(threshold.Milliseconds())
	if thresholdMillis <= 0 {
		return weights, nil
	}
	split := make(map[string]int, len(weights))
	lines := make(map[string][]string)
	for file, weight := range weights {
		tests := durations.Files[file]
		parts := min((weight+thresholdMillis-1)/thresholdMillis, len(tests))
		if weight <= thresholdMillis || parts < 2 {
			split[file] = weight
			continue
		}
		for i, part := range partitionTests(tests, parts) {
			unit := fmt.Sprintf("%s[%d/%d]", file, i+1, parts)
			split[unit] = part.weight(weight)
			for _, name := range part.names {
				lines[unit] = append(lines[unit], file+"::"+name)
			}
		}
	}
	return split, lines
}

type testPart struct {
	names    []string
	duration int64
	// share is the part's fraction of the file's tests' total duration.
	share float64
}

// weight is the part's share of the file's weight, at least 1 ms.
func (p testPart) weight(fileWeight int) int {
	return max(1, int(math.Round(float64(fileWeight)*p.share)))
}

// partitionTests deals tests into parts, longest first, each to the part
// with the least duration so far, and then sorts each part's names. Tests
// without a duration are dealt by count.
func partitionTests(tests map[string]int64, parts int) []testPart {
	names := make([]string, 0, len(tests))
	var total int64
	for name, duration := range tests {
		names = append(names, name)
		total += max(duration, 0)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(tests[b], tests[a]), cmp.Compare(a, b))
	})
	split := make([]testPart, parts)
	for _, name := range names {
		lightest := 0
		for i := range split {
			if split[i].duration < split[lightest].duration ||
				(split[i].duration == split[lightest].duration && len(split[i].names) < len(split[lightest].names)) {
				lightest = i
			}
		}
		split[lightest].names = append(split[lightest].names, name)
		split[lightest].duration += max(tests[name], 0)
	}
	for i := range split {
		slices.Sort(split[i].names)
		if total > 0 {
			split[i].share = float64(split[i].duration) / float64(total)
		} else {
			split[i].share = float64(len(split[i].names)) / float64(len(names))
		}
	}
	return split
}

// splitLongTestFiles splits the long files of the plan by test when
// --split-files-over and --test-durations-file are set.
func (tp *TestPlanner) splitLongTestFiles() error {
	threshold, path := settings.GetSplitFilesOver(), settings.GetTestDurationsFile()
	if threshold <= 0 || path == "" {
		return nil
	}
	durations, err := readTestDurationsFile(path)
	if err != nil {
		return errcode.WithCode(errcode.PlanTestDurationsFileInvalid, err)
	}
	tp.testFileWeights, tp.splitLines = splitLongFiles(tp.testFileWeights, durations, threshold)
	for unit, ids := range tp.splitLines {
		slog.Info("Split a long test file by test", "part", unit, "tests", len(ids), "estimatedRuntime", time.Duration(tp.testFileWeights[unit])*time.Millisecond)
	}
	return nil
}

// expandSplitUnits replaces the parts of split files in each runner's list
// with their test IDs.
func expandSplitUnits(distribution [][]string, lines map[string][]string) [][]string {
	if len(lines) == 0 {
		return distribution
	}
	expanded := make([][]string, len(distribution))
	for i, units := range distribution {
		for _, unit := range units {
			if ids, ok := lines[unit]; ok {
				expanded[i] = append(expanded[i], ids...)
			} else {
				expanded[i] = append(expanded[i], unit)
			}
		}
	}
	return expanded
}
