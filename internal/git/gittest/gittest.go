// Package gittest provides convenience helpers for integration tests that need
// real Git repositories with deterministic commits and remotes.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	AuthorName     = "Test Author"
	AuthorEmail    = "author@example.com"
	CommitterName  = "Test Committer"
	CommitterEmail = "committer@example.com"
)

type Fixture struct {
	Path        string
	Commits     []string
	AuthorDates []time.Time
}

func NewRepository(t testing.TB) Fixture {
	t.Helper()
	RequireGit(t)

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	Run(t, repo, "init")
	Run(t, repo, "branch", "-M", "main")
	Run(t, repo, "config", "user.name", AuthorName)
	Run(t, repo, "config", "user.email", AuthorEmail)
	Run(t, repo, "config", "commit.gpgsign", "false")
	Run(t, repo, "remote", "add", "origin", "https://token@example.com/org/repo.git")

	firstDate := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	WriteFile(t, repo, "README.md", "Hello, world!\n")
	firstCommit := CommitAll(t, repo, "initial commit", firstDate)

	secondDate := firstDate.Add(time.Minute)
	WriteFile(t, repo, "README.md", "Hello, world!\nHello again.\n")
	secondCommit := CommitAll(t, repo, "second commit\n\nMore details", secondDate)
	Run(t, repo, "update-ref", "refs/remotes/origin/main", secondCommit)

	return Fixture{
		Path:        repo,
		Commits:     []string{firstCommit, secondCommit},
		AuthorDates: []time.Time{firstDate, secondDate},
	}
}

func NewShallowRepository(t testing.TB) Fixture {
	t.Helper()
	RequireGit(t)

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	source := filepath.Join(root, "source")
	clone := filepath.Join(root, "clone")

	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}

	origin, err := filepath.EvalSymlinks(origin)
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}

	Run(t, origin, "init", "--bare")
	Run(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	Run(t, source, "init")
	Run(t, source, "branch", "-M", "main")
	Run(t, source, "config", "user.name", AuthorName)
	Run(t, source, "config", "user.email", AuthorEmail)
	Run(t, source, "config", "commit.gpgsign", "false")
	Run(t, source, "remote", "add", "origin", "file://"+origin)

	firstDate := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	WriteFile(t, source, "README.md", "one\n")
	firstCommit := CommitAll(t, source, "first commit", firstDate)

	secondDate := firstDate.Add(time.Minute)
	WriteFile(t, source, "README.md", "one\ntwo\n")
	secondCommit := CommitAll(t, source, "second commit", secondDate)

	thirdDate := secondDate.Add(time.Minute)
	WriteFile(t, source, "README.md", "one\ntwo\nthree\n")
	thirdCommit := CommitAll(t, source, "third commit", thirdDate)

	Run(t, source, "push", "-u", "origin", "main")
	Run(t, root, "clone", "--depth", "1", "--branch", "main", "file://"+origin, clone)
	clone, err = filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}

	return Fixture{
		Path:        clone,
		Commits:     []string{firstCommit, secondCommit, thirdCommit},
		AuthorDates: []time.Time{firstDate, secondDate, thirdDate},
	}
}

func RequireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func CommitAll(t testing.TB, repo string, message string, authorDate time.Time) string {
	t.Helper()

	Run(t, repo, "add", ".")

	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = repo
	committerDate := authorDate.Add(time.Second)
	cmd.Env = append(Env(),
		fmt.Sprintf("GIT_AUTHOR_DATE=%s", authorDate.Format(time.RFC3339)),
		fmt.Sprintf("GIT_COMMITTER_DATE=%s", committerDate.Format(time.RFC3339)),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}

	return Run(t, repo, "rev-parse", "HEAD")
}

func WriteFile(t testing.TB, repo string, name string, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func Env() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+AuthorName,
		"GIT_AUTHOR_EMAIL="+AuthorEmail,
		"GIT_COMMITTER_NAME="+CommitterName,
		"GIT_COMMITTER_EMAIL="+CommitterEmail,
		"GIT_ALLOW_PROTOCOL=file",
	)
}
