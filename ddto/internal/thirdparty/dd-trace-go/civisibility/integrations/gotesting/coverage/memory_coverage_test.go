// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

type fakeUnit [6]uint32 // StLine, StCol, EnLine, EnCol, NxStmts, Parent

type fakeFunc struct {
	name, file string
	lit        bool
	units      []fakeUnit
}

type fakePackage struct {
	mode, granularity, path string
	funcs                   []fakeFunc
}

// fakeMeta encodes packages as the hook's meta does, with the runtime's
// counter segment offsets.
func fakeMeta(pkgs []fakePackage) ([]string, []uint32) {
	var strs []string
	index := map[string]uint32{}
	str := func(s string) uint32 {
		i, ok := index[s]
		if !ok {
			i = uint32(len(strs))
			index[s] = i
			strs = append(strs, s)
		}
		return i
	}
	records := []uint32{0, 1, 2, 3, uint32(len(pkgs))}
	for _, p := range pkgs {
		records = append(records, str(p.mode), str(p.granularity), str(p.path), uint32(len(p.funcs)))
		for _, f := range p.funcs {
			lit := uint32(0)
			if f.lit {
				lit = 1
			}
			records = append(records, str(f.name), str(f.file), lit, uint32(len(f.units)))
			for _, u := range f.units {
				records = append(records, u[:]...)
			}
		}
	}
	return strs, records
}

// fakeSegments copies counters as the hook's counters does: one segment per
// package, holding each function that has run as its header and counters,
// and zeros for a function that has not (nil).
func fakeSegments(counters [][][]uint32) []uint32 {
	var raw []uint32
	for slot, funcs := range counters {
		var segment []uint32
		for fn, ctrs := range funcs {
			if ctrs == nil {
				segment = append(segment, 0, 0, 0, 0)
				continue
			}
			segment = append(segment, uint32(len(ctrs)), uint32(slot+1), uint32(fn))
			segment = append(segment, ctrs...)
		}
		raw = append(append(raw, uint32(len(segment))), segment...)
	}
	return raw
}

// memoryPackages registers b before a, which sorts first; a's slots repeat
// unit 3.2,5.3 of First, which merges. Units with a parent and a
// registration-only package have no blocks.
var memoryPackages = []fakePackage{
	{"atomic", "perblock", "example.com/b", []fakeFunc{
		{"B", "example.com/b/b.go", false, []fakeUnit{{4, 1, 6, 10, 3, 0}}},
	}},
	{"atomic", "perblock", "example.com/a", []fakeFunc{
		{"Second", "example.com/a/a.go", false, []fakeUnit{{7, 1, 9, 2, 1, 0}, {8, 1, 8, 4, 1, 1}}},
		{"First", "example.com/a/a.go", false, []fakeUnit{{3, 2, 5, 3, 2, 0}}},
	}},
	{"regonly", "perblock", "example.com/c", nil},
	{"atomic", "perblock", "example.com/a", []fakeFunc{
		{"First", "example.com/a/a.go", false, []fakeUnit{{3, 2, 5, 3, 2, 0}}},
	}},
}

// memoryText is the text profile that cformat renders for memoryPackages
// with every count at its maximum.
const memoryText = "mode: atomic\n" +
	"example.com/a/a.go:3.2,5.3 2 4294967295\n" +
	"example.com/a/a.go:7.1,9.2 1 4294967295\n" +
	"example.com/b/b.go:4.1,6.10 3 4294967295\n"

// fakeTestingCoverage registers a hook over packages, with counters that the
// test sets per package slot and function.
type fakeTestingCoverage struct {
	pkgs     []fakePackage
	text     string
	refuse   string
	counters [][][]uint32
	slots    int
}

func (f *fakeTestingCoverage) register() {
	registerTestingCoverage(
		func() ([]string, []uint32, string) {
			strs, records := fakeMeta(f.pkgs)
			return strs, records, f.refuse
		},
		func(dst []uint32) ([]uint32, int) { return append(dst[:0], fakeSegments(f.counters)...), f.slots },
		func([]uint32) []byte { return []byte(f.text) },
	)
}

