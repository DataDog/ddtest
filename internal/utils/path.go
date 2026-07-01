// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DataDog/ddtest/internal/git"
	"github.com/bmatcuk/doublestar/v4"
)

var cwdSubdirPrefix = sync.OnceValue(computeCwdSubdirPrefix)

func CwdSubdirPrefix() string {
	return cwdSubdirPrefix()
}

// ExpandPath expands a file path that starts with '~' to the user's home directory.
// If the path does not start with '~', it is returned unchanged.
func ExpandPath(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}

	// If the second character is not '/' or '\', return the path unchanged.
	if len(path) > 1 && path[1] != '/' && path[1] != '\\' {
		return path
	}

	homeFolder := getHomeDir()
	if len(homeFolder) > 0 {
		return filepath.Join(homeFolder, path[1:])
	}

	return path
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

	normalizedPath := NormalizePath(path)
	prefixWithSlash := NormalizePath(subdirPrefix) + "/"
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

type PathMatcher struct {
	pattern string
}

func NewPathMatcher(pattern string) (PathMatcher, error) {
	normalized := NormalizePattern(pattern)
	return newPathMatcher(normalized, pattern)
}

func NewNormalizedPathMatcher(normalizedPattern string) (PathMatcher, error) {
	return newPathMatcher(normalizedPattern, normalizedPattern)
}

func newPathMatcher(normalizedPattern string, originalPattern string) (PathMatcher, error) {
	if normalizedPattern == "" {
		return PathMatcher{}, nil
	}
	if !doublestar.ValidatePattern(normalizedPattern) {
		return PathMatcher{}, fmt.Errorf("invalid path pattern %q: %w", originalPattern, doublestar.ErrBadPattern)
	}
	return PathMatcher{pattern: normalizedPattern}, nil
}

func (m PathMatcher) Empty() bool {
	return m.pattern == ""
}

func (m PathMatcher) Match(path string) bool {
	return m.MatchNormalizedPath(NormalizePath(path))
}

func (m PathMatcher) MatchNormalizedPath(normalizedPath string) bool {
	if m.pattern == "" || normalizedPath == "" {
		return false
	}
	return doublestar.MatchUnvalidated(m.pattern, normalizedPath)
}

func trimLeadingCurrentDir(path string) string {
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	return path
}
