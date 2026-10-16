package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// --experimental-file-coverage: the packages of the repository compile with
// a mark per source file, and Mini takes each test's coverage from the marks
// instead of Go's coverage counters. See instrument.MarkFileCoverage.

const (
	// Generated files: a package's own bank, its internal test files' bank
	// and its external test package's bank.
	fileCoverageBankFile      = "zz_ddto_file_coverage.go"
	fileCoverageTestBankFile  = "zz_ddto_file_coverage_test.go"
	fileCoverageXTestBankFile = "zz_ddto_file_coverage_x_test.go"

	// fileCoverageListFields are what file coverage needs from the build
	// with tests: every source file, including test embeds that only the
	// test build resolves.
	fileCoverageListFields = "Dir,Name,ImportPath,ForTest,Standard,Module,Error,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,EmbedPatterns,TestEmbedPatterns,XTestEmbedPatterns,EmbedFiles,TestEmbedFiles,XTestEmbedFiles,SFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SwigFiles,SwigCXXFiles,SysoFiles"
)

// fileCoverageSummary is what a build with file coverage instrumented.
type fileCoverageSummary struct {
	packages, marked, always int
}

// fileCoverageRefusal returns why the build keeps Go's coverage counters for
// per-test coverage, or "". Go's coverage flags keep them: marks would change
// what cover counts. Under -race, the marks' atomics would order the
// goroutines that the race detector checks.
func fileCoverageRefusal(plan *Plan, opts options, vendorProvided bool, listed []goPackage, replacements map[string]string) string {
	switch {
	case !plan.mini:
		return "the SDK runtime"
	case opts.coverage:
		return "Go's coverage flags are set"
	case opts.race:
		return "-race"
	case plan.orchestrion:
		return "Orchestrion's -toolexec"
	case vendorProvided:
		// The vendored packages compile from ddto's cache, outside the
		// repository, and their files would be missing.
		return "ddto provides the vendored dependencies from its cache"
	}
	coverage := findPackage(listed, miniCoveragePackage)
	if coverage == nil {
		return "the runtime's coverage package not listed"
	}
	return checkSources(coverage, replacements, instrument.FileCoverageRuntimeError)
}

// fileCoverageBankPlan is one generated bank file and the files it marks.
type fileCoverageBankPlan struct {
	logical string
	bank    instrument.FileCoverageBank
	files   []string // marked candidates, absolute, in candidate order
	// compiled are the package variant's other Go files: its cgo files.
	compiled []string
}

// isTestMain reports the test main that go list -test synthesizes for p.test:
// its only Go file is in the build cache, so its name is absolute. A package
// whose own import path ends in .test lists relative names.
func isTestMain(p goPackage) bool {
	base, _, _ := strings.Cut(p.ImportPath, " [")
	return p.Name == "main" && strings.HasSuffix(base, ".test") && len(p.GoFiles) == 1 && filepath.IsAbs(p.GoFiles[0])
}

// matchingEmbedPattern returns the pattern of a package in dir that would
// embed the generated file, or "": the file itself matches, or a directory
// that holds it, which a pattern embeds with everything inside. Patterns
// separate elements with slashes; on Windows, a pattern with a backslash
// refuses file coverage before it gets here.
func matchingEmbedPattern(dir string, patterns []string, generated string) string {
	rel, err := filepath.Rel(dir, generated)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	for _, pattern := range patterns {
		glob := filepath.FromSlash(strings.TrimPrefix(pattern, "all:"))
		for candidate := rel; candidate != "." && candidate != string(filepath.Separator); candidate = filepath.Dir(candidate) {
			if ok, _ := filepath.Match(glob, candidate); ok {
				return pattern
			}
		}
	}
	return ""
}

