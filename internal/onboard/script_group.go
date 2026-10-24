package onboard

import (
	"bytes"
	"encoding/json"
	"github.com/kballard/go-shellquote"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Expand only literal npm-run-all selectors. Parallel branches never acquire
// ordering dependencies on one another; unknown branches remain unresolved.
func resolveScriptGroup(root, directory string, words, stack []string, remaining *int, framework string) commandResolution {
	if len(words) < 2 {
		return unresolvedCommand("Missing script selectors")
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	var pkg struct{ Scripts map[string]string }
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return unresolvedCommand("Cannot read scripts for " + words[0])
	}
	parallel := words[0] == "run-p"
	var commands []string
	seen := map[string]map[string]bool{}
	names := orderedScriptNames(data)
	for _, arg := range words[1:] {
		switch arg {
		case "--print-label", "-l", "--print-name", "-n":
			continue
		case "--parallel", "-p":
			if len(commands) > 0 && !parallel {
				return unresolvedCommand("Mixed sequential/parallel groups require review")
			}
			parallel = true
			continue
		case "--serial", "--sequential", "-s":
			if len(commands) > 0 && parallel {
				return unresolvedCommand("Mixed sequential/parallel groups require review")
			}
			parallel = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return unresolvedCommand("Unsupported script orchestration flag: " + arg)
		}
		selector, err := shellquote.Split(arg)
		if err != nil || len(selector) == 0 {
			return unresolvedCommand("Invalid script selector: " + arg)
		}
		if strings.ContainsAny(selector[0], "*?[") {
			if !scriptSelectorGlob.MatchString(selector[0]) {
				return unresolvedCommand("Unsupported script selector pattern: " + selector[0])
			}
			paths, err := filepath.Glob(filepath.Join(directory, selector[0]))
			if err != nil || len(paths) > 0 {
				return unresolvedCommand("Script selector also matches filesystem paths; quote or review " + selector[0])
			}
		}
		matched := false
		for _, name := range names {
			swap := strings.NewReplacer(":", "/", "/", ":")
			match, err := path.Match(swap.Replace(selector[0]), swap.Replace(name))
			if err != nil {
				return unresolvedCommand("Invalid script pattern: " + selector[0])
			}
			if match {
				matched = true
				command := shellquote.Join(append([]string{"npm", "run", name}, selector[1:]...)...)
				if len(seen[command]) == 0 || seen[command][selector[0]] {
					commands = append(commands, command)
				}
				if seen[command] == nil {
					seen[command] = map[string]bool{}
				}
				seen[command][selector[0]] = true
			}
		}
		if !matched {
			return unresolvedCommand("No package scripts match " + selector[0])
		}
	}
	if !parallel {
		return resolveJestSequence(root, directory, strings.Join(commands, " && "), stack, remaining, framework)
	}
	result := commandResolution{}
	for _, command := range commands {
		branch := resolveJestSequence(root, directory, command, stack, remaining, framework)
		result.Tests = append(result.Tests, branch.Tests...)
		result.Builds = append(result.Builds, branch.Builds...)
		result.Setup = append(result.Setup, branch.Setup...)
		result.Matched = result.Matched || branch.Matched
		result.OtherFramework = result.OtherFramework || branch.OtherFramework
		result.Reason = joinDiagnostic(result.Reason, branch.Reason)
		result.ReviewReason = joinDiagnostic(result.ReviewReason, branch.ReviewReason)
	}
	result.SingleJest = len(result.Tests) == 1 && result.Reason == "" && !result.OtherFramework
	return result
}

// JSON object order is the npm-run-all task order. Maps would reorder serial builds.
func orderedScriptNames(data []byte) []string {
	var pkg struct{ Scripts json.RawMessage }
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(pkg.Scripts))
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	var names []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil
		}
		var value any
		if decoder.Decode(&value) != nil {
			return nil
		}
		name, ok := key.(string)
		if !ok {
			return nil
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}
