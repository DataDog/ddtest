package testdrive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

const pytestArgumentFilesEnvironment = "DDTEST_PYTEST_ARGUMENT_FILES"

func (t *Testdrive) rebasePytestConfiguration(directory string) error {
	t.configurationEnvironment["DDTEST_PYTEST_CONFIG_OVERRIDES"] = ext.UnsetEnvironment
	t.configurationEnvironment[pytestArgumentFilesEnvironment] = ext.UnsetEnvironment
	usedArgumentFiles := false
	coverageFile := os.Getenv("COVERAGE_FILE")
	if configured, ok := t.configurationEnvironment["COVERAGE_FILE"]; ok {
		coverageFile = configured
	}
	if coverageFile != "" {
		t.configurationEnvironment["COVERAGE_FILE"] = rebasePytestPaths([]string{coverageFile}, t.configurationSourceRoot, t.checkoutRoot())[0]
	}
	pythonPath := os.Getenv("PYTHONPATH")
	if configured, ok := t.configurationEnvironment["PYTHONPATH"]; ok {
		pythonPath = configured
	}
	if pythonPath != "" {
		paths := strings.Split(pythonPath, string(os.PathListSeparator))
		for i, path := range paths {
			if filepath.IsAbs(path) {
				paths[i] = rebasePytestPaths([]string{path}, t.configurationSourceRoot, t.checkoutRoot())[0]
			}
		}
		t.configurationEnvironment["PYTHONPATH"] = strings.Join(paths, string(os.PathListSeparator))
	}
	words := append([]string{t.command}, t.args...)
	args, ok := onboard.PytestArguments(words)
	if !ok {
		return nil // Preflight reports unsupported command shapes.
	}
	prefix := len(words) - len(args)
	commandPrefix := mapUVDirectories(words[:prefix], func(option, value string) string {
		value = rebasePytestPaths([]string{value}, t.configurationSourceRoot, t.checkoutRoot())[0]
		if option == "--directory" {
			if filepath.IsAbs(value) {
				directory = value
			} else {
				directory = filepath.Join(directory, value)
			}
		}
		return value
	})
	relative, err := filepath.Rel(workspace.CanonicalRoot(t.checkoutRoot()), workspace.CanonicalRoot(directory))
	if err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf("pytest working directory leaves the isolated checkout")
	}
	addopts := os.Getenv("PYTEST_ADDOPTS")
	if value, ok := t.configurationEnvironment["PYTEST_ADDOPTS"]; ok {
		addopts = value
	}
	options, err := shellquote.Split(addopts)
	if err != nil {
		return fmt.Errorf("cannot parse PYTEST_ADDOPTS: %w", err)
	}
	options, err = expandPytestArgumentFiles(options, directory, t.configurationSourceRoot, t.checkoutRoot(), map[string]bool{}, &usedArgumentFiles)
	if err != nil {
		return err
	}
	args, err = expandPytestArgumentFiles(args, directory, t.configurationSourceRoot, t.checkoutRoot(), map[string]bool{}, &usedArgumentFiles)
	if err != nil {
		return err
	}
	environmentCount := len(options)
	combined, overridden, err := isolatePytestOptions(append(options, args...), directory, t.configurationSourceRoot, t.checkoutRoot(), map[string]bool{}, &usedArgumentFiles)
	if err != nil {
		return err
	}
	options, args = combined[:environmentCount], combined[environmentCount:]
	words = append(commandPrefix, args...)
	t.args = words[1:]
	var owned []string
	if !overridden {
		defaults, configErr := onboard.PytestConfigOptions(t.checkoutRoot(), directory, shellquote.Join(options...), words)
		if configErr != nil {
			return fmt.Errorf("cannot isolate pytest configuration: %w", configErr)
		}
		isolated, _, err := isolatePytestOptions(defaults, directory, t.configurationSourceRoot, t.checkoutRoot(), map[string]bool{}, &usedArgumentFiles)
		if err != nil {
			return err
		}
		isolated = stripPytestFlags(isolated, "--ddtrace", "--ddtrace-patch-all")
		if !slices.Equal(defaults, isolated) {
			// Put the copied defaults before user options so their overrides
			// retain pytest's normal environment/command precedence.
			owned = append(owned, "-o", "addopts="+shellquote.Join(isolated...))
		}
	}
	paths, configErr := onboard.PytestConfigPaths(t.checkoutRoot(), directory, shellquote.Join(options...), words)
	if configErr != nil {
		return fmt.Errorf("cannot isolate pytest configuration: %w", configErr)
	}
	for _, key := range []string{"testpaths", "pythonpath", "cache_dir", "log_file"} {
		isolated := rebasePytestPaths(paths[key], t.configurationSourceRoot, t.checkoutRoot())
		if !slices.Equal(paths[key], isolated) {
			value := shellquote.Join(isolated...)
			if key == "cache_dir" || key == "log_file" {
				value = isolated[0]
			}
			owned = append(owned, "-o", key+"="+value)
		}
	}
	if usedArgumentFiles {
		t.configurationEnvironment[pytestArgumentFilesEnvironment] = "true"
	}
	if len(owned) > 0 {
		options = append(owned, options...)
		data, err := json.Marshal(owned)
		if err != nil {
			return err
		}
		t.configurationEnvironment["DDTEST_PYTEST_CONFIG_OVERRIDES"] = string(data)
	}
	if addopts != "" || len(options) > 0 {
		t.configurationEnvironment["PYTEST_ADDOPTS"] = shellquote.Join(options...)
	}
	return nil
}

