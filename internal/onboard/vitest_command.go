// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"github.com/kballard/go-shellquote"
	"os"
	"path/filepath"
	"strings"
)

// VitestValidationCommand selects the same statically resolved CI/script scope
// used by --all. Setup is never silently discarded when extracting a runner.
func VitestValidationCommand(root, command string) (TestCommand, error) {
	var scope ValidationScope
	var err error
	if command == "" {
		scope, err = DiscoverValidationScope(root, "vitest")
	}
	return VitestValidationCommandInScope(root, command, scope, err)
}

// VitestValidationCommandInScope selects from an already discovered scope.
func VitestValidationCommandInScope(root, command string, scope ValidationScope, scopeErr error) (TestCommand, error) {
	selectOne := func(commands []TestCommand) (TestCommand, error) {
		if len(commands) != 1 {
			return TestCommand{}, fmt.Errorf("select one Vitest command with --command, or use --all for every CI configuration")
		}
		entry := commands[0]
		if entry.Directory != "." || len(entry.Prerequisites) > 0 || len(entry.UnvalidatedSetup) > 0 {
			return TestCommand{}, fmt.Errorf("vitest setup or package working directory requires --all, or an explicit command after preparing its environment")
		}
		return entry, nil
	}
	if command != "" {
		words, err := shellquote.Split(command)
		if err == nil && len(words) > 1 && strings.TrimSuffix(filepath.Base(words[0]), ".exe") == "node" && filepath.Base(words[1]) == "vitest.mjs" {
			return TestCommand{Directory: ".", Command: command}, nil
		}
		return selectOne(ValidationCommands(root, command, "vitest"))
	}
	if scopeErr != nil {
		return TestCommand{}, scopeErr
	}
	if len(scope.Commands) > 0 {
		return selectOne(scope.Commands)
	}
	if len(scope.Unresolved) > 0 {
		return TestCommand{}, fmt.Errorf("vitest CI command needs review: %s; use --command after reviewing setup", strings.Join(scope.Unresolved, "; "))
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return TestCommand{}, err
	}
	var manifest struct {
		Scripts        map[string]string
		PackageManager string
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return TestCommand{}, err
	}
	manager, _, _ := strings.Cut(manifest.PackageManager, "@")
	if manager == "" {
		manager = "npm"
	}
	for _, name := range []string{"test:ci", "test"} {
		if manifest.Scripts[name] != "" {
			return selectOne(ValidationCommands(root, manager+" run "+name, "vitest"))
		}
	}
	return TestCommand{Directory: ".", Command: "vitest run"}, nil
}