// renameFile renames a file in the packages and the text profile.
func (f *fakeTestingCoverage) renameFile(from, to string) {
	pkgs := slices.Clone(f.pkgs)
	for i := range pkgs {
		pkgs[i].funcs = slices.Clone(pkgs[i].funcs)
		for j := range pkgs[i].funcs {
			if pkgs[i].funcs[j].file == from {
				pkgs[i].funcs[j].file = to
			}
		}
	}
	f.pkgs = pkgs
	f.text = strings.ReplaceAll(f.text, from+":", to+":")
}

func setUpMemoryCoverage(t *testing.T) *fakeTestingCoverage {
	t.Helper()
	if strconv.IntSize < 64 {
		t.Skip("32-bit platforms keep text profiles: TestMemoryCoverageNeeds64BitCounts")
	}
	return fakeMemoryCoverage(t)
}

// fakeMemoryCoverage is setUpMemoryCoverage on any platform.
func fakeMemoryCoverage(t *testing.T) *fakeTestingCoverage {
	t.Helper()
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	mode = "atomic"
	f := &fakeTestingCoverage{
		pkgs:     memoryPackages,
		text:     memoryText,
		counters: [][][]uint32{{nil}, {nil, nil}, {}, {nil}},
		slots:    len(memoryPackages),
	}
	f.register()
	return f
}

// The layout holds the text profile's blocks in its order, and maps each
// unit to its block.
func TestBuildCoverageLayout(t *testing.T) {
	fakeMemoryCoverage(t)
	strs, records := fakeMeta(memoryPackages)
	layout, refusal := buildCoverageLayout(strs, records, "atomic")
	require.Empty(t, refusal)
	require.Equal(t, [4]int{0, 1, 2, 3}, layout.offsets)
	require.Equal(t, 4, layout.slots)
	require.Equal(t, []string{"example.com/a/a.go", "example.com/b/b.go"}, layout.files)
	require.Equal(t, []uint32{0, 3, 2, 5, 3, 2, 0, 7, 1, 9, 2, 1, 1, 4, 1, 6, 10, 3}, layout.entries)
	// b.B; a.Second's units, the second with a parent; a.First twice.
	require.Equal(t, []int32{2, 1, -1, 0, 0}, layout.units)
	require.Equal(t, [][]layoutFunc{{{0, 1}}, {{1, 2}, {3, 1}}, {}, {{4, 1}}}, layout.funcs)
	// On 32-bit platforms, text profiles cannot read the maximum count.
	require.Equal(t, strconv.IntSize == 64, layout.matchesTextProfile([]byte(memoryText)))

	// Units of other functions or literals at the same position are distinct
	// blocks, which share a position.
	for name, other := range map[string]fakeFunc{
		"function": {"Other", "example.com/a/a.go", false, []fakeUnit{{3, 2, 5, 3, 2, 0}}},
		"literal":  {"First", "example.com/a/a.go", true, []fakeUnit{{3, 2, 5, 3, 2, 0}}},
	} {
		strs, records := fakeMeta(append(slices.Clone(memoryPackages), fakePackage{"atomic", "perblock", "example.com/a", []fakeFunc{other}}))
		layout, refusal := buildCoverageLayout(strs, records, "atomic")
		require.Nil(t, layout, name)
		require.Equal(t, "blocks share a position", refusal, name)
	}
}

