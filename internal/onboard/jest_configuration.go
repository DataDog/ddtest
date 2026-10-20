// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"github.com/kballard/go-shellquote"
	"path/filepath"
)

// JestConfigurationCommand isolates the one statically resolved Jest invocation.
// Check-only must never execute another runner, package lifecycle or shell setup.
// A Node entry point also avoids POSIX forwarding shells and Windows .cmd files.
func JestConfigurationCommand(root, command string) (TestCommand, error) {
	if command == "" {
		var err error
		command, err = jestValidationCommand(root, false)
		if err != nil {
			return TestCommand{}, err
		}
		if command == "" {
			command = "jest"
		}
	}
	resolution := resolveJestCommand(root, command, nil)
	if !resolution.Matched || len(resolution.Tests) != 1 || resolution.OtherFramework || resolution.Reason != "" {
		return TestCommand{}, fmt.Errorf("cannot safely inspect Jest configuration through %q; use --command with the actual Node/Jest executable and original options; other tests and unresolved setup cannot run during check-only", command)
	}
	entry := resolution.Tests[0]
	if filepath.Clean(entry.Directory) != "." || len(entry.UnvalidatedSetup) > 0 {
		return TestCommand{}, fmt.Errorf("configuration check requires a different directory or unresolved setup; run check-only from the package directory with its actual Jest command")
	}
	words, err := shellquote.Split(entry.Command)
	if err != nil {
		return TestCommand{}, err
	}
	if words[0] == "jest" {
		words = append([]string{"node", filepath.Join(root, "node_modules", "jest", "bin", "jest.js")}, words[1:]...)
	}
	entry.Command = shellquote.Join(words...)
	return entry, nil
}
