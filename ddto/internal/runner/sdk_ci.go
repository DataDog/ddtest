package runner

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/ddto/internal/instrument"
	"github.com/DataDog/ddtest/ddto/internal/toolwrap"
)

const (
	sdkCIConfigPackage      = toolwrap.SDKCIConfigPackage
	sdkCIEnvironmentPackage = toolwrap.SDKCIEnvironmentPackage
	sdkCIBazelPackage       = toolwrap.SDKCIBazelPackage
	sdkTracerPackage        = toolwrap.SDKTracerPackage
)

func isSDKCIPackage(pkg string) bool {
	return toolwrap.IsSDKCIPackage(pkg)
}

// sdkCompilePackage returns the guarded SDK package whose compiler cache
// marker a compile receives. Go applies a package's gcflags to its internal
// test variant, "pkg [pkg.test]", and also to its external test package and
// test main, so all of them carry the marker. own reports whether the inputs
// are the package's sources, which the guards rewrite; the external test and
// the test main only need the marker removed, as for goleak.
func sdkCompilePackage(importPath string) (pkg string, own bool) {
	return toolwrap.SDKCompilePackage(importPath)
}

// Direct package keys are needed because transitive testing export data can
// stay unchanged even when a toolchain rebuilds testing with different hooks.
// Bump these contracts when the corresponding compiler edits change.
func sdkCompilerCacheMarker(pkg string) string {
	if pkg == sdkTracerPackage {
		return "-I=ddto-sdk-span-mirror-v8"
	}
	return "-I=ddto-sdk-ci-guard-v7"
}

// Mirror hooks are optional. CI ownership guards remain mandatory so an APM
// SDK can keep its transport without reporting a second copy of the tests.
// The second result explains when copies work but the SDK's spans under tests
// also go to APM: adoption's edits are missing.
func preflightSDKMirror(pkg *goPackage, replacements map[string]string) (string, string) {
	if pkg == nil || pkg.Error != nil {
		return "", "" // Late SDK imports are checked against actual compiler inputs.
	}
	found, err := sdkMirrorEdits(pkg, replacements)
	if err != nil {
		return fmt.Sprintf("SDK span copies disabled: %v", err), ""
	}
	if found&instrument.SDKMirrorCopies != instrument.SDKMirrorCopies {
		return fmt.Sprintf("SDK span copies disabled: missing hooks (found %d)", found), ""
	}
	if apm, _ := strconv.ParseBool(os.Getenv("DD_CIVISIBILITY_APM_SPANS_ENABLED")); !apm && found&instrument.SDKMirrorAdoption != instrument.SDKMirrorAdoption {
		return "", fmt.Sprintf("SDK spans under tests also go to APM: this dd-trace-go lacks the hooks that keep them to Test Optimization (found %d)", found)
	}
	return "", ""
}

// sdkMirrorEdits returns the span mirror's edits that pkg's sources accept.
func sdkMirrorEdits(pkg *goPackage, replacements map[string]string) (uint8, error) {
	var found uint8
	for _, file := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, file)
		if replacement, exists := replacements[path]; exists {
			path = replacement
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		_, hooks, err := instrument.TransformSDKMirror(path, source)
		if err != nil {
			return 0, err
		}
		if found&hooks != 0 {
			return 0, errors.New("ambiguous hooks")
		}
		found |= hooks
	}
	return found, nil
}

// SDKEdits applies the CLI's edits to the dd-trace-go/v2 module extracted at
// dir, through the steps of its compiles and without changing dir: the
// mandatory CI guards and the span mirror. It returns the mirror's edits; an
// error means that a guard or the copies cannot be installed. The
// compatibility tests check every supported SDK version with it.
func SDKEdits(dir string) (uint8, error) {
	var tracer *goPackage
	for _, pkg := range []string{sdkCIConfigPackage, sdkCIEnvironmentPackage, sdkCIBazelPackage, sdkTracerPackage} {
		relative := strings.TrimPrefix(pkg, "github.com/DataDog/dd-trace-go/v2/")
		source, err := build.Default.ImportDir(filepath.Join(dir, filepath.FromSlash(relative)), 0)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", pkg, err)
		}
		args := []string{"compile"}
		for _, file := range source.GoFiles {
			args = append(args, filepath.Join(source.Dir, file))
		}
		_, cleanup, err := prepareSDKCICompile(args, pkg)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", pkg, err)
		}
		cleanup()
		tracer = &goPackage{Dir: source.Dir, GoFiles: source.GoFiles}
	}
	return sdkMirrorEdits(tracer, nil)
}

