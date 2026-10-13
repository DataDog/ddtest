// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"cmp"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	_ "unsafe" // for go:linkname

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Per-test coverage reads the counters in memory when ddto's testing overlay
// registers its hook. From the hook's decoded meta-data, Mini builds the
// layout of the text profile that testing/internal/testdeps would write, once
// per process, and each capture yields one count per layout entry: the counts
// that the text profile would hold, without files, text or parsing. Processes
// whose layout is not exactly the text profile's keep the text profiles.
const (
	// coverageInMemoryEnv set to false keeps the text profiles.
	coverageInMemoryEnv = "DD_CIVISIBILITY_INTERNAL_COVERAGE_IN_MEMORY"
	// coverageOracleEnv set to true also computes each in-memory collector's
	// result from text profiles of the same counters, and logs both.
	coverageOracleEnv = "DD_CIVISIBILITY_INTERNAL_COVERAGE_ORACLE"
	// memoryCoverageBudget bounds the baselines that in-memory collectors
	// hold at once. Nested subtests keep their ancestors' baselines.
	memoryCoverageBudget = 64 << 20
)

// testingCoverage holds the hook that ddto's testing overlay registers.
var testingCoverage struct {
	meta     func() ([]string, []uint32, string)
	counters func([]uint32) ([]uint32, int)
	profile  func([]uint32) []byte
}

// registerTestingCoverage is called by ddto's testing overlay.
//
//go:linkname registerTestingCoverage
func registerTestingCoverage(meta func() ([]string, []uint32, string), counters func([]uint32) ([]uint32, int), profile func([]uint32) []byte) {
	testingCoverage.meta, testingCoverage.counters, testingCoverage.profile = meta, counters, profile
}

var memoryCoverage struct {
	once    sync.Once
	enabled atomic.Bool
	oracle  bool
	layout  *coverageLayout
	held    atomic.Int64
}

// coverageLayout is the text profile's blocks, and where each counter goes.
type coverageLayout struct {
	// offsets are the positions of a function's counter count, package ID,
	// function ID and first counter in a counter segment.
	offsets [4]int
	slots   int
	files   []string
	// entries holds six values per text profile block, in profile order: the
	// index of its file name, its start line and column, its end line and
	// column, and its statements.
	entries []uint32
	// funcs locates each package slot's functions in units, which holds each
	// unit's entry, or -1 for a unit that the text profile omits.
	funcs [][]layoutFunc
	units []int32
}

type layoutFunc struct{ start, units int32 }

var (
	// errMemoryCoverageCapture reports a capture that does not match the
	// layout, as when coverage meta-data was registered after it was built.
	errMemoryCoverageCapture = errors.New("coverage counters do not match the meta-data")
	errCoverageMetaData      = errors.New("malformed coverage meta-data")
)

// memoryCollector is an in-memory collector's state.
type memoryCollector struct {
	before   []uint32
	reserved int64
	covered  []int32
	// The oracle keeps the counters of both captures.
	rawBefore, rawAfter []uint32
}

// memoryCoverageReady reports whether captures read the counters in memory,
// deciding once, before the process's first capture.
func memoryCoverageReady() bool {
	memoryCoverage.once.Do(initMemoryCoverage)
	return memoryCoverage.enabled.Load()
}

func initMemoryCoverage() {
	reason := ""
	switch {
	case testingCoverage.meta == nil:
		reason = "no testing hook"
	case mode != "count" && mode != "atomic":
		reason = "coverage mode " + mode
	case strconv.IntSize < 64:
		// Text profiles reject counts beyond the platform's int.
		reason = "32-bit counts"
	case os.Getenv(coverageInMemoryEnv) == "false":
		reason = "disabled"
	}
	if reason == "" {
		strs, records, refusal := testingCoverage.meta()
		layout := (*coverageLayout)(nil)
		if refusal == "" {
			layout, refusal = buildCoverageLayout(strs, records, mode)
		}
		switch {
		case refusal != "":
			reason = refusal
		case !layout.matchesTextProfile(testingCoverage.profile(nil)):
			reason = "layout differs from the text profile"
		default:
			memoryCoverage.layout = layout
			memoryCoverage.oracle = os.Getenv(coverageOracleEnv) == "true"
			memoryCoverage.enabled.Store(true)
		}
	}
	log.Debug("civisibility.cov: per-test coverage in memory: %t (%s)", reason == "", reason)
}

