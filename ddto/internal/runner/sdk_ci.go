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

// This exported testing constant changes the SDK's cache inputs through
// internal/env's testing dependency. Bump it when SDK guard or mirror compiler edits change.
const miniSDKCICacheMarker = `
const DDTestMiniSDKCIContract = "mini-sdk-ci-mirror-v4"
`

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
				err = fmt.Errorf("ambiguous SDK mirror API")
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
		file, err := os.CreateTemp("", "ddtest-ci-env-*.go")
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
			return nil, nil, fmt.Errorf("unsupported SDK span mirror API: missing hooks (found %d)", mirrorFound)
		}
		file, err := os.CreateTemp("", "ddtest-sdk-mirror-*.go")
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
	return forwarded, cleanup, nil
}
