package testdrive

import (
	"os"
	"strings"
)

func pythonEnvironment(path string) map[string]string {
	env := map[string]string{}
	if path != "" {
		env["PYTHONPATH"] = path
		if existing := os.Getenv("PYTHONPATH"); existing != "" {
			env["PYTHONPATH"] += string(os.PathListSeparator) + existing
		}
	}
	env["PYTEST_ADDOPTS"] = strings.TrimSpace(os.Getenv("PYTEST_ADDOPTS") + " --ddtrace")

	return env
}
