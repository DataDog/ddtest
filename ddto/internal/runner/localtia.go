package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/instrument"
	"github.com/DataDog/ddtest/ddto/internal/localtia"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
	"github.com/DataDog/ddtest/ddto/internal/version"
)

// Local Test Impact Analysis (DD_TIA_ENABLED) selects the tests to skip from
// the per-test coverage that earlier runs recorded, instead of asking
// Datadog: see package localtia for the test processes' side.
//
// The map holds a graph of the repository's packages: each one's own files,
// its imports and, for a test package, its test files and testdata. The
// closure C(P) of a test package P, the files that its tests can depend on,
// is P's files, test files and testdata, the files of every package that it
// reaches through the imports, and the main modules' go.mod, go.sum and
// go.work files. Files outside the repository belong to the build's
// configuration, which keys the map, or to the module cache, which go.sum
// pins. The map also holds one table of every file's content hash, and the
// size and modification time with which it had that content, so that a run
// reads only the files that changed, and of each Go file the hash of its
// declarations, which every test of a package whose closure holds the file
// can use without running its code. For each test it holds the last final
// status and the covered files; a test's dependencies D(t) are those and the
// files of C(P) that the coverage source cannot see.
//
// Every test that the map keeps is valid against the table: a merge that
// records a file's new content removes the tests that depended on the old
// one. Validity comes from content hashes only, so a dirty worktree records
// and selects as a clean one does, and every worktree of a repository shares
// the maps.

// localTIAMapVersion is the format of the map; a map of any other version is
// ignored and rebuilt.
const localTIAMapVersion = 1

// localTIARacyWindow is how recent a modification time may be for the
// table to vouch for the content that the run read: a file written again
// within the file system's time resolution keeps its size and time.
const localTIARacyWindow = 2 * time.Second

// localTIAIgnoredFlags are go test flags that change neither the tests'
// build nor what a test does, only which tests run, how often, what go test
// prints or saves, how long it waits, or the coverage that it collects: the
// map is the same with or without them.
var localTIAIgnoredFlags = words("run skip count v json timeout a c n o p x work cover covermode coverpkg coverprofile outputdir parallel failfast fullpath cpuprofile memprofile memprofilerate blockprofile blockprofilerate mutexprofile mutexprofilefraction trace artifacts debug-actiongraph debug-runtime-trace debug-trace")

// tiaFile is a file's state: its content hash ("" for a file that does not
// exist), the size and modification time in nanoseconds with which it had
// that content, and for a Go file whether it has code that runs after
// package initialization and the hash of its declarations
// (instrument.GoFileDeclarations), which tests can depend on without
// running the file's code; a Go file that does not parse hashes as a whole.
type tiaFile struct {
	Size, ModTime int64
	Hash, Decl    string
	Code          bool
	// racy is set when ModTime was too recent to vouch for Hash.
	racy bool
}

// tiaNode is a package of the repository: its own files, the packages that it
// imports and, for a test package, its test files, the packages that they
// import, its testdata, its tests, and, by coverage source, the files of its
// closure that the source cannot see when its tests recorded with it.
type tiaNode struct {
	Files, Imports                   []string
	Tested                           bool
	TestFiles, TestImports, Testdata []string
	Unseen                           map[string][]string
	Tests                            []tiaTest
	// counted reports, in this run's graph, that Go's counters count the
	// package's code; dir is its directory; embedded are the files that
	// it embeds, which are data whatever their extension.
	counted  bool
	dir      string
	embedded []string
}

// tiaTest is a test's last final status, the coverage source that recorded
// it (localTIASourceID) and the files that it covered, with their line
// bitmaps when Go's counters gave them.
type tiaTest struct {
	localtia.Test
	Status  string
	Source  string
	Covered []string
	Bitmaps map[string][]byte
}

// tiaMap is a build configuration's map, or this run's view of the
// repository: files by repository path, the module files and the packages
// by import path. In this run's view, Source is the coverage source that
// records the run's tests, or "" for a run that does not record.
type tiaMap struct {
	Files    map[string]tiaFile
	Modules  []string
	Packages map[string]*tiaNode
	Source   string
}

// testUnseen returns the files of C(P) that every test of the package
// depends on when t was recorded by its coverage source: the files that its
// source could not see, and those that the current run's source cannot see
// when the current run records.
func testUnseen(stored, current *tiaNode, t *tiaTest, source string) []string {
	unseen := stored.Unseen[t.Source]
	if current != nil && source != "" {
		unseen = union(unseen, current.Unseen[source])
	}
	return unseen
}

// closure returns the files of C(P) for the test package pkg, and the
// packages that it reaches.
func (m *tiaMap) closure(pkg string) (files, packages map[string]bool) {
	files, packages = map[string]bool{}, map[string]bool{}
	root := m.Packages[pkg]
	if root == nil {
		return files, packages
	}
	for _, list := range [][]string{root.Files, root.TestFiles, root.Testdata, m.Modules} {
		for _, file := range list {
			files[file] = true
		}
	}
	packages[pkg] = true
	pending := append(slices.Clone(root.Imports), root.TestImports...)
	for len(pending) != 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		node := m.Packages[path]
		if node == nil || packages[path] {
			continue
		}
		packages[path] = true
		for _, file := range node.Files {
			files[file] = true
		}
		pending = append(pending, node.Imports...)
	}
	return files, packages
}

