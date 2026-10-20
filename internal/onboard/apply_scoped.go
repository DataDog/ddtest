package onboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// SetupDependencyGap names a package-manager operation, not an invitation to
// redesign hooks. The automatic editor is retried after this changes the lockfile.
type SetupDependencyGap struct {
	Command  []string
	Packages []string
}

func (g *SetupDependencyGap) Error() string {
	return "Missing CI coverage dependencies: " + strings.Join(g.Packages, ", ")
}

func scopedEnvironment(framework string) string {
	if framework == "cucumber" {
		return "DD_CUCUMBER_NODE_OPTIONS"
	}
	if framework == "webdriverio" {
		return "DD_WDIO_NODE_OPTIONS"
	}
	return "NODE_OPTIONS"
}

// Only literal package-script leaves are changed. Framework configs, profiles,
// imports, reporters, services and other framework consumers remain untouched.
func scopedSetupEdits(root, framework string) ([]setupEdit, string, error) {
	if framework != "cucumber" && framework != "webdriverio" {
		return nil, "", nil
	}
	edit, err := readSetupEdit(root, "package.json")
	if err != nil {
		return nil, "", err
	}
	var pkg struct {
		Type                          string
		Scripts                       map[string]string
		Dependencies, DevDependencies map[string]string
	}
	if err := json.Unmarshal(edit.before, &pkg); err != nil {
		return nil, "", err
	}
	options := strings.TrimPrefix(setupPreload(framework), "${{ env.NODE_OPTIONS }} ")
	if framework == "cucumber" {
		packages := []string{}
		for _, name := range []string{"nyc", "@istanbuljs/esm-loader-hook"} {
			if name != "nyc" && pkg.Type != "module" {
				continue
			}
			if pkg.Dependencies[name] == "" && pkg.DevDependencies[name] == "" {
				packages = append(packages, name)
			}
		}
		if len(packages) > 0 {
			// npm owns manifest and lockfile consistency. Other package managers
			// receive the precise dependency gap rather than a guessed command.
			manager := "npm"
			for _, name := range []string{"pnpm-lock.yaml", "yarn.lock"} {
				data, err := readSetupEdit(root, name)
				if err != nil {
					return nil, "", err
				}
				if data.before != nil {
					manager = "other"
				}
			}
			if manager != "npm" {
				return nil, "", &SetupGap{"package.json / devDependencies", "Install " + strings.Join(packages, ", ") + " with this repository's package manager, then rerun onboard --apply."}
			}
			return nil, "", &SetupDependencyGap{Packages: packages, Command: append([]string{"npm", "install", "--save-dev", "--no-audit", "--no-fund"}, packages...)}
		}
		if pkg.Type == "module" {
			options += " --experimental-loader @istanbuljs/esm-loader-hook"
		}
	}
	changed := false
	for _, name := range slices.Sorted(maps.Keys(pkg.Scripts)) {
		script := pkg.Scripts[name]
		prefix := cucumberScopedOptions
		if framework == "webdriverio" {
			prefix = webdriverIOScopedOptions
		}
		if strings.HasPrefix(script, prefix+" ") {
			if framework != "cucumber" || strings.Contains(script, cucumberImpactedWorkaround) {
				continue
			}
			script = strings.TrimPrefix(script, prefix+" ")
		}
		binary := "cucumber-js"
		if framework == "webdriverio" {
			binary = "wdio"
		}
		// A leaf must contain the literal framework binary and resolve to one
		// supported invocation; orchestration aliases retain their original text.
		if !strings.Contains(script, binary) {
			continue
		}
		remaining := 256
		command := script
		if framework == "cucumber" {
			command = cucumberDiscoveryScript(command)
		}
		resolution := resolveJestSequence(root, root, command, nil, &remaining, framework)
		if resolution.Reason != "" || len(resolution.Tests) != 1 || resolution.OtherFramework || !resolution.SingleJest {
			return nil, "", &SetupGap{"package.json / scripts / " + name, "Custom framework wrapper requires an explicit scoped initialization; preserve its command."}
		}
		if framework == "webdriverio" {
			local, err := localWDIOLeaf(root, script)
			if err != nil {
				return nil, "", &SetupGap{"package.json / scripts / " + name, err.Error()}
			}
			if !local {
				continue
			} // Explicit browser runner remains outside instrumentation.
		} else if !resolution.Tests[0].usesNYC {
			start, end, insertion, err := cucumberCoverageEdit(script)
			if err != nil {
				return nil, "", &SetupGap{"package.json / scripts / " + name, err.Error()}
			}
			script = script[:start] + insertion + script[end:]
		}
		extra := ""
		if framework == "cucumber" {
			extra = " " + cucumberImpactedWorkaround
		}
		replacement := prefix + extra + " " + script
		key, _ := json.Marshal(name)
		pattern := regexp.MustCompile(`(` + regexp.QuoteMeta(string(key)) + `\s*:\s*)("(?:\\.|[^"\\])*")`)
		matches := pattern.FindAllSubmatchIndex(edit.before, -1)
		if len(matches) != 1 {
			return nil, "", &SetupGap{"package.json / scripts / " + name, "Ambiguous script property; edit only its framework leaf explicitly."}
		}
		value, _ := json.Marshal(replacement)
		match := matches[0]
		edit.before = append(slices.Clone(edit.before[:match[4]]), append(value, edit.before[match[5]:]...)...)
		changed = true
	}
	if !changed {
		return nil, options, nil
	}
	edit.after = edit.before
	original, err := readSetupEdit(root, "package.json")
	if err != nil {
		return nil, "", err
	}
	edit.before = original.before
	return []setupEdit{edit}, options, nil
}

