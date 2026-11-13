package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// PythonTestCommand selects the existing environment used by onboarding and
// testdrive. Production pytest discovery and RunTests keep their framework command.
func PythonTestCommand(root, command string, args []string, explicit bool) (string, []string, string) {
	if explicit {
		return command, args, "Explicit test command"
	}
	if uvLockRoot(root) != "" {
		return "uv", []string{"run", "--no-sync", "python", "-m", "pytest"}, "uv project (uv.lock)"
	}
	for _, variable := range []string{"VIRTUAL_ENV", "CONDA_PREFIX"} {
		if directory := os.Getenv(variable); directory != "" {
			return PythonEnvironmentExecutable(directory), args, "Activated environment (" + variable + ")"
		}
	}
	return command, args, "Python from PATH"
}

// PythonEnvironmentExecutable locates an activated venv or Conda interpreter.
func PythonEnvironmentExecutable(directory string) string {
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
