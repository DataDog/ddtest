package runner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

// --experimental-file-coverage=context: goroutines carry the tests that they
// work for, so tests keep t.Parallel and each reports its own files. See
// instrument.TransformContextRuntime.

// contextHookFile is the file that context file coverage adds to each package
// that it edits.
const contextHookFile = "zz_ddto_context.go"

// contextEdit is what context file coverage changes in one package.
type contextEdit struct {
	pkg       string
	transform func(map[string][]byte) (map[string][]byte, error)
	hook      string
	// optional packages are edited only when the build lists them.
	optional bool
}

// contextEdits returns the edits of context file coverage.
func contextEdits() []contextEdit {
	return []contextEdit{
		{pkg: "runtime", transform: instrument.TransformContextRuntime, hook: instrument.ContextRuntimeHook(instrument.FileCoveragePackage)},
		{pkg: "internal/poll", transform: instrument.TransformContextPoll, hook: instrument.ContextPollHook},
		{pkg: "context", transform: instrument.TransformContextContext, hook: instrument.ContextCaptureHook("context")},
		{pkg: "testing", transform: instrument.TransformContextTesting},
		{pkg: "net/http", transform: instrument.TransformContextHTTP, hook: instrument.ContextCaptureHook("http"), optional: true},
		{pkg: "internal/singleflight", transform: instrument.TransformContextSingleflight, hook: instrument.ContextSingleflightHook("singleflight", instrument.FileCoveragePackage), optional: true},
	}
}

// contextChanges are the overlay changes of context file coverage, which the
// caller applies once file coverage is installed: replacements of edited
// files and added hook files.
type contextChanges struct {
	replace map[string]string
	add     map[string]string
}

// prepareContextCoverage edits the toolchain packages that context file
// coverage needs, as the build with tests lists them, and returns the
// changes, or why the build goes without context file coverage. listed has
// the runtime's packages. Nothing changes the overlay yet.
func prepareContextCoverage(tested, listed []goPackage, replacements map[string]string, temp string) (contextChanges, string, error) {
	changes := contextChanges{replace: map[string]string{}, add: map[string]string{}}
	if coverage := findPackage(listed, miniCoveragePackage); coverage == nil {
		return changes, "the runtime's coverage package not listed", nil
	} else if refusal := checkSources(coverage, replacements, instrument.FileCoverageContextRuntimeError); refusal != "" {
		return changes, refusal, nil
	}
	for _, edit := range contextEdits() {
		pkg := findPackage(tested, edit.pkg)
		if pkg == nil {
			if edit.optional {
				continue
			}
			return changes, edit.pkg + " not listed", nil
		}
		sources, err := packageSources(pkg, replacements)
		if err != nil {
			return changes, err.Error(), nil
		}
		edited, err := edit.transform(sources)
		if err != nil {
			return changes, err.Error(), nil
		}
		for logical, src := range edited {
			backing := filepath.Join(temp, "context-"+filepath.Base(pkg.Dir)+"-"+filepath.Base(logical))
			if err := os.WriteFile(backing, src, 0600); err != nil {
				return changes, "", err
			}
			changes.replace[logical] = backing
		}
		if edit.hook != "" {
			changes.add[filepath.Join(pkg.Dir, contextHookFile)] = edit.hook
		}
	}
	return changes, "", nil
}

// prepareSingleflight edits golang.org/x/sync/singleflight, which the build
// compiles from the module cache, where Go refuses overlays: ddto's compiler
// wrapper swaps in the edited file and adds the hook file, as it does for
// goleak. It returns why the build goes without context file coverage
// instead, or "".
func prepareSingleflight(pkg *goPackage, replacements map[string]string, temp string) (*LibraryEntry, string, error) {
	sources, err := packageSources(pkg, replacements)
	if err != nil {
		return nil, err.Error(), nil
	}
	edited, err := instrument.TransformContextSingleflight(sources)
	if err != nil {
		return nil, toolwrap.SingleflightImport + ": " + err.Error(), nil
	}
	entry := &LibraryEntry{Package: toolwrap.SingleflightImport, Sources: map[string]string{}, HookFile: filepath.Join(temp, "singleflight-hook.go")}
	hook := instrument.ContextSingleflightHook("singleflight", instrument.FileCoveragePackage)
	hash := sha256.New()
	hash.Write([]byte("ddto-singleflight-v1"))
	hash.Write([]byte(hook))
	names := make([]string, 0, len(edited))
	for logical := range edited {
		names = append(names, logical)
	}
	sort.Strings(names)
	for _, logical := range names {
		backing := filepath.Join(temp, "singleflight-"+filepath.Base(logical))
		if err := os.WriteFile(backing, edited[logical], 0600); err != nil {
			return nil, "", err
		}
		entry.Sources[logical] = backing
		if actual, ok := replacements[logical]; ok && actual != "" {
			entry.Sources[actual] = backing
		}
		hash.Write(edited[logical])
	}
	if err := os.WriteFile(entry.HookFile, []byte(hook), 0600); err != nil {
		return nil, "", err
	}
	entry.Fingerprint = fmt.Sprintf("%x", hash.Sum(nil))
	return entry, "", nil
}

// singleflightCacheMarker keys the edited singleflight's compilation, as
// goleakCacheFlag's marker keys goleak's.
func singleflightCacheMarker(fingerprint string) string {
	return "-I=ddto-singleflight-" + fingerprint
}
