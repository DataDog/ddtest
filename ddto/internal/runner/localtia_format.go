package runner

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"maps"
	"slices"

	"github.com/DataDog/ddtest/ddto/internal/localtia"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
)

// tiaMapFile is the stored form of a map. Files are one table, by column,
// that packages and tests name by index, and line bitmaps another, so that
// a file that many packages share, or the lines that many tests cover in a
// shared helper, appear once. Lists of indices are sorted and stored as the
// differences between consecutive indices, which are small numbers.
type tiaMapFile struct {
	Version  int                         `json:"version"`
	Files    tiaStoredFiles              `json:"files"`
	Bitmaps  [][]byte                    `json:"bitmaps,omitempty"`
	Modules  []int                       `json:"modules,omitempty"`
	Packages map[string]tiaStoredPackage `json:"packages"`
}

// tiaStoredFiles are the files' paths and states, by column. Sizes and
// ModTimes are absent from a map that leaves this machine: they vouch for a
// hash only on the machine that read the file. A modification time of 0
// does not vouch for the hash either: the next run reads the file. Code
// lists the Go files with code after initialization.
type tiaStoredFiles struct {
	Paths    []string `json:"paths"`
	Sizes    []int64  `json:"sizes,omitempty"`
	ModTimes []int64  `json:"mtimes,omitempty"`
	Hashes   []string `json:"hashes"`
	Decls    []string `json:"decls"`
	Code     []int    `json:"code,omitempty"`
}

// tiaStoredPackage is a package. Its tests name their module, suite and
// coverage source by index in Modules, Suites and Sources, and their covered
// files without Common, the files that every test of the package covered.
type tiaStoredPackage struct {
	Files       []int            `json:"files,omitempty"`
	Imports     []string         `json:"imports,omitempty"`
	Tested      bool             `json:"tested,omitempty"`
	TestFiles   []int            `json:"test_files,omitempty"`
	TestImports []string         `json:"test_imports,omitempty"`
	Testdata    []int            `json:"testdata,omitempty"`
	Unseen      map[string][]int `json:"unseen,omitempty"`
	Modules     []string         `json:"modules,omitempty"`
	Suites      []string         `json:"suites,omitempty"`
	Sources     []string         `json:"sources,omitempty"`
	Common      []int            `json:"common,omitempty"`
	Tests       []tiaStoredTest  `json:"tests,omitempty"`
}

// tiaStoredTest is a test. Bitmaps are pairs of a covered file's index,
// as the difference from the previous pair's, and the index of its line
// bitmap.
type tiaStoredTest struct {
	Name    string `json:"n"`
	Module  int    `json:"m,omitempty"`
	Suite   int    `json:"s,omitempty"`
	Source  int    `json:"o,omitempty"`
	Status  string `json:"t"`
	Covered []int  `json:"c,omitempty"`
	Bitmaps []int  `json:"b,omitempty"`
}

// marshalTIAMap returns m's stored form as JSON. withStats keeps the files'
// sizes and modification times, which only the machine that read the files
// can trust.
func marshalTIAMap(m *tiaMap, withStats bool) ([]byte, error) {
	return json.Marshal(encodeTIAMap(m, withStats))
}

// unmarshalTIAMap returns the map that data stores, as JSON or, should a
// stored map come back compressed, gzip-compressed JSON, or false when it is
// not a map of this format or is inconsistent.
func unmarshalTIAMap(data []byte) (*tiaMap, bool) {
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		// No larger than the TIA artifacts API stores: a small download
		// cannot expand without bound.
		if data, err = io.ReadAll(io.LimitReader(reader, net.MaxTIAArtifactBytes+1)); err != nil || len(data) > net.MaxTIAArtifactBytes {
			return nil, false
		}
	}
	var stored tiaMapFile
	if json.Unmarshal(data, &stored) != nil || stored.Version != localTIAMapVersion {
		return nil, false
	}
	return stored.decode()
}