// prepareFileCoverage instruments the listed packages of the build with
// tests that belong to the repository. roots are the directories whose
// packages are the repository's; excluded are directories that never are,
// such as GOROOT and the module cache. userOverlay holds the paths that the
// caller's -overlay replaces: a file that ddto replaced itself is reported
// for every test instead of marked.
func prepareFileCoverage(tested []goPackage, roots, excluded []string, replacements map[string]string, userOverlay map[string]bool, temp string, add func(logical, content string) error) (fileCoverageSummary, string, error) {
	var summary fileCoverageSummary
	inside := func(dir string, roots []string) bool {
		for _, root := range roots {
			if rel, err := filepath.Rel(root, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	// A plugin built without the marks would not load: Go checks that the
	// packages it shares with the test binary are the same.
	for _, p := range tested {
		if base, _, _ := strings.Cut(p.ImportPath, " ["); base == "plugin" {
			return summary, "plugin", nil
		}
	}
	testsBuilt := map[string]bool{}
	// Embedding reads the overlay: a Go file that a package embeds keeps
	// its content and is reported for every test. A pattern can match the
	// files of a package in a subdirectory, so the set spans the build.
	embedded := map[string]bool{}
	for _, p := range tested {
		if p.ForTest != "" {
			testsBuilt[p.ForTest] = true
		}
		for _, list := range [][]string{p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles} {
			for _, name := range list {
				embedded[filepath.Join(p.Dir, name)] = true
			}
		}
	}
	// A package that imports the one under test, and that only its tests
	// import, is listed only as the copy recompiled for the test, such as
	// "q [p.test]": take each package once, from its own entry or that copy.
	// External test packages ("p_test [p.test]") and test mains come from
	// p's entry.
	var packages []goPackage
	seen := map[string]bool{}
	for _, p := range tested {
		base, _, _ := strings.Cut(p.ImportPath, " [")
		if isTestMain(p) || p.ForTest != "" && base == p.ForTest+"_test" || seen[base] {
			continue
		}
		seen[base] = true
		p.ImportPath = base
		packages = append(packages, p)
	}
	var plans []fileCoverageBankPlan
	for _, p := range packages {
		if p.Standard || p.Error != nil || p.Dir == "" || p.Name == "" ||
			p.ImportPath == miniModule || strings.HasPrefix(p.ImportPath, miniModule+"/") || !inside(p.Dir, roots) || inside(p.Dir, excluded) {
			continue
		}
		summary.packages++
		join := func(names ...[]string) []string {
			var paths []string
			for _, list := range names {
				for _, name := range list {
					paths = append(paths, filepath.Join(p.Dir, name))
				}
			}
			return paths
		}
		// Every non-Go file of a linked package goes to every test: a test
		// can read an embedded variable without running its package's code.
		always := join(p.CgoFiles, p.EmbedFiles, p.SFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles)
		regular := len(p.GoFiles)+len(p.CgoFiles) > 0
		if regular {
			plans = append(plans, fileCoverageBankPlan{
				logical:  filepath.Join(p.Dir, fileCoverageBankFile),
				bank:     instrument.FileCoverageBank{Package: p.Name, Marks: "__ddto_m", Always: always, Shared: true},
				files:    join(p.GoFiles),
				compiled: join(p.CgoFiles),
			})
		}
		if !testsBuilt[p.ImportPath] {
			continue
		}
		if len(p.TestGoFiles) > 0 {
			testAlways := join(p.TestEmbedFiles)
			if !regular {
				testAlways = append(always, testAlways...)
			}
			plans = append(plans, fileCoverageBankPlan{
				logical: filepath.Join(p.Dir, fileCoverageTestBankFile),
				bank:    instrument.FileCoverageBank{Package: p.Name, Marks: "__ddto_t", Always: testAlways, Shared: !regular},
				files:   join(p.TestGoFiles),
			})
		}
		if len(p.XTestGoFiles) > 0 {
			plans = append(plans, fileCoverageBankPlan{
				logical: filepath.Join(p.Dir, fileCoverageXTestBankFile),
				bank:    instrument.FileCoverageBank{Package: p.Name + "_test", Marks: "__ddto_x", Always: join(p.XTestEmbedFiles), Shared: true},
				files:   join(p.XTestGoFiles),
			})
		}
	}
	// Refuse before changing the overlay: a package without its bank would
	// report none of its files.
	for _, plan := range plans {
		if _, exists := replacements[plan.logical]; exists {
			return summary, "generated file in the overlay: " + plan.logical, nil
		}
		if _, err := os.Lstat(plan.logical); err == nil {
			return summary, "generated file already exists: " + plan.logical, nil
		}
		// Embed patterns resolve through the overlay: a bank they matched
		// would change what a package embeds. On Windows, go resolves a
		// backslash in a pattern as a path separator, with rules that are
		// not worth repeating here.
		for _, p := range packages {
			for _, patterns := range [][]string{p.EmbedPatterns, p.TestEmbedPatterns, p.XTestEmbedPatterns} {
				for _, pattern := range patterns {
					if runtime.GOOS == "windows" && strings.Contains(pattern, `\`) {
						return summary, fmt.Sprintf("%s's embed pattern %q has a backslash", p.ImportPath, pattern), nil
					}
				}
				if pattern := matchingEmbedPattern(p.Dir, patterns, plan.logical); pattern != "" {
					return summary, fmt.Sprintf("%s's embed pattern %q would match %s", p.ImportPath, pattern, plan.logical), nil
				}
			}
		}
	}
	// Every compiled Go file of a package shares names with its bank.
	for _, plan := range plans {
		for _, path := range append(append([]string(nil), plan.files...), plan.compiled...) {
			actual, replaced := replacements[path]
			if replaced && actual == "" {
				continue
			}
			if !replaced {
				actual = path
			}
			src, err := os.ReadFile(actual)
			if err != nil {
				return summary, "", err
			}
			if err := instrument.FileCoverageNameClashError(path, src); err != nil {
				return summary, err.Error(), nil
			}
		}
	}
	// Mark every file in memory first: a file that declares an identifier
	// with the generated code's prefix could collide with its package's
	// bank, so it refuses file coverage before the overlay changes.
	type markedFile struct {
		path   string
		source []byte
	}
	marked := make([][]markedFile, len(plans))
	for i := range plans {
		plan := &plans[i]
		for _, path := range plan.files {
			actual, replaced := replacements[path]
			if replaced && actual == "" {
				continue // The overlay removes the file from the build.
			}
			if replaced && !userOverlay[path] || embedded[path] {
				plan.bank.Always = append(plan.bank.Always, path)
				continue
			}
			if !replaced {
				actual = path
			}
			src, err := os.ReadFile(actual)
			if err != nil {
				return summary, "", err
			}
			source, refusal := instrument.MarkFileCoverage(path, src, plan.bank.Marks, len(plan.bank.Names))
			if refusal != "" {
				plan.bank.Always = append(plan.bank.Always, path)
				continue
			}
			marked[i] = append(marked[i], markedFile{path, source})
			plan.bank.Names = append(plan.bank.Names, path)
		}
	}
	for i := range plans {
		plan := &plans[i]
		for _, file := range marked[i] {
			backing := filepath.Join(temp, fmt.Sprintf("filecov-%d-%s", len(replacements), filepath.Base(file.path)))
			if err := os.WriteFile(backing, file.source, 0600); err != nil {
				return summary, "", err
			}
			replacements[file.path] = backing
		}
		sort.Strings(plan.bank.Always)
		summary.marked += len(plan.bank.Names)
		summary.always += len(plan.bank.Always)
		if err := add(plan.logical, plan.bank.Source()); err != nil {
			return summary, "", err
		}
	}
	return summary, "", nil
}

// repositoryRoots are the directories whose packages file coverage
// instruments: the Git working tree that holds dir, or, outside Git, the
// main modules' directories.
func repositoryRoots(dir string, listed []goPackage) []string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return []string{d}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	seen := map[string]bool{}
	var roots []string
	for _, p := range listed {
		if p.Module != nil && p.Module.Main && p.Module.Dir != "" && !seen[p.Module.Dir] {
			seen[p.Module.Dir] = true
			roots = append(roots, p.Module.Dir)
		}
	}
	return roots
}
