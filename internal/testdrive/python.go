package testdrive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Selection only affects testdrive. Production pytest discovery and RunTests
// continue to use the command chosen by the framework.
func pythonTestdriveCommand(root, command string, args []string, explicit bool) (string, []string, string) {
	if explicit {
		return command, args, "Explicit test command"
	}
	if uvLockRoot(root) != "" {
		return "uv", []string{"run", "--no-sync", "python", "-m", "pytest"}, "uv project (uv.lock)"
	}
	for _, variable := range []string{"VIRTUAL_ENV", "CONDA_PREFIX"} {
		if directory := os.Getenv(variable); directory != "" {
			return environmentPython(directory), args, "Activated environment (" + variable + ")"
		}
	}
	return command, args, "Python from PATH"
}

func environmentPython(directory string) string {
	if runtime.GOOS == "windows" {
		// Conda puts python.exe at its root; venv uses Scripts.
		if _, err := os.Stat(filepath.Join(directory, "python.exe")); err == nil {
			return filepath.Join(directory, "python.exe")
		}
		return filepath.Join(directory, "Scripts", "python.exe")
	}
	return filepath.Join(directory, "bin", "python")
}

func uvLockRoot(root string) string {
	for directory := root; ; directory = filepath.Dir(directory) {
		if info, err := os.Stat(filepath.Join(directory, "uv.lock")); err == nil && !info.IsDir() {
			return directory
		}
		// Do not select an unrelated project outside this checkout.
		if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil || filepath.Dir(directory) == directory {
			return ""
		}
	}
}

func pythonEnvironmentInfo(ctx context.Context, command string, args []string, source string, env map[string]string) (environmentInfo, *reportCard) {
	info := environmentInfo{Source: source, Runtime: "Python"}
	probeCommand, prefix, err := pythonProbeCommand(command, args)
	advice := "Activate the environment used to run your tests, or pass --command with its Python interpreter and pytest arguments. Install the project's test dependencies in that environment, then rerun testdrive."
	if err != nil {
		return info, environmentFinding("python-environment-unresolved", "Cannot inspect the selected Python environment", err.Error(), advice+" For wrappers, run ddtest inside the wrapper, for example: uv run --no-sync ddtest testdrive.")
	}
	if strings.TrimSuffix(filepath.Base(command), ".exe") == "uv" {
		advice = "Prepare the project's uv environment with its test dependencies, including pytest, then rerun testdrive. Select the same dependency groups or extras as CI when preparing it. Testdrive uses --no-sync for environment checks and does not install project dependencies."
		if _, err := exec.LookPath(command); err != nil {
			return info, environmentFinding("uv-not-found", "uv is not available", err.Error(), "Install uv and prepare the project's test environment, or pass --command with an existing test environment's Python interpreter and pytest arguments.")
		}
	}
	var result struct {
		Executable string
		Version    string
		Pytest     bool
		Error      string
	}
	err = probeEnvironment(ctx, probeCommand, append(prefix, "-c", pythonEnvironmentProbe), env, &result)
	if err != nil {
		return info, environmentFinding("python-environment-unresolved", "Cannot inspect the selected Python environment", err.Error(), advice)
	}
	info.Executable, info.Version = result.Executable, result.Version
	if !result.Pytest {
		return info, environmentFinding("pytest-unavailable", "pytest is unavailable in the selected Python environment", info.Executable+": "+result.Error+". Tests were not run.", advice)
	}
	return info, nil
}

const pythonEnvironmentProbe = `import json, pathlib, platform, sys
result = {"executable": sys.executable, "version": platform.python_version(), "pytest": False}
try:
    import pytest
    result["pytest"] = True
except Exception as error:
    result["error"] = str(error)
pathlib.Path(sys.argv[1]).write_text(json.dumps(result))
`

// Recognize the supported runner forms without guessing through shell commands
// or arbitrary wrappers. Keep uv's environment selectors on every probe.
func pythonProbeCommand(command string, args []string) (string, []string, error) {
	base := strings.TrimSuffix(filepath.Base(command), ".exe")
	if strings.HasPrefix(base, "python") {
		if index := onboard.PythonModuleIndex(args); index >= 0 {
			return command, slices.Clone(args[:index]), nil
		}
		return command, nil, nil
	}
	if base == "pytest" {
		if path, err := exec.LookPath(command); err == nil {
			command = path
		}
		return filepath.Join(filepath.Dir(command), "python"+filepath.Ext(command)), nil, nil
	}
	if (base == "uv" || base == "poetry") && len(args) >= 2 && args[0] == "run" {
		prefix := []string{"run"}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if arg == "--" {
				prefix = append(prefix, arg)
				continue
			}
			if strings.HasPrefix(filepath.Base(arg), "python") || strings.TrimSuffix(filepath.Base(arg), ".exe") == "pytest" {
				interpreter, interpreterArgs, err := pythonProbeCommand(arg, args[i+1:])
				if strings.TrimSuffix(arg, ".exe") == "pytest" {
					interpreter = "python" + filepath.Ext(arg)
				}
				if base == "uv" && !slices.Contains(prefix, "--no-sync") {
					prefix = append([]string{"run", "--no-sync"}, prefix[1:]...)
				}
				return command, append(append(prefix, interpreter), interpreterArgs...), err
			}
			if base != "uv" {
				break
			}
			option, _, hasValue := strings.Cut(arg, "=")
			switch option {
			case "--no-sync", "--locked", "--frozen", "--offline", "--active", "--no-python-downloads", "--no-default-groups":
				prefix = append(prefix, arg)
			case "--project", "--directory", "--package", "--python", "-p", "--group", "--extra":
				prefix = append(prefix, arg)
				if !hasValue && i+1 < len(args) {
					i++
					prefix = append(prefix, args[i])
				}
			default:
				return "", nil, fmt.Errorf("cannot inspect uv option %q; run ddtest inside the selected uv environment", arg)
			}
		}
	}
	return "", nil, fmt.Errorf("cannot identify a Python interpreter in %q", command)
}

func pythonEnvironment(path string) map[string]string {
	env := map[string]string{}
	if path != "" {
		env["PYTHONPATH"] = path
		if existing := os.Getenv("PYTHONPATH"); existing != "" {
			env["PYTHONPATH"] += string(os.PathListSeparator) + existing
		}
	}
	env["PYTEST_ADDOPTS"] = strings.TrimSpace("--ddtrace " + os.Getenv("PYTEST_ADDOPTS"))

	return env
}
