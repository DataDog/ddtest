package testdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFingerprintIncludesSubmoduleCommitAndWorktree(t *testing.T) {
	root := t.TempDir()
	git := func(directory string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	git(root, "init", "-q")
	first, second := strings.Repeat("1", 40), strings.Repeat("2", 40)
	// Index gitlinks and detached HEADs suffice; the fixture creates no commits.
	git(root, "update-index", "--add", "--cacheinfo", "160000,"+first+",library")
	uninitialized, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	sub := filepath.Join(root, "library")
	require.NoError(t, os.Mkdir(sub, 0700))
	git(sub, "init", "-q")
	requireWriteFile(t, filepath.Join(sub, ".git/HEAD"), first+"\n")
	requireWriteFile(t, filepath.Join(sub, "source.js"), "original\n")
	git(sub, "add", "source.js")
	clean, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, uninitialized, clean)
	metadata := filepath.Join(t.TempDir(), "git")
	require.NoError(t, os.Rename(filepath.Join(sub, ".git"), metadata))
	requireWriteFile(t, filepath.Join(sub, ".git"), "gitdir: "+metadata+"\n")
	gitfile, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, clean, gitfile, "ordinary submodule gitfiles preserve the same source identity")
	requireWriteFile(t, filepath.Join(sub, "source.js"), "edited\n")
	dirty, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, clean, dirty)
	git(sub, "add", "source.js")
	staged, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, dirty, staged, "staging unchanged content does not make evidence current again")
	requireWriteFile(t, filepath.Join(sub, "extra.js"), "untracked\n")
	untracked, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, staged, untracked)
	requireWriteFile(t, filepath.Join(metadata, "HEAD"), second+"\n")
	checkout, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, untracked, checkout)
	git(root, "update-index", "--cacheinfo", "160000,"+second+",library")
	index, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, checkout, index)
}
