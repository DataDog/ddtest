// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func cucumberSourceCoverage(root string, facts intake.Facts, native []jestTest, support *cucumberSupport) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	// Config and feature files are ignored; support code is reported separately.
	isSupport, ignored := cucumberSupportClassifier(root, support)
	for _, test := range native {
		if local, ok := cucumberCoverageFile(root, test.File); ok {
			ignored[local] = true
		}
	}
	var supportCovered []string
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
			if valid && (ignored[local] || strings.HasSuffix(local, ".feature") || strings.HasSuffix(local, ".feature.md")) {
				continue
			}
			if valid && isSupport(local) {
				if !slices.Contains(supportCovered, local) {
					supportCovered = append(supportCovered, local)
				}
				continue
			}
			application = append(application, file)
		}
		facts.Tests[i].CoveredFiles = application
	}
	invalidExample := ""
	result := &coverageSummary{Status: "not observed", TotalTests: facts.TestCount, Level: facts.CoverageLevel}
	for _, test := range facts.Tests {
		covered := false
		for _, file := range test.CoveredFiles {
			local, valid := cucumberCoverageFile(root, file)
			if !valid {
				result.Status = "invalid"
				if invalidExample == "" {
					invalidExample = reportText(file)
				}
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
		result.Reason = fmt.Sprintf("Cucumber source coverage names missing or outside-repository files, for example %q. Preserve repository-relative Istanbul source paths.", invalidExample)
	case "not observed":
		if len(supportCovered) == 0 {
			result.Reason = cucumberMissingCoverageReason
			break
		}
		slices.Sort(supportCovered)
		if len(supportCovered) > 5 {
			supportCovered = append(supportCovered[:5], "…")
		}
		result.Reason = fmt.Sprintf("Coverage was observed only in Cucumber support code (%s). Files that Cucumber loads as support code, and helpers next to them, don't count as application coverage. Keep application code outside the support paths and import it from the steps.", strings.Join(supportCovered, ", "))
	default:
		result.Reason = cucumberMissingCoverageReason
	}
	return result
}

const cucumberMissingCoverageReason = "No application source coverage observed. Install and configure NYC for Cucumber; native ESM also needs Istanbul instrumentation such as the project's @istanbuljs/esm-loader-hook. Config, support and feature files alone are insufficient."

// cucumberSupportClassifier reports which covered files are Cucumber support
// code: every file Cucumber loaded as support, and helpers in the folders
// holding them. A folder that is the repository or a package root is never a
// support folder, so application code next to a root-level step file counts.
func cucumberSupportClassifier(root string, support *cucumberSupport) (func(string) bool, map[string]bool) {
	files, directories, configFiles := map[string]bool{}, map[string]bool{}, map[string]bool{}
	if support == nil {
		return func(string) bool { return false }, configFiles
	}
	for _, file := range support.ConfigFiles {
		if local, ok := cucumberCoverageFile(root, file); ok {
			configFiles[local] = true
		}
	}
	for _, file := range slices.Concat(support.RequirePaths, support.ImportPaths) {
		local, ok := cucumberCoverageFile(root, file)
		if !ok {
			continue
		}
		files[local] = true
		directory := path.Dir(local)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(directory), "package.json")); directory != "." && err != nil {
			directories[directory] = true
		}
	}
	return func(local string) bool {
		if files[local] {
			return true
		}
		for directory := range directories {
			if strings.HasPrefix(local, directory+"/") {
				return true
			}
		}
		return false
	}, configFiles
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