// unseen returns the files of C(P) for the test package pkg that its
// coverage source cannot see: what is not Go source, and with Go's counters
// the test files, the files of the packages that they do not count, and Go
// files without code after initialization. Without -coverpkg, the counters
// count only the package under test (go help testflag); with it, the
// packages whose counted flag its patterns set. File marks see every Go
// file, and report those without marks for every test.
func (m *tiaMap) unseen(pkg string, counters, coverPackages bool) []string {
	root := m.Packages[pkg]
	if root == nil {
		return nil
	}
	unseen := map[string]bool{}
	add := func(files []string, seen func(string) bool) {
		for _, file := range files {
			if !strings.HasSuffix(file, ".go") || !seen(file) {
				unseen[file] = true
			}
		}
	}
	never := func(string) bool { return false }
	add(root.TestFiles, func(string) bool { return !counters })
	add(root.Testdata, never)
	add(m.Modules, never)
	_, packages := m.closure(pkg)
	for path := range packages {
		node := m.Packages[path]
		counted := node.counted || !coverPackages && path == pkg
		add(node.Files, func(file string) bool { return !counters || counted && m.Files[file].Code })
		add(node.embedded, never)
	}
	return slices.Sorted(maps.Keys(unseen))
}

// unaffected reports whether none of a test's dependencies changed: its
// covered files and the package's unseen files.
func (t *tiaTest) unaffected(unseen []string, changed map[string]bool) bool {
	for _, files := range [][]string{t.Covered, unseen} {
		for _, file := range files {
			if changed[file] {
				return false
			}
		}
	}
	return true
}

// skippable returns the tests of the test package pkg that local TIA lets
// ITR skip in the current run: those that passed with coverage the last time
// they ran and whose dependencies have not changed since, unless a file of
// C(P) was added or removed, or the declarations of one of its Go files
// changed, which can change what any test depends on.
func skippable(stored, current *tiaMap, pkg string) []*tiaTest {
	node, now := stored.Packages[pkg], current.Packages[pkg]
	if node == nil || !node.Tested || now == nil || len(node.Tests) == 0 {
		return nil
	}
	before, _ := stored.closure(pkg)
	files, _ := current.closure(pkg)
	if !maps.Equal(before, files) {
		return nil
	}
	changed := map[string]bool{}
	for file := range files {
		old, ok := stored.Files[file]
		if !ok || old.Hash != current.Files[file].Hash {
			changed[file] = true
		}
		if ok && old.Decl != current.Files[file].Decl {
			return nil // Declarations that any test may use changed.
		}
	}
	var skip []*tiaTest
	for i := range node.Tests {
		t := &node.Tests[i]
		if t.Status == "pass" && len(t.Covered) != 0 && t.unaffected(testUnseen(node, now, t, current.Source), changed) {
			skip = append(skip, t)
		}
	}
	return skip
}

func union(a, b []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(a), b...))))
}

// tiaPackageRun is what a test package's test process left: its records,
// whether it ended without completing them, and whether a file of its
// closure changed while it ran.
type tiaPackageRun struct {
	records    []localtia.TestRecord
	incomplete bool
	changed    bool
}

// mergeTIAMap returns the map after a run: stored, with current's files and
// packages, and the tests that ran recorded by current's coverage source. A test that did not run keeps
// its entry only while it stays valid with the new files: a test that -run
// left out is not exempt from a change that it depends on, and neither is a
// test of a package that this run did not list. A package whose process
// ended without completing its records loses its tests, since the test that
// crashed is not known. A package whose files changed while it ran keeps
// none of that run's records: they may describe either version.
func mergeTIAMap(stored, current *tiaMap, runs map[string]tiaPackageRun) *tiaMap {
	merged := &tiaMap{Files: maps.Clone(stored.Files), Modules: current.Modules, Packages: map[string]*tiaNode{}}
	changed := map[string]bool{}
	for file, state := range current.Files {
		if old, ok := stored.Files[file]; !ok || old.Hash != state.Hash {
			changed[file] = true
		}
		merged.Files[file] = state
	}
	for path, node := range stored.Packages {
		copied := *node
		merged.Packages[path] = &copied
	}
	changedPackages := map[string]bool{}
	for path, node := range current.Packages {
		old := merged.Packages[path]
		copied := *node
		copied.Unseen = map[string][]string{}
		if old != nil {
			copied.Tests = old.Tests
			maps.Copy(copied.Unseen, old.Unseen)
			if old.Tested && !node.Tested {
				// Listed only as a dependency: its tests' files stay.
				copied.Tested, copied.TestFiles, copied.TestImports, copied.Testdata = true, old.TestFiles, old.TestImports, old.Testdata
			}
		}
		if old == nil || !sameStructure(old, &copied) {
			changedPackages[path] = true
		}
		if current.Source != "" && node.Tested {
			copied.Unseen[current.Source] = node.Unseen[current.Source]
		}
		merged.Packages[path] = &copied
	}
	declarations := declarationsChanged(stored.Files, current.Files)
	modulesChanged := !slices.Equal(slices.Sorted(slices.Values(stored.Modules)), slices.Sorted(slices.Values(current.Modules)))
	for path, node := range merged.Packages {
		if !node.Tested {
			node.Tests, node.Unseen = nil, nil
			continue
		}
		run, ran := runs[path]
		if ran && run.incomplete {
			node.Tests = nil
			continue
		}
		// A changed module file or declaration, or an added or removed
		// file or package, can change what any test of the package uses.
		before, reached := stored.closure(path)
		structural := modulesChanged || hasAny(before, declarations)
		if !structural && hasAny(reached, changedPackages) {
			after, _ := merged.closure(path)
			structural = !maps.Equal(before, after)
		}
		old := stored.Packages[path]
		if old == nil {
			old = &tiaNode{}
		}
		// Only a run that records adds tests.
		recording := ran && !run.changed && current.Source != ""
		recorded := map[localtia.Test]bool{}
		if recording {
			for _, record := range run.records {
				recorded[record.Test] = true
			}
		}
		var tests []tiaTest
		for _, t := range node.Tests {
			if !recorded[t.Test] && !structural && t.unaffected(testUnseen(old, current.Packages[path], &t, current.Source), changed) {
				tests = append(tests, t)
			}
		}
		if recording {
			tests = appendRecords(tests, run.records, merged.Files, current.Source)
		}
		// A test that this run's source recorded before, and that stays,
		// keeps what the source could not see then.
		if current.Source != "" && slices.ContainsFunc(tests, func(t tiaTest) bool { return t.Source == current.Source && !recorded[t.Test] }) {
			node.Unseen[current.Source] = union(old.Unseen[current.Source], node.Unseen[current.Source])
		}
		// Each source's unseen files stay while a test that it recorded does.
		for source := range node.Unseen {
			if !slices.ContainsFunc(tests, func(t tiaTest) bool { return t.Source == source }) {
				delete(node.Unseen, source)
			}
		}
		slices.SortFunc(tests, func(a, b tiaTest) int {
			return strings.Compare(a.Module+"\x00"+a.Suite+"\x00"+a.Name, b.Module+"\x00"+b.Suite+"\x00"+b.Name)
		})
		node.Tests = tests
	}
	return merged
}

