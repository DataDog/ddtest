package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// packageGraph indexes a go list -deps result, which lists each package once.
type packageGraph map[string]*goPackage

func newPackageGraph(packages []goPackage) packageGraph {
	graph := make(packageGraph, len(packages))
	for i := range packages {
		graph[packages[i].ImportPath] = &packages[i]
	}
	return graph
}

// imported returns every listed package that roots import, directly or
// indirectly: the union of their Deps. A root is included only when another
// root imports it.
func (g packageGraph) imported(roots ...*goPackage) map[string]bool {
	reached := map[string]bool{}
	var pending []string
	for _, root := range roots {
		pending = appendImports(pending, root)
	}
	for len(pending) != 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		// Imports also name "C", which is not a package and is not listed.
		if p := g[path]; p != nil && !reached[path] {
			reached[path] = true
			pending = appendImports(pending, p)
		}
	}
	return reached
}

// appendImports appends the packages that p imports. Deps also contains the
// imports that cgo adds to a package importing "C", which Imports omits, such
// as runtime/cgo for net on Linux; cmd/go's load package adds them with these
// exceptions. SWIG, SIMD and a main package's linker dependencies add only
// standard packages too, which cannot reach the libraries this graph answers
// for, and are not repeated here.
func appendImports(pending []string, p *goPackage) []string {
	pending = append(pending, p.Imports...)
	if !slices.Contains(p.Imports, "C") {
		return pending
	}
	pending = append(pending, "unsafe")
	if p.ImportPath != "runtime/cgo" {
		pending = append(pending, "runtime/cgo")
	}
	switch p.ImportPath {
	case "runtime/cgo", "runtime/race", "runtime/msan", "runtime/asan":
	default:
		pending = append(pending, "syscall")
	}
	return pending
}

// plainListing returns, from a go list -deps -test listing, what go list
// -deps of the same patterns lists: the named packages and those that they
// import, in listing order, without the copies recompiled for tests, the
// test mains and the packages that only tests import.
func plainListing(listing []goPackage) []goPackage {
	graph := packageGraph{}
	var pending []string
	for i := range listing {
		p := &listing[i]
		if p.ForTest != "" || strings.Contains(p.ImportPath, " [") || isTestMain(*p) {
			continue
		}
		graph[p.ImportPath] = p
		if !p.DepOnly {
			pending = append(pending, p.ImportPath)
		}
	}
	reached := map[string]bool{}
	for len(pending) != 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if p := graph[path]; p != nil && !reached[path] {
			reached[path] = true
			pending = appendImports(pending, p)
		}
	}
	var plain []goPackage
	for i := range listing {
		if p := &listing[i]; reached[p.ImportPath] && graph[p.ImportPath] == p {
			plain = append(plain, *p)
		}
	}
	return plain
}

// callerTestsListing returns, from a go list -deps -test listing, what go
// list -deps -test of the caller's packages lists, without added, the
// packages that ddto adds to the query: each caller's package, its test
// main and what they import.
func callerTestsListing(listing []goPackage, added ...string) []goPackage {
	graph := make(packageGraph, len(listing))
	var pending []string
	for i := range listing {
		p := &listing[i]
		graph[p.ImportPath] = p
		if !p.DepOnly && p.ForTest == "" && !slices.Contains(added, p.ImportPath) && !isTestMain(*p) {
			pending = append(pending, p.ImportPath, p.ImportPath+".test")
		}
	}
	reached := map[string]bool{}
	for len(pending) != 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if p := graph[path]; p != nil && !reached[path] {
			reached[path] = true
			pending = appendImports(pending, p)
		}
	}
	var tests []goPackage
	for i := range listing {
		if reached[listing[i].ImportPath] {
			tests = append(tests, listing[i])
		}
	}
	return tests
}

// readPackages decodes go list output as it arrives instead of retaining a
// second, complete JSON copy. It always drains stdout and waits for the command,
// including after a decoding failure. A command failure takes precedence over
// partial or malformed output, as it did with Cmd.Output.
func readPackages(ctx context.Context, cmd *exec.Cmd, failureContext string) ([]goPackage, error) {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// A canceled go list is killed; do not wait indefinitely for descendants
	// that might still hold its output pipes.
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	// StdoutPipe is read by this goroutine, outside exec's copying goroutines.
	// WaitDelay cannot bound a read that prevents us from reaching Wait.
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stdout.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	var packages []goPackage
	decoder := json.NewDecoder(stdout)
	var decodeErr error
	for {
		var p goPackage
		if err := decoder.Decode(&p); err != nil {
			if err != io.EOF {
				decodeErr = err
			}
			break
		}
		packages = append(packages, p)
	}
	if decodeErr != nil {
		// A producer can keep writing after a malformed record. Draining avoids
		// blocking it on a full pipe while we wait for its exit status.
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", failureContext, err)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return packages, nil
}