// Expand argument files before finding configuration or applying option
// precedence. Inline addopts is another source of pytest arguments.
func isolatePytestOptions(args []string, directory, original, copied string, active map[string]bool, used *bool) ([]string, bool, error) {
	args, err := expandPytestArgumentFiles(args, directory, original, copied, active, used)
	if err != nil {
		return nil, false, err
	}
	args = rebasePytestPaths(args, original, copied)
	overrideIndex, overrideValue, overrideAttached := -1, "", false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if slices.Contains([]string{"-k", "-m", "-p", "-c", "--config-file", "--rootdir", "--confcutdir", "--basetemp", "--ignore", "--deselect", "--junitxml", "--junit-xml"}, arg) {
			i++
			continue
		}
		value, attached := "", false
		switch {
		case arg == "-o" || arg == "--override-ini":
			i++
			if i >= len(args) {
				return nil, false, fmt.Errorf("%s requires an ini override", arg)
			}
			value = args[i]
		case strings.HasPrefix(arg, "--override-ini="):
			value, attached = strings.TrimPrefix(arg, "--override-ini="), true
		case strings.HasPrefix(arg, "-o") && !strings.HasPrefix(arg, "--"):
			value, attached = strings.TrimPrefix(strings.TrimPrefix(arg, "-o"), "="), true
		default:
			continue
		}
		key, value, _ := strings.Cut(value, "=")
		if slices.Contains([]string{"testpaths", "pythonpath", "cache_dir", "log_file"}, key) {
			paths := []string{value}
			if key != "cache_dir" && key != "log_file" {
				paths, err = shellquote.Split(value)
				if err != nil {
					return nil, false, fmt.Errorf("cannot parse inline pytest %s: %w", key, err)
				}
			}
			isolated := rebasePytestPaths(paths, original, copied)
			if !slices.Equal(paths, isolated) {
				value = shellquote.Join(isolated...)
				if key == "cache_dir" || key == "log_file" {
					value = isolated[0]
				}
				args[i] = key + "=" + value
				if attached {
					args[i] = "--override-ini=" + args[i]
				}
			}
			continue
		}
		if key != "addopts" {
			continue
		}
		overrideIndex, overrideValue, overrideAttached = i, value, attached
	}
	if overrideIndex < 0 {
		return args, false, nil
	}
	if active["addopts:"+overrideValue] {
		return nil, false, fmt.Errorf("recursive pytest addopts override")
	}
	options, err := shellquote.Split(overrideValue)
	if err != nil {
		return nil, false, fmt.Errorf("cannot parse inline pytest addopts: %w", err)
	}
	active["addopts:"+overrideValue] = true
	options, _, err = isolatePytestOptions(options, directory, original, copied, active, used)
	delete(active, "addopts:"+overrideValue)
	if err != nil {
		return nil, false, err
	}
	options = stripPytestFlags(options, "--ddtrace", "--ddtrace-patch-all")
	args[overrideIndex] = "addopts=" + shellquote.Join(options...)
	if overrideAttached {
		args[overrideIndex] = "--override-ini=" + args[overrideIndex]
	}
	return args, true, nil
}