// buildCoverageLayout lays out the text profile that
// cfile.ProcessCoverTestDir writes for this meta-data in counter mode mode,
// as its processPod and cformat do:
//   - units with a parent are omitted;
//   - units with the same file, function, literal flag and position merge;
//   - packages are in import path order;
//   - each package's units are sorted by file and position.
//
// It refuses a counter mode or granularity that the text path rejects or that
// per-test coverage does not use, file names with line breaks, which the text
// path reads as more than one line, and blocks that share a file and
// position, where the text path's result depends on map order.
func buildCoverageLayout(strs []string, records []uint32, mode string) (layout *coverageLayout, refusal string) {
	defer func() {
		// A short or inconsistent stream is malformed.
		if recover() != nil {
			layout, refusal = nil, errCoverageMetaData.Error()
		}
	}()
	next := func() uint32 {
		value := records[0]
		records = records[1:]
		return value
	}
	// count reads a number of items that take at least size values each, so
	// that a malformed count cannot allocate beyond the stream's size.
	count := func(size int) int {
		n := int(next())
		if n > len(records)/size {
			panic(errCoverageMetaData)
		}
		return n
	}
	str := func() string { return strs[next()] }
	layout = &coverageLayout{}
	for i := range layout.offsets {
		layout.offsets[i] = int(next())
	}
	layout.slots = count(4)
	type unitKey struct {
		file, fname string
		lit         bool
		unit        [6]uint32
	}
	type pkgUnits struct {
		keys   []unitKey
		index  map[unitKey]int32
		global []int32
	}
	type unitRef struct {
		pkg   *pkgUnits
		local int32
	}
	pkgs := map[string]*pkgUnits{}
	var paths []string
	var refs []unitRef
	cmode := ""
	layout.funcs = make([][]layoutFunc, layout.slots)
	for slot := range layout.funcs {
		switch m := str(); {
		case m == "regonly" || m == "testmain":
		case cmode == "":
			cmode = m
		case m != cmode:
			return nil, "counter mode clash"
		}
		if str() != "perblock" {
			return nil, "counter granularity"
		}
		path := str()
		p := pkgs[path]
		if p == nil {
			p = &pkgUnits{index: map[unitKey]int32{}}
			pkgs[path] = p
			paths = append(paths, path)
		}
		layout.funcs[slot] = make([]layoutFunc, count(4))
		for fn := range layout.funcs[slot] {
			fname, file, lit, units := str(), str(), next() != 0, next()
			layout.funcs[slot][fn] = layoutFunc{start: int32(len(refs)), units: int32(units)}
			for range units {
				key := unitKey{file: file, fname: fname, lit: lit}
				for i := range key.unit {
					key.unit[i] = next()
				}
				if key.unit[5] != 0 {
					refs = append(refs, unitRef{local: -1})
					continue
				}
				local, ok := p.index[key]
				if !ok {
					local = int32(len(p.keys))
					p.index[key] = local
					p.keys = append(p.keys, key)
				}
				refs = append(refs, unitRef{p, local})
			}
		}
	}
	if len(records) != 0 {
		return nil, errCoverageMetaData.Error()
	}
	if cmode != mode {
		return nil, "counter mode " + cmode
	}
	type position struct {
		file string
		at   [4]uint32
	}
	seen := map[position]bool{}
	fileIndex := map[string]uint32{}
	slices.Sort(paths)
	for _, path := range paths {
		p := pkgs[path]
		order := make([]int32, len(p.keys))
		for i := range order {
			order[i] = int32(i)
		}
		slices.SortFunc(order, func(i, j int32) int {
			a, b := &p.keys[i], &p.keys[j]
			// StLine, EnLine, StCol, EnCol, NxStmts, as cformat sorts.
			return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.unit[0], b.unit[0]), cmp.Compare(a.unit[2], b.unit[2]),
				cmp.Compare(a.unit[1], b.unit[1]), cmp.Compare(a.unit[3], b.unit[3]), cmp.Compare(a.unit[4], b.unit[4]))
		})
		p.global = make([]int32, len(p.keys))
		for _, local := range order {
			k := &p.keys[local]
			if strings.ContainsAny(k.file, "\r\n") {
				return nil, "file name with a line break"
			}
			at := position{k.file, [4]uint32{k.unit[0], k.unit[1], k.unit[2], k.unit[3]}}
			if seen[at] {
				return nil, "blocks share a position"
			}
			seen[at] = true
			file, ok := fileIndex[k.file]
			if !ok {
				file = uint32(len(layout.files))
				fileIndex[k.file] = file
				layout.files = append(layout.files, k.file)
			}
			p.global[local] = int32(len(layout.entries) / 6)
			layout.entries = append(layout.entries, file, k.unit[0], k.unit[1], k.unit[2], k.unit[3], k.unit[4])
		}
	}
	layout.units = make([]int32, len(refs))
	for i, r := range refs {
		layout.units[i] = -1
		if r.pkg != nil {
			layout.units[i] = r.pkg.global[r.local]
		}
	}
	return layout, ""
}

