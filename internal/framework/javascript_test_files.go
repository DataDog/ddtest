package framework

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/utils"
)

func normalizeJavaScriptTestFiles(paths []string) []string {
	cwd, _ := os.Getwd()
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolvedCwd
	}

	testFiles := make([]string, 0, len(paths))
	for _, candidate := range paths {
		testFile := strings.TrimSpace(candidate)
		if testFile == "" {
			continue
		}

		if filepath.IsAbs(testFile) && cwd != "" {
			pathForRel := testFile
			if resolvedPath, err := filepath.EvalSymlinks(testFile); err == nil {
				pathForRel = resolvedPath
			}
			relativePath, err := filepath.Rel(cwd, pathForRel)
			if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
				continue
			}
			testFile = relativePath
		}

		normalized := utils.NormalizePath(testFile)
		if normalized == "" {
			continue
		}
		if _, err := os.Stat(normalized); err != nil {
			continue
		}
		testFiles = append(testFiles, normalized)
	}

	slices.Sort(testFiles)
	return slices.Compact(testFiles)
}

func filterJavaScriptTestFiles(testFiles []string, selectedFiles discovery.TestFileSet) ([]string, error) {
	if settings.GetTestsExcludePattern() != "" {
		selectedFiles.ExplicitFiles = nil
	}
	if settings.GetTestsLocation() == "" {
		selectedFiles.Pattern = ""
	}

	matcher, err := discovery.NewTestFileSetMatcher(selectedFiles, settings.GetTestsExcludePattern())
	if err != nil {
		return nil, err
	}

	filtered := make([]string, 0, len(testFiles))
	for _, testFile := range testFiles {
		normalized := utils.NormalizePath(testFile)
		if normalized != "" && matcher.MatchNormalizedPath(normalized) {
			filtered = append(filtered, normalized)
		}
	}

	slices.Sort(filtered)
	return slices.Compact(filtered), nil
}
