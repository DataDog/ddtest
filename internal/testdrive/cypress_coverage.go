// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const cypressMissingCoverageReason = "No application source coverage observed. Instrument the application served to Cypress with Istanbul (babel-plugin-istanbul or vite-plugin-istanbul) so the tested page exposes window.__coverage__. Specs, support files and dependencies alone are insufficient."

// cypressSourceCoverage checks that the real suite's coverage, read by the
// tracer from the tested page's window.__coverage__, names application files.
// Files in the Cypress folder, specs and dependencies are not application code.
func cypressSourceCoverage(root, project string, facts intake.Facts, native []jestTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	specs := map[string]bool{}
	for _, test := range native {
		if local, ok := cucumberCoverageFile(root, test.File); ok {
			specs[local] = true
		}
	}
	testFolder := path.Join(project, "cypress") + "/"
	bySuite := map[[2]string][]string{}
	for _, suite := range facts.SuiteCoverages {
		bySuite[[2]string{suite.Module, suite.Suite}] = suite.Files
	}
	result := &coverageSummary{Status: "not observed", TotalTests: facts.TestCount, Level: facts.CoverageLevel}
	invalidExample, testOnly := "", false
	for _, test := range facts.Tests {
		covered := false
		for _, file := range slices.Concat(test.CoveredFiles, bySuite[[2]string{test.Module, test.Suite}]) {
			local, valid := cucumberCoverageFile(root, file)
			switch {
			case !valid:
				if invalidExample == "" {
					invalidExample = reportText(file)
				}
			case specs[local] || strings.HasPrefix(local, testFolder) || strings.HasPrefix(local, "node_modules/") || strings.Contains(local, "/node_modules/"):
				testOnly = true
			default:
				covered = true
			}
		}
		if covered {
			result.CoveredTests++
		}
	}
	if result.TotalTests > 0 {
		result.Ratio = float64(result.CoveredTests) / float64(result.TotalTests)
	}
	switch {
	case result.CoveredTests > 0:
		result.Status = "observed"
		result.Reason = "Istanbul coverage from the tested page names existing repository application files."
	case invalidExample != "":
		result.Status = "invalid"
		result.Reason = fmt.Sprintf("Cypress coverage names only missing or outside-repository files, for example %q. Preserve repository-relative Istanbul source paths in the application build.", invalidExample)
	case testOnly:
		result.Reason = "Coverage named only Cypress specs, support code or dependencies. Instrument the application served to Cypress, not only the test bundle."
	default:
		result.Reason = cypressMissingCoverageReason
	}
	return result
}
