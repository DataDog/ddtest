package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var mochaESMReference = regexp.MustCompile(`(?m)(?:^|[=:"',\[\]\s])(?:mjs|mts)(?:$|[:"',\[\]\s])|\.(?:mjs|mts)\b`)

// Static evidence only: never evaluate a Mocha configuration to inspect CI.
// A missing import is a blocker when literal inputs select ESM. Native local
// execution remains required even when static initialization checks pass.
func mochaESMInputs(directory string, args []string) bool {
	selectsESM := func(value string) bool { return mochaESMReference.MatchString(value) }
	configs := mochaConfigFiles(args)
	for _, arg := range args {
		if selectsESM(arg) {
			return true
		}
		// A .js input is ESM when its own package, possibly nested, says so.
		if !strings.HasPrefix(arg, "-") && strings.HasSuffix(arg, ".js") && nearestPackageType(directory, arg) == "module" {
			return true
		}
	}
	var manifest struct {
		Type  string          `json:"type"`
		Mocha json.RawMessage `json:"mocha"`
	}
	if data, err := os.ReadFile(filepath.Join(directory, "package.json")); err == nil && json.Unmarshal(data, &manifest) == nil {
		if manifest.Type == "module" {
			return true
		}
		if selectsESM(string(manifest.Mocha)) {
			return true
		}
	}
	for _, config := range configs {
		if !filepath.IsLocal(config) {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(directory, config)); err == nil && selectsESM(string(data)) {
			return true
		}
	}
	return false
}

// Mocha reads an explicit --config instead of its default files, and none
// with --no-config.
func mochaConfigFiles(args []string) []string {
	if slices.Contains(args, "--no-config") {
		return nil
	}
	var explicit []string
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			explicit = append(explicit, args[i+1])
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			explicit = append(explicit, value)
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	return []string{".mocharc.js", ".mocharc.cjs", ".mocharc.mjs", ".mocharc.json", ".mocharc.jsonc", ".mocharc.yaml", ".mocharc.yml"}
}

// The package type that applies to input, looking up from its static
// directory prefix to the command's working directory.
func nearestPackageType(directory, input string) string {
	prefix := input
	if index := strings.IndexAny(prefix, "*?[{"); index >= 0 {
		prefix = prefix[:index]
	}
	if !filepath.IsLocal(filepath.Clean(prefix)) && filepath.Clean(prefix) != "." {
		return ""
	}
	// The "_" keeps a trailing separator in a glob prefix ("unit/**") meaningful.
	for current := filepath.Dir(filepath.Join(directory, prefix+"_")); ; current = filepath.Dir(current) {
		if relative, err := filepath.Rel(directory, current); err != nil || !filepath.IsLocal(relative) {
			return ""
		}
		if data, err := os.ReadFile(filepath.Join(current, "package.json")); err == nil {
			var manifest struct{ Type string }
			if json.Unmarshal(data, &manifest) == nil {
				return manifest.Type
			}
			return ""
		}
	}
}
