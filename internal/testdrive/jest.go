// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"path/filepath"

	"slices"

	"github.com/DataDog/ddtest/internal/framework"
)

func appendJestArgs(command string, args []string, additions ...string) []string {
	args = slices.Clone(args)
	if filepath.Base(command) == "npm" && !slices.Contains(args, "--") {
		args = append(args, "--")
	}
	if index := jestSeparator(command, args); index >= 0 {
		return slices.Insert(args, index, additions...)
	}
	return append(args, additions...)
}

func jestSeparator(command string, args []string) int {
	index := framework.FrameworkSeparator(command, args, "jest")
	// npm consumes its first separator when forwarding script arguments.
	if filepath.Base(command) == "npm" && index >= 0 && index == slices.Index(args, "--") {
		if next := slices.Index(args[index+1:], "--"); next >= 0 {
			return index + 1 + next
		}
		return -1
	}
	return index
}
