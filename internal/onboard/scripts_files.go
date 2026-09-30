// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path/filepath"
	"strings"
)

// These literal file operations are not test entry points. Flags that can load
// programs and sed's command-execution forms remain unresolved. Never execute IO.
func literalFileOperation(words []string) bool {
	localPaths := func(paths []string) bool {
		if len(paths) == 0 {
			return false
		}
		for _, path := range paths {
			if !filepath.IsLocal(path) || strings.HasPrefix(path, "-") || strings.ContainsAny(path, "*?[]$`\n:") {
				return false
			}
		}
		return true
	}
	switch words[0] {
	case "rimraf", "rm":
		return localPaths(words[1:])
	case "rsync":
		return len(words) == 4 && words[1] == "-a" && localPaths(words[2:])
	case "sed":
		args := words[1:]
		if len(args) > 0 && strings.HasPrefix(args[0], "-i") {
			args = args[1:]
		}
		if len(args) != 2 || !localPaths(args[1:]) {
			return false
		}
		return literalSedSubstitution(args[0])
	}
	return false
}

// Accept a single substitution with no execution flag or following command.
func literalSedSubstitution(script string) bool {
	if len(script) < 4 || len(script) > 4096 || script[0] != 's' || strings.ContainsAny(script, "\n\r") {
		return false
	}
	delimiter := script[1]
	if delimiter != '/' && delimiter != '#' && delimiter != '|' {
		return false
	}
	separators := 0
	for i := 2; i < len(script); i++ {
		if script[i] == '\\' {
			i++
			continue
		}
		if script[i] == delimiter {
			separators++
			if separators == 2 {
				flags := script[i+1:]
				return flags == "" || flags == "g"
			}
		}
	}
	return false
}