// Locate the executable word, not an occurrence in an assignment or argument.
// Offsets refer to the original script, including its quoting and local path.
func cucumberCoverageEdit(script string) (int, int, string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return 0, 0, "", err
	}
	offset, end := -1, -1
	insertion := "npx --no-install nyc -- "
	valid := true
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		i := 0
		if call.Args[i].Lit() == "cross-env" {
			i++
			for i < len(call.Args) && strings.Contains(call.Args[i].Lit(), "=") {
				i++
			}
		}
		launcher := i
		if i < len(call.Args) && slices.Contains([]string{"npx", "pnpx"}, call.Args[i].Lit()) {
			i++
		} else if i+1 < len(call.Args) && slices.Contains([]string{"npm", "pnpm"}, call.Args[i].Lit()) && call.Args[i+1].Lit() == "exec" {
			i += 2
		}
		for i < len(call.Args) && slices.Contains([]string{"--no-install", "--"}, call.Args[i].Lit()) {
			i++
		}
		if i < len(call.Args) && filepath.Base(call.Args[i].Lit()) == "cucumber-js" {
			if offset >= 0 {
				valid = false
			}
			offset = int(call.Args[i].Pos().Offset())
			end = offset
			if i > launcher {
				insertion = "nyc -- "
				if call.Args[launcher].Lit() == "npx" {
					offset = int(call.Args[launcher].Pos().Offset())
					insertion = "npx --no-install nyc -- "
				}
			}
		}
		return true
	})
	if !valid || offset < 0 {
		return 0, 0, "", fmt.Errorf("cannot locate one literal Cucumber executable; preserve this script and configure coverage explicitly")
	}
	return offset, end, insertion, nil
}

var localWDIORunner = regexp.MustCompile(`(?m)^\s*runner:\s*['"]local['"]\s*,`)
var browserWDIORunner = regexp.MustCompile(`(?m)^\s*runner:\s*(?:\[\s*)?['"]browser['"]`)
var wdioBaseConfig = regexp.MustCompile(`(?m)^import\s+\{\s*config\s+as\s+baseConfig\s*\}\s+from\s+['"]([^'"]+)['"]`)

func localWDIOLeaf(root, script string) (bool, error) {
	parts, err := staticCommands(script)
	if err != nil || len(parts) != 1 {
		return false, fmt.Errorf("wDIO leaf needs a literal CLI command")
	}
	words := parts[0]
	for i, word := range words {
		if filepath.Base(word) != "wdio" || i+2 >= len(words) || words[i+1] != "run" {
			continue
		}
		config := words[i+2]
		for range 4 {
			if !filepath.IsLocal(config) {
				return false, fmt.Errorf("wDIO config must be repository-local")
			}
			edit, err := readSetupEdit(root, config)
			if err != nil {
				return false, err
			}
			if browserWDIORunner.Match(edit.before) {
				return false, nil
			}
			if localWDIORunner.Match(edit.before) {
				return true, nil
			}
			base := wdioBaseConfig.FindSubmatch(edit.before)
			if len(base) != 2 {
				break
			}
			config = filepath.Join(filepath.Dir(config), string(base[1]))
		}
		return false, fmt.Errorf("cannot identify a literal local/browser runner in %s; review its scoped worker initialization", words[i+2])
	}
	return false, fmt.Errorf("cannot identify the WDIO configuration argument")
}
