package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This is a read-only inventory of native tox TOML, not an implementation of
// tox provisioning. Retain the wrapper as unresolved local execution while
// checking its runtime and instrumentation metadata statically.
type pythonTox struct {
	Name     string
	Commands [][]string
	Settings map[string]any
}

var toxPythonVersion = regexp.MustCompile(`^(py|pypy)([0-9])\.?([0-9]+)$`)

func (t pythonTox) runtime(python string) (string, string) {
	if match := toxPythonVersion.FindStringSubmatch(t.Name); match != nil {
		version := match[2] + "." + match[3]
		if match[1] == "pypy" {
			return "inconclusive", "The selected tox environment uses PyPy; validate its tracer support separately."
		}
		if python != version && !strings.HasPrefix(python, version+".") {
			return "inconclusive", fmt.Sprintf("tox selects Python %s, while setup-python selects %s; validate the tox interpreter separately", version, python)
		}
		return "", ""
	}
	return "inconclusive", "Cannot identify the selected tox interpreter from its environment name; validate it separately."
}

func readPythonTox(root string, args []string) (pythonTox, error) {
	var result pythonTox
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	var posargs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			posargs = args[i+1:]
			i = len(args)
		case (arg == "-e" || arg == "--environment") && i+1 < len(args):
			i++
			result.Name = args[i]
		case strings.HasPrefix(arg, "--environment="):
			result.Name = strings.TrimPrefix(arg, "--environment=")
		case strings.HasPrefix(arg, "-e") && len(arg) > 2:
			result.Name = arg[2:]
		case arg == "-q" || arg == "-v":
		default:
			return result, fmt.Errorf("unsupported tox option %q", arg)
		}
	}
	if result.Name == "" || !shellMatrixScalar.MatchString(result.Name) || strings.ContainsAny(result.Name, " /@:+") {
		return result, fmt.Errorf("select one literal tox environment with -e")
	}
	// These files take precedence over native TOML. Never inspect a different
	// configuration from the one tox would execute.
	for _, name := range []string{"tox.ini", "setup.cfg"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			return result, fmt.Errorf("%s requires separate tox configuration review", name)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return result, err
	}
	var project struct {
		Tool struct {
			Tox struct {
				Base map[string]any            `toml:"env_run_base"`
				Env  map[string]map[string]any `toml:"env"`
			} `toml:"tox"`
		} `toml:"tool"`
	}
	if err := toml.Unmarshal(contents, &project); err != nil {
		return result, err
	}
	result.Settings = maps.Clone(project.Tool.Tox.Base)
	if result.Settings == nil {
		result.Settings = map[string]any{}
	}
	maps.Copy(result.Settings, project.Tool.Tox.Env[result.Name])
	for _, key := range []string{"base", "base_python", "change_dir", "changedir", "commands_pre", "commands_post"} {
		if _, exists := result.Settings[key]; exists {
			return result, fmt.Errorf("tox environment %s uses %s; runtime/setup requires review", result.Name, key)
		}
	}
	commands, ok := result.Settings["commands"].([]any)
	if !ok {
		return result, fmt.Errorf("tox environment %s needs literal native TOML commands", result.Name)
	}
	for _, value := range commands {
		items, ok := value.([]any)
		if !ok {
			return result, fmt.Errorf("dynamic tox command requires review")
		}
		var words []string
		for _, item := range items {
			if text, ok := item.(string); ok {
				text = strings.ReplaceAll(text, "{envpython}", "python")
				// This substitution changes a pytest output path, not its options.
				text = strings.ReplaceAll(text, "{env_tmp_dir}", "TOX_ENV_TMP_DIR")
				if strings.ContainsAny(text, "{}") {
					return result, fmt.Errorf("dynamic tox argument requires review: %s", text)
				}
				words = append(words, text)
				continue
			}
			replacement, ok := item.(map[string]any)
			if !ok || replacement["replace"] != "posargs" || replacement["extend"] != true || len(replacement) != 3 {
				return result, fmt.Errorf("unsupported tox argument replacement")
			}
			defaults, ok := replacement["default"].([]any)
			if !ok {
				return result, fmt.Errorf("tox posargs need a literal default list")
			}
			selected := slices.Clone(posargs)
			if posargs == nil {
				for _, value := range defaults {
					text, ok := value.(string)
					if !ok || strings.ContainsAny(text, "{}") {
						return result, fmt.Errorf("dynamic tox posargs default requires review")
					}
					selected = append(selected, text)
				}
			}
			words = append(words, selected...)
		}
		if pythonInvocation(words) {
			result.Commands = append(result.Commands, words)
		} else if len(words) > 0 && !slices.Contains([]string{"mypy", "pyright", "ruff", "pre-commit", "sphinx-build"}, words[0]) {
			return result, fmt.Errorf("unresolved command in tox environment %s", result.Name)
		}
	}
	return result, nil
}

func (t pythonTox) instrumentation() (string, string) {
	for _, key := range []string{"set_env", "disallow_pass_env", "env_file"} {
		if _, exists := t.Settings[key]; exists {
			return "inconclusive", fmt.Sprintf("tox environment %s uses %s; verify effective tracing variables inside tox", t.Name, key)
		}
	}
	patterns, ok := t.Settings["pass_env"].([]any)
	if !ok {
		return "incompatible", "tox filters the action's tracing environment; add pass_env = [\"DD_*\", \"PYTHONPATH\", \"PYTEST_ADDOPTS\"] to the selected tox environment."
	}
	for _, required := range []string{"DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"} {
		forwarded := false
		for _, value := range patterns {
			pattern, ok := value.(string)
			if !ok {
				return "inconclusive", "Dynamic tox pass_env requires review."
			}
			pattern = strings.ToUpper(pattern)
			// Require the whole DD namespace, not just one sampled variable.
			forwarded = forwarded || pattern == "*" || pattern == required
		}
		if !forwarded {
			return "incompatible", fmt.Sprintf("tox environment %s does not forward %s; preserve the action's tracing variables with pass_env", t.Name, required)
		}
	}
	return "", ""
}
