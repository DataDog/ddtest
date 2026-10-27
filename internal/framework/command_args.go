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
			if frameworkExecutableName(arg) == executable || executable == "pytest" && arg == "-mpytest" {
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
func WithFrameworkOptions(command string, args []string, executable string, options ...string) []string {
	args = slices.Clone(args)
	if npmScriptCommand(command, args) {
		if index := slices.Index(args, "--"); index >= 0 {
			tail := WithFrameworkOptions(executable, args[index+1:], executable, options...)
			return append(args[:index+1], tail...)
		}
		return append(append(args, "--"), options...)
	}
	if index := frameworkSeparator(command, args, executable); index >= 0 {
		return slices.Insert(args, index, options...)
	}
	return append(args, options...)
}

// Everything after a framework's -- is positional, so replace that explicit
// selection with the selected files. Leave options before it untouched.
func withFrameworkFiles(command string, args []string, executable string, files []string) []string {
	args = slices.Clone(args)
	if npmScriptCommand(command, args) {
		if index := slices.Index(args, "--"); index >= 0 {
			tail := withFrameworkFiles(executable, args[index+1:], executable, files)
			return append(args[:index+1], tail...)
		}
		return append(append(args, "--"), files...)
	}
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

func npmScriptCommand(command string, args []string) bool {
	return frameworkExecutableName(command) == "npm" && len(args) > 0 && slices.Contains([]string{"test", "t", "tst", "run", "run-script"}, args[0])
}
