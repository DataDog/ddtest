// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"path"
	"strings"
)

// Package ownership follows directory rules rather than synthetic file names.
// Match inherited directory names, * within one segment and ** across segments.
// A terminal /* selects direct children; a trailing / also includes descendants.
func codeOwnerDirectoryPatternMatches(pattern, directory string) bool {
	if pattern == "" || pattern == "/" || strings.Contains(pattern, "***") {
		return false
	}
	directory = strings.Trim(directory, "/")
	rooted := strings.HasPrefix(pattern, "/")
	folder := strings.HasSuffix(pattern, "/")
	pattern = strings.Trim(pattern, "/")
	if pattern == "*" || pattern == "**" {
		return true
	}
	if !rooted && !strings.Contains(pattern, "/") {
		for {
			segment, remaining, more := strings.Cut(directory, "/")
			if matched, _ := path.Match(pattern, segment); segment != "" && matched {
				return true
			}
			if !more {
				return false
			}
			directory = remaining
		}
	}
	inherit := folder || path.Base(pattern) != "*"
	for {
		if matchCodeOwnerDirectoryPath(pattern, directory) {
			return true
		}
		if !inherit {
			return false
		}
		slash := strings.LastIndexByte(directory, '/')
		if slash < 0 {
			return false
		}
		directory = directory[:slash]
	}
}

func matchCodeOwnerDirectoryPath(pattern, directory string) bool {
	segment, remainingPattern, morePattern := strings.Cut(pattern, "/")
	if segment == "**" {
		if !morePattern {
			return true
		}
		for {
			if matchCodeOwnerDirectoryPath(remainingPattern, directory) {
				return true
			}
			_, remaining, more := strings.Cut(directory, "/")
			if !more {
				return matchCodeOwnerDirectoryPath(remainingPattern, "")
			}
			directory = remaining
		}
	}
	name, remainingDirectory, moreDirectory := strings.Cut(directory, "/")
	if matched, _ := path.Match(segment, name); name == "" || !matched {
		return false
	}
	if !morePattern {
		return !moreDirectory
	}
	return matchCodeOwnerDirectoryPath(remainingPattern, remainingDirectory)
}
