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

const cypressMissingCoverageReason = "No application source coverage observed, so Test Impact Analysis cannot be validated. The tested page must expose Istanbul's window.__coverage__: instrument the application build served to Cypress with babel-plugin-istanbul (Babel) or vite-plugin-istanbul (Vite), limited to test builds. That changes the application build and its dependencies, so propose the exact change to the user and apply it only with their approval. Specs, support files and dependencies alone are insufficient."

// cypressTestCode locates the code Cypress itself loads: the effective
// project, whose cypress/ folder holds test code, and the configured support
// file. Paths are absolute or relative to the checkout.
type cypressTestCode struct {
	Project     string
	SupportFile string
	// Source is the original checkout when Cypress ran in an isolated copy.
	// A prebuilt bundle keeps Istanbul's absolute paths into it.
	Source string
}

// cypressSourceCoverage checks that the real suite's coverage, read by the
// tracer from the tested page's window.__coverage__, names application files.
// Files in the Cypress folder, support files and their helpers, specs and
// dependencies are not application code.
func cypressSourceCoverage(root string, code cypressTestCode, facts intake.Facts, native []jestTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	project := filepath.ToSlash(code.Project)
	if filepath.IsAbs(code.Project) {
		project = ""
		if resolved, err := filepath.EvalSymlinks(code.Project); err == nil {
			if relative, err := filepath.Rel(root, resolved); err == nil && filepath.IsLocal(relative) {
				project = filepath.ToSlash(relative)
			}
		}
	}
	isSupport, _ := cucumberSupportClassifier(root, &cucumberSupport{RequirePaths: []string{code.SupportFile}})
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
			local, valid := cucumberCoverageFile(root, rebaseCoverageFile(code.Source, root, file))
			switch {
			case !valid:
				if invalidExample == "" {
					invalidExample = reportText(file)
				}
			case specs[local] || isSupport(local) || strings.HasPrefix(local, testFolder) || strings.HasPrefix(local, "node_modules/") || strings.Contains(local, "/node_modules/"):
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
		if edit := cypressCoverageEdit(root); edit != nil {
			result.Edit = edit
			result.Reason = cypressCoverageEditReason(edit)
		}
	}
	return result
}

// rebaseCoverageFile maps an absolute path beneath the original checkout to
// the same file in the isolated copy.
func rebaseCoverageFile(source, root, file string) string {
	if source == "" {
		return file
	}
	native := filepath.FromSlash(strings.ReplaceAll(file, "\\", "/"))
	if !filepath.IsAbs(native) {
		return file
	}
	bases := []string{source}
	if canonical, err := filepath.EvalSymlinks(source); err == nil {
		bases = append(bases, canonical)
	}
	for _, base := range bases {
		if relative, err := filepath.Rel(base, native); err == nil && filepath.IsLocal(relative) {
			return filepath.Join(root, relative)
		}
	}
	return file
}