// matchesTextProfile reports whether parsing the hook's text profile, with
// every count at its maximum, yields exactly the layout's blocks in order:
// the text path would then read the same blocks.
func (l *coverageLayout) matchesTextProfile(text []byte) bool {
	profile, err := parseCoverProfileText(text)
	if err != nil || len(profile.blocks)*6 != len(l.entries) || !strings.HasPrefix(string(text), "mode: "+mode+"\n") {
		return false
	}
	for i := range profile.blocks {
		block, entry := &profile.blocks[i], l.entries[i*6:i*6+6]
		if block.fileName != l.files[entry[0]] ||
			block.startLine != int(entry[1]) || block.startCol != int(entry[2]) ||
			block.endLine != int(entry[3]) || block.endCol != int(entry[4]) ||
			block.numStmt != int(entry[5]) || block.count != int(^uint32(0)) {
			return false
		}
	}
	return true
}

// counts maps a copy of the counter segments, as the hook's counters makes
// it, to one count per layout entry, summing merged units without
// overflowing as cformat does. It fails for a copy that it cannot map.
func (l *coverageLayout) counts(raw, dst []uint32) ([]uint32, bool) {
	n := len(l.entries) / 6
	if dst == nil || cap(dst) < n {
		// Non-nil even for an empty layout: a nil baseline means no capture.
		dst = make([]uint32, n)
	} else {
		dst = dst[:n]
		clear(dst)
	}
	for len(raw) > 0 {
		size := int(raw[0])
		if 1+size > len(raw) {
			return dst, false
		}
		segment := raw[1 : 1+size]
		raw = raw[1+size:]
		for i := 0; i < len(segment); i++ {
			if segment[i] == 0 {
				continue
			}
			if i+l.offsets[3] > len(segment) {
				return dst, false
			}
			ctrs, pkg, fn := int(segment[i+l.offsets[0]]), int32(segment[i+l.offsets[1]]), segment[i+l.offsets[2]]
			start := i + l.offsets[3]
			if start+ctrs > len(segment) {
				return dst, false
			}
			counters := segment[start : start+ctrs]
			i = start + ctrs - 1
			// As cfile does, a function without a positive counter is skipped
			// before its IDs are read: one that starts running during the copy
			// may have written only part of its header.
			if !slices.ContainsFunc(counters, func(v uint32) bool { return v != 0 }) {
				continue
			}
			// Coverage of standard packages, which use other package IDs, is
			// never read in memory.
			if pkg <= 0 || int(pkg) > len(l.funcs) || int(fn) >= len(l.funcs[pkg-1]) {
				return dst, false
			}
			f := l.funcs[pkg-1][fn]
			if ctrs < int(f.units) {
				return dst, false
			}
			for u, entry := range l.units[f.start : f.start+f.units] {
				if entry < 0 {
					continue
				}
				if sum := dst[entry] + counters[u]; sum >= dst[entry] {
					dst[entry] = sum
				} else {
					dst[entry] = ^uint32(0)
				}
			}
		}
	}
	return dst, true
}

