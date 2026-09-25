package framework

import (
	"path/filepath"
	"slices"
	"strings"
)

// frameworkSeparator finds the framework's end-of-options marker, skipping a
// wrapper's marker before the framework executable (for example npx -- jest).
func frameworkSeparator(command string, args []string, executable string) int {
	start := 0
	if frameworkExecutableName(command) != executable {
		for i, arg := range args {
			if frameworkExecutableName(arg) == executable {
				start = i + 1
				break
			}
		}
	}

	if index := slices.Index(args[start:], "--"); index >= 0 {
		return start + index
	}
	return -1
}

// Framework-generated options must precede its end-of-options marker.
func withFrameworkOptions(command string, args []string, executable string, options ...string) []string {
	args = slices.Clone(args)
	if index := frameworkSeparator(command, args, executable); index >= 0 {
		return slices.Insert(args, index, options...)
	}
	return append(args, options...)
}

// Everything after a framework's -- is positional, so replace that explicit
// selection with the selected files. Leave options before it untouched.
func withFrameworkFiles(command string, args []string, executable string, files []string) []string {
	if index := frameworkSeparator(command, args, executable); index >= 0 {
		args = args[:index+1]
	}
	return append(slices.Clone(args), files...)
}

func frameworkExecutableName(value string) string {
	name := filepath.Base(strings.ReplaceAll(value, `\`, "/"))
	for _, suffix := range []string{".js", ".mjs", ".cmd", ".ps1"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}
