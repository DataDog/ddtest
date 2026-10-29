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
		options, _, err := readPytestConfigOptions(root, config)
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

func findPytestConfig(root, directory string) (string, []string, error) {
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
			options, matched, err := readPytestConfigOptions(root, path)
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

func readPytestConfigOptions(root, path string) ([]string, bool, error) {
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
		switch value := settings["addopts"].(type) {
		case nil:
			return nil, matched, nil
		case string:
			options, err := shellquote.Split(value)
			return options, matched, err
		case []any:
			var options []string
			for _, item := range value {
				word, ok := item.(string)
				if !ok {
					return nil, false, fmt.Errorf("pytest configuration addopts must contain strings")
				}
				options = append(options, word)
			}
			return options, matched, nil
		default:
			return nil, false, fmt.Errorf("pytest configuration addopts must be a string or string array")
		}
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
		options, err := shellquote.Split(sections[section]["addopts"])
		return options, sections[section] != nil || filepath.Base(path) == "pytest.ini", err
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
