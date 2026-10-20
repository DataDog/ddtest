package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var mochaESMReference = regexp.MustCompile(`(?m)(?:^|[=:"',\[\]\s])(?:mjs|mts)(?:$|[:"',\[\]\s])|\.(?:mjs|mts)\b`)

// Static evidence only: never evaluate a Mocha configuration to inspect CI.
// A missing import is a blocker when literal inputs select ESM. Native local
// execution remains required even when static initialization checks pass.
func mochaESMInputs(directory string, args []string) bool {
	selectsESM := func(value string) bool { return mochaESMReference.MatchString(value) }
	configs := []string{".mocharc.js", ".mocharc.cjs", ".mocharc.mjs", ".mocharc.json", ".mocharc.jsonc", ".mocharc.yaml", ".mocharc.yml"}
	for i, arg := range args {
		if selectsESM(arg) {
			return true
		}
		if arg == "--config" && i+1 < len(args) {
			configs = append(configs, args[i+1])
		}
		if strings.HasPrefix(arg, "--config=") {
			configs = append(configs, strings.TrimPrefix(arg, "--config="))
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
