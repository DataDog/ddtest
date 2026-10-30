package utils

import (
	"os"
	"path/filepath"
)

// withRunfilesSource finds a relative source path among a Bazel test's
// runfiles, where the build stages the test's sources on request, when no
// workspace tag resolved it to a file that exists, so that the test's end line
// and source parsing work there as with go test. rlocation is
// bazel.SourceRlocation outside tests.
func withRunfilesSource(result SourceFilePath, rlocation func(string) (string, bool)) SourceFilePath {
	if result.RuntimePath == "" || filepath.IsAbs(result.RuntimePath) {
		return result
	}
	if result.FilesystemKnown {
		if _, err := os.Stat(result.FilesystemPath); err == nil {
			return result
		}
	}
	if found, ok := rlocation(cleanLogicalSourcePath(result.RuntimePath)); ok {
		result.FilesystemPath, result.FilesystemKnown = found, true
	}
	return result
}
