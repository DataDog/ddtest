package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/pelletier/go-toml/v2"
)

type pythonProject struct{ python, pytest bool }

func (p *Python) detectFramework(root, hint string) (framework.Framework, error) {
	candidates := []framework.Framework{framework.NewPytest()}
	if hint == "" {
		info, err := inspectPythonProject(root)
		if err != nil {
			return nil, err
		}
		if !info.pytest {
			candidates = nil
		}
	}
	fw, err := selectFramework(p.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	fw.SetPlatformEnv(p.GetPlatformEnv())
	return fw, nil
}

func inspectPythonProject(root string) (pythonProject, error) {
	var result pythonProject
	marker, err := detectAnyFile(root, "pytest.ini", ".pytest.ini", "conftest.py")
	if err != nil {
		return result, err
	}
	result.pytest = marker
	result.python = marker
	setup, err := detectAnyFile(root, "setup.py", "requirements.txt")
	if err != nil {
		return result, err
	}
	result.python = result.python || setup
	data, err := readProjectFile(root, "pyproject.toml")
	if err != nil {
		return result, err
	}
	if data != nil {
		var config map[string]any
		if err := toml.Unmarshal(data, &config); err != nil {
			return result, fmt.Errorf("parse pyproject.toml: %w", err)
		}
		for _, key := range []string{"project", "build-system", "dependency-groups"} {
			if _, ok := config[key]; ok {
				result.python = true
			}
		}
		tool := tomlTable(config["tool"])
		for _, key := range []string{"pytest", "poetry", "pdm", "hatch", "setuptools", "flit", "ruff", "black", "mypy", "isort", "coverage", "tox"} {
			if _, ok := tool[key]; ok {
				result.python = true
			}
		}
		if _, ok := tool["pytest"]; ok {
			result.pytest = true
		}
		project := tomlTable(config["project"])
		result.pytest = result.pytest || hasPytestRequirement(project["dependencies"]) || hasPytestRequirement(project["optional-dependencies"]) || hasPytestRequirement(config["dependency-groups"])
		poetry := tomlTable(tool["poetry"])
		for _, key := range []string{"dependencies", "dev-dependencies"} {
			if _, ok := tomlTable(poetry[key])["pytest"]; ok {
				result.pytest = true
			}
		}
		for _, group := range tomlTable(poetry["group"]) {
			if _, ok := tomlTable(tomlTable(group)["dependencies"])["pytest"]; ok {
				result.pytest = true
			}
		}
		result.pytest = result.pytest || hasPytestRequirement(tomlTable(tool["pdm"])["dev-dependencies"])
	}
	for _, name := range []string{"setup.cfg", "tox.ini"} {
		data, err := readProjectFile(root, name)
		if err != nil {
			return result, err
		}
		sections := parseProjectINI(data)
		if name == "setup.cfg" {
			for _, section := range []string{"options", "options.packages.find", "options.extras_require", "tool:pytest", "flake8", "isort", "mypy", "coverage:run", "coverage:report"} {
				if _, ok := sections[section]; ok {
					result.python = true
				}
			}
			if _, ok := sections["tool:pytest"]; ok {
				result.pytest = true
			}
			for _, value := range sections["options.extras_require"] {
				result.pytest = result.pytest || hasPytestRequirement(value)
			}
			for _, key := range []string{"install_requires", "tests_require"} {
				result.pytest = result.pytest || hasPytestRequirement(sections["options"][key])
			}
		} else {
			for section, values := range sections {
				if section == "tox" || section == "testenv" || strings.HasPrefix(section, "testenv:") {
					result.python = true
				}
				if section == "pytest" {
					result.python = true
					result.pytest = true
				}
				if section == "testenv" || strings.HasPrefix(section, "testenv:") {
					result.pytest = result.pytest || hasPytestRequirement(values["deps"]) || runsPytest(values["commands"])
				}
			}
		}
	}
	for _, name := range []string{"requirements.txt", "requirements-dev.txt", "requirements-test.txt"} {
		data, err := readProjectFile(root, name)
		if err != nil {
			return result, err
		}
		result.pytest = result.pytest || hasPytestRequirement(string(data))
	}
	result.python = result.python || result.pytest
	return result, nil
}

func readProjectFile(root, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

func tomlTable(value any) map[string]any { table, _ := value.(map[string]any); return table }

var pytestRequirement = regexp.MustCompile(`(?i)^pytest(?:\[[^]]*\])?(?:\s*[<>=!~;@].*)?$`)

// Inspect dependency values only, never arbitrary descriptions or tool options.
func hasPytestRequirement(value any) bool {
	switch value := value.(type) {
	case string:
		for _, line := range strings.Split(value, "\n") {
			line, _, _ = strings.Cut(line, "#")
			if pytestRequirement.MatchString(strings.TrimSpace(line)) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if _, ok := item.(string); ok && hasPytestRequirement(item) {
				return true
			}
		}
	case map[string]any:
		for _, group := range value {
			if hasPytestRequirement(group) {
				return true
			}
		}
	}
	return false
}

func runsPytest(commands string) bool {
	for _, line := range strings.Split(commands, "\n") {
		args := strings.Fields(line)
		if len(args) == 0 {
			continue
		}
		if args[0] == "pytest" || args[0] == "py.test" {
			return true
		}
		if len(args) >= 3 && (args[0] == "python" || args[0] == "python3" || args[0] == "{envpython}") && args[1] == "-m" && args[2] == "pytest" {
			return true
		}
	}
	return false
}

// Only INI sections and their own values provide evidence. Comments and values
// in unrelated sections must not turn an arbitrary setup.cfg into a pytest project.
func parseProjectINI(data []byte) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	section, key := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			name, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "["), "]")
			if ok {
				section = strings.ToLower(strings.TrimSpace(name))
				key = ""
				sections[section] = make(map[string]string)
			}
			continue
		}
		if section == "" {
			continue
		}
		if key != "" && (line[0] == ' ' || line[0] == '\t') {
			sections[section][key] += "\n" + trimmed
			continue
		}
		name, value, ok := strings.Cut(trimmed, "=")
		if ok {
			key = strings.ToLower(strings.TrimSpace(name))
			sections[section][key] = strings.TrimSpace(value)
		}
	}
	return sections
}