func rebaseUVDirectories(words []string, original, copied string) []string {
	return mapUVDirectories(words, func(_, value string) string {
		return rebasePytestPaths([]string{value}, original, copied)[0]
	})
}

func mapUVDirectories(words []string, transform func(option, value string) string) []string {
	words = slices.Clone(words)
	if len(words) == 0 || strings.TrimSuffix(filepath.Base(words[0]), ".exe") != "uv" {
		return words
	}
	for i := 2; i < len(words); i++ {
		if words[i] == "--" || platform.IsPythonExecutable(words[i]) || strings.TrimSuffix(filepath.Base(words[i]), ".exe") == "pytest" {
			break
		}
		option, value, assigned := strings.Cut(words[i], "=")
		if option != "--directory" && option != "--project" {
			if !assigned && slices.Contains([]string{"--package", "--python", "-p", "--group", "--no-group", "--only-group", "--extra", "--no-extra"}, option) {
				i++
			}
			continue
		}
		if assigned {
			words[i] = option + "=" + transform(option, value)
		} else if i+1 < len(words) {
			i++
			words[i] = transform(option, words[i])
		}
	}
	return words
}

func rebasePytestPaths(args []string, original, copied string) []string {
	args = slices.Clone(args)
	lexicalOriginal := original
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
		} else if relative, err := filepath.Rel(lexicalOriginal, path); err == nil && filepath.IsLocal(relative) {
			path = filepath.Join(original, relative)
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
			if equal && slices.Contains([]string{"--config-file", "--rootdir", "--confcutdir", "--basetemp", "--ignore", "--ignore-glob", "--deselect", "--junitxml", "--junit-xml", "--cov", "--cov-config", "--log-file"}, option) {
				args[i] = option + "=" + rebase(value)
			} else if option == "--cov-report" {
				if !equal {
					if i+1 >= len(args) {
						continue
					}
					i++
					value = args[i]
				}
				if format, path, hasPath := strings.Cut(value, ":"); hasPath {
					value = format + ":" + rebase(path)
				}
				args[i] = value
				if equal {
					args[i] = option + "=" + value
				}
			} else if strings.HasPrefix(arg, "-c") && arg != "-c" {
				args[i] = "-c" + rebase(strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "="))
			}
			continue
		}
		args[i] = rebase(arg)
	}
	return args
}

// pytest reads one argument per line, including nested @files. Read only
// the disposable checkout; relative references use pytest's working directory.
func expandPytestArgumentFiles(args []string, directory, original, copied string, active map[string]bool, used *bool) ([]string, error) {
	var expanded []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "@") {
			expanded = append(expanded, arg)
			continue
		}
		if used != nil {
			*used = true
		}
		path := strings.TrimPrefix(rebasePytestPaths([]string{arg}, original, copied)[0], "@")
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("pytest argument file unavailable: %w", err)
		}
		relative, err := filepath.Rel(workspace.CanonicalRoot(copied), resolved)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, fmt.Errorf("pytest argument file resolves outside checkout: %s; keep argument files inside the repository", path)
		}
		if active[resolved] {
			return nil, fmt.Errorf("recursive pytest argument file: %s", path)
		}
		active[resolved] = true
		data, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("read pytest argument file: %w", err)
		}
		lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines, err = expandPytestArgumentFiles(lines, directory, original, copied, active, used)
		delete(active, resolved)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, lines...)
	}
	return expanded, nil
}
