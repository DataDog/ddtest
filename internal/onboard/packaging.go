// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kballard/go-shellquote"
)

// Recognize only one literal jq filter with JSON input/output paths. Do not
// relax the general shell parser for pipelines, expansion or chained commands.
var jsonRedirect = regexp.MustCompile("^jq[ \\t]+(?:'[^']*'|\"[^\"$`]*\")[ \\t]+([a-zA-Z0-9_./-]+\\.json)[ \\t]*>[ \\t]*([a-zA-Z0-9_./-]+\\.json)[ \\t]*$")

func jsonPackagingStep(command string) bool {
	match := jsonRedirect.FindStringSubmatch(command)
	return len(match) == 3 && filepath.IsLocal(match[1]) && filepath.IsLocal(match[2])
}

func resolveNpmPublish(directory string, words []string) commandResolution {
	if len(words) > 3 || (len(words) == 3 && (strings.HasPrefix(words[2], "-") || !filepath.IsLocal(words[2]))) {
		return unresolvedCommand("Dynamic or unsupported npm publish target requires review: " + shellquote.Join(words...))
	}
	targets := []string{directory}
	if len(words) == 3 && words[2] != "." {
		targets = append(targets, filepath.Join(directory, words[2]))
	}
	for _, target := range targets {
		data, err := os.ReadFile(filepath.Join(target, "package.json"))
		if os.IsNotExist(err) {
			continue // A generated publication directory is reviewed separately.
		}
		if err != nil {
			return unresolvedCommand("Cannot inspect npm publish lifecycle scripts: " + err.Error())
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return unresolvedCommand("Cannot inspect npm publish manifest: " + err.Error())
		}
		for _, hook := range []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"} {
			if manifest.Scripts[hook] != "" {
				return unresolvedCommand("npm publish lifecycle script " + hook + " in " + target + " requires review; it may run tests")
			}
		}
	}
	return commandResolution{ReviewReason: "Package publication is outside Jest validation; review separately, including lifecycle scripts in generated package manifests: " + shellquote.Join(words...)}
}
