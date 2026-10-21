// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
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
	for _, file := range excluded {
		if local, ok := cypressCoverageFile(root, file); ok {
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
			local, valid := cypressCoverageFile(root, file)
			if valid && (supportFiles[local] || strings.HasSuffix(local, ".feature") || strings.HasSuffix(local, ".feature.md")) {
				continue
			}
			application = append(application, file)
		}
		facts.Tests[i].CoveredFiles = application
	}
	result := cypressSourceCoverage(root, facts, native)
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
