package gittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewRepository(t *testing.T) {
	repo := NewRepository(t)

	if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err != nil {
		t.Fatalf("expected git repository at %s: %v", repo.Path, err)
	}
	if len(repo.Commits) != 2 {
		t.Fatalf("expected 2 commits, got %v", repo.Commits)
	}
	if len(repo.AuthorDates) != 2 || !repo.AuthorDates[1].After(repo.AuthorDates[0]) {
		t.Fatalf("unexpected author dates: %v", repo.AuthorDates)
	}
	if got := Run(t, repo.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
	if got := Run(t, repo.Path, "remote", "get-url", "origin"); got != "https://token@example.com/org/repo.git" {
		t.Fatalf("origin URL = %q", got)
	}
	if got := Run(t, repo.Path, "log", "--format=%s", "-n", "2"); !strings.Contains(got, "second commit") || !strings.Contains(got, "initial commit") {
		t.Fatalf("unexpected log output: %q", got)
	}

	WriteFile(t, repo.Path, "CHANGELOG.md", "changes\n")
	thirdCommit := CommitAll(t, repo.Path, "third commit", repo.AuthorDates[1].Add(time.Minute))
	if thirdCommit == "" || thirdCommit == repo.Commits[1] {
		t.Fatalf("unexpected third commit SHA: %q", thirdCommit)
	}
	if got := Run(t, repo.Path, "log", "-1", "--format=%s"); got != "third commit" {
		t.Fatalf("latest commit message = %q, want third commit", got)
	}
}

func TestNewShallowRepository(t *testing.T) {
	repo := NewShallowRepository(t)

	if _, err := os.Stat(filepath.Join(repo.Path, ".git")); err != nil {
		t.Fatalf("expected git repository at %s: %v", repo.Path, err)
	}
	if len(repo.Commits) != 3 {
		t.Fatalf("expected 3 source commits, got %v", repo.Commits)
	}
	if len(repo.AuthorDates) != 3 || !repo.AuthorDates[2].After(repo.AuthorDates[1]) {
		t.Fatalf("unexpected author dates: %v", repo.AuthorDates)
	}
	if got := Run(t, repo.Path, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("shallow state = %q, want true", got)
	}
	if got := Run(t, repo.Path, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("visible commit count = %q, want 1", got)
	}
	if got := Run(t, repo.Path, "log", "-1", "--format=%H"); got != repo.Commits[2] {
		t.Fatalf("shallow HEAD = %q, want %q", got, repo.Commits[2])
	}
}

func TestEnvIncludesDeterministicGitSettings(t *testing.T) {
	env := strings.Join(Env(), "\n")
	for _, want := range []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=" + AuthorName,
		"GIT_AUTHOR_EMAIL=" + AuthorEmail,
		"GIT_COMMITTER_NAME=" + CommitterName,
		"GIT_COMMITTER_EMAIL=" + CommitterEmail,
		"GIT_ALLOW_PROTOCOL=file",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("Env() missing %q in %q", want, env)
		}
	}
}