// Blocks are sorted as cformat sorts them: by file, then start line, end
// line, start column and end column. Blocks share a position only when every
// coordinate and the file match.
func TestBuildCoverageLayoutOrder(t *testing.T) {
	strs, records := fakeMeta([]fakePackage{{"atomic", "perblock", "example.com/s", []fakeFunc{
		{"Z", "example.com/s/z.go", false, []fakeUnit{{3, 5, 4, 1, 8, 0}}},
		{"Y", "example.com/s/y.go", false, []fakeUnit{
			{3, 4, 6, 1, 1, 0}, {3, 2, 6, 9, 2, 0}, {3, 5, 4, 1, 3, 0}, {3, 2, 6, 4, 4, 0},
			{2, 5, 4, 1, 5, 0}, {3, 5, 6, 1, 6, 0}, {3, 2, 6, 1, 7, 0},
		}},
	}}})
	layout, refusal := buildCoverageLayout(strs, records, "atomic")
	require.Empty(t, refusal)
	require.Equal(t, []string{"example.com/s/y.go", "example.com/s/z.go"}, layout.files)
	require.Equal(t, []uint32{
		0, 2, 5, 4, 1, 5,
		0, 3, 5, 4, 1, 3,
		0, 3, 2, 6, 1, 7,
		0, 3, 2, 6, 4, 4,
		0, 3, 2, 6, 9, 2,
		0, 3, 4, 6, 1, 1,
		0, 3, 5, 6, 1, 6,
		1, 3, 5, 4, 1, 8,
	}, layout.entries)
	require.Equal(t, []int32{7, 5, 4, 1, 3, 0, 6, 2}, layout.units)
}

// Meta-data that the text path rejects, reads otherwise or resolves by map
// order has no layout, and neither has a malformed stream.
func TestBuildCoverageLayoutRefusals(t *testing.T) {
	unit := []fakeUnit{{1, 1, 2, 2, 1, 0}}
	for name, tc := range map[string]struct {
		pkgs    []fakePackage
		refusal string
	}{
		"mode clash":  {[]fakePackage{{"atomic", "perblock", "a", nil}, {"count", "perblock", "b", nil}}, "counter mode clash"},
		"granularity": {[]fakePackage{{"atomic", "perfunc", "a", nil}}, "counter granularity"},
		"other mode":  {[]fakePackage{{"testmain", "perblock", "t", nil}, {"count", "perblock", "a", nil}}, "counter mode count"},
		"no mode":     {[]fakePackage{{"testmain", "perblock", "a", nil}}, "counter mode "},
		"line feed":   {[]fakePackage{{"atomic", "perblock", "a", []fakeFunc{{"F", "a/x\ny.go", false, unit}}}}, "file name with a line break"},
		"return":      {[]fakePackage{{"atomic", "perblock", "a", []fakeFunc{{"F", "a/x\ry.go", false, unit}}}}, "file name with a line break"},
		"statements": {[]fakePackage{{"atomic", "perblock", "a", []fakeFunc{
			{"F", "a/a.go", false, unit},
			{"F", "a/a.go", false, []fakeUnit{{1, 1, 2, 2, 2, 0}}},
		}}}, "blocks share a position"},
		"packages": {[]fakePackage{
			{"atomic", "perblock", "a", []fakeFunc{{"F", "a/a.go", false, unit}}},
			{"atomic", "perblock", "b", []fakeFunc{{"F", "a/a.go", false, unit}}},
		}, "blocks share a position"},
	} {
		strs, records := fakeMeta(tc.pkgs)
		layout, refusal := buildCoverageLayout(strs, records, "atomic")
		require.Nil(t, layout, name)
		require.Equal(t, tc.refusal, refusal, name)
	}

	malformed := errCoverageMetaData.Error()
	strs, records := fakeMeta(memoryPackages)
	for cut := range records {
		layout, refusal := buildCoverageLayout(strs, records[:cut], "atomic")
		require.Nil(t, layout)
		require.Equal(t, malformed, refusal, "records cut at %d", cut)
	}
	for name, records := range map[string][]uint32{
		"trailing records": append(slices.Clone(records), 0),
		"slots":            {0, 1, 2, 3, 1 << 20},
		"functions":        {0, 1, 2, 3, 1, 0, 1, 2, 1 << 20},
	} {
		var start, end runtime.MemStats
		runtime.ReadMemStats(&start)
		_, refusal := buildCoverageLayout(strs, records, "atomic")
		runtime.ReadMemStats(&end)
		require.Equal(t, malformed, refusal, name)
		require.Greater(t, uint64(1<<20), end.TotalAlloc-start.TotalAlloc, "%s: allocated for counts beyond the stream", name)
	}
	_, refusal := buildCoverageLayout(strs[:len(strs)-1], records, "atomic")
	require.Equal(t, malformed, refusal, "missing string")
}

