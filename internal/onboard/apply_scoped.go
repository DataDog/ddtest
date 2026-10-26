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
// redesign hooks. Install it with the repository package manager, then rerun onboarding.
type SetupDependencyGap struct {
	Command  []string
	Packages []string
}

func (g *SetupDependencyGap) Error() string {
	return "Missing CI coverage dependencies: " + strings.Join(g.Packages, ", ") + ". Install with: " + strings.Join(g.Command, " ")
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
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return nil, "", err
	}
	selected := map[string]bool{}
	for _, entry := range scope.Commands {
		if len(entry.packageScripts) == 0 {
			return nil, "", &SetupGap{entry.Command, "Scoped initialization requires an identified package-script consumer; initialize this direct invocation explicitly."}
		}
		for _, script := range entry.packageScripts {
			if !script.Leaf {
				continue
			}
			if script.Directory != "." {
				return nil, "", &SetupGap{script.Directory, "Scoped package edits require explicit initialization in this package"}
			}
			selected[script.Name] = true
		}
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
			return nil, "", setupDependencyGap(root, packages)
		}

		if pkg.Type == "module" {
			options += " --experimental-loader @istanbuljs/esm-loader-hook"
		}
	}
	changed := false
	for _, name := range slices.Sorted(maps.Keys(pkg.Scripts)) {
		if !selected[name] {
			continue
		}
		script := pkg.Scripts[name]
		prefix := cucumberScopedOptions
		if framework == "webdriverio" {
			prefix = webdriverIOScopedOptions
		}
		loaders := ""
		if framework == "cucumber" {
			// An earlier apply already wrote the scoped prefix, possibly with the
			// loaders it kept; adding another would hide them from probes.
			if found, scoped := cucumberScopedLoaders(script); scoped {
				if strings.Contains(script, cucumberImpactedWorkaround) {
					continue
				}
				if match := cucumberScopedPrefix.FindString(strings.TrimSpace(script)); match != "" {
					script, loaders = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), match)), found
				}
			} else if rest, found, ok := cucumberLoaderOverride(script); ok {
				// Keep in-script source loaders after the inherited options instead
				// of letting their NODE_OPTIONS replace the tracer preload.
				script, loaders = rest, found
			}
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
		if loaders != "" {
			prefix = strings.TrimSuffix(prefix, `"`) + " " + loaders + `"`
		}
		replacement := prefix + extra + " " + script
		key, _ := json.Marshal(name)
		pattern := regexp.MustCompile(`(` + regexp.QuoteMeta(string(key)) + `\s*:\s*)("(?:\\.|[^"\\])*")`)
		matches := pattern.FindAllSubmatchIndex(edit.before, -1)
		if len(matches) != 1 {
			return nil, "", &SetupGap{"package.json / scripts / " + name, "Ambiguous script property; edit only its framework leaf explicitly."}
		}
		value := setupJSON(replacement)
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
		for i < len(call.Args) && slices.Contains([]string{"--no-install", "--no", "--"}, call.Args[i].Lit()) {
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
				} else if call.Args[launcher].Lit() == "npm" {
					offset = int(call.Args[launcher].Pos().Offset())
					insertion = "npm exec --no -- nyc -- "
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

func setupPinnedDependencies(packages []string) []string {
	result := make([]string, len(packages))
	for i, name := range packages {
		if name == "nyc" {
			result[i] = name + "@17.1.0"
		} else {
			result[i] = name + "@0.3.0"
		}
	}
	return result
}

func setupDependencyGap(root string, packages []string) error {
	manager := "npm"
	for _, lock := range []struct{ path, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}} {
		edit, err := readSetupEdit(root, lock.path)
		if err != nil {
			return err
		}
		if edit.before != nil {
			if manager != "npm" {
				return &SetupGap{"dependencies", "Multiple package-manager lockfiles require review before installing coverage dependencies."}
			}
			manager = lock.manager
		}
	}
	command := map[string][]string{"npm": {"npm", "install", "--save-dev", "--no-audit", "--no-fund"}, "pnpm": {"pnpm", "add", "--save-dev"}, "yarn": {"yarn", "add", "--dev"}}[manager]
	return &SetupDependencyGap{Packages: packages, Command: append(command, setupPinnedDependencies(packages)...)}
}
