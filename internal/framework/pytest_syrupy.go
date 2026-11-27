package framework

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
)

// pytestSyrupyPartialPlugin makes syrupy warn about unused snapshots in a
// run of part of the suite; see the script.
//
//go:embed scripts/pytest_syrupy_partial.py
var pytestSyrupyPartialPlugin string

const syrupyPartialModule = "ddtest_syrupy_partial"

// loadSyrupyPartialPlugin writes the plugin to a temporary directory and makes
// pytest load it through env, the run's environment. It returns a function
// that removes the directory.
func loadSyrupyPartialPlugin(env map[string]string) (func(), error) {
	dir, err := os.MkdirTemp("", "ddtest-pytest-syrupy-*")
	if err != nil {
		return nil, fmt.Errorf("create the syrupy plugin directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, syrupyPartialModule+".py"), []byte(pytestSyrupyPartialPlugin), 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write the syrupy plugin: %w", err)
	}
	env["PYTHONPATH"] = prependToEnvList(env, "PYTHONPATH", string(os.PathListSeparator), dir)
	// PYTEST_PLUGINS, unlike -p, reaches pytest through wrappers that pass
	// their own arguments, such as a command override's script.
	env["PYTEST_PLUGINS"] = prependToEnvList(env, "PYTEST_PLUGINS", ",", syrupyPartialModule)
	return cleanup, nil
}

// prependToEnvList puts value first in the list variable key of env, or of the
// process environment when env does not set it; a variable env unsets starts
// empty.
func prependToEnvList(env map[string]string, key string, separator string, value string) string {
	current, ok := env[key]
	if !ok {
		current = os.Getenv(key)
	}
	if current == "" || current == ext.UnsetEnvironment {
		return value
	}
	return value + separator + current
}

// ignoresPythonEnvironment reports whether a test command runs Python with -I
// or -E, which ignore PYTHONPATH: pytest would still read PYTEST_PLUGINS and
// fail to import the plugin, so it isn't loaded then. It reads Python's
// options as Python does, up to the module, code or script.
func ignoresPythonEnvironment(command string, args []string) bool {
	if !strings.HasPrefix(filepath.Base(command), "python") {
		return false
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-"):
			return false
		case arg == "--check-hash-based-pycs":
			i++ // the only long option with a separate value
			continue
		case strings.HasPrefix(arg, "--"):
			continue
		}
		for j, flag := range arg[1:] {
			switch flag {
			case 'I', 'E':
				return true
			case 'c', 'm':
				// The rest, here or in the next argument, is the code or the
				// module, and what follows is its own.
				return false
			case 'W', 'X':
				if j == len(arg)-2 {
					i++ // the value is the next argument
				}
			}
			if flag == 'W' || flag == 'X' {
				break
			}
		}
	}
	return false
}
