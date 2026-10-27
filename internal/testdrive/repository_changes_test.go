// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Prescribed edits are made without asking, so the final response lists every
// change outside CI and ignore files and how to revert it, lockfile included.
func TestFinalResponseListsPrescribedRepositoryChangesWithRevert(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@example.test", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	write := func(name, content string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
	}
	git("init", "-q")
	write("package.json", `{"scripts":{"test:bdd":"cucumber-js"}}`)
	write("pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	write(".gitignore", "node_modules/\n")
	write(".github/workflows/ci.yml", "jobs: {}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")

	write("package.json", `{"scripts":{"test:bdd":"nyc cucumber-js"},"devDependencies":{"nyc":"17.1.0"}}`)
	write("pnpm-lock.yaml", "lockfileVersion: '9.0'\npackages: {}\n")
	write(".nycrc.json", "{}\n")
	write(".gitignore", "node_modules/\n/.testoptimization/\n")
	write(".github/workflows/ci.yml", "jobs: {test: {}}\n")
	write(".testoptimization/testdrive.json", "{}\n")

	changes := nonCIChanges(root)
	require.Equal(t, []repositoryChange{{Path: ".nycrc.json", Added: true}, {Path: "package.json"}, {Path: "pnpm-lock.yaml"}}, changes)
	require.Equal(t, "Repository changes outside CI and ignore files (uncommitted): .nycrc.json (new), package.json, pnpm-lock.yaml. To revert them: git checkout -- package.json pnpm-lock.yaml && rm -- .nycrc.json && pnpm install --frozen-lockfile.\n", repositoryChangesHandoff(changes))

	result := completeValidationFixture("cucumber")
	result.RepositoryChanges = changes
	result.Summary = summarizeValidation(result)
	require.Contains(t, validationHandoff(result), "To revert them: git checkout -- package.json pnpm-lock.yaml")

	require.Empty(t, repositoryChangesHandoff(nil))
	require.Nil(t, nonCIChanges(t.TempDir()), "outside Git there is nothing to list")
}