// beginMemoryCoverage captures an in-memory baseline, or returns nil when
// the collector must use text profiles: the process's hook is unusable, or
// the baselines held would exceed the budget.
func beginMemoryCoverage() (*memoryCollector, error) {
	if !memoryCoverageReady() {
		return nil, nil
	}
	size := int64(len(memoryCoverage.layout.entries) / 6 * 4)
	if memoryCoverage.held.Add(size) > memoryCoverageBudget {
		memoryCoverage.held.Add(-size)
		return nil, nil
	}
	c := &memoryCollector{reserved: size}
	before, raw, err := captureMemoryCoverage()
	if err != nil {
		c.release()
		return c, err
	}
	c.before = before
	if memoryCoverage.oracle {
		c.rawBefore = raw
	}
	return c, nil
}

// finish captures the counters after the test and keeps the entries whose
// count grew, then releases the baseline.
func (c *memoryCollector) finish() error {
	defer c.release()
	if c.before == nil {
		return errMemoryCoverageCapture
	}
	after, raw, err := captureMemoryCoverage()
	if err != nil {
		return err
	}
	for i, count := range after {
		if count > c.before[i] {
			c.covered = append(c.covered, int32(i))
		}
	}
	if memoryCoverage.oracle {
		c.rawAfter = raw
	}
	return nil
}

func (c *memoryCollector) release() {
	if c.reserved != 0 {
		memoryCoverage.held.Add(-c.reserved)
		c.reserved = 0
	}
	c.before = nil
}

// captureMemoryCoverage reads one count per layout entry. A capture that
// does not match the layout turns in-memory coverage off for later
// baselines.
func captureMemoryCoverage() (counts, raw []uint32, err error) {
	layout := memoryCoverage.layout
	raw, slots := testingCoverage.counters(nil)
	ok := slots == layout.slots
	if ok {
		counts, ok = layout.counts(raw, nil)
	}
	if !ok {
		memoryCoverage.enabled.Store(false)
		return nil, nil, errMemoryCoverageCapture
	}
	return counts, raw, nil
}

// files returns the covered files and line bitmaps as coveredFilesBetween
// does for the same counts in text profiles.
func (c *memoryCollector) files(testFile string) []coveredFile {
	layout := memoryCoverage.layout
	var covered coveredLines
	for _, i := range c.covered {
		entry := layout.entries[i*6 : i*6+6]
		block := coverProfileBlock{fileName: layout.files[entry[0]], coverageBlock: coverageBlock{
			startLine: int(entry[1]), startCol: int(entry[2]), endLine: int(entry[3]), endCol: int(entry[4]), numStmt: int(entry[5]),
		}}
		covered.add(&block)
	}
	return covered.files(testFile)
}

// checkOracle computes the result from text profiles of the same counters,
// rendered by the toolchain's formatter, and logs whether both agree.
func (c *memoryCollector) checkOracle(testFile string, got []coveredFile) {
	if c.rawBefore == nil || c.rawAfter == nil {
		return
	}
	before, errBefore := parseCoverProfileText(testingCoverage.profile(c.rawBefore))
	after, errAfter := parseCoverProfileText(testingCoverage.profile(c.rawAfter))
	c.rawBefore, c.rawAfter = nil, nil
	if errBefore != nil || errAfter != nil {
		log.Debug("civisibility.cov: oracle failed to parse text profiles")
		return
	}
	want := coveredFilesBetween(testFile, before, after)
	log.Debug("civisibility.cov: oracle test file %s match=%t files=%d", testFile, reflect.DeepEqual(got, want), len(want))
}

// resetMemoryCoverageForTesting forgets the hook and the process's decision.
func resetMemoryCoverageForTesting() {
	testingCoverage.meta, testingCoverage.counters, testingCoverage.profile = nil, nil, nil
	memoryCoverage.once = sync.Once{}
	memoryCoverage.enabled.Store(false)
	memoryCoverage.oracle = false
	memoryCoverage.layout = nil
	memoryCoverage.held.Store(0)
}
