// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
)

// A linked worktree and a submodule have a .git file instead of a directory.
// Their own checkout root is the safe.directory that git asks for.
func TestSafeDirectoryConfigAcceptsGitFiles(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	main := filepath.Join(root, "main")
	runFixtureGit(t, root, "init", main)
	runFixtureGit(t, main, "commit", "--allow-empty", "-m", "base")
	worktree := filepath.Join(root, "worktree")
	runFixtureGit(t, main, "worktree", "add", "--detach", worktree)
	library := filepath.Join(root, "library")
	runFixtureGit(t, root, "init", library)
	runFixtureGit(t, library, "commit", "--allow-empty", "-m", "library")
	runFixtureGit(t, main, "-c", "protocol.file.allow=always", "submodule", "add", library, "vendor/library")
	submodule := filepath.Join(main, "vendor", "library")

	for _, tc := range []struct{ name, checkout string }{
		{name: "repository", checkout: main},
		{name: "worktree", checkout: worktree},
		{name: "submodule", checkout: submodule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nested := filepath.Join(tc.checkout, "pkg", "nested")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(nested)
			resetGitCommandCachesForTesting(t)
			got, err := filepath.EvalSymlinks(getSafeDirectoryConfig())
			if err != nil {
				t.Fatalf("safe.directory %q: %v", getSafeDirectoryConfig(), err)
			}
			want, err := filepath.EvalSymlinks(tc.checkout)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("safe.directory = %q, want %q", got, want)
			}
			if _, err := execGitString(telemetry.NotSpecifiedCommandsType, "rev-parse", "--show-toplevel"); err != nil {
				t.Fatalf("git with safe.directory failed: %v", err)
			}
		})
	}
}
