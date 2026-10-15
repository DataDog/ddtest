package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

// Cypress adds the spec itself to coverage. That alone, or coverage naming a
// temporary served copy, cannot prove coverage of repository application code.
func cypressSourceCoverage(root string, facts intake.Facts, native []jestTest) *coverageSummary {
	result := &coverageSummary{Status: "not observed", TotalTests: facts.TestCount, Level: facts.CoverageLevel}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		result.Status, result.Reason = "invalid", "Cannot resolve the repository for application coverage."
		return result
	}
	specs := map[string]bool{}
	for _, test := range native {
		if file, ok := cypressCoverageFile(root, test.File); ok {
			specs[file] = true
		}
	}
	for _, test := range facts.Tests {
		if file, ok := cypressCoverageFile(root, test.SourceFile); ok {
			specs[file] = true
		}
	}
	// Validate each distinct filename once, even when thousands of tests cover it.
	files := map[string]int{}
	for _, test := range facts.Tests {
		covered := false
		for _, name := range test.CoveredFiles {
			kind, known := files[name]
			if !known {
				file, valid := cypressCoverageFile(root, name)
				switch {
				case !valid:
					kind = -1
					result.InvalidCount++
					if len(result.InvalidFiles) < 5 {
						result.InvalidFiles = append(result.InvalidFiles, reportText(name))
					}
				case specs[file] || strings.HasPrefix(file, "node_modules/") || strings.Contains(file, "/node_modules/"):
					kind = 0
				default:
					kind = 1
				}
				files[name] = kind
			}
			covered = covered || kind == 1
		}
		if covered {
			result.CoveredTests++
		}
	}
	switch {
	case result.InvalidCount > 0:
		result.Status = "invalid"
		result.Reason = fmt.Sprintf("Coverage names %d missing or outside-repository files, for example %q. Pass the original repository source filename to Istanbul, not the temporary served-copy filename.", result.InvalidCount, result.InvalidFiles[0])
	case result.CoveredTests > 0:
		result.Status = "observed"
	default:
		result.Reason = "No repository application source was covered; spec-file coverage alone is insufficient. Enable application Istanbul instrumentation in the selected local command and CI startup/build."
	}
	return result
}

func cypressCoverageFile(root, file string) (string, bool) {
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
