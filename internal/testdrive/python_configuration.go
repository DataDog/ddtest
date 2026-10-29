package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

func (t *Testdrive) rebasePytestConfiguration() error {
	words := append([]string{t.command}, t.args...)
	args, ok := onboard.PytestArguments(words)
	if !ok {
		return nil // Preflight reports unsupported command shapes.
	}
	prefix := len(words) - len(args)
	words = append(slices.Clone(words[:prefix]), rebasePytestPaths(args, t.configurationSourceRoot, t.checkoutRoot())...)
	t.args = words[1:]
	addopts := os.Getenv("PYTEST_ADDOPTS")
	if value, ok := t.configurationEnvironment["PYTEST_ADDOPTS"]; ok {
		addopts = value
	}
	if addopts != "" {
		options, err := shellquote.Split(addopts)
		if err != nil {
			return fmt.Errorf("cannot parse PYTEST_ADDOPTS: %w", err)
		}
		t.configurationEnvironment["PYTEST_ADDOPTS"] = shellquote.Join(rebasePytestPaths(options, t.configurationSourceRoot, t.checkoutRoot())...)
	}
	return nil
}

func rebasePytestPaths(args []string, original, copied string) []string {
	args = slices.Clone(args)
	if canonical, err := filepath.EvalSymlinks(original); err == nil {
		original = canonical
	}
	rebase := func(value string) string {
		path, selector, selected := strings.Cut(value, "::")
		if original == "" || !filepath.IsAbs(path) {
			return value
		}
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			path = canonical
		}
		relative, err := filepath.Rel(original, path)
		if err != nil || !filepath.IsLocal(relative) {
			return value
		}
		value = filepath.Join(copied, relative)
		if selected {
			value += "::" + selector
		}
		return value
	}
	positional := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = true
			continue
		}
		if !positional && slices.Contains([]string{"-k", "-m", "-p", "-o", "--override-ini"}, arg) && i+1 < len(args) {
			// These values are expressions, plugin names, or ini overrides.
			i++
			continue
		}
		if !positional && strings.HasPrefix(arg, "-") {
			option, value, equal := strings.Cut(arg, "=")
			if equal && slices.Contains([]string{"--config-file", "--rootdir", "--confcutdir", "--basetemp", "--ignore", "--ignore-glob", "--deselect", "--junitxml", "--junit-xml"}, option) {
				args[i] = option + "=" + rebase(value)
			} else if strings.HasPrefix(arg, "-c") && arg != "-c" {
				args[i] = "-c" + rebase(strings.TrimPrefix(arg, "-c"))
			}
			continue
		}
		args[i] = rebase(arg)
	}
	return args
}
