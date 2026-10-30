package bazel

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/env"
	logger "github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/log"
)

// SourceRlocation finds, in a Bazel test's runfiles, a source file that the
// compiler recorded relative to the execution root, as rules_go's -trimpath
// does: "external/<repository>/<path>" names a file of that repository, and
// any other relative path a file of the main repository, TEST_WORKSPACE. The
// test runs in its package's directory, so the relative path does not open
// from there. The file is found only when the build stages it as a runfile.
func SourceRlocation(logical string) (string, bool) {
	workspace := strings.TrimSpace(env.Get("TEST_WORKSPACE"))
	if workspace == "" || logical == "" || filepath.IsAbs(logical) {
		return "", false
	}
	logical = path.Clean(filepath.ToSlash(logical))
	if logical == "." || logical == ".." || strings.HasPrefix(logical, "../") {
		return "", false
	}
	rlocation := path.Join(workspace, logical)
	if external, ok := strings.CutPrefix(logical, "external/"); ok {
		rlocation = external
	}
	for _, root := range []string{"RUNFILES_DIR", "TEST_SRCDIR"} {
		if dir := strings.TrimSpace(env.Get(root)); dir != "" {
			if found, ok := existingFilePath(filepath.Join(dir, filepath.FromSlash(rlocation))); ok {
				logger.Debug("civisibility: resolved source %s via %s to %s", logical, root, absolutePathForLog(found))
				return found, true
			}
		}
	}
	if manifest := strings.TrimSpace(env.Get("RUNFILES_MANIFEST_FILE")); manifest != "" {
		if candidate, ok := resolveRunfilesManifestEntry(manifest, rlocation); ok {
			if found, ok := existingFilePath(candidate); ok {
				return found, true
			}
		}
	}
	logger.Debug("civisibility: source %s is not a runfile of the test (rlocation %s)", logical, rlocation)
	return "", false
}
