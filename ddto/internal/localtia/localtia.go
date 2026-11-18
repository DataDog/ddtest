// Package localtia holds what ddto's CLI and the Mini test runtime share for
// local Test Impact Analysis (DD_TIA_ENABLED): the files that they exchange
// through the CLI's Git state directory, and the test runtime's side of it.
//
// Local TIA uses ITR's per-test coverage, from Go's counters or from ddto's
// file marks, but computes the skippable tests itself instead of asking
// Datadog. Before go test runs, the CLI compares each test package's files
// with the per-test dependency map that earlier runs stored, and writes the
// tests that it may skip to SelectionFile. A test process that finds that
// file behaves as if Datadog had enabled ITR and per-test coverage: it skips
// what the file lists, through ITR's own decision, and writes the coverage
// and final status of each top-level test that ran to a records file. After
// go test exits, the CLI merges those records into the map.
package localtia

import (
	"path"
	"path/filepath"
	"strings"
)

// Files in the CLI's Git state directory.
const (
	// SelectionFile holds the Selection that the CLI writes before go test
	// runs. Its presence is what activates local TIA in a test process.
	SelectionFile = "tia.json"
	// RecordsFilePrefix starts the name of each test process's Records file.
	RecordsFilePrefix = "tia-records-"
)

// Test names a top-level test as ITR does: the package's import path as the
// module (an external test package ends in _test, and every main package is
// main), the base name of the test's file as the suite, and the test's name.
type Test struct {
	Module string `json:"module"`
	Suite  string `json:"suite"`
	Name   string `json:"name"`
}

// Selection is what the CLI hands the test processes: for each test package
// with tests to skip, those tests.
type Selection struct {
	Packages []PackageSelection `json:"packages,omitempty"`
}

// PackageSelection is the selection of the test package in Dir, the
// directory that go test runs its test binary in.
type PackageSelection struct {
	Dir string `json:"dir"`
	// Skip are the tests that local TIA lets ITR skip.
	Skip []Test `json:"skip"`
	// Coverage is the union of the skipped tests' line bitmaps by
	// repository path, from their last run, with which ITR completes the
	// package's coverage profile.
	Coverage map[string][]byte `json:"coverage,omitempty"`
}

// Records are what one test process ran: its package's directory, where go
// test started it, and its top-level tests that finished with coverage.
// Complete is false until the process has written its tests: a process that
// crashed leaves it false.
type Records struct {
	Dir      string       `json:"dir"`
	Complete bool         `json:"complete"`
	Tests    []TestRecord `json:"tests"`
}

// TestRecord is one top-level test's final status after retries (pass, fail
// or skip) and the files that its first execution covered.
type TestRecord struct {
	Test
	Status string `json:"status"`
	Files  []File `json:"files"`
}

// File is a covered file's repository path, with slashes, and its line
// bitmap when Go's counters provide one.
type File struct {
	Path   string `json:"path"`
	Bitmap []byte `json:"bitmap,omitempty"`
}

// RepositoryPath returns name, a path relative to the repository's root, in
// the form that local TIA stores: cleaned, with slashes. It reports false
// for a path outside the repository, such as one that the CLI or a test
// process could not make relative to it.
func RepositoryPath(name string) (string, bool) {
	return repositoryPath(name, filepath.Separator)
}

// repositoryPath is RepositoryPath for a system whose separator is
// separator: only Windows separates elements with backslashes, which a
// file name may contain elsewhere.
func repositoryPath(name string, separator byte) (string, bool) {
	if separator == '\\' {
		name = strings.ReplaceAll(name, `\`, "/")
		if len(name) >= 2 && name[1] == ':' {
			return "", false // a volume, as C:/repo
		}
	}
	if name == "" || path.IsAbs(name) {
		return "", false
	}
	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}
