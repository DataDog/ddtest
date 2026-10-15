package runner

import (
	"os"
	"path/filepath"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

const miniIntegrationsPackage = miniModule + "/internal/thirdparty/dd-trace-go/civisibility/integrations"

// Overlay files that ddto adds to os/signal and testing for Mini's forced
// exits.
const (
	signalHookFile = "zz_dd_ci_visibility_signal.go"
	exitHookFile   = "zz_dd_ci_visibility_exit.go"
)

// prepareSignalHook edits os/signal so that Mini receives SIGINT and SIGTERM
// only when no other channel wants them, and adds instrument.SignalHook. It
// returns the edited os/signal package, or why the build keeps os/signal
// unchanged; Mini then handles no signal, as under go test.
func prepareSignalHook(listed []goPackage, replacements map[string]string, temp string, add func(logical, content string) error) (*goPackage, string, error) {
	signal, integrations := findPackage(listed, "os/signal"), findPackage(listed, miniIntegrationsPackage)
	if signal == nil || integrations == nil {
		return nil, "os/signal or the runtime's integrations package not listed", nil
	}
	// A client that requires another version of this module selects its runtime.
	if refusal := checkSources(integrations, replacements, instrument.SignalHookRuntimeError); refusal != "" {
		return nil, refusal, nil
	}
	sources, err := packageSources(signal, replacements)
	if err != nil {
		return nil, err.Error(), nil
	}
	edited, err := instrument.TransformSignal(sources)
	if err != nil {
		return nil, err.Error(), nil
	}
	for logical, src := range edited {
		backing := filepath.Join(temp, "signal-"+filepath.Base(logical))
		if err := os.WriteFile(backing, src, 0600); err != nil {
			return nil, "", err
		}
		replacements[logical] = backing
	}
	if err := add(filepath.Join(signal.Dir, signalHookFile), instrument.SignalHook); err != nil {
		return nil, "", err
	}
	return signal, "", nil
}

// prepareExitHook adds instrument.ExitHook to testing, in testingDir, so that
// os.Exit closes Mini's CI Visibility before the process ends. It returns why
// the build goes without it, or "".
func prepareExitHook(listed []goPackage, replacements map[string]string, testingDir string, add func(logical, content string) error) (string, error) {
	exithook, integrations := findPackage(listed, "internal/runtime/exithook"), findPackage(listed, miniIntegrationsPackage)
	if exithook == nil || integrations == nil {
		return "internal/runtime/exithook or the runtime's integrations package not listed", nil
	}
	if refusal := checkSources(exithook, replacements, instrument.ExitHookToolchainError); refusal != "" {
		return refusal, nil
	}
	if refusal := checkSources(integrations, replacements, instrument.ExitHookRuntimeError); refusal != "" {
		return refusal, nil
	}
	return "", add(filepath.Join(testingDir, exitHookFile), instrument.ExitHook)
}

// findPackage returns the listed package path that go list resolved without
// an error, or nil.
func findPackage(listed []goPackage, path string) *goPackage {
	for i := range listed {
		if p := &listed[i]; p.ImportPath == path && p.Error == nil {
			return p
		}
	}
	return nil
}

// checkSources returns why check rejects p's sources, or "".
func checkSources(p *goPackage, replacements map[string]string, check func(map[string][]byte) error) string {
	sources, err := packageSources(p, replacements)
	if err == nil {
		err = check(sources)
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// packageSources reads the Go files of p that the build compiles, through the
// overlay's replacements. A file that the overlay deletes is left out.
func packageSources(p *goPackage, replacements map[string]string) (map[string][]byte, error) {
	sources := make(map[string][]byte, len(p.GoFiles))
	for _, name := range p.GoFiles {
		path := filepath.Join(p.Dir, name)
		actual, replaced := replacements[path]
		if !replaced {
			actual = path
		} else if actual == "" {
			continue
		}
		src, err := os.ReadFile(actual)
		if err != nil {
			return nil, err
		}
		sources[path] = src
	}
	return sources, nil
}
