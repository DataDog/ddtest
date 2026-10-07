package runner

import (
	"context"
	"os/exec"
	"sort"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Both optional integrations share the existing test-import graph query.
// Known dependency closures are not queried again; external helpers remain.
func resolveTestLibraries(ctx context.Context, dir string, opts options, packages []goPackage) (selected map[string]*goPackage, err error) {
	debug := debugFromContext(ctx)
	phase := debug.start("resolve test libraries")
	defer func() { phase.finish(err) }()
	selected = map[string]*goPackage{}
	paths, suite := testifyDependencyImports(packages)
	wanted := map[string]bool{}
	if suite {
		wanted[instrument.TestifySuiteImport] = true
	}
	knownGoleak := false
	for _, p := range packages {
		if p.ImportPath == sdkPackage || p.ImportPath == miniPackage {
			continue
		}
		if p.ImportPath == instrument.GoleakImport || p.ImportPath == instrument.TestifySuiteImport {
			copy := p
			copy.commandLine = true
			selected[p.ImportPath] = &copy
		}
		for _, imports := range [][]string{p.Deps, p.TestImports, p.XTestImports} {
			for _, path := range imports {
				if path == instrument.GoleakImport {
					knownGoleak = true
				}
			}
		}
	}
	if knownGoleak {
		wanted[instrument.GoleakImport] = true
	}
	mode := "-find"
	requests := map[string]bool{}
	if len(paths) != 0 {
		mode = "-deps"
		for _, path := range paths {
			requests[path] = true
		}
	}
	for path := range wanted {
		if selected[path] == nil {
			requests[path] = true
		}
	}
	debug.printf("test-library query pending_imports=%d known_libraries=%d", len(requests), len(selected))
	if len(requests) == 0 {
		return selected, nil
	}
	paths = paths[:0]
	for path := range requests {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	args := []string{"list", mode, "-json=Dir,Name,ImportPath,Standard,GoFiles,Module,Error"}
	args = append(args, opts.buildFlags...)
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	dependencies, err := readPackages(cmd, "resolve optional test-library dependencies")
	if err != nil {
		return nil, err
	}
	debug.printf("test-library query resolved=%d", len(dependencies))
	for _, p := range dependencies {
		if selected[p.ImportPath] == nil && (p.ImportPath == instrument.GoleakImport || p.ImportPath == instrument.TestifySuiteImport) {
			copy := p
			selected[p.ImportPath] = &copy
		}
	}
	return selected, nil
}

// libraryVersion selects the upstream version used for compatibility checks.
// Forks have independent version schemes, so use the original module's effective
// version from go list. A versioned replacement within the same module instead
// selects that upstream release; it can downgrade below our supported minimum.
// Local replacements use the original version. All selected sources still pass
// API validation before the build can use its cache.
func libraryVersion(pkg *goPackage) string {
	if pkg.Module == nil {
		return "(unknown version)"
	}
	version := pkg.Module.Version
	if replacement := pkg.Module.Replace; replacement != nil && replacement.Version != "" &&
		(replacement.Path == "" || replacement.Path == pkg.Module.Path) {
		version = replacement.Version
	}
	if version == "" {
		return "(unknown version)"
	}
	return version
}
