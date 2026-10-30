package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kballard/go-shellquote"
	"github.com/pelletier/go-toml/v2"
)

// PytestConfigOptions reads configuration without importing project code. For
// collection paths which could change config discovery, require an explicit -c
// instead of guessing which file pytest will select. Version-dependent native
// TOML and config-based Datadog activation remain unverified.
func PytestConfigOptions(root, directory, addopts string, words []string) ([]string, error) {
	settings, err := pytestConfigSettings(root, directory, addopts, words)
	if err != nil {
		return nil, err
	}
	return pytestConfigList(settings["addopts"], "addopts")
}

// PytestConfigPaths reads the native path settings from the selected configuration.
func PytestConfigPaths(root, directory, addopts string, words []string) (map[string][]string, error) {
	settings, err := pytestConfigSettings(root, directory, addopts, words)
	if err != nil {
		return nil, err
	}
	paths := map[string][]string{}
	for _, key := range []string{"testpaths", "pythonpath", "cache_dir", "log_file"} {
		value := settings[key]
		if (key == "cache_dir" || key == "log_file") && value != nil {
			path, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("pytest configuration %s must be a string", key)
			}
			paths[key] = []string{path}
			continue
		}
		paths[key], err = pytestConfigList(value, key)
		if err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func pytestConfigList(value any, key string) ([]string, error) {
	switch value := value.(type) {
	case nil:
		return nil, nil
	case string:
		return shellquote.Split(value)
	case []any:
		var options []string
		for _, item := range value {
			word, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("pytest configuration %s must contain strings", key)
			}
			options = append(options, word)
		}
		return options, nil
	default:
		return nil, fmt.Errorf("pytest configuration %s must be a string or string array", key)
	}
}

func pytestConfigSettings(root, directory, addopts string, words []string) (map[string]any, error) {
	args, ok := pythonRunnerArguments(words, "pytest")
	if !ok {
		return nil, fmt.Errorf("cannot identify pytest configuration arguments")
	}
	options, err := shellquote.Split(addopts)
	if err != nil {
		return nil, err
	}
	options = append(options, args...)
	var config string
	var paths []string
	positional := false
	for i := 0; i < len(options); i++ {
		option, value, assigned := strings.Cut(options[i], "=")
		if !positional && option == "--" {
			positional = true
			continue
		}
		if !positional && (option == "-c" || option == "--config-file") {
			if !assigned {
				i++
				if i >= len(options) {
					return nil, fmt.Errorf("%s requires a pytest configuration file", option)
				}
				value = options[i]
			}
			config = value
			continue
		}
		if !positional && strings.HasPrefix(option, "-c") && len(option) > 2 {
			config = strings.TrimPrefix(options[i][2:], "=")
			continue
		}
		if !positional && (option == "-k" || option == "-m" || option == "-o" || option == "--override-ini" || option == "-p" || option == "--rootdir") {
			if !assigned {
				i++
			}
			continue
		}
		if positional || !strings.HasPrefix(option, "-") {
			paths = append(paths, options[i])
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	if config != "" {
		if !filepath.IsAbs(config) {
			config = filepath.Join(directory, config)
		}
		options, _, err := readPytestConfigSettings(root, config)
		return options, err
	}
	selected, defaults, err := findPytestConfig(root, directory)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		path, _, _ = strings.Cut(path, "::")
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			if strings.ContainsAny(path, "*?[]{}$`~") {
				return nil, fmt.Errorf("pytest configuration depends on a non-literal collection path; select its config explicitly with -c")
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			path = filepath.Dir(path)
		}
		candidate, _, err := findPytestConfig(root, path)
		if err != nil {
			return nil, err
		}
		if candidate != selected {
			return nil, fmt.Errorf("pytest collection paths may select a different configuration; select its config explicitly with -c")
		}
	}
	return defaults, nil
}

func findPytestConfig(root, directory string) (string, map[string]any, error) {
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", nil, err
	}
	if relative, err := filepath.Rel(root, directory); err != nil || !filepath.IsLocal(relative) {
		return "", nil, fmt.Errorf("pytest configuration search leaves the repository")
	}
	for current := directory; ; current = filepath.Dir(current) {
		for _, name := range []string{"pytest.toml", ".pytest.toml", "pytest.ini", ".pytest.ini", "pyproject.toml", "tox.ini", "setup.cfg"} {
			path := filepath.Join(current, name)
			options, matched, err := readPytestConfigSettings(root, path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", nil, err
			}
			if name == ".pytest.ini" {
				return "", nil, fmt.Errorf("hidden pytest configuration selection is version-dependent; select it explicitly with -c")
			}
			if matched {
				return path, options, nil
			}
		}
		if current == root {
			return "", nil, nil
		}
	}
}