func hasAny(set, of map[string]bool) bool {
	for key := range of {
		if set[key] {
			return true
		}
	}
	return false
}

// sameStructure reports whether two versions of a package have the same
// files and imports.
func sameStructure(a, b *tiaNode) bool {
	return a.Tested == b.Tested && slices.Equal(a.Files, b.Files) && slices.Equal(a.Imports, b.Imports) &&
		slices.Equal(a.TestFiles, b.TestFiles) && slices.Equal(a.TestImports, b.TestImports) && slices.Equal(a.Testdata, b.Testdata)
}

// appendRecords appends the tests that a process recorded with the coverage
// source source, with their covered files that the map tracks; a test that a process recorded twice
// keeps its last record.
func appendRecords(tests []tiaTest, records []localtia.TestRecord, known map[string]tiaFile, source string) []tiaTest {
	at := map[localtia.Test]int{}
	for _, record := range records {
		t := tiaTest{Test: record.Test, Status: record.Status, Source: source, Covered: []string{}}
		for _, file := range record.Files {
			name, ok := localtia.RepositoryPath(file.Path)
			if _, tracked := known[name]; !ok || !tracked {
				continue // Outside the repository: the configuration's.
			}
			t.Covered = append(t.Covered, name)
			if len(file.Bitmap) != 0 {
				if t.Bitmaps == nil {
					t.Bitmaps = map[string][]byte{}
				}
				t.Bitmaps[name] = file.Bitmap
			}
		}
		slices.Sort(t.Covered)
		t.Covered = slices.Compact(t.Covered)
		if i, ok := at[t.Test]; ok {
			tests[i] = t
			continue
		}
		at[t.Test] = len(tests)
		tests = append(tests, t)
	}
	return tests
}

// tiaSelection returns what the test process of the package in dir skips,
// with the union of the skipped tests' line bitmaps, which completes the
// package's coverage profile, or nil when it skips nothing. counters is the
// source of the coverage counters that the run collects, or "": a test that
// another source recorded, as file marks or counters of other packages,
// holds no line coverage that they could take.
func tiaSelection(dir string, skip []*tiaTest, counters string) *localtia.PackageSelection {
	if len(skip) == 0 {
		return nil
	}
	selection := &localtia.PackageSelection{Dir: dir}
	coverage := map[string]*filebitmap.FileBitmap{}
	for _, t := range skip {
		selection.Skip = append(selection.Skip, t.Test)
		if counters != "" && t.Source != counters {
			selection.NoLineCoverage = append(selection.NoLineCoverage, t.Test)
			continue
		}
		for file, bitmap := range t.Bitmaps {
			b := filebitmap.NewFileBitmapFromBytes(bitmap)
			if existing := coverage[file]; existing != nil {
				b = filebitmap.Or(existing, b, false)
			}
			coverage[file] = b
		}
	}
	if len(coverage) != 0 {
		selection.Coverage = make(map[string][]byte, len(coverage))
		for file, b := range coverage {
			selection.Coverage[file] = b.GetBuffer()
		}
	}
	return selection
}

// localTIACoverageSource names the per-test coverage that the build collects,
// or returns "" for none that local TIA can use: Go's counters in set mode
// tell no test apart.
func localTIACoverageSource(plan *Plan, opts options) string {
	switch {
	case plan.fileCoverageContext:
		return "file marks with contexts"
	case plan.fileCoverage:
		return "file marks"
	case !opts.coverage:
		return ""
	}
	mode := opts.coverMode
	if mode == "" && opts.race {
		mode = "atomic"
	}
	if mode != "count" && mode != "atomic" {
		return ""
	}
	return "counters " + mode
}

