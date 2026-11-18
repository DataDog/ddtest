// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func checkoutGit(t *testing.T, directory, input string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func checkoutRepository(t *testing.T) (string, string) {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG"} {
		value, set := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if set {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	directory := t.TempDir()
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, directory, "", "-c", "init.defaultBranch=main", "init")
	checkoutGit(t, directory, "", "remote", "add", "origin", "https://mirror.example.com/repo.git")
	blob := checkoutGit(t, directory, "checkout contents", "hash-object", "-w", "--stdin")
	tree := checkoutGit(t, directory, "100644 blob "+blob+"\tfile.txt\n", "mktree")
	head := checkoutCommit(t, directory, tree, "", time.Now().Unix(), "checkout commit")
	checkoutGit(t, directory, "", "update-ref", "refs/heads/main", head)
	t.Chdir(directory)
	resetGitCommandCachesForTesting(t)
	return directory, head
}

func checkoutCommit(t *testing.T, directory, tree, parent string, date int64, message string) string {
	t.Helper()
	data := "tree " + tree + "\n"
	if parent != "" {
		data += "parent " + parent + "\n"
	}
	data += fmt.Sprintf("author Fixture <fixture@example.com> %d +0000\ncommitter Fixture <fixture@example.com> %d +0000\n\n%s\n", date, date, message)
	return checkoutGit(t, directory, data, "hash-object", "-t", "commit", "-w", "--stdin")
}

func selectedCommits(sha string) []string              { return GetLocalGitCommitShas(sha) }
func selectedPacks(include, exclude []string) []string { return CreatePackFiles(include, exclude) }

func TestCheckoutGitContextSurvivesDirectoryChange(t *testing.T) {
	directory, head := checkoutRepository(t)
	data, err := getLocalGitData()
	if err != nil || data.CommitSha != head {
		t.Fatalf("metadata=%+v, %v", data, err)
	}
	other := t.TempDir()
	checkoutGit(t, other, "", "-c", "init.defaultBranch=main", "init")
	tree := checkoutGit(t, other, "", "mktree")
	foreign := checkoutCommit(t, other, tree, "", time.Now().Unix(), "other repository")
	checkoutGit(t, other, "", "update-ref", "refs/heads/main", foreign)
	t.Chdir(other)
	if commits := selectedCommits(strings.ToUpper(head)); len(commits) != 1 || commits[0] != head {
		t.Fatalf("selected commits=%v", commits)
	}
	paths := selectedPacks([]string{head}, nil)
	if len(paths) == 0 {
		t.Fatal("selected checkout produced no pack")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Dir(paths[0])); err != nil {
			t.Error(err)
		}
	})
	index := strings.TrimSuffix(paths[0], ".pack") + ".idx"
	objects := checkoutGit(t, directory, "", "verify-pack", "-v", index)
	if !strings.Contains(objects, head+" commit") || strings.Contains(objects, foreign) {
		t.Fatalf("pack selected another checkout: %s", objects)
	}
}

func TestSelectedOldRevisionPackExcludesHEAD(t *testing.T) {
	directory, head := checkoutRepository(t)
	tree := checkoutGit(t, directory, "", "mktree")
	selected := checkoutCommit(t, directory, tree, "", 1, "old selected commit")
	if got := selectedCommits(strings.ToUpper(selected)); len(got) != 1 || got[0] != selected {
		t.Fatalf("old selected commits=%v", got)
	}
	paths := selectedPacks([]string{selected}, nil)
	if len(paths) == 0 {
		t.Fatal("old selected commit produced no pack")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Dir(paths[0])); err != nil {
			t.Error(err)
		}
	})
	objects := checkoutGit(t, directory, "", "verify-pack", "-v", strings.TrimSuffix(paths[0], ".pack")+".idx")
	if !strings.Contains(objects, selected+" commit") || !strings.Contains(objects, tree+" tree") || strings.Contains(objects, head) || strings.Contains(objects, " blob ") {
		t.Fatalf("wrong selected objects: %s", objects)
	}
	descendant := checkoutCommit(t, directory, tree, selected, time.Now().Unix(), "known descendant")
	if paths := selectedPacks([]string{selected}, []string{descendant}); len(paths) != 0 {
		t.Cleanup(func() {
			if err := os.RemoveAll(filepath.Dir(paths[0])); err != nil {
				t.Error(err)
			}
		})
		t.Fatal("transitive exclusion was ignored")
	}
	if got := selectedCommits(strings.Repeat("f", 40)); len(got) != 0 {
		t.Fatal("missing selected revision fell back to HEAD")
	}
}

