package runner

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

const minimumTestifyVersion = "v1.4.0"

// Bump when compiler-side edits change without changing prepared inputs.
const testifyContractVersion = "ddtest-testify-entry-v1"

// LibraryEntry contains the inputs for one selected library entry. Other packages never
// need to read the tool plan. Covered sources are transformed after go cover.
type LibraryEntry struct {
	Package     string `json:",omitempty"`
	Sources     map[string]string
	HookFile    string
	Fingerprint string
}

// prepareTestifyPackage returns a warning instead of an entry when the selected
// Testify is unsupported; its suites then run as ordinary tests.
func prepareTestifyPackage(suite *goPackage, replacements map[string]string, runtime Runtime, temp string) (*LibraryEntry, string, error) {
	if suite == nil {
		return nil, "", nil
	}
	entry, err := prepareTestifyEntry(suite, replacements, runtime, temp)
	var unsupported unsupportedLibrary
	if errors.As(err, &unsupported) || errors.Is(err, instrument.ErrUnsupportedAPI) {
		return nil, fmt.Sprintf("Testify %s is not instrumented (%v); its suites run as ordinary tests without Testify suite metadata", testifyVersion(suite), err), nil
	}
	return entry, "", err
}

// unsupportedLibrary reports a selected library version outside the supported range.
type unsupportedLibrary struct{ message string }

func (u unsupportedLibrary) Error() string { return u.message }

func testifyVersion(suite *goPackage) string {
	if suite.Module == nil {
		return "(unknown version)"
	}
	if suite.Module.Replace != nil && suite.Module.Replace.Version != "" {
		return suite.Module.Replace.Version
	}
	if suite.Module.Version == "" {
		return "(unknown version)"
	}
	return suite.Module.Version
}

func prepareTestifyEntry(suite *goPackage, replacements map[string]string, runtime Runtime, temp string) (*LibraryEntry, error) {
	selected := ""
	if suite.Module != nil {
		selected = suite.Module.Version
		if suite.Module.Replace != nil && suite.Module.Replace.Version != "" {
			selected = suite.Module.Replace.Version
		}
	}
	if !instrument.SupportsTestifyVersion(selected) {
		return nil, unsupportedLibrary{fmt.Sprintf("requires %s or a later v1 release", minimumTestifyVersion)}
	}
	files := map[string][]byte{}
	actualPaths := map[string]string{}
	for _, name := range suite.GoFiles {
		logical := filepath.Join(suite.Dir, name)
		actual := logical
		if to, ok := replacements[logical]; ok {
			actual = to
		}
		if actual == "" {
			continue
		}
		src, err := os.ReadFile(actual)
		if err != nil {
			return nil, err
		}
		files[logical] = src
		actualPaths[logical] = actual
	}
	rewritten, err := instrument.TransformTestifyPackage(files)
	if err != nil {
		return nil, err
	}
	hook := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestifySuiteRun"
	if runtime == Mini {
		hook = strings.Replace(hook, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/", 1)
	}
	tool := &LibraryEntry{Sources: map[string]string{}, HookFile: filepath.Join(temp, "testify-hook.go")}
	hookSource := instrument.TestifyEntryHook(hook)
	hash := sha256.New()
	hash.Write([]byte(testifyContractVersion))
	hash.Write([]byte(hookSource))
	names := make([]string, 0, len(rewritten))
	for name := range rewritten {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		src := rewritten[name]
		backing := filepath.Join(temp, "testify-entry.go")
		if err := os.WriteFile(backing, src, 0600); err != nil {
			return nil, err
		}
		tool.Sources[name] = backing
		tool.Sources[actualPaths[name]] = backing
		hash.Write(src)
	}
	if len(tool.Sources) == 0 {
		return nil, fmt.Errorf("%w: Testify suite.Run entry was not found", instrument.ErrUnsupportedAPI)
	}
	if err := os.WriteFile(tool.HookFile, []byte(hookSource), 0600); err != nil {
		return nil, err
	}
	tool.Fingerprint = fmt.Sprintf("%x", hash.Sum(nil))
	return tool, nil
}

// testifyDependencyImports walks only test imports whose dependency closures
// were not already inspected by the first go list. Unknown imports remain in
// the query: a helper in another module may call suite.Run on the client's behalf.
func testifyDependencyImports(packages []goPackage) ([]string, bool) {
	known := map[string]bool{"testing": true}
	hasSuite := false
	for _, p := range packages {
		if p.ImportPath == sdkPackage || p.ImportPath == miniPackage {
			// The runtime's graph says nothing about the client's test helpers.
			continue
		}
		known[p.ImportPath] = true
		for _, path := range p.Deps {
			known[path] = true
			if p.ImportPath != "testing" && path == instrument.TestifySuiteImport {
				hasSuite = true
			}
		}
	}
	requests := map[string]bool{}
	for _, p := range packages {
		if p.ImportPath == "testing" || p.ImportPath == sdkPackage || p.ImportPath == miniPackage {
			continue
		}
		for _, imports := range [][]string{p.TestImports, p.XTestImports} {
			for _, path := range imports {
				// Check reachability before pruning: known imports still need
				// suite metadata and version validation on every invocation.
				if path == instrument.TestifySuiteImport {
					hasSuite = true
				}
				if !known[path] {
					requests[path] = true
				}
			}
		}
	}
	paths := make([]string, 0, len(requests))
	for path := range requests {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, hasSuite
}

func prepareLibraryCompile(plan *LibraryEntry, args []string) ([]string, func(), error) {
	if plan == nil {
		return nil, nil, fmt.Errorf("missing optional library tool plan")
	}
	result := append([]string(nil), args...)
	var temporary []string
	cleanup := func() {
		for _, path := range temporary {
			os.Remove(path)
		}
	}
	changed := false
	// Go places compiler source inputs last. Flags may themselves contain '.go'.
	start := len(args)
	for start > 1 && strings.HasSuffix(args[start-1], ".go") {
		start--
	}
	for i := start; i < len(args); i++ {
		if replacement, ok := plan.Sources[filepath.Clean(args[i])]; ok {
			result[i] = replacement
			changed = true
			continue
		}
		// Covered source paths belong to $WORK, not the original module. Prepending
		// after cover preserves the original coverage counters and denominator.
		src, err := os.ReadFile(args[i])
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		rewrite := instrument.TransformTestifyEntry
		if plan.Package == instrument.GoleakImport {
			rewrite = instrument.TransformGoleakEntry
		}
		transformed, found, err := rewrite(args[i], src)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if !found {
			continue
		}
		file, err := os.CreateTemp(filepath.Dir(args[i]), "ddtest-suite-*.go")
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		temporary = append(temporary, file.Name())
		_, err = file.Write(transformed)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		result[i] = file.Name()
		changed = true
	}
	if !changed {
		cleanup()
		return nil, nil, fmt.Errorf("optional library compiler inputs contain no instrumented entry")
	}
	return append(result, plan.HookFile), cleanup, nil
}
