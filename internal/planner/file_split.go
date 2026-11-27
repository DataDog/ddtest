package planner

import (
	"cmp"
	"container/heap"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
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

// checkOneGranularity refuses pytest durations in which a file lists a test
// together with a class or function it lies under, such as "test_f" and
// "test_f[1]", or "TestC" and "TestC::test_m": the two could land in
// different parts, and pytest would run the test in both.
func checkOneGranularity(durations testDurationsFile) error {
	files := make([]string, 0, len(durations.Files))
	for file := range durations.Files {
		files = append(files, file)
	}
	slices.Sort(files)
	for _, file := range files {
		tests := durations.Files[file]
		names := make([]string, 0, len(tests))
		for name := range tests {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			for _, ancestor := range testAncestors(name) {
				if _, ok := tests[ancestor]; ok {
					return fmt.Errorf("test durations file lists %s both as %q and as %q, which lies under it; list each file's tests at one level", file, ancestor, name)
				}
			}
		}
	}
	return nil
}

// testAncestors lists the classes and the function a pytest test name lies
// under, outermost first: "TestC::test_m[1]" lies under "TestC" and
// "TestC::test_m". Parameters end the name and may hold "::" themselves.
func testAncestors(name string) []string {
	var ancestors []string
	for i := 0; i < len(name); i++ {
		if name[i] == '[' {
			if i > 0 {
				ancestors = append(ancestors, name[:i])
			}
			break
		}
		if strings.HasPrefix(name[i:], "::") {
			ancestors = append(ancestors, name[:i])
			i++
		}
	}
	return ancestors
}

// lastPart says what the last part of a split file runs.
type lastPart int

const (
	// lastPartListed runs the tests durations lists for it, like the others.
	lastPartListed lastPart = iota
	// lastPartComplement runs the file minus the tests of its other parts, so
	// tests durations leaves out, such as new ones, still run once. The
	// runner needs the other parts' tests to exclude them: see
	// writeSplitFileTests.
	lastPartComplement
)

// splitLongFiles replaces each file expected to take longer than threshold,
// and whose tests durations lists, with parts of its tests: as many as it
// takes to bring each under threshold, at most one per test, balanced by the
// tests' durations. A part is a unit of the split with the file's name and
// its number, such as "tests/test_a.py[2/3]", weighted with its share of the
// file's weight, and lines maps it to the test IDs a runner passes the
// framework instead of the file.
//
// With lastPartListed, splitting relies on durations listing every test of
// the file: a test it leaves out runs nowhere, so the caller lists only files
// whose tests it knows. With lastPartComplement, the last part's line is the
// file itself, and listed maps the file to the test IDs of its other parts,
// which the runner of the last part excludes.
func splitLongFiles(weights map[string]int, durations testDurationsFile, threshold time.Duration, last lastPart) (split map[string]int, lines map[string][]string, listed map[string][]string) {
	thresholdMillis := int(threshold.Milliseconds())
	if thresholdMillis <= 0 {
		return weights, nil, nil
	}
	split = make(map[string]int, len(weights))
	lines = make(map[string][]string)
	listed = make(map[string][]string)
	for file, weight := range weights {
		tests := durations.Files[file]
		parts := min((weight+thresholdMillis-1)/thresholdMillis, len(tests))
		if weight <= thresholdMillis || parts < 2 {
			split[file] = weight
			continue
		}
		// Balancing by duration can leave a part over the threshold, such as
		// three tests of 6s in two parts of 12s and 6s under 10s: add parts
		// until each is under it or has one test: a test longer than the
		// threshold makes a part of its own.
		overweight := func(partition []testPart) bool {
			return slices.ContainsFunc(partition, func(p testPart) bool { return len(p.names) > 1 && p.weight(weight) > thresholdMillis })
		}
		partition := partitionTests(tests, parts)
		if overweight(partition) {
			// One part per test is never overweight: search the fewest
			// parts above parts that aren't.
			low, high, best := parts+1, len(tests), partitionTests(tests, len(tests))
			for low <= high {
				mid := low + (high-low)/2
				if candidate := partitionTests(tests, mid); overweight(candidate) {
					low = mid + 1
				} else {
					high, best = mid-1, candidate
				}
			}
			partition, parts = best, len(best)
		}
		for i, part := range partition {
			unit := splitPartName(file, i+1, parts)
			split[unit] = part.weight(weight)
			if last == lastPartComplement && i == parts-1 {
				lines[unit] = []string{file}
				continue
			}
			for _, name := range part.names {
				lines[unit] = append(lines[unit], file+"::"+name)
			}
			if last == lastPartComplement {
				listed[file] = append(listed[file], lines[unit]...)
			}
		}
	}
	return split, lines, listed
}

// splitPartName names part i of parts of a split file, such as
// "tests/test_a.py[2/3]", and splitPartFile reads the file back from it.
func splitPartName(file string, i, parts int) string {
	return fmt.Sprintf("%s[%d/%d]", file, i, parts)
}

// splitPartFile is the test file a unit of the distribution runs: the file a
// part names (see splitPartName), or the unit itself when it is a whole file.
func splitPartFile(unit string) string {
	open := strings.LastIndexByte(unit, '[')
	if open < 0 || !strings.HasSuffix(unit, "]") {
		return unit
	}
	part, parts, ok := strings.Cut(unit[open+1:len(unit)-1], "/")
	if !ok || !isDigits(part) || !isDigits(parts) {
		return unit
	}
	return unit[:open]
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
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
	lightest := &partHeap{parts: split, order: make([]int, parts)}
	for i := range lightest.order {
		lightest.order[i] = i
	}
	heap.Init(lightest)
	for _, name := range names {
		i := lightest.order[0]
		split[i].names = append(split[i].names, name)
		split[i].duration += max(tests[name], 0)
		heap.Fix(lightest, 0)
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
	// Only pytest's runs take the parts of a split file (see the split filter
	// plugin): other frameworks' runners would get test IDs as paths.
	if tp.framework == nil || tp.framework.Name() != "pytest" {
		framework := ""
		if tp.framework != nil {
			framework = tp.framework.Name()
		}
		slog.Warn("Only pytest runs parts of split test files; planning whole files", "framework", framework)
		return nil
	}
	if err := checkOneGranularity(durations); err != nil {
		return errcode.WithCode(errcode.PlanTestDurationsFileInvalid, err)
	}
	tp.testFileWeights, tp.splitLines, tp.splitListed = splitLongFiles(tp.testFileWeights, durations, threshold, lastPartComplement)
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

// writeSplitFileTests writes, for the runners of the last parts of split
// files, the test IDs of each file's other parts (see lastPartComplement),
// and removes the file a previous plan wrote when there are none. Runners
// read every split file's path among their lines as its last part, so the
// caller passes nil when it gives runners whole files instead of parts.
func writeSplitFileTests(listed map[string][]string) error {
	if len(listed) == 0 {
		if err := os.Remove(constants.SplitFileTestsPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove split file tests: %w", err)
		}
		return nil
	}
	for _, ids := range listed {
		slices.Sort(ids)
	}
	data, err := json.Marshal(listed)
	if err != nil {
		return fmt.Errorf("encode split file tests: %w", err)
	}
	return writePlanFile(constants.SplitFileTestsPath, data)
}

// partHeap orders the parts being dealt, the lightest first: by duration,
// then by number of tests, then by index.
type partHeap struct {
	parts []testPart
	order []int
}

func (h *partHeap) Len() int { return len(h.order) }

func (h *partHeap) Less(i, j int) bool {
	a, b := h.parts[h.order[i]], h.parts[h.order[j]]
	return cmp.Or(cmp.Compare(a.duration, b.duration), cmp.Compare(len(a.names), len(b.names)), cmp.Compare(h.order[i], h.order[j])) < 0
}

func (h *partHeap) Swap(i, j int) { h.order[i], h.order[j] = h.order[j], h.order[i] }

func (h *partHeap) Push(x any) { h.order = append(h.order, x.(int)) }

func (h *partHeap) Pop() any {
	last := h.order[len(h.order)-1]
	h.order = h.order[:len(h.order)-1]
	return last
}
