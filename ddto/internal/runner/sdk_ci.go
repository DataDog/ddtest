package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

const (
	sdkCIConfigPackage      = "github.com/DataDog/dd-trace-go/v2/internal/config"
	sdkCIEnvironmentPackage = "github.com/DataDog/dd-trace-go/v2/internal/civisibility/envconfig"
	sdkTracerPackage        = "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func isSDKCIPackage(pkg string) bool {
	return pkg == sdkCIEnvironmentPackage || pkg == sdkCIConfigPackage
}

// sdkCompilePackage returns the guarded SDK package whose compiler cache
// marker a compile receives. Go applies a package's gcflags to its internal
// test variant, "pkg [pkg.test]", and also to its external test package and
// test main, so all of them carry the marker. own reports whether the inputs
// are the package's sources, which the guards rewrite; the external test and
// the test main only need the marker removed, as for goleak.
func sdkCompilePackage(importPath string) (pkg string, own bool) {
	path, _, _ := strings.Cut(importPath, " [")
	own = true
	if base, ok := strings.CutSuffix(path, "_test"); ok {
		path, own = base, false
	} else if base, ok := strings.CutSuffix(path, ".test"); ok {
		path, own = base, false
	}
	if isSDKCIPackage(path) || path == sdkTracerPackage {
		return path, own
	}
	return "", false
}

// Direct package keys are needed because transitive testing export data can
// stay unchanged even when a toolchain rebuilds testing with different hooks.
// Bump these contracts when the corresponding compiler edits change.
func sdkCompilerCacheMarker(pkg string) string {
	if pkg == sdkTracerPackage {
		return "-I=ddto-sdk-span-mirror-v6"
	}
	return "-I=ddto-sdk-ci-guard-v6"
}

// Mirror hooks are optional. CI ownership guards remain mandatory so an APM
// SDK can keep its transport without reporting a second copy of the tests.
func preflightSDKMirror(pkg *goPackage, replacements map[string]string) string {
	if pkg == nil || pkg.Error != nil {
		return "" // Late SDK imports are checked against actual compiler inputs.
	}
	var found uint8
	for _, file := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, file)
		if replacement, exists := replacements[path]; exists {
			path = replacement
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("SDK span copies disabled: %v", err)
		}
		_, hooks, err := instrument.TransformSDKMirror(path, source)
		if err != nil {
			return fmt.Sprintf("SDK span copies disabled: %v", err)
		}
		if found&hooks != 0 {
			return "SDK span copies disabled: ambiguous hooks"
		}
		found |= hooks
	}
	if found != 15 {
		return fmt.Sprintf("SDK span copies disabled: missing hooks (found %d)", found)
	}
	return ""
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
		if pkg == sdkCIConfigPackage {
			transform = instrument.TransformSDKCIConfig
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
		file, err := os.CreateTemp("", "ddto-ci-env-*.go")
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
	}
	if pkg == sdkTracerPackage {
		if mirrorFound != 15 {
			cleanup()
			return nil, nil, fmt.Errorf("%w: SDK span mirror missing hooks (found %d)", instrument.ErrUnsupportedAPI, mirrorFound)
		}
		file, err := os.CreateTemp("", "ddto-sdk-mirror-*.go")
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		temporary = append(temporary, file.Name())
		_, err = file.WriteString(instrument.SDKMirrorHook)
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
