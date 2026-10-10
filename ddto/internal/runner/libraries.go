package runner

import (
	"context"
	"os/exec"
	"sort"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

// Optional integrations and SDK CI ownership share the test-import graph query.
// Known dependency closures are not queried again; external helpers remain.
// packages are the named packages; graph also holds their dependencies.
func resolveTestLibraries(ctx context.Context, dir string, opts options, packages []goPackage, graph packageGraph) (selected map[string]*goPackage, sdkCI bool, err error) {
	debug := debugFromContext(ctx)
	phase := debug.start("resolve test libraries")
	defer func() { phase.finish(err) }()
	selected = map[string]*goPackage{}
	reached := clientImports(packages, graph)
	paths, suite := testifyDependencyImports(packages, reached)
	wanted := map[string]bool{}
	if suite {
		wanted[instrument.TestifySuiteImport] = true
	}
	sdkCI = reached[sdkCIConfigPackage] || reached[sdkCIEnvironmentPackage]
	knownGoleak := reached[instrument.GoleakImport]
	for _, p := range packages {
		sdkCI = sdkCI || p.ImportPath == sdkPackage || isSDKCIPackage(p.ImportPath)
		if p.ImportPath == sdkPackage || p.ImportPath == miniPackage {
			continue
		}
		if isTestLibrary(p.ImportPath) {
			copy := p
			copy.commandLine = true
			selected[p.ImportPath] = &copy
		}
		for _, imports := range [][]string{p.TestImports, p.XTestImports} {
			for _, path := range imports {
				sdkCI = sdkCI || isSDKCIPackage(path)
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
	libraries := make([]string, 0, 5)
	for path := range wanted {
		libraries = append(libraries, path)
	}
	if sdkCI || isOrchestrionToolexec(opts.toolexec) {
		libraries = append(libraries, sdkTracerPackage, sdkCIConfigPackage, sdkCIEnvironmentPackage)
	}
	for _, path := range libraries {
		if selected[path] != nil {
			continue
		}
		// The package query already listed each package that it reached; only
		// libraries outside that graph need another query.
		if p := graph[path]; p != nil {
			copy := *p
			selected[path] = &copy
		} else {
			requests[path] = true
		}
	}
	debug.printf("test-library query pending_imports=%d known_libraries=%d", len(requests), len(selected))
	if len(requests) == 0 {
		return selected, sdkCI, nil
	}
	paths = paths[:0]
	for path := range requests {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	args := []string{"list", "-e", mode, "-json=Dir,Name,ImportPath,Standard,GoFiles,Module,Error"}
	args = append(args, opts.buildFlags...)
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	if opts.workfile != "" {
		cmd.Env = append(cmd.Environ(), "GOWORK="+opts.workfile)
		if opts.workspaceGoFlags != nil {
			cmd.Env = append(cmd.Env, "GOFLAGS="+*opts.workspaceGoFlags)
		}
	}
	dependencies, err := readPackages(ctx, cmd, "resolve optional test-library dependencies")
	if err != nil {
		return nil, false, err
	}
	debug.printf("test-library query resolved=%d", len(dependencies))
	for _, p := range dependencies {
		sdkCI = sdkCI || isSDKCIPackage(p.ImportPath)
		if selected[p.ImportPath] == nil && isTestLibrary(p.ImportPath) {
			copy := p
			selected[p.ImportPath] = &copy
		}
	}
	return selected, sdkCI, nil
}

// clientImports returns what the named packages other than a runtime import.
// The runtime's graph says nothing about the client's test helpers.
func clientImports(packages []goPackage, graph packageGraph) map[string]bool {
	var roots []*goPackage
	for i := range packages {
		if p := &packages[i]; p.ImportPath != sdkPackage && p.ImportPath != miniPackage {
			roots = append(roots, p)
		}
	}
	return graph.imported(roots...)
}

// isTestLibrary reports whether preparation instruments or guards the package.
func isTestLibrary(path string) bool {
	return path == instrument.GoleakImport || path == instrument.TestifySuiteImport || path == sdkTracerPackage || isSDKCIPackage(path)
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