// deltas returns sorted indices as the differences between consecutive ones.
func deltas(indices []int) []int {
	if len(indices) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(indices))
	out := make([]int, len(sorted))
	previous := 0
	for i, index := range sorted {
		out[i], previous = index-previous, index
	}
	return out
}

// undeltas returns the indices that deltas stored.
func undeltas(stored []int) []int {
	out := make([]int, len(stored))
	index := 0
	for i, d := range stored {
		index += d
		out[i] = index
	}
	return out
}

// encodeTIAMap returns m's stored form, with the files that a package or a
// test names.
func encodeTIAMap(m *tiaMap, withStats bool) tiaMapFile {
	used := map[string]bool{}
	mark := func(files ...[]string) {
		for _, list := range files {
			for _, file := range list {
				used[file] = true
			}
		}
	}
	mark(m.Modules)
	for _, node := range m.Packages {
		mark(node.Files, node.TestFiles, node.Testdata)
		for _, unseen := range node.Unseen {
			mark(unseen)
		}
		for _, t := range node.Tests {
			mark(t.Covered)
		}
	}
	stored := tiaMapFile{Version: localTIAMapVersion, Packages: make(map[string]tiaStoredPackage, len(m.Packages))}
	paths := slices.Sorted(maps.Keys(used))
	index := make(map[string]int, len(paths))
	files := &stored.Files
	files.Paths, files.Hashes, files.Decls = paths, make([]string, len(paths)), make([]string, len(paths))
	if withStats {
		files.Sizes, files.ModTimes = make([]int64, len(paths)), make([]int64, len(paths))
	}
	var code []int
	for i, file := range paths {
		state := m.Files[file]
		index[file] = i
		files.Hashes[i], files.Decls[i] = state.Hash, state.Decl
		if state.Code {
			code = append(code, i)
		}
		if withStats {
			files.Sizes[i] = state.Size
			if !state.racy {
				files.ModTimes[i] = state.ModTime
			}
		}
	}
	files.Code = deltas(code)
	indices := func(files []string) []int {
		out := make([]int, len(files))
		for i, file := range files {
			out[i] = index[file]
		}
		return deltas(out)
	}
	stored.Modules = indices(m.Modules)
	bitmaps := map[string]int{}
	for path, node := range m.Packages {
		p := tiaStoredPackage{Files: indices(node.Files), Imports: node.Imports, Tested: node.Tested, TestFiles: indices(node.TestFiles),
			TestImports: node.TestImports, Testdata: indices(node.Testdata)}
		for source, unseen := range node.Unseen {
			if p.Unseen == nil {
				p.Unseen = map[string][]int{}
			}
			p.Unseen[source] = indices(unseen)
		}
		if len(node.Tests) != 0 {
			encodeTests(&p, node.Tests, paths, index, bitmaps, &stored.Bitmaps)
		}
		stored.Packages[path] = p
	}
	return stored
}

// encodeTests stores a package's tests in p.
func encodeTests(p *tiaStoredPackage, tests []tiaTest, paths []string, index map[string]int, bitmaps map[string]int, table *[][]byte) {
	position := func(list *[]string, value string) int {
		if i := slices.Index(*list, value); i >= 0 {
			return i
		}
		*list = append(*list, value)
		return len(*list) - 1
	}
	common := map[string]bool{}
	for _, file := range tests[0].Covered {
		common[file] = true
	}
	for _, t := range tests[1:] {
		maps.DeleteFunc(common, func(file string, _ bool) bool {
			_, found := slices.BinarySearch(t.Covered, file)
			return !found
		})
	}
	var commonIndices []int
	for file := range common {
		commonIndices = append(commonIndices, index[file])
	}
	p.Common = deltas(commonIndices)
	for _, t := range tests {
		s := tiaStoredTest{Name: t.Name, Module: position(&p.Modules, t.Module), Suite: position(&p.Suites, t.Suite), Source: position(&p.Sources, t.Source), Status: t.Status}
		var own []int
		for _, file := range t.Covered {
			if !common[file] {
				own = append(own, index[file])
			}
		}
		s.Covered = deltas(own)
		var withBitmaps []int
		for file := range t.Bitmaps {
			if i, ok := index[file]; ok {
				withBitmaps = append(withBitmaps, i)
			}
		}
		slices.Sort(withBitmaps)
		previous := 0
		for _, i := range withBitmaps {
			bitmap := t.Bitmaps[paths[i]]
			b, ok := bitmaps[string(bitmap)]
			if !ok {
				b = len(*table)
				bitmaps[string(bitmap)] = b
				*table = append(*table, bitmap)
			}
			s.Bitmaps = append(s.Bitmaps, i-previous, b)
			previous = i
		}
		p.Tests = append(p.Tests, s)
	}
}