// prepareSDKCICompile rewrites the compiler's actual inputs, including covered
// files, without modifying the SDK in GOMODCACHE or adding it to the client graph.
func prepareSDKCICompile(args []string, pkg string) ([]string, func(), error) {
	forwarded := append([]string(nil), args...)
	var temporary []string
	cleanup := func() {
		for _, path := range temporary {
			os.Remove(path)
		}
	}
	start := len(args)
	for start > 1 && strings.HasSuffix(args[start-1], ".go") {
		start--
	}
	found := false
	var mirrorFound uint8
	for i := start; i < len(args); i++ {
		source, err := os.ReadFile(args[i])
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		transform := instrument.TransformSDKCIEnvironment
		switch pkg {
		case sdkCIConfigPackage:
			transform = instrument.TransformSDKCIConfig
		case sdkCIBazelPackage:
			transform = instrument.TransformSDKBazelMode
		}
		var rewritten []byte
		var changed bool
		if pkg == sdkTracerPackage {
			var hooks uint8
			rewritten, hooks, err = instrument.TransformSDKMirror(args[i], source)
			if hooks&mirrorFound != 0 {
				err = fmt.Errorf("%w: ambiguous SDK mirror API", instrument.ErrUnsupportedAPI)
			}
			mirrorFound |= hooks
			changed = hooks != 0
		} else {
			rewritten, changed, err = transform(args[i], source)
		}
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if !changed {
			continue
		}
		if found && pkg != sdkTracerPackage {
			cleanup()
			return nil, nil, fmt.Errorf("ambiguous SDK CI configuration entry")
		}
		found = true
		// The module cache is read-only; preserve source identity in a temp file.
		// A leading _ keeps go's package discovery from reading it, with a
		// temporary directory inside a module.
		file, err := os.CreateTemp("", "_ddtest-instrument-ci-env-*.go")
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		temporary = append(temporary, file.Name())
		prefix := fmt.Sprintf("//line %s:1:1\n", filepath.ToSlash(args[i]))
		_, writeErr := file.Write(append([]byte(prefix), rewritten...))
		closeErr := file.Close()
		if writeErr != nil {
			cleanup()
			return nil, nil, writeErr
		}
		if closeErr != nil {
			cleanup()
			return nil, nil, closeErr
		}
		forwarded[i] = file.Name()
		if pkg != sdkTracerPackage {
			// The guards' variable, in a file of its own: the rewritten file
			// keeps its imports and its line numbers.
			guard, err := instrument.SDKGuardFile(filepath.ToSlash(args[i]), source)
			if err != nil {
				cleanup()
				return nil, nil, err
			}
			file, err := os.CreateTemp("", "_ddtest-instrument-ci-guard-*.go")
			if err != nil {
				cleanup()
				return nil, nil, err
			}
			temporary = append(temporary, file.Name())
			_, writeErr := file.WriteString(guard)
			closeErr := file.Close()
			if writeErr != nil {
				cleanup()
				return nil, nil, writeErr
			}
			if closeErr != nil {
				cleanup()
				return nil, nil, closeErr
			}
			forwarded = append(forwarded, file.Name())
		}
	}
	if pkg == sdkTracerPackage {
		if mirrorFound&instrument.SDKMirrorCopies != instrument.SDKMirrorCopies {
			cleanup()
			return nil, nil, fmt.Errorf("%w: SDK span mirror missing hooks (found %d)", instrument.ErrUnsupportedAPI, mirrorFound)
		}
		file, err := os.CreateTemp("", "_ddtest-instrument-sdk-mirror-*.go")
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		temporary = append(temporary, file.Name())
		_, err = file.WriteString(instrument.SDKMirrorHook(mirrorFound))
		closeErr := file.Close()
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if closeErr != nil {
			cleanup()
			return nil, nil, closeErr
		}
		forwarded = append(forwarded, file.Name())
	}
	if !found {
		cleanup()
		return nil, nil, fmt.Errorf("unsupported SDK CI configuration API in %s", pkg)
	}
	return removeCompilerCacheMarker(forwarded, sdkCompilerCacheMarker(pkg)), cleanup, nil
}