func readPytestConfigSettings(root, path string) (map[string]any, bool, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, false, err
	}
	if relative, err := filepath.Rel(root, canonical); err != nil || !filepath.IsLocal(relative) {
		return nil, false, fmt.Errorf("pytest configuration file leaves the repository")
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, false, fmt.Errorf("pytest configuration must be a regular file no larger than 1 MiB")
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return nil, false, err
	}
	if !utf8.Valid(data) {
		return nil, false, fmt.Errorf("pytest configuration is not UTF-8")
	}
	switch filepath.Ext(path) {
	case ".toml":
		var project struct {
			Pytest map[string]any `toml:"pytest"`
			Tool   struct {
				Pytest map[string]any `toml:"pytest"`
			} `toml:"tool"`
		}
		if err := toml.Unmarshal(data, &project); err != nil {
			return nil, false, fmt.Errorf("pytest configuration: %w", err)
		}
		if project.Pytest != nil || filepath.Base(path) == "pytest.toml" || filepath.Base(path) == ".pytest.toml" || len(project.Tool.Pytest) > 1 {
			return nil, false, fmt.Errorf("native pytest TOML configuration requires runtime validation")
		}
		settings, matched := project.Tool.Pytest["ini_options"].(map[string]any)
		if !matched && project.Tool.Pytest != nil {
			return nil, false, fmt.Errorf("native pytest TOML configuration requires runtime validation")
		}
		for _, key := range []string{"ddtrace", "no-ddtrace"} {
			if _, exists := settings[key]; exists {
				return nil, false, fmt.Errorf("pytest config-based Datadog activation requires runtime validation")
			}
		}
		return settings, matched, nil
	case ".ini", ".cfg":
		sections, err := pytestINISections(string(data))
		if err != nil {
			return nil, false, err
		}
		section := "pytest"
		if filepath.Ext(path) == ".cfg" {
			section = "tool:pytest"
			if sections[section] == nil && sections["pytest"] != nil {
				return nil, false, fmt.Errorf("pytest configuration in setup.cfg requires [tool:pytest]")
			}
		}
		for _, key := range []string{"ddtrace", "no-ddtrace"} {
			if _, exists := sections[section][key]; exists {
				return nil, false, fmt.Errorf("pytest config-based Datadog activation requires runtime validation")
			}
		}
		settings := map[string]any{}
		for key, value := range sections[section] {
			settings[key] = value
		}
		return settings, sections[section] != nil || filepath.Base(path) == "pytest.ini", nil
	default:
		return nil, false, fmt.Errorf("unsupported pytest configuration format")
	}
}

func pytestINISections(data string) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	section, key := "", ""
	for _, line := range strings.Split(strings.TrimPrefix(data, "\ufeff"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if line[0] == '[' {
			header := line
			if end := strings.IndexAny(header, "#;"); end >= 0 {
				header = header[:end]
			}
			header = strings.TrimSpace(header)
			if !strings.HasSuffix(header, "]") || len(header) < 3 || sections[header[1:len(header)-1]] != nil {
				return nil, fmt.Errorf("pytest configuration has an invalid or duplicate INI section")
			}
			section, key = header[1:len(header)-1], ""
			sections[section] = map[string]string{}
			continue
		}
		if section == "" {
			return nil, fmt.Errorf("pytest configuration has no INI section")
		}
		if line[0] == ' ' || line[0] == '\t' {
			if key == "" {
				return nil, fmt.Errorf("pytest configuration has an unexpected INI continuation")
			}
			sections[section][key] += "\n" + trimmed
			continue
		}
		separator := strings.IndexAny(line, "=:")
		if separator < 0 {
			return nil, fmt.Errorf("pytest configuration has an invalid INI option")
		}
		key = strings.TrimSpace(line[:separator])
		if _, duplicate := sections[section][key]; key == "" || duplicate {
			return nil, fmt.Errorf("pytest configuration has an empty or duplicate INI option")
		}
		sections[section][key] = strings.TrimSpace(line[separator+1:])
	}
	return sections, nil
}