// decode returns the map that f stores, or false when it is inconsistent, as
// with an index out of range.
func (f tiaMapFile) decode() (*tiaMap, bool) {
	files := f.Files
	n := len(files.Paths)
	// Sizes and times come together, one of each per file, or not at all.
	stats := files.Sizes != nil || files.ModTimes != nil
	if len(files.Hashes) != n || len(files.Decls) != n || stats && (len(files.Sizes) != n || len(files.ModTimes) != n) {
		return nil, false
	}
	m := emptyTIAMap()
	for i, path := range files.Paths {
		state := tiaFile{Hash: files.Hashes[i], Decl: files.Decls[i]}
		if stats {
			state.Size, state.ModTime = files.Sizes[i], files.ModTimes[i]
		}
		m.Files[path] = state
	}
	ok := true
	paths := func(stored []int) []string {
		var out []string
		for _, i := range undeltas(stored) {
			if i < 0 || i >= n {
				ok = false
				continue
			}
			out = append(out, files.Paths[i])
		}
		return out
	}
	for _, path := range paths(files.Code) {
		state := m.Files[path]
		state.Code = true
		m.Files[path] = state
	}
	m.Modules = paths(f.Modules)
	at := func(list []string, i int) string {
		if i < 0 || i >= len(list) {
			ok = false
			return ""
		}
		return list[i]
	}
	for name, p := range f.Packages {
		node := &tiaNode{Files: paths(p.Files), Imports: p.Imports, Tested: p.Tested, TestFiles: paths(p.TestFiles),
			TestImports: p.TestImports, Testdata: paths(p.Testdata)}
		for source, unseen := range p.Unseen {
			if node.Unseen == nil {
				node.Unseen = map[string][]string{}
			}
			node.Unseen[source] = paths(unseen)
		}
		common := paths(p.Common)
		for _, s := range p.Tests {
			t := tiaTest{Test: localtia.Test{Module: at(p.Modules, s.Module), Suite: at(p.Suites, s.Suite), Name: s.Name}, Status: s.Status, Source: at(p.Sources, s.Source)}
			t.Covered = slices.Concat(common, paths(s.Covered))
			slices.Sort(t.Covered)
			t.Covered = slices.Compact(t.Covered)
			if t.Covered == nil {
				t.Covered = []string{}
			}
			if len(s.Bitmaps)%2 != 0 {
				ok = false
			}
			for j, i := 0, 0; j+1 < len(s.Bitmaps); j += 2 {
				i += s.Bitmaps[j]
				b := s.Bitmaps[j+1]
				if i < 0 || i >= n || b < 0 || b >= len(f.Bitmaps) {
					ok = false
					continue
				}
				if t.Bitmaps == nil {
					t.Bitmaps = map[string][]byte{}
				}
				t.Bitmaps[files.Paths[i]] = f.Bitmaps[b]
			}
			node.Tests = append(node.Tests, t)
		}
		m.Packages[name] = node
	}
	if !ok {
		return nil, false
	}
	return m, true
}
