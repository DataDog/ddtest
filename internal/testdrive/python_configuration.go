package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

func (t *Testdrive) rebasePytestConfiguration(directory string) error {
	words := append([]string{t.command}, t.args...)
	args, ok := onboard.PytestArguments(words)
	if !ok {
		return nil // Preflight reports unsupported command shapes.
	}
	prefix := len(words) - len(args)
	words = append(slices.Clone(words[:prefix]), rebasePytestPaths(args, t.configurationSourceRoot, t.checkoutRoot())...)
	t.args = words[1:]
	files := map[string]bool{}
	if err := rebasePytestArgumentFiles(words[prefix:], directory, t.configurationSourceRoot, t.checkoutRoot(), files); err != nil {
		return err
	}
	addopts := os.Getenv("PYTEST_ADDOPTS")
	if value, ok := t.configurationEnvironment["PYTEST_ADDOPTS"]; ok {
		addopts = value
	}
	if addopts != "" {
		options, err := shellquote.Split(addopts)
		if err != nil {
			return fmt.Errorf("cannot parse PYTEST_ADDOPTS: %w", err)
		}
		options = rebasePytestPaths(options, t.configurationSourceRoot, t.checkoutRoot())
		if err := rebasePytestArgumentFiles(options, directory, t.configurationSourceRoot, t.checkoutRoot(), files); err != nil {
			return err
		}
		t.configurationEnvironment["PYTEST_ADDOPTS"] = shellquote.Join(options...)
	}
	return nil
}

func rebasePytestPaths(args []string, original, copied string) []string {
	args = slices.Clone(args)
	if canonical, err := filepath.EvalSymlinks(original); err == nil {
		original = canonical
	}
	rebase := func(value string) string {
		argument, fromFile := strings.CutPrefix(value, "@")
		path, selector, selected := strings.Cut(argument, "::")
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
		if fromFile {
			value = "@" + value
		}
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

// pytest reads one argument per line, including nested @files. Rewrite only
// files owned by the disposable checkout; relative references use pytest's cwd.
func rebasePytestArgumentFiles(args []string, directory, original, copied string, visited map[string]bool) error {
	for _, arg := range args {
		path, fromFile := strings.CutPrefix(arg, "@")
		if !fromFile {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("pytest argument file unavailable: %w", err)
		}
		relative, err := filepath.Rel(workspace.CanonicalRoot(copied), resolved)
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("pytest argument file resolves outside checkout: %s; keep argument files inside the repository", path)
		}
		if visited[resolved] {
			continue
		}
		visited[resolved] = true
		data, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Errorf("read pytest argument file: %w", err)
		}
		lines := rebasePytestPaths(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), original, copied)
		if err := rebasePytestArgumentFiles(lines, directory, original, copied, visited); err != nil {
			return err
		}
		if err := os.WriteFile(resolved, []byte(strings.Join(lines, "\n")), 0600); err != nil {
			return fmt.Errorf("write isolated pytest argument file: %w", err)
		}
	}
	return nil
}
