package runner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	ciutils "github.com/DataDog/ddtest/civisibility/utils"
)

// getCwdSubdirPrefix calculates the relative path from the git root to the
// current working directory. Returns empty string if CWD is the git root
// or if the git root cannot be determined.
//
// Example: git root = /repo, CWD = /repo/core -> returns "core"
// Example: git root = /repo, CWD = /repo/packages/core -> returns "packages/core"
// Example: git root = /repo, CWD = /repo -> returns ""
func getCwdSubdirPrefix() string {
	gitRoot := ciutils.GetSourceRoot()
	if gitRoot == "" {
		return ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Resolve symlinks for both to ensure correct comparison
	gitRootResolved, err := filepath.EvalSymlinks(gitRoot)
	if err != nil {
		gitRootResolved = gitRoot
	}
	cwdResolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		cwdResolved = cwd
	}

	rel, err := filepath.Rel(gitRootResolved, cwdResolved)
	if err != nil {
		return ""
	}

	// "." means CWD == git root
	if rel == "." {
		return ""
	}

	// Use forward slashes for consistent path matching
	return filepath.ToSlash(rel)
}

// normalizeTestFilePath converts a test file path that may be repo-root-relative
// to a CWD-relative path. This is needed when running ddtest from a monorepo
// subdirectory (e.g., "cd core && ddtest plan") where full test discovery returns
// paths relative to the git root (e.g., "core/spec/...") but workers need
// paths relative to CWD (e.g., "spec/...").
//
// Safety rules:
//   - If the path does not start with the CWD subdir prefix, it is returned unchanged
//   - If CWD is the git root, the path is returned unchanged
//   - If the git root cannot be determined, the path is returned unchanged (fail-safe)
//   - Absolute paths are returned unchanged
//   - Empty paths are returned unchanged
func normalizeTestFilePath(path string) string {
	return normalizeTestFilePathWithPrefix(path, getCwdSubdirPrefix())
}

// normalizeTestFilePathWithPrefix is the pure version of normalizeTestFilePath
// that accepts a pre-computed subdir prefix. This avoids calling git once per file.
func normalizeTestFilePathWithPrefix(path string, subdirPrefix string) string {
	if path == "" || subdirPrefix == "" {
		return path
	}

	// Don't modify absolute paths
	if filepath.IsAbs(path) {
		return path
	}

	// Use forward slashes for matching
	normalizedPath := filepath.ToSlash(path)
	prefixWithSlash := subdirPrefix + "/"

	if strings.HasPrefix(normalizedPath, prefixWithSlash) {
		stripped := strings.TrimPrefix(normalizedPath, prefixWithSlash)
		slog.Debug("Normalized test file path for subdirectory execution",
			"original", path, "normalized", stripped, "subdirPrefix", subdirPrefix)
		return stripped
	}

	// Path doesn't match CWD subdir prefix - return unchanged
	return path
}