// localTIASourceID names the coverage source that records a run's tests, as
// far as what it can see goes: file marks, or Go's counters with the
// repository's packages that they count, which -coverpkg's patterns select
// from the run's directory; "" for none. The counters' mode changes nothing
// that they see.
func localTIASourceID(plan *Plan, opts options, graph *tiaMap) string {
	switch source := localTIACoverageSource(plan, opts); {
	case source == "":
		return ""
	case !strings.HasPrefix(source, "counters"):
		return "file marks"
	}
	var counted []string
	for path, node := range graph.Packages {
		if node.counted {
			counted = append(counted, path)
		}
	}
	slices.Sort(counted)
	sum := sha256.Sum256([]byte(strings.Join(counted, "\n")))
	return "counters " + hex.EncodeToString(sum[:8])
}

// localTIAConfiguration returns the key of the map that a run reads and
// updates: what changes the tests' build or behavior without changing a file
// of the repository. Coverage is not part of it: a run that does not record,
// such as a pull request's or a developer's without coverage flags, reads the
// map that a run with them recorded. Each test keeps the coverage source
// that recorded it instead. goflags is the caller's GOFLAGS.
func localTIAConfiguration(opts options, goflags string, env *goEnvironment, goVersion string) string {
	h := sha256.New()
	fmt.Fprintf(h, "map %d\nddto %s\ngo %s %s\nplatform %s/%s cgo %s\n", localTIAMapVersion, version.Number, goVersion, env.GOEXPERIMENT, env.GOOS, env.GOARCH, env.CGOEnabled)
	for _, name := range buildSettings {
		fmt.Fprintf(h, "env %s %q\n", name, env.Build[name])
	}
	// parseOptions already accepted GOFLAGS.
	flags, _ := splitFlags(goflags)
	for _, flag := range flags {
		name, _, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		if canonical, _, known := lookupFlag(name); known && !localTIAIgnoredFlags[canonical] {
			fmt.Fprintf(h, "goflag %q\n", flag)
		}
	}
	for _, argument := range opts.arguments {
		switch {
		case argument.flag != "" && localTIAIgnoredFlags[argument.flag]:
			continue
		case argument.flag == "" && len(argument.raw) == 1 && slices.Contains(opts.packages, argument.raw[0]):
			continue // A package pattern: the map keeps each package apart.
		}
		fmt.Fprintf(h, "argument %q\n", argument.raw)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// tiaGraphInput is what localTIAGraph needs from the run.
type tiaGraphInput struct {
	// tested is the build with tests, from go list -deps -test.
	tested []goPackage
	// root is the repository's root; excluded are directories inside it
	// whose packages are not the repository's, as a module cache.
	root     string
	excluded []string
	// moduleFiles are the main modules' go.mod, go.sum and go.work files.
	moduleFiles []string
	// coverPatterns are -coverpkg's patterns, relative to dir: Go's counters
	// count only the code of the packages that they match, or without them
	// that of the package under test.
	coverPatterns []string
	dir           string
}

// tiaGraph is this run's view of the repository: the graph, the absolute
// path of each of its files, and each test package's testdata directory.
type tiaGraph struct {
	*tiaMap
	paths    map[string]string
	testdata map[string]string
}

// localTIAGraph returns the repository's packages that the build with tests
// links, each test package with its test files and testdata. Only files
// inside the repository's root are tracked. File states stay empty.
func localTIAGraph(in tiaGraphInput) tiaGraph {
	g := tiaGraph{tiaMap: &tiaMap{Files: map[string]tiaFile{}, Packages: map[string]*tiaNode{}}, paths: map[string]string{}, testdata: map[string]string{}}
	repositoryPath := func(path string) (string, bool) {
		rel, err := filepath.Rel(in.root, path)
		if err != nil {
			return "", false
		}
		return localtia.RepositoryPath(rel)
	}
	inside := func(dir string) bool {
		if _, ok := repositoryPath(dir); !ok && filepath.Clean(dir) != filepath.Clean(in.root) {
			return false
		}
		for _, excluded := range in.excluded {
			if excluded != "" && (dir == excluded || filePathHasPrefix(dir, excluded)) {
				return false
			}
		}
		return true
	}
	files := func(dir string, lists ...[]string) []string {
		var names []string
		for _, list := range lists {
			for _, name := range list {
				path := name
				if !filepath.IsAbs(path) {
					path = filepath.Join(dir, name)
				}
				if rel, ok := repositoryPath(path); ok {
					names = append(names, rel)
					g.paths[rel] = path
				}
			}
		}
		slices.Sort(names)
		return slices.Compact(names)
	}
	bases := func(imports ...[]string) []string {
		var paths []string
		for _, list := range imports {
			for _, path := range list {
				base, _, _ := strings.Cut(path, " [")
				paths = append(paths, base)
			}
		}
		slices.Sort(paths)
		return slices.Compact(paths)
	}
	testMains := map[string]bool{}
	for _, p := range in.tested {
		if isTestMain(p) {
			testMains[strings.TrimSuffix(p.ImportPath, ".test")] = true
		}
	}
	// A package that imports one under test, and that only its tests import,
	// is listed only as the copy recompiled for the test, such as
	// "q [p.test]", with the same files: take each package once, from its
	// own entry or that copy. External test packages and test mains are
	// their package's tests.
	for _, plain := range []bool{true, false} {
		for i := range in.tested {
			p := &in.tested[i]
			base, _, _ := strings.Cut(p.ImportPath, " [")
			if plain != (base == p.ImportPath) || isTestMain(*p) || p.ForTest != "" && (base == p.ForTest || base == p.ForTest+"_test") ||
				g.Packages[base] != nil || p.Standard || p.Dir == "" || !inside(p.Dir) {
				continue
			}
			node := &tiaNode{dir: p.Dir, Imports: bases(p.Imports), counted: in.countedByCover(base, p),
				Files: files(p.Dir, p.GoFiles, p.CgoFiles, p.EmbedFiles, p.SFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles)}
			node.embedded = files(p.Dir, p.EmbedFiles)
			if testMains[base] {
				node.embedded = files(p.Dir, p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles)
				node.Tested = true
				node.TestFiles = files(p.Dir, p.TestGoFiles, p.XTestGoFiles, p.TestEmbedFiles, p.XTestEmbedFiles)
				node.TestImports = bases(p.TestImports, p.XTestImports)
				g.testdata[base] = filepath.Join(p.Dir, "testdata")
				node.Testdata = files("", testdataFiles(g.testdata[base]))
			}
			g.Packages[base] = node
		}
	}
	g.Modules = files("", in.moduleFiles)
	return g
}

// countedByCover reports whether -coverpkg's patterns name the package p,
// whose import path is base, so that Go's counters count its code in every
// test binary. Without them, the counters count only the package under
// test, which unseen knows.
func (in *tiaGraphInput) countedByCover(base string, p *goPackage) bool {
	if len(in.coverPatterns) == 0 {
		return false
	}
	named := *p
	named.ImportPath = base
	for _, pattern := range in.coverPatterns {
		if matchPackagePattern(pattern, in.dir, &named) {
			return true
		}
	}
	return false
}

// testdataFiles returns every file under dir, sorted, or none without it.
// It reads the directories only.
func testdataFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	slices.Sort(files)
	return files
}

// fileStates returns the state of each file, by repository path, and how
// many files it read. A file whose size and modification time match the
// state that known vouches for is not read; any other is read, hashed and,
// for a Go file, parsed. now is when the files are read.
func fileStates(paths map[string]string, known map[string]tiaFile, now time.Time) (map[string]tiaFile, int) {
	states := make(map[string]tiaFile, len(paths))
	read := 0
	for name, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			states[name] = tiaFile{}
			continue
		}
		state := tiaFile{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if old, ok := known[name]; ok && !old.racy && old.ModTime != 0 && old.ModTime == state.ModTime && old.Size == state.Size && old.Hash != "" {
			states[name] = old
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			states[name] = tiaFile{}
			continue
		}
		read++
		state.Hash = contentHash(data)
		if strings.HasSuffix(name, ".go") {
			declarations, code, ok := instrument.GoFileDeclarations(data)
			if !ok {
				declarations = data
			}
			state.Decl, state.Code = contentHash(declarations), code
		}
		state.racy = now.Sub(info.ModTime()) < localTIARacyWindow
		states[name] = state
	}
	return states, read
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// declarationsChanged returns the files whose declarations differ between the
// two tables, which every test of a package whose closure holds them depends
// on: a test can use a constant, a variable or a type without running any of
// the file's code. A file that only one table holds has no previous
// declarations to compare.
func declarationsChanged(stored, current map[string]tiaFile) map[string]bool {
	changed := map[string]bool{}
	for file, state := range current {
		if old, ok := stored[file]; ok && old.Decl != state.Decl {
			changed[file] = true
		}
	}
	return changed
}

// statsChanged returns the files whose size, modification time or existence
// differ from states, without reading them.
func statsChanged(paths map[string]string, states map[string]tiaFile) map[string]bool {
	changed := map[string]bool{}
	for name, path := range paths {
		info, err := os.Stat(path)
		state := states[name]
		switch {
		case err != nil || !info.Mode().IsRegular():
			if state.Hash != "" {
				changed[name] = true
			}
		case state.Hash == "" || info.Size() != state.Size || info.ModTime().UnixNano() != state.ModTime:
			changed[name] = true
		}
	}
	return changed
}

// localTIARun is one invocation's local TIA: the map that it reads and
// updates, and the repository as this run found it before go test.
type localTIARun struct {
	debug    *cliDebug
	stderr   io.Writer
	state    string
	mapDir   string
	key      string
	counters bool
	current  tiaGraph
	// spent is how long local TIA took before and after go test.
	spent time.Duration
	// loaded is the map that the run read, and loadedFile its file, which
	// the merge reads again only if another run replaced it meanwhile.
	loaded     *tiaMap
	loadedFile os.FileInfo
}

// narrowsSubtests reports whether a -run or -skip pattern, in args or in
// goflags, the caller's GOFLAGS, has a slash: it can select some subtests of a
// top-level test and not others, whose coverage and status would then not be
// the whole test's. A pattern without one selects whole top-level tests.
func narrowsSubtests(opts options, goflags string) bool {
	var words []string
	for _, argument := range opts.arguments {
		words = append(words, argument.raw...)
	}
	flags, _ := splitFlags(goflags)
	words = append(words, flags...)
	for i, word := range words {
		name, value, hasValue := strings.Cut(strings.TrimLeft(word, "-"), "=")
		if !strings.HasPrefix(word, "-") || strings.TrimPrefix(name, "test.") != "run" && strings.TrimPrefix(name, "test.") != "skip" {
			continue
		}
		if !hasValue && i+1 < len(words) {
			value = words[i+1]
		}
		if strings.Contains(value, "/") {
			return true
		}
	}
	return false
}

// localTIARefusal returns why the run cannot use local TIA, after
// preparation and before the Git worker hands its state over, or "". A run
// that cannot record only skips instead (localTIACannotRecord).
func localTIARefusal(plan *Plan, opts options) string {
	switch {
	case opts.overlay != "":
		// Go compiles the overlay's files instead of the repository's,
		// whose contents local TIA compares.
		return "a user's -overlay replaces files that it would compare"
	case plan.vendorProvided:
		// Go compiles the vendored packages from the CLI's cache, as
		// file coverage also refuses (fileCoverageRefusal).
		return "the CLI provides the vendored dependencies from its cache, outside the repository"
	}
	return ""
}

// localTIACannotRecord returns why the run's tests cannot record, or "".
func localTIACannotRecord(plan *Plan, opts options) string {
	switch source := localTIACoverageSource(plan, opts); {
	case source == "":
		return "recording needs per-test coverage: build with -cover -coverpkg=./... and -covermode=count or atomic, with -race, or with --experimental-file-coverage"
	case strings.HasPrefix(source, "counters") && len(opts.coverPatterns) == 0:
		// Without it, the counters see only the package under test: every
		// test would depend on every other file that it links.
		return "recording with Go's coverage counters needs -coverpkg, such as -coverpkg=./..."
	}
	return ""
}

// startLocalTIA selects the tests to skip and writes the selection to the
// state that the Git worker hands to the test processes, which record their
// tests when record is set and the run can record. A run that cannot, for
// want of per-test coverage that local TIA can use, says so and only skips.
// It returns why it could not select, in which case the tests run without
// local TIA.
func startLocalTIA(ctx context.Context, plan *Plan, opts options, record bool, dir, state, root string, debug *cliDebug, stderr io.Writer) (*localTIARun, string) {
	started := time.Now()
	phase := debug.start("local tia selection")
	if root == "" {
		phase.finish(errors.New("no checkout"))
		return nil, "the Git worker found no checkout"
	}
	common, err := gitCommonDir(ctx, root)
	if err != nil {
		phase.finish(err)
		return nil, "the Git common directory is unknown: " + err.Error()
	}
	source := localTIACoverageSource(plan, opts)
	run := &localTIARun{debug: debug, stderr: stderr, state: state, mapDir: filepath.Join(common, "ddtest", "tia"), counters: strings.HasPrefix(source, "counters")}
	run.key = localTIAConfiguration(opts, callerGoFlags(), plan.environment, plan.goVersion)
	excluded := []string{plan.environment.GOROOT, plan.environment.GOMODCACHE}
	if plan.gorootMirror != nil {
		excluded = append(excluded, plan.gorootMirror.root)
	}
	run.current = localTIAGraph(tiaGraphInput{
		tested: plan.tested, root: root, excluded: excluded, moduleFiles: localTIAModuleFiles(plan),
		coverPatterns: opts.coverPatterns, dir: dir,
	})
	stored, err := run.readMap(ctx)
	if err != nil {
		phase.finish(err)
		return nil, "the map could not be read: " + err.Error()
	}
	var read int
	run.current.Files, read = fileStates(run.current.paths, stored.Files, time.Now())
	if record {
		if cannot := localTIACannotRecord(plan, opts); cannot != "" {
			fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN: local TIA ("+LocalTIARecordEnv+"): "+cannot+"; tests only skip, nothing is recorded")
			record = false
		}
	}
	if record && narrowsSubtests(opts, callerGoFlags()) {
		// The run skips as any other does, and records nothing.
		debug.printf("local tia record=false reason=%q", "-run or -skip selects subtests")
		record = false
	}
	if record {
		run.current.Source = localTIASourceID(plan, opts, run.current.tiaMap)
	}
	// The run's coverage counters, recording or not, need line coverage.
	counters := ""
	if run.counters {
		counters = localTIASourceID(plan, opts, run.current.tiaMap)
	}
	selection := localtia.Selection{Record: record}
	skipped, tested := 0, 0
	for path, node := range run.current.Packages {
		if !node.Tested {
			continue
		}
		tested++
		// A run that does not record has no coverage source of its own: its
		// tests' sources decide what they could not see.
		if record {
			node.Unseen = map[string][]string{run.current.Source: run.current.unseen(path, run.counters, len(opts.coverPatterns) != 0)}
		}
		if selected := tiaSelection(node.dir, skippable(stored, run.current.tiaMap, path), counters); selected != nil {
			selection.Packages = append(selection.Packages, *selected)
			skipped += len(selected.Skip)
		}
	}
	err = utils.WriteCLIGitStateFile(state, localtia.SelectionFile, selection)
	phase.finish(err)
	if err != nil {
		return nil, "the selection could not be written: " + err.Error()
	}
	run.spent = time.Since(started)
	debug.printf("local tia record=%t map=%s source=%q test_packages=%d packages=%d files=%d files_read=%d stored_packages=%d skippable=%d", record, filepath.Join(run.mapDir, run.key+".json"), source, tested, len(run.current.Packages), len(run.current.paths), read, len(stored.Packages), skipped)
	return run, ""
}

// localTIAModuleFiles returns the main modules' go.mod and go.sum files and
// the workspace's go.work files, whether they exist or not: a go.sum that
// appears changes the build too.
func localTIAModuleFiles(plan *Plan) []string {
	var files []string
	seen := map[string]bool{}
	for _, p := range plan.tested {
		if p.Module != nil && p.Module.Main && p.Module.Dir != "" && !seen[p.Module.Dir] {
			seen[p.Module.Dir] = true
			files = append(files, filepath.Join(p.Module.Dir, "go.mod"), filepath.Join(p.Module.Dir, "go.sum"))
		}
	}
	if work := plan.environment.GOWORK; work != "" && work != "off" {
		files = append(files, work, work+".sum")
	}
	return files
}

// gitCommonDir returns the Git directory that every worktree of the
// repository that holds root shares.
func gitCommonDir(ctx context.Context, root string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir, nil
}

// finish merges what the test processes recorded into the map, once go test
// has exited and before the state is removed. It reads no file of the
// repository: whether one changed while go test ran comes from its size and
// modification time. After a run that does not record, the merge still takes
// the files as the run found them and drops the tests that their changes
// affect, and adds none.
func (r *localTIARun) finish(ctx context.Context) {
	if r == nil {
		return
	}
	started := time.Now()
	phase := r.debug.start("local tia merge")
	records := readTIARecords(r.state)
	changed := statsChanged(r.current.paths, r.current.Files)
	runs := map[string]tiaPackageRun{}
	for path, node := range r.current.Packages {
		recorded, ok := records[filepath.Clean(node.dir)]
		if !node.Tested || !ok {
			continue // Go took the result from its cache.
		}
		run := tiaPackageRun{records: recorded.tests, incomplete: recorded.incomplete}
		files, _ := r.current.closure(path)
		run.changed = hasAny(files, changed) || !slices.Equal(testdataFiles(r.current.testdata[path]), r.current.absolute(node.Testdata))
		if run.changed {
			r.debug.printf("local tia package=%s changed while its tests ran; records discarded", path)
		}
		runs[path] = run
	}
	stored, unlock, err := r.lockMap(ctx)
	size := 0
	if err == nil {
		var data []byte
		if data, err = json.Marshal(encodeTIAMap(mergeTIAMap(stored, r.current.tiaMap, runs))); err == nil {
			size = len(data)
			// Written whole and renamed, as the state's files are.
			err = utils.WriteCLIGitStateFile(r.mapDir, r.key+".json", json.RawMessage(data))
		}
		unlock()
	}
	phase.finish(err)
	r.spent += time.Since(started)
	r.debug.printf("local tia merged packages_run=%d map_bytes=%d overhead=%s", len(runs), size, r.spent.Round(time.Microsecond))
	if err != nil {
		fmt.Fprintln(r.stderr, version.BuildLogPrefix+" WARN: local TIA ("+LocalTIAEnv+"): the map was not updated: "+err.Error())
	}
}

// absolute returns the absolute paths of files, sorted.
func (g tiaGraph) absolute(files []string) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = g.paths[file]
	}
	slices.Sort(paths)
	return paths
}

