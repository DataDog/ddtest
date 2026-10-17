package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// miniCoveragePackage holds Mini's per-test coverage, which receives
// instrument.CoverageHook's registration.
const miniCoveragePackage = miniModule + "/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage"

// coverageHookRefusal returns why per-test coverage keeps text profiles in a
// Mini plan's builds, or "" when testing gets instrument.CoverageHook. The
// hook's layout is built once, so the process must not register coverage
// meta-data later, as plugins do. Its own code, and Mini's code between two
// captures, must not be instrumented, or each capture would add coverage of
// its own: no standard or Mini package may be selected for coverage. The
// toolchain and the selected Mini must also declare what the hook uses.
// listed holds every package that the package query returned; testPlugin
// reports package plugin among dependencies of test imports outside it.
// replacements is the overlay that the build uses.
func coverageHookRefusal(dir string, opts options, runtimePackage string, goroots []string, listed []goPackage, testPlugin bool, replacements map[string]string) string {
	var coverage *goPackage
	for i := range listed {
		p := &listed[i]
		if p.ImportPath == "plugin" || testPlugin {
			return "plugin"
		}
		for _, imports := range [][]string{p.Imports, p.TestImports, p.XTestImports} {
			for _, path := range imports {
				if path == "plugin" {
					return "plugin"
				}
			}
		}
		if p.ImportPath == miniCoveragePackage {
			coverage = p
		}
	}
	if coverage == nil || coverage.Module == nil {
		return "runtime packages not listed"
	}
	if len(opts.coverPatterns) == 0 {
		// Without -coverpkg, go test instruments the packages under test:
		// the named ones, apart from testing and the runtime that ddto names.
		for i := range listed {
			p := &listed[i]
			if p.DepOnly || p.ImportPath == "testing" || p.ImportPath == runtimePackage || len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
				continue
			}
			if p.Standard || p.Module != nil && p.Module.Path == miniModule {
				return "standard or runtime package under test"
			}
		}
	}
	for _, pattern := range opts.coverPatterns {
		if coverPatternReachesCollector(pattern, dir, coverage.Module.Dir, coverage.Module.Main, goroots) {
			return "coverage selection may instrument " + pattern
		}
	}
	if err := instrument.CoverageHookToolchainError(goroots[0]); err != nil {
		return err.Error()
	}
	// The registration must be in the files that the build compiles, which
	// the CLI's own runtime sources have: see ownSources.
	if ownSources(coverage, replacements) {
		return ""
	}
	sources := map[string][]byte{}
	for _, name := range coverage.GoFiles {
		path := filepath.Join(coverage.Dir, name)
		actual, replaced := replacements[path]
		if !replaced {
			actual = path
		} else if actual == "" {
			// The overlay deletes the file.
			continue
		}
		src, err := os.ReadFile(actual)
		if err != nil {
			return err.Error()
		}
		sources[path] = src
	}
	if err := instrument.CoverageHookRuntimeError(sources); err != nil {
		return err.Error()
	}
	return ""
}

// coverPatternReachesCollector reports whether a -coverpkg pattern could
// select a standard package or a package of the Mini module, in miniDir.
// Unclear patterns count as reaching it.
func coverPatternReachesCollector(pattern, dir, miniDir string, miniMain bool, goroots []string) bool {
	if pattern == "work" {
		// Main-module packages, which are Mini's only in its own module.
		return miniMain
	}
	if pattern == "." || pattern == ".." || strings.HasPrefix(pattern, "./") || strings.HasPrefix(pattern, "../") || filepath.IsAbs(pattern) || filepath.VolumeName(pattern) != "" {
		base := pattern
		if i := strings.Index(base, "..."); i >= 0 {
			// Directories below the base can match, also its siblings for a
			// wildcard within an element.
			base = filepath.Dir(base[:i] + "x")
		}
		if !filepath.IsAbs(base) {
			base = filepath.Join(dir, filepath.FromSlash(base))
		}
		base = filepath.Clean(base)
		for _, protected := range append([]string{miniDir}, goroots...) {
			if protected == "" {
				continue
			}
			if protected = filepath.Clean(protected); withinDir(base, protected) || withinDir(protected, base) {
				return true
			}
		}
		return false
	}
	first, _, _ := strings.Cut(pattern, "/")
	if !strings.Contains(first, ".") || strings.Contains(first, "...") {
		// Standard packages have no dot in their first element, nor have the
		// all, std, cmd and tool patterns: tool directives may name any package.
		return true
	}
	literal, wildcard := pattern, false
	if i := strings.Index(pattern, "..."); i >= 0 {
		literal, wildcard = pattern[:i], true
	}
	return literal == miniModule || strings.HasPrefix(literal, miniModule+"/") || wildcard && strings.HasPrefix(miniModule, literal)
}

// withinDir reports whether path is dir or lies below it.
func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