// Counts sum merged units without overflowing and skip units without a
// block; copies that do not match the layout fail.
func TestCoverageLayoutCounts(t *testing.T) {
	strs, records := fakeMeta(memoryPackages)
	layout, refusal := buildCoverageLayout(strs, records, "atomic")
	require.Empty(t, refusal)
	counts, ok := layout.counts(fakeSegments([][][]uint32{{{5}}, {{7, 9}, {2}}, {}, {{3}}}), nil)
	require.True(t, ok)
	require.Equal(t, []uint32{5, 7, 5}, counts)
	counts, ok = layout.counts(fakeSegments([][][]uint32{{nil}, {nil, {^uint32(0) - 1}}, {}, {{4}}}), counts)
	require.True(t, ok)
	require.Equal(t, []uint32{^uint32(0), 0, 0}, counts, "saturated, and the rest cleared")
	// Functions without a positive counter are skipped before their IDs are
	// read, as one that starts running during a copy may have only part of its
	// header.
	counts, ok = layout.counts([]uint32{13, 1, 0, 0, 0, 2, 9, 7, 0, 0, 1, 1, 0, 6}, counts)
	require.True(t, ok)
	require.Equal(t, []uint32{0, 0, 6}, counts)
	for name, raw := range map[string][]uint32{
		"truncated counters": {5, 3, 0, 0, 0},
		"truncated segment":  {5, 1, 1, 0},
		"truncated header":   {2, 0, 1},
		"standard package":   {4, 1, 0, 0, 5},
		"hard-coded package": {4, 1, ^uint32(0), 0, 5},
		"unknown package":    {4, 1, 5, 0, 5},
		"unknown function":   {4, 1, 1, 1, 5},
		"too few counters":   {4, 1, 2, 0, 5},
		"counters overflow":  {4, 2, 1, 0, 5},
	} {
		_, ok := layout.counts(raw, nil)
		require.False(t, ok, name)
	}
}

// A collector reads the counters in memory and finds the files and lines
// that coveredFilesBetween finds in text profiles of the same counts, and
// releases its baseline.
func TestMemoryCoverageMatchesTextProfiles(t *testing.T) {
	f := setUpMemoryCoverage(t)
	f.counters = [][][]uint32{{{2}}, {nil, nil}, {}, {{1}}}
	collector := &testCoverage{testFile: "example.com/a/a_test.go"}
	require.NoError(t, collector.collectCoverageBeforeTestExecution())
	require.NotNil(t, collector.memory, "in-memory collector")
	require.Equal(t, int64(12), memoryCoverage.held.Load())
	f.counters = [][][]uint32{{{2}}, {{1, 0}, nil}, {}, {{2}}}
	require.NoError(t, collector.collectCoverageAfterTestExecution())
	require.Zero(t, memoryCoverage.held.Load(), "baseline released")
	require.True(t, collector.loadCoverageData())

	before, err := parseCoverProfileText([]byte("mode: atomic\nexample.com/a/a.go:3.2,5.3 2 1\nexample.com/a/a.go:7.1,9.2 1 0\nexample.com/b/b.go:4.1,6.10 3 2\n"))
	require.NoError(t, err)
	after, err := parseCoverProfileText([]byte("mode: atomic\nexample.com/a/a.go:3.2,5.3 2 2\nexample.com/a/a.go:7.1,9.2 1 1\nexample.com/b/b.go:4.1,6.10 3 2\n"))
	require.NoError(t, err)
	require.Equal(t, coveredFilesBetween(collector.testFile, before, after), collector.filesCovered)
	require.Len(t, collector.filesCovered, 2, "the test file and a.go")
}

// A process without coverable units still captures each test, which then
// covers only its test file.
func TestMemoryCoverageEmptyLayout(t *testing.T) {
	f := setUpMemoryCoverage(t)
	f.pkgs = []fakePackage{{"atomic", "perblock", "example.com/e", nil}}
	f.text, f.counters, f.slots = "mode: atomic\n", [][][]uint32{{}}, 1
	collector := &testCoverage{testFile: "example.com/e/e_test.go"}
	require.NoError(t, collector.collectCoverageBeforeTestExecution())
	require.NotNil(t, collector.memory, "in-memory collector")
	require.NoError(t, collector.collectCoverageAfterTestExecution())
	require.True(t, collector.loadCoverageData())
	require.Equal(t, coveredFilesBetween(collector.testFile, coverProfile{}, coverProfile{}), collector.filesCovered)
	require.True(t, memoryCoverageReady())
}

