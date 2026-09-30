package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Treat a workflow input as release data only in this complete command form.
// Never discard substitutions or trailing shell commands that might run tests.
var releaseVersionInput = regexp.MustCompile(`^(npm|pnpm|yarn)[ \t]+version[ \t]+\$\{\{[ \t]*(github\.event\.inputs|inputs)\.[a-zA-Z0-9_-]+[ \t]*\}\}[ \t]*$`)

func resolveReleaseInput(directory, command, framework string) (commandResolution, bool) {
	if !releaseVersionInput.MatchString(strings.TrimSpace(command)) {
		return commandResolution{}, false
	}
	result := reviewReleaseLifecycle(directory, command, framework, []string{"preversion", "version", "postversion"})
	if result.Reason == "" {
		result.Review, result.Reason = true, result.ReviewReason
	}
	return result, true
}

func gitReleaseOperation(words []string) bool {
	if len(words) >= 3 && words[1] == "push" {
		return true
	}
	if len(words) < 4 || words[1] != "config" {
		return false
	}
	args := words[2:]
	if args[0] == "--global" || args[0] == "--local" {
		args = args[1:]
	}
	return len(args) == 2 && slices.Contains([]string{"user.name", "user.email"}, args[0])
}

func reviewReleaseLifecycle(directory, command, framework string, hooks []string) commandResolution {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return unresolvedCommand("Cannot inspect release lifecycle scripts: " + err.Error())
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return unresolvedCommand("Cannot inspect release lifecycle scripts: " + err.Error())
	}
	for _, hook := range hooks {
		// A recognized publication wrapper is still reviewed separately; it is
		// not an opaque test hook. Other scripts, including chains, stay blocked.
		if hook == "publish" && strings.TrimSpace(manifest.Scripts[hook]) == "clean-publish" {
			continue
		}
		if manifest.Scripts[hook] != "" {
			return unresolvedCommand("Release lifecycle script " + hook + " requires review alongside " + command + "; it may run tests")
		}
	}
	return commandResolution{ReviewReason: "Release operation is outside " + framework + " validation; review separately, including publication configuration and generated package hooks: " + command}
}