// tiaRecords are the tests that a package's test processes recorded, and
// whether one of them ended without completing its records.
type tiaRecords struct {
	tests      []localtia.TestRecord
	incomplete bool
}

// readTIARecords returns what this run's test processes recorded, by their
// package's directory. A package whose process did not run is absent.
func readTIARecords(state string) map[string]tiaRecords {
	records := map[string]tiaRecords{}
	entries, _ := os.ReadDir(state)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, localtia.RecordsFilePrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(state, name))
		var file localtia.Records
		if err == nil {
			err = json.Unmarshal(data, &file)
		}
		if err != nil || file.Dir == "" {
			continue
		}
		dir := filepath.Clean(file.Dir)
		recorded := records[dir]
		recorded.tests = append(recorded.tests, file.Tests...)
		recorded.incomplete = recorded.incomplete || !file.Complete
		records[dir] = recorded
	}
	return records
}

// readMap reads the map under a shared lock, or returns an empty one when
// none exists yet.
func (r *localTIARun) readMap(ctx context.Context) (*tiaMap, error) {
	unlock, err := r.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.loadMap()
}

// lockMap reads the map under an exclusive lock, which the returned function
// releases once the caller has written it.
func (r *localTIARun) lockMap(ctx context.Context) (*tiaMap, func(), error) {
	unlock, err := r.lock(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	stored, err := r.loadMap()
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return stored, unlock, nil
}

// loadMap reads the map. A map that does not parse, or of another format,
// is replaced: it only costs the tests that it would have skipped.
func (r *localTIARun) loadMap() (*tiaMap, error) {
	name := filepath.Join(r.mapDir, r.key+".json")
	info, err := os.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return emptyTIAMap(), nil
	}
	if err != nil {
		return nil, err
	}
	if r.loaded != nil && os.SameFile(info, r.loadedFile) && info.Size() == r.loadedFile.Size() && info.ModTime().Equal(r.loadedFile.ModTime()) {
		return r.loaded, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var stored tiaMapFile
	if json.Unmarshal(data, &stored) != nil || stored.Version != localTIAMapVersion {
		r.debug.printf("local tia map unreadable; starting a new one")
		return emptyTIAMap(), nil
	}
	m, ok := stored.decode()
	if !ok {
		r.debug.printf("local tia map inconsistent; starting a new one")
		return emptyTIAMap(), nil
	}
	r.loaded, r.loadedFile = m, info
	return m, nil
}

func emptyTIAMap() *tiaMap {
	return &tiaMap{Files: map[string]tiaFile{}, Packages: map[string]*tiaNode{}}
}

// lock takes the map's lock file, shared or exclusive, so that runs in other
// worktrees of the repository never write over each other's merges. Where
// the platform has no file locks, it takes none.
func (r *localTIARun) lock(ctx context.Context, exclusive bool) (func(), error) {
	if err := os.MkdirAll(r.mapDir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(r.mapDir, r.key+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := waitForLock(ctx, file, exclusive); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = file.Close()
		return nil, err
	}
	return func() { _ = file.Close() }, nil
}

// tiaMapFile is the stored form of a map: one table of files and one of
// line bitmaps, which the packages and tests name by index, so that a file
// that many packages share, or the lines that many tests cover in a shared
// helper, appear once.
type tiaMapFile struct {
	Version  int                         `json:"version"`
	Files    []tiaStoredFile             `json:"files"`
	Bitmaps  [][]byte                    `json:"bitmaps,omitempty"`
	Modules  []int                       `json:"modules,omitempty"`
	Packages map[string]tiaStoredPackage `json:"packages"`
}

// tiaStoredFile is a file's path and state. A modification time of 0 does not
// vouch for the hash: the next run reads the file.
type tiaStoredFile struct {
	Path    string `json:"p"`
	Size    int64  `json:"s,omitempty"`
	ModTime int64  `json:"m,omitempty"`
	Hash    string `json:"h,omitempty"`
	Decl    string `json:"d,omitempty"`
	Code    bool   `json:"c,omitempty"`
}

type tiaStoredPackage struct {
	Files       []int            `json:"files,omitempty"`
	Imports     []string         `json:"imports,omitempty"`
	Tested      bool             `json:"tested,omitempty"`
	TestFiles   []int            `json:"test_files,omitempty"`
	TestImports []string         `json:"test_imports,omitempty"`
	Testdata    []int            `json:"testdata,omitempty"`
	Unseen      map[string][]int `json:"unseen,omitempty"`
	Tests       []tiaStoredTest  `json:"tests,omitempty"`
}

// tiaStoredTest is a test; Bitmaps maps the index of a covered file to the
// index of its line bitmap.
type tiaStoredTest struct {
	localtia.Test
	Status  string      `json:"status"`
	Source  string      `json:"source,omitempty"`
	Covered []int       `json:"covered"`
	Bitmaps map[int]int `json:"bitmaps,omitempty"`
}

// encodeTIAMap returns m's stored form, with the files that a package or a
// test names.
func encodeTIAMap(m *tiaMap) tiaMapFile {
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
	index := map[string]int{}
	for _, file := range slices.Sorted(maps.Keys(used)) {
		state := m.Files[file]
		modTime := state.ModTime
		if state.racy {
			modTime = 0
		}
		index[file] = len(stored.Files)
		stored.Files = append(stored.Files, tiaStoredFile{Path: file, Size: state.Size, ModTime: modTime, Hash: state.Hash, Decl: state.Decl, Code: state.Code})
	}
	indices := func(files []string) []int {
		out := make([]int, len(files))
		for i, file := range files {
			out[i] = index[file]
		}
		return out
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
		for _, t := range node.Tests {
			s := tiaStoredTest{Test: t.Test, Status: t.Status, Source: t.Source, Covered: indices(t.Covered)}
			if len(t.Bitmaps) != 0 {
				s.Bitmaps = make(map[int]int, len(t.Bitmaps))
				for file, bitmap := range t.Bitmaps {
					i, ok := index[file]
					if !ok {
						continue
					}
					b, ok := bitmaps[string(bitmap)]
					if !ok {
						b = len(stored.Bitmaps)
						bitmaps[string(bitmap)] = b
						stored.Bitmaps = append(stored.Bitmaps, bitmap)
					}
					s.Bitmaps[i] = b
				}
			}
			p.Tests = append(p.Tests, s)
		}
		stored.Packages[path] = p
	}
	return stored
}

// decode returns the map that f stores, or false when an index is out of
// range.
func (f tiaMapFile) decode() (*tiaMap, bool) {
	m := emptyTIAMap()
	for _, file := range f.Files {
		m.Files[file.Path] = tiaFile{Size: file.Size, ModTime: file.ModTime, Hash: file.Hash, Decl: file.Decl, Code: file.Code}
	}
	ok := true
	path := func(i int) (string, bool) {
		if i < 0 || i >= len(f.Files) {
			ok = false
			return "", false
		}
		return f.Files[i].Path, true
	}
	paths := func(indices []int) []string {
		var out []string
		for _, i := range indices {
			if name, valid := path(i); valid {
				out = append(out, name)
			}
		}
		return out
	}
	m.Modules = paths(f.Modules)
	for name, p := range f.Packages {
		node := &tiaNode{Files: paths(p.Files), Imports: p.Imports, Tested: p.Tested, TestFiles: paths(p.TestFiles),
			TestImports: p.TestImports, Testdata: paths(p.Testdata)}
		for source, unseen := range p.Unseen {
			if node.Unseen == nil {
				node.Unseen = map[string][]string{}
			}
			node.Unseen[source] = paths(unseen)
		}
		for _, s := range p.Tests {
			t := tiaTest{Test: s.Test, Status: s.Status, Source: s.Source, Covered: paths(s.Covered)}
			for i, b := range s.Bitmaps {
				file, valid := path(i)
				if b < 0 || b >= len(f.Bitmaps) {
					ok = false
					continue
				}
				if valid {
					if t.Bitmaps == nil {
						t.Bitmaps = map[string][]byte{}
					}
					t.Bitmaps[file] = f.Bitmaps[b]
				}
			}
			node.Tests = append(node.Tests, t)
		}
		m.Packages[name] = node
	}
	return m, ok
}