// The process keeps text profiles without the hook, for a counter mode
// without per-test coverage, when settings turn it off, when the hook or the
// layout refuses, and when the text profile does not read as the layout.
func TestMemoryCoverageRefusals(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, f *fakeTestingCoverage){
		"no hook": func(t *testing.T, f *fakeTestingCoverage) { resetMemoryCoverageForTesting() },
		"set mode": func(t *testing.T, f *fakeTestingCoverage) {
			// The layout would match: set mode's flags are not counts.
			mode = "set"
			f.pkgs = slices.Clone(f.pkgs)
			for i := range f.pkgs {
				if f.pkgs[i].mode == "atomic" {
					f.pkgs[i].mode = "set"
				}
			}
			f.text = strings.Replace(f.text, "mode: atomic", "mode: set", 1)
		},
		"settings":       func(t *testing.T, f *fakeTestingCoverage) { t.Setenv(coverageInMemoryEnv, "false") },
		"hook refusal":   func(t *testing.T, f *fakeTestingCoverage) { f.refuse = "meta-data: malformed" },
		"layout refusal": func(t *testing.T, f *fakeTestingCoverage) { f.renameFile("example.com/b/b.go", "example.com/b/b\n.go") },
		"reordered text": func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "3.2,5.3", "7.1,9.2", 1) },
		"start line":     func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "3.2,5.3", "4.2,5.3", 1) },
		"start column":   func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "3.2,5.3", "3.1,5.3", 1) },
		"end line":       func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "3.2,5.3", "3.2,6.3", 1) },
		"end column":     func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "3.2,5.3", "3.2,5.4", 1) },
		"missing block": func(t *testing.T, f *fakeTestingCoverage) {
			f.text = strings.TrimSuffix(f.text, "4.1,6.10 3 4294967295\n")
		},
		"file": func(t *testing.T, f *fakeTestingCoverage) { f.text = strings.Replace(f.text, "b/b.go", "b/c.go", 1) },
		"statements": func(t *testing.T, f *fakeTestingCoverage) {
			f.text = strings.Replace(f.text, " 3 4294967295", " 4 4294967295", 1)
		},
		"count": func(t *testing.T, f *fakeTestingCoverage) {
			f.text = strings.Replace(f.text, " 3 4294967295", " 3 1", 1)
		},
		"wrong mode": func(t *testing.T, f *fakeTestingCoverage) {
			f.text = strings.Replace(f.text, "mode: atomic", "mode: count", 1)
		},
		"oversized line": func(t *testing.T, f *fakeTestingCoverage) {
			f.renameFile("example.com/b/b.go", "example.com/b/"+strings.Repeat("b", 70000)+".go")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := setUpMemoryCoverage(t)
			change(t, f)
			require.False(t, memoryCoverageReady())
		})
	}
	f := setUpMemoryCoverage(t)
	f.renameFile("example.com/b/b.go", "example.com/b/"+strings.Repeat("b", 1000)+".go")
	require.True(t, memoryCoverageReady(), "a renamed file")
}

// On 32-bit platforms, where text profiles cannot read counts beyond int,
// the process keeps text profiles.
func TestMemoryCoverageNeeds64BitCounts(t *testing.T) {
	fakeMemoryCoverage(t)
	require.Equal(t, strconv.IntSize == 64, memoryCoverageReady())
}

