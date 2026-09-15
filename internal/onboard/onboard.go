// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
)

const githubAction = "datadog/test-visibility-github-action"

//go:embed instructions/javascript-jest-github.md
var javascriptJestGitHubInstructions string

// Run detects the first supported onboarding path and prints its instructions.
func Run(repositoryRoot string, output io.Writer) error {
	detected, err := platform.NewJavaScript().Detect(repositoryRoot)
	if err != nil {
		return err
	}
	if !detected {
		return fmt.Errorf("onboard currently supports JavaScript projects with a package.json")
	}

	detected, err = framework.NewJest().Detect(repositoryRoot)
	if err != nil {
		return err
	}
	if !detected {
		return fmt.Errorf("onboard could not find a Jest test script in package.json")
	}

	workflows, configured, err := findJestWorkflows(repositoryRoot)
	if err != nil {
		return err
	}
	if len(workflows) == 0 {
		return fmt.Errorf("onboard could not find a GitHub Actions workflow that runs Jest")
	}

	_, _ = fmt.Fprintln(output, "DDTest found JavaScript, Jest, and GitHub Actions.")
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Test workflow(s):")
	for _, workflow := range workflows {
		_, _ = fmt.Fprintf(output, "  - %s\n", workflow)
	}

	if len(configured) == len(workflows) {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Datadog Test Optimization already appears in every detected test workflow.")
		_, _ = fmt.Fprintln(output, "Run `ddtest testdrive` to check the setup locally.")
		return nil
	}

	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, strings.TrimSpace(javascriptJestGitHubInstructions))
	return nil
}

func findJestWorkflows(repositoryRoot string) ([]string, []string, error) {
	patterns := []string{
		filepath.Join(repositoryRoot, ".github", "workflows", "*.yml"),
		filepath.Join(repositoryRoot, ".github", "workflows", "*.yaml"),
	}

	var workflows []string
	var configured []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
		}
		for _, path := range matches {
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", path, err)
			}
			text := strings.ToLower(string(contents))
			if !looksLikeJestWorkflow(text) {
				continue
			}

			relativePath, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return nil, nil, fmt.Errorf("make workflow path relative: %w", err)
			}
			relativePath = filepath.ToSlash(relativePath)
			workflows = append(workflows, relativePath)
			if strings.Contains(text, githubAction) {
				configured = append(configured, relativePath)
			}
		}
	}

	sort.Strings(workflows)
	sort.Strings(configured)
	return workflows, configured, nil
}

func looksLikeJestWorkflow(workflow string) bool {
	for _, marker := range []string{"npm test", "npm run test", "npx jest", "yarn test", "pnpm test", "bun test", githubAction} {
		if strings.Contains(workflow, marker) {
			return true
		}
	}
	return false
}
