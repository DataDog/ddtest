// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

type lcovLineCoverage struct {
	executable bool
	covered    bool
}

// WriteLCOVReport writes an LCOV report from the current runtime coverage snapshot.
func WriteLCOVReport(w io.Writer) error {
	snapshot, err := RuntimeCoverageSnapshot()
	if err != nil {
		return err
	}
	return WriteLCOVReportFromProfile(snapshot.path, w)
}

// WriteLCOVReportFromProfile writes an LCOV report from a Go coverprofile.
func WriteLCOVReportFromProfile(profilePath string, w io.Writer) error {
	profile, err := parseOrderedCoverProfile(profilePath)
	if err != nil {
		return err
	}

	files := make(map[string]map[int]lcovLineCoverage)
	for _, profileLine := range profile.lines {
		if profileLine.block == nil {
			continue
		}
		fileName := lcovSourceFilePath(profileLine.fileName)
		if fileName == "" {
			log.Debug("civisibility.coverage_report: skipping coverage profile file without repository-relative path [file:%s]", profileLine.fileName)
			continue
		}
		lines := files[fileName]
		if lines == nil {
			lines = make(map[int]lcovLineCoverage)
			files[fileName] = lines
		}
		covered := profileLine.block.count > 0
		endLine := lcovEffectiveEndLine(profileLine.block)
		for line := profileLine.block.startLine; line <= endLine; line++ {
			lineCoverage := lines[line]
			lineCoverage.executable = true
			lineCoverage.covered = lineCoverage.covered || covered
			lines[line] = lineCoverage
		}
	}

	fileNames := make([]string, 0, len(files))
	for fileName, lines := range files {
		if len(lines) == 0 {
			continue
		}
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)

	for _, fileName := range fileNames {
		lines := files[fileName]
		lineNumbers := make([]int, 0, len(lines))
		for lineNumber, lineCoverage := range lines {
			if lineCoverage.executable {
				lineNumbers = append(lineNumbers, lineNumber)
			}
		}
		sort.Ints(lineNumbers)
		if len(lineNumbers) == 0 {
			continue
		}

		if _, err := fmt.Fprintf(w, "SF:%s\n", fileName); err != nil {
			return err
		}
		coveredLines := 0
		for _, lineNumber := range lineNumbers {
			covered := 0
			if lines[lineNumber].covered {
				covered = 1
				coveredLines++
			}
			if _, err := fmt.Fprintf(w, "DA:%d,%d\n", lineNumber, covered); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "LH:%d\n", coveredLines); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "LF:%d\n", len(lineNumbers)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "end_of_record"); err != nil {
			return err
		}
	}

	return nil
}

// lcovEffectiveEndLine adjusts Go coverprofile ranges that end at column 1 so
// the LCOV line range does not include the next executable line.
func lcovEffectiveEndLine(block *coverageBlock) int {
	if block.endLine > block.startLine && block.endCol == 1 {
		return block.endLine - 1
	}
	return block.endLine
}

// lcovSourceFilePath returns the repository-relative source path to emit in the
// LCOV report, falling back through CI tags and the original profile path.
func lcovSourceFilePath(profileFile string) string {
	// A path resolved within the module is the repository's, even when its
	// first directory has a dot, as api.v2.
	if fileName := lcovModuleSourceFilePath(profileFile); isLCOVResolvedPath(fileName) {
		return fileName
	}
	if resolved := cleanLCOVSourcePath(utils.ResolveSourceFilePathFromCITags(profileFile).RelativePath); isLCOVRepositoryRelativePath(resolved) {
		return resolved
	}
	if fallback := cleanLCOVSourcePath(profileFile); isLCOVRepositoryRelativePath(fallback) {
		return fallback
	}
	return ""
}

// lcovModuleSourceFilePath converts module-relative or absolute profile paths
// into repository-relative source paths when they belong to the current module.
func lcovModuleSourceFilePath(profileFile string) string {
	resolveModuleInfo()
	moduleRepoPrefix := lcovModuleRepositoryPrefix()
	normalizedProfileFile := cleanLCOVSourcePath(profileFile)
	normalizedModulePath := strings.TrimSuffix(cleanLCOVSourcePath(modulePath), "/")
	if normalizedProfileFile != "" && normalizedModulePath != "" {
		if suffix, ok := strings.CutPrefix(normalizedProfileFile, normalizedModulePath+"/"); ok {
			return cleanLCOVSourcePath(path.Join(moduleRepoPrefix, suffix))
		}
	}

	if moduleDir == "" || !filepath.IsAbs(profileFile) {
		return ""
	}
	relPath, err := filepath.Rel(moduleDir, profileFile)
	if err != nil || relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return ""
	}
	return cleanLCOVSourcePath(path.Join(moduleRepoPrefix, filepath.ToSlash(relPath)))
}

// lcovModuleRepositoryPrefix returns the module directory's path in the
// repository: relative to the CI workspace when the module lies inside it, as
// a nested module such as services/api does, or else what the module path's
// major version suffix implies.
func lcovModuleRepositoryPrefix() string {
	if workspace := utils.GetCITagsReadOnly()[constants.CIWorkspacePath]; workspace != "" && moduleDir != "" {
		if rel, err := filepath.Rel(workspace, moduleDir); err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return cleanLCOVSourcePath(filepath.ToSlash(rel))
		}
	}
	return moduleRepositoryRelativePrefix(modulePath)
}

// moduleRepositoryFile returns the path in the repository of file, a source
// path that starts with the current module's path, from the module's
// directory in the CI workspace. A nested module's directory need not match
// its path: github.com/acme/project/api can live in services/api. Without that
// directory, or for another module's file, ok is false.
func moduleRepositoryFile(file string) (string, bool) {
	resolveModuleInfo()
	workspace := utils.GetCITagsReadOnly()[constants.CIWorkspacePath]
	if workspace == "" || moduleDir == "" || modulePath == "" {
		return "", false
	}
	rel, err := filepath.Rel(workspace, moduleDir)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	suffix, ok := strings.CutPrefix(filepath.ToSlash(file), strings.TrimSuffix(modulePath, "/")+"/")
	if !ok {
		return "", false
	}
	return cleanLCOVSourcePath(path.Join(filepath.ToSlash(rel), suffix)), true
}

// cleanLCOVSourcePath normalizes path separators and removes empty or current
// directory path values before they are written to the LCOV report.
func cleanLCOVSourcePath(fileName string) string {
	fileName = strings.TrimSpace(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" {
		return ""
	}
	fileName = path.Clean(fileName)
	if fileName == "." {
		return ""
	}
	return strings.TrimPrefix(fileName, "./")
}

// isLCOVResolvedPath rejects absolute and parent-directory paths.
func isLCOVResolvedPath(fileName string) bool {
	if fileName == "" || path.IsAbs(fileName) || strings.Contains(fileName, ":") {
		return false
	}
	return fileName != ".." && !strings.HasPrefix(fileName, "../")
}

// isLCOVRepositoryRelativePath rejects absolute, parent-directory, and likely
// module-cache paths so uploaded reports only reference repository files.
func isLCOVRepositoryRelativePath(fileName string) bool {
	if !isLCOVResolvedPath(fileName) {
		return false
	}
	firstSegment, _, hasSlash := strings.Cut(fileName, "/")
	if !hasSlash {
		return true
	}
	return !strings.Contains(firstSegment, ".")
}