// Over the budget for baselines, a collector uses text profiles, and its
// after-capture does too.
func TestMemoryCoverageBudget(t *testing.T) {
	setUpMemoryCoverage(t)
	temporaryDir = t.TempDir()
	written := 0
	tearDown = func(profile, _ string) (string, error) {
		written++
		return "", os.WriteFile(profile, []byte(memoryText), 0o600)
	}
	require.True(t, memoryCoverageReady())
	memoryCoverage.held.Store(memoryCoverageBudget - 11)
	collector := &testCoverage{testFile: "example.com/a/a_test.go"}
	require.NoError(t, collector.collectCoverageBeforeTestExecution())
	require.Nil(t, collector.memory)
	require.Equal(t, int64(memoryCoverageBudget-11), memoryCoverage.held.Load())
	require.NoError(t, collector.collectCoverageAfterTestExecution())
	require.Equal(t, 2, written)
	require.FileExists(t, filepath.Join(temporaryDir, filepath.Base(collector.postCoverageFilename)))

	memoryCoverage.held.Store(memoryCoverageBudget - 12)
	collector = &testCoverage{testFile: "example.com/a/a_test.go"}
	require.NoError(t, collector.collectCoverageBeforeTestExecution())
	require.NotNil(t, collector.memory, "a baseline that fits exactly")
}

// A capture that does not match the layout fails the collector, releases its
// baseline and keeps later collectors on text profiles.
func TestMemoryCoverageCaptureFailure(t *testing.T) {
	for name, change := range map[string]func(f *fakeTestingCoverage){
		"registered package": func(f *fakeTestingCoverage) { f.slots++ },
		"unknown function":   func(f *fakeTestingCoverage) { f.counters = [][][]uint32{{nil, {1}}} },
	} {
		f := setUpMemoryCoverage(t)
		collector := &testCoverage{testFile: "example.com/a/a_test.go"}
		require.NoError(t, collector.collectCoverageBeforeTestExecution(), name)
		require.Equal(t, int64(12), memoryCoverage.held.Load(), name)
		change(f)
		require.Error(t, collector.collectCoverageAfterTestExecution(), name)
		require.Zero(t, memoryCoverage.held.Load(), name)
		require.False(t, memoryCoverageReady(), name)

		// A collector without a baseline fails even when its after-capture
		// would succeed.
		f = setUpMemoryCoverage(t)
		valid := *f
		change(f)
		collector = &testCoverage{testFile: "example.com/a/a_test.go"}
		require.Error(t, collector.collectCoverageBeforeTestExecution(), name)
		require.NotNil(t, collector.memory, name)
		require.Zero(t, memoryCoverage.held.Load(), name)
		*f = valid
		memoryCoverage.enabled.Store(true)
		require.Error(t, collector.collectCoverageAfterTestExecution(), name)
		require.Zero(t, memoryCoverage.held.Load(), name)
	}
}

// Process collectors pause and resume through the same captures, each
// segment releases its baseline, and coverage while paused is excluded.
func TestMemoryCoverageProcessSegments(t *testing.T) {
	f := setUpMemoryCoverage(t)
	collector := BeginProcessTestCoverage("example.com/a/a_test.go")
	require.NotNil(t, collector)
	f.counters = [][][]uint32{{nil}, {nil, {1}}, {}, {nil}}
	collector.Pause()
	require.Zero(t, memoryCoverage.held.Load())
	f.counters = [][][]uint32{{nil}, {{1, 0}, {1}}, {}, {nil}}
	collector.Resume()
	require.Equal(t, int64(12), memoryCoverage.held.Load())
	f.counters = [][][]uint32{{{3}}, {{1, 0}, {1}}, {}, {nil}}
	files := collector.Finish()
	require.Zero(t, memoryCoverage.held.Load())

	var want coveredLines
	for _, block := range []coverProfileBlock{
		{fileName: "example.com/a/a.go", coverageBlock: coverageBlock{startLine: 3, startCol: 2, endLine: 5, endCol: 3, numStmt: 2}},
		{fileName: "example.com/b/b.go", coverageBlock: coverageBlock{startLine: 4, startCol: 1, endLine: 6, endCol: 10, numStmt: 3}},
	} {
		want.add(&block)
	}
	require.Equal(t, mergeProcessTestCoverageFiles(nil, want.files(utils.GetRelativePathFromCITagsSourceRoot("example.com/a/a_test.go"))), files)
}
