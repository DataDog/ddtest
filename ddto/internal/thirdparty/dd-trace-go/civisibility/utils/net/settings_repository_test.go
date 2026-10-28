package net

import (
	"errors"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/bazel"
)

// Without a repository URL or a commit SHA, as outside a Git repository,
// GetSettings makes no request and says why with ErrNoRepositoryOrCommit.
func TestGetSettingsWithoutRepositoryOrCommit(t *testing.T) {
	t.Setenv(bazel.ManifestFilePathEnv, "")
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)
	for _, c := range []*client{{}, {repositoryURL: "https://github.com/example/repo"}, {commitSha: "0123456789abcdef0123456789abcdef01234567"}} {
		if _, err := c.GetSettings(); !errors.Is(err, ErrNoRepositoryOrCommit) || err.Error() != "civisibility.GetSettings: repository URL and commit SHA are required" {
			t.Errorf("repository %q, commit %q: %v", c.repositoryURL, c.commitSha, err)
		}
	}
}
