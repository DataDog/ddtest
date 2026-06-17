// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DataDog/ddtest/internal/git"
)

var cwdSubdirPrefix = sync.OnceValue(computeCwdSubdirPrefix)

func CwdSubdirPrefix() string {
	return cwdSubdirPrefix()
}

func computeCwdSubdirPrefix() string {
	gitRoot := git.GetSourceRoot()
	if gitRoot == "" {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	gitRootResolved, err := filepath.EvalSymlinks(gitRoot)
	if err != nil {
		gitRootResolved = gitRoot
	}
	cwdResolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		cwdResolved = cwd
	}

	rel, err := filepath.Rel(gitRootResolved, cwdResolved)
	if err != nil {
		return ""
	}
	if rel == "." {
		return ""
	}

	return filepath.ToSlash(rel)
}

// ResetCwdSubdirPrefixForTesting resets the process-level cwd subdirectory cache.
func ResetCwdSubdirPrefixForTesting() {
	cwdSubdirPrefix = sync.OnceValue(computeCwdSubdirPrefix)
}

func StripCwdSubdirPrefix(path string) string {
	return stripSubdirPrefix(path, CwdSubdirPrefix())
}

func stripSubdirPrefix(path string, subdirPrefix string) string {
	if path == "" || subdirPrefix == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}

	normalizedPath := filepath.ToSlash(path)
	prefixWithSlash := subdirPrefix + "/"
	if strings.HasPrefix(normalizedPath, prefixWithSlash) {
		stripped := strings.TrimPrefix(normalizedPath, prefixWithSlash)
		slog.Debug("Normalized test file path for subdirectory execution",
			"original", path, "normalized", stripped, "subdirPrefix", subdirPrefix)
		return stripped
	}

	return path
}

func NormalizePath(path string) string {
	if path == "" {
		return ""
	}
	normalized := filepath.ToSlash(filepath.Clean(path))
	if normalized == "." {
		return ""
	}
	return trimLeadingCurrentDir(normalized)
}

func NormalizePattern(pattern string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(pattern))
	return trimLeadingCurrentDir(normalized)
}

func trimLeadingCurrentDir(path string) string {
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	return path
}
