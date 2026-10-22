// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func cucumberSourceCoverage(root string, facts intake.Facts, native []jestTest, excluded ...string) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	supportFiles := map[string]bool{}
	for _, test := range native {
		excluded = append(excluded, test.File)
	}
	for _, file := range excluded {
		if local, ok := cucumberCoverageFile(root, file); ok {
			supportFiles[local] = true
		}
	}
	// Cucumber reports suite coverage. Associate it with the suite's scenarios
	// without claiming independent per-scenario precision.
	facts.Tests = slices.Clone(facts.Tests)
	bySuite := map[[2]string][]string{}
	for _, suite := range facts.SuiteCoverages {
		bySuite[[2]string{suite.Module, suite.Suite}] = suite.Files
	}
	for i, test := range facts.Tests {
		files := slices.Clone(test.CoveredFiles)
		files = append(files, bySuite[[2]string{test.Module, test.Suite}]...)
		var application []string
		for _, file := range files {
			local, valid := cucumberCoverageFile(root, file)
			if valid && (supportFiles[local] || strings.Contains(local, "/.probes/") || strings.HasSuffix(local, ".feature") || strings.HasSuffix(local, ".feature.md")) {
				continue
			}
			application = append(application, file)
		}
		facts.Tests[i].CoveredFiles = application
	}
	result := &coverageSummary{Status: "not observed", TotalTests: facts.TestCount, Level: facts.CoverageLevel}
	for _, test := range facts.Tests {
		covered := false
		for _, file := range test.CoveredFiles {
			local, valid := cucumberCoverageFile(root, file)
			if !valid {
				result.Status = "invalid"
				continue
			}
			if !strings.HasPrefix(local, "node_modules/") && !strings.Contains(local, "/node_modules/") {
				covered = true
			}
		}
		if covered {
			result.CoveredTests++
		}
	}
	if result.CoveredTests > 0 && result.Status != "invalid" {
		result.Status = "observed"
	}
	if result.TotalTests > 0 {
		result.Ratio = float64(result.CoveredTests) / float64(result.TotalTests)
	}
	switch result.Status {
	case "observed":
		result.Reason = "Istanbul coverage names existing repository application files. Cucumber suite coverage is associated with its scenarios; it is not independent per-scenario coverage."
	case "invalid":
		result.Reason = "Cucumber source coverage names missing or outside-repository files; inspect the bounded invalid-file examples."
	default:
		result.Reason = "No application source coverage observed. Install and configure NYC for Cucumber; native ESM also needs Istanbul instrumentation such as the project's @istanbuljs/esm-loader-hook. Config, support and feature files alone are insufficient."
	}
	return result
}

func cucumberCoverageFile(root, file string) (string, bool) {
	if file == "" {
		return "", false
	}
	file = filepath.FromSlash(strings.ReplaceAll(file, "\\", "/"))
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return filepath.ToSlash(relative), true
}