func TestSelectedHistoryRemainsCapped(t *testing.T) {
	directory, _ := checkoutRepository(t)
	var stream strings.Builder
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	for range 1005 {
		stream.WriteString("commit refs/heads/history\ncommitter Fixture <fixture@example.com> " + timestamp + " +0000\ndata 7\nhistory\n\n")
	}
	checkoutGit(t, directory, stream.String(), "fast-import", "--quiet")
	selected := checkoutGit(t, directory, "", "rev-parse", "history")
	commits := selectedCommits(selected)
	if len(commits) != 1000 || commits[0] != selected {
		t.Fatalf("selected history count=%d, selected=%v", len(commits), len(commits) > 0 && commits[0] == selected)
	}
	tracePath := filepath.Join(t.TempDir(), "git.trace")
	t.Setenv("GIT_TRACE", filepath.ToSlash(tracePath))
	paths := selectedPacks(commits, nil)
	if len(paths) == 0 {
		t.Fatal("capped history produced no pack")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Dir(paths[0])); err != nil {
			t.Error(err)
		}
	})
	count := 0
	for _, path := range paths {
		output := checkoutGit(t, directory, "", "verify-pack", "-v", strings.TrimSuffix(path, ".pack")+".idx")
		for line := range strings.SplitSeq(output, "\n") {
			if strings.Contains(line, " commit ") {
				count++
			}
		}
	}
	if count != 1000 {
		t.Fatalf("pack contains %d commits, want 1000", count)
	}

	// Exercise the old-selected fallback with a full list of known revisions too.
	tree := checkoutGit(t, directory, "", "mktree")
	old := checkoutCommit(t, directory, tree, "", 1, "old selected root")
	objects := getObjectsSha([]string{old}, commits)
	if len(objects) != 2 || !slices.Contains(objects, old) || !slices.Contains(objects, tree) {
		t.Fatalf("old selected objects with capped exclusions=%v", objects)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	commands := 0
	for line := range strings.SplitSeq(string(trace), "\n") {
		if strings.Contains(line, "built-in: git rev-list ") {
			commands++
			if len(line) >= 32767 {
				t.Errorf("revision command is %d bytes, exceeding the Windows process limit", len(line))
			}
		}
	}
	if commands != 3 {
		t.Fatalf("revision commands=%d, want one capped query and two old-selected queries", commands)
	}
}

func TestCheckoutMetadataWithoutParentObjects(t *testing.T) {
	directory, _ := checkoutRepository(t)
	tree := checkoutGit(t, directory, "", "mktree")
	missing := strings.Repeat("b", 40)
	selected := checkoutCommit(t, directory, tree, missing, time.Now().Unix(), "stored message with \",\" separator")
	checkoutGit(t, directory, "", "update-ref", "refs/heads/main", selected)
	data, err := getLocalGitData()
	if err != nil || data.CommitSha != selected || data.CommitMessage != "stored message with \",\" separator" {
		t.Fatalf("stored metadata=%+v, %v", data, err)
	}
	commit, err := readCommitData(selected, false)
	if err != nil || commit.CommitSha != selected || commit.CommitMessage != data.CommitMessage {
		t.Fatalf("stored head=%+v, %v", commit, err)
	}
}

func TestDetachedTagInferenceAndCloneRemote(t *testing.T) {
	directory, head := checkoutRepository(t)
	checkoutGit(t, directory, "", "tag", "v1", head)
	data, err := getLocalGitData()
	if err != nil || data.Tag != "" {
		t.Fatalf("attached tag=%q, %v", data.Tag, err)
	}
	checkoutGit(t, directory, "", "remote", "rename", "origin", "upstream")
	checkoutGit(t, directory, "", "config", "branch.main.remote", "upstream")
	checkoutGit(t, directory, "", "remote", "add", "backup", "https://example.com/backup.git")
	checkoutGit(t, directory, "", "checkout", "--detach", "HEAD")
	config := filepath.Join(directory, ".git", "remote settings")
	if err := os.WriteFile(config, []byte("[feature]\n enabled\n[clone]\n defaultRemoteName=upstream\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, directory, "", "config", "include.path", "remote settings")
	data, err = getLocalGitData()
	if err != nil || data.Branch != "HEAD" || data.Tag != "v1" || data.RepositoryURL != "https://mirror.example.com/repo.git" {
		t.Fatalf("detached clone=%+v, %v", data, err)
	}
	checkoutGit(t, directory, "", "tag", "v2", head)
	data, err = getLocalGitData()
	if err != nil || data.Tag != "" {
		t.Fatalf("ambiguous tag=%q, %v", data.Tag, err)
	}
	checkoutGit(t, directory, "", "remote", "remove", "upstream")
	checkoutGit(t, directory, "", "remote", "remove", "backup")
	data, err = getLocalGitData()
	if err != nil || data.RepositoryURL != "" {
		t.Fatalf("invented remote=%q, %v", data.RepositoryURL, err)
	}
}

func TestObjectEnumerationRejectsRevisionLineBreaks(t *testing.T) {
	directory, head := checkoutRepository(t)
	tree := checkoutGit(t, directory, "", "mktree")
	selected := checkoutCommit(t, directory, tree, "", 1, "independent selected root")
	for _, broken := range []string{head + "\n" + head, head + "\r"} {
		if objects := getObjectsSha([]string{broken}, nil); len(objects) != 0 {
			t.Errorf("malformed included revision produced %d objects", len(objects))
		}
		if objects := getObjectsSha([]string{selected}, []string{broken}); len(objects) != 0 {
			t.Errorf("malformed excluded revision produced %d objects", len(objects))
		}
	}
}
