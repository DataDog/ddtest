// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyIsIndependentAndPreservesLinks(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(source, "manifest"), "original")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "deps/pkg"), 0755))
	writeFile(t, filepath.Join(source, "deps/pkg/index.js"), "original dependency")
	require.NoError(t, os.Symlink("pkg", filepath.Join(source, "deps/link")))
	require.NoError(t, os.Mkdir(filepath.Join(source, ".testoptimization"), 0755))
	writeFile(t, filepath.Join(source, ".testoptimization/testdrive.json"), "old evidence")
	require.NoError(t, Copy(t.Context(), source, target, nil))
	writeFile(t, filepath.Join(target, "manifest"), "changed")
	writeFile(t, filepath.Join(target, "deps/link/index.js"), "changed dependency")
	for path, expected := range map[string]string{"manifest": "original", "deps/pkg/index.js": "original dependency"} {
		data, err := os.ReadFile(filepath.Join(source, path))
		require.NoError(t, err)
		require.Equal(t, expected, string(data))
	}
	require.NoFileExists(t, filepath.Join(target, ".testoptimization/testdrive.json"))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(source, "outside")))
	outsideCopy := t.TempDir()
	require.NoError(t, Copy(t.Context(), source, outsideCopy, nil))
	link, err := os.Readlink(filepath.Join(outsideCopy, "outside"))
	require.NoError(t, err)
	require.NotEmpty(t, link)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, Copy(ctx, source, t.TempDir(), nil), context.Canceled)
}

func TestCopyClosesEachFile(t *testing.T) {
	if os.Getenv("DDTEST_COPY_LOW_FD_CHILD") != "1" {
		if runtime.GOOS == "windows" {
			t.Skip("requires POSIX descriptor limit")
		}
		shell, err := exec.LookPath("sh")
		require.NoError(t, err)
		cmd := exec.Command(shell, "-c", `ulimit -n 64; exec "$@"`, "copy-test", os.Args[0], "-test.run=^TestCopyClosesEachFile$")
		cmd.Env = append(os.Environ(), "DDTEST_COPY_LOW_FD_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return
	}
	source, target := t.TempDir(), t.TempDir()
	for i := range 256 {
		writeFile(t, filepath.Join(source, fmt.Sprintf("file-%d", i)), "contents")
	}
	require.NoError(t, Copy(t.Context(), source, target, nil))
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Len(t, entries, 256)
}

func TestCopyRejectsDestinationInsideRepository(t *testing.T) {
	source := t.TempDir()
	scratch := filepath.Join(source, "tmp", "ddtest-configuration")
	require.NoError(t, os.MkdirAll(scratch, 0700))
	t.Setenv("TMPDIR", filepath.Join(source, "tmp"))
	writeFile(t, filepath.Join(source, "source"), "original")
	for _, target := range []string{source, scratch, filepath.Join(scratch, "checkout")} {
		require.ErrorContains(t, Copy(t.Context(), source, target, nil), "set TMPDIR outside the repository")
	}
	require.NoDirExists(t, filepath.Join(scratch, "checkout"))
	alias := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.Symlink(source, alias))
	require.ErrorContains(t, Copy(t.Context(), source, filepath.Join(alias, "tmp"), nil), "inside the repository")
}

func TestWalkSkipsOnlyIgnoredPathsTheFilterRejects(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", source).Run())
	writeFile(t, filepath.Join(source, ".gitignore"), "build/\ncache/\n")
	for _, path := range []string{"build/output", "cache/entry", "tracked"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, path)), 0700))
		writeFile(t, filepath.Join(source, path), path)
	}
	copied := func(skipIgnored func(string) bool) []string {
		target := filepath.Join(t.TempDir(), "copy")
		require.NoError(t, Copy(t.Context(), source, target, skipIgnored))
		var files []string
		for _, path := range []string{"build/output", "cache/entry", "tracked"} {
			if _, err := os.Stat(filepath.Join(target, path)); err == nil {
				files = append(files, path)
			}
		}
		return files
	}
	require.Equal(t, []string{"build/output", "cache/entry", "tracked"}, copied(nil), "a nil filter keeps every ignored path")
	require.Equal(t, []string{"cache/entry", "tracked"}, copied(func(path string) bool { return path == "build" }))
}
