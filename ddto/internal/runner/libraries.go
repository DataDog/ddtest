package runner

import (
	"context"
	"os/exec"
	"sort"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Both optional integrations share the existing test-import graph query.
// Known dependency closures are not queried again; external helpers remain.
func resolveTestLibraries(ctx context.Context, dir string, opts options, packages []goPackage) (map[string]*goPackage, error) {
	selected := map[string]*goPackage{}
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
	for _, p := range dependencies {
		if selected[p.ImportPath] == nil && (p.ImportPath == instrument.GoleakImport || p.ImportPath == instrument.TestifySuiteImport) {
			copy := p
			selected[p.ImportPath] = &copy
		}
	}
	return selected, nil
}
