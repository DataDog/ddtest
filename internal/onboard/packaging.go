// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"mvdan.cc/sh/v3/syntax"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// Classify a single literal data-tool invocation with a local output redirect.
// Never accept substitutions, command chains, arbitrary wrappers or external paths.
func literalDataRedirect(command string) bool {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return false
	}
	stmt := file.Stmts[0]
	if stmt.Background || stmt.Negated || len(stmt.Redirs) != 1 {
		return false
	}
	redirect := stmt.Redirs[0]
	if redirect.Op != syntax.RdrOut || redirect.N != nil {
		return false
	}
	output, err := literalShellWord(redirect.Word)
	if err != nil || !filepath.IsLocal(output) {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) != 3 {
		return false
	}
	var words []string
	for _, word := range call.Args {
		value, err := literalShellWord(word)
		if err != nil {
			return false
		}
		words = append(words, value)
	}
	return words[0] == "jq" && !strings.HasPrefix(words[1], "-") && filepath.IsLocal(words[2])
}

func resolveNpmPublish(directory string, words []string, framework string) commandResolution {
	command := shellquote.Join(words...)
	if words[0] == "pnpm" {
		words = slices.DeleteFunc(slices.Clone(words), func(word string) bool { return word == "--no-git-checks" })
	}
	if len(words) > 3 || (len(words) == 3 && (strings.HasPrefix(words[2], "-") || !filepath.IsLocal(words[2]))) {
		return unresolvedCommand("Dynamic or unsupported package publish target requires review: " + shellquote.Join(words...))
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
			return unresolvedCommand("Cannot inspect package publish lifecycle scripts: " + err.Error())
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return unresolvedCommand("Cannot inspect package publish manifest: " + err.Error())
		}
		hooks := []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"}
		if words[0] == "pnpm" {
			hooks = append(hooks, "prepublish")
		}
		for _, hook := range hooks {
			if manifest.Scripts[hook] != "" {
				return unresolvedCommand("Package publish lifecycle script " + hook + " in " + repositoryPath(directory, target) + " requires review; it may run tests")
			}
		}
	}
	return commandResolution{ReviewReason: "Package publication is outside " + framework + " validation; review separately, including lifecycle scripts in generated package manifests: " + command}
}
