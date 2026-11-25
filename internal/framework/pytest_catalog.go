// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/kballard/go-shellquote"
)

// PytestCommandArguments records an invocation and its inherited options.
func PytestCommandArguments(args []string, addopts string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("describe requires one pytest invocation")
	}
	insert := 1
	if filepath.Base(args[0]) != "pytest" {
		if len(args) < 3 || (filepath.Base(args[0]) != "python" && filepath.Base(args[0]) != "python3") || args[1] != "-m" || args[2] != "pytest" {
			return nil, fmt.Errorf("describe requires one pytest or python -m pytest invocation")
		}
		insert = 3
	}
	for _, arg := range args {
		if slices.Contains([]string{"&&", "||", ";", "|", ">", "<"}, arg) {
			return nil, fmt.Errorf("describe requires one pytest invocation")
		}
	}
	options, err := shellquote.Split(addopts)
	if err != nil {
		return nil, fmt.Errorf("parse PYTEST_ADDOPTS: %w", err)
	}
	argv := slices.Clone(args[:insert])
	argv = append(argv, options...)
	return append(argv, args[insert:]...), nil
}

// CandidateFiles discovers files under explicit roots without collecting tests.
func (p *PyTest) CandidateFiles(ctx context.Context, roots []string) ([]string, []string, error) {
	var normalizedRoots []string
	for _, root := range roots {
		if root == "" || !filepath.IsLocal(root) {
			return nil, nil, fmt.Errorf("test roots must be directories inside the repository")
		}
		root = filepath.Clean(root)
		info, err := os.Stat(root)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("test root %q is not a directory", root)
		}
		normalizedRoots = append(normalizedRoots, filepath.ToSlash(root))
	}
	matcher, err := utils.NewPathMatcher(p.TestPatternForRoots(normalizedRoots))
	if err != nil {
		return nil, nil, err
	}
	var files []string
	for _, root := range normalizedRoots {
		discovered, err := p.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: filepath.ToSlash(filepath.Join(root, "**", "*.py"))})
		if err != nil {
			return nil, nil, err
		}
		for _, file := range discovered {
			if matcher.Match(file) {
				files = append(files, file)
			}
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("the candidate test file inventory is empty")
	}
	return files, normalizedRoots, nil
}
