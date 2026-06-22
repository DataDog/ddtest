package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/git/gittest"
)

func TestCheckAvailableSuccess(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	if err := CheckAvailable(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCheckAvailableGitNotInstalled(t *testing.T) {
	resetGitPackageState(t)

	LookPathFunc = func(file string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	}

	err := CheckAvailable()
	if err == nil {
		t.Fatal("expected error when git is not installed")
	}
	if !strings.Contains(err.Error(), "git executable not found") {
		t.Fatalf("expected 'git executable not found' in error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "git is required for ddtest to work") {
		t.Fatalf("expected 'git is required for ddtest to work' in error message, got: %v", err)
	}
}

func TestCheckAvailableNotAGitRepo(t *testing.T) {
	gittest.RequireGit(t)
	t.Chdir(t.TempDir())
	resetGitPackageState(t)

	err := CheckAvailable()
	if err == nil {
		t.Fatal("expected error when not in a git repository")
	}
	if !strings.Contains(err.Error(), "current directory is not a git repository") {
		t.Fatalf("expected 'current directory is not a git repository' in error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "git is required for ddtest to work") {
		t.Fatalf("expected 'git is required for ddtest to work' in error message, got: %v", err)
	}
}

func TestFilterSensitiveInfo(t *testing.T) {
	testCases := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "https credentials",
			url:  "https://token@example.com/org/repo.git",
			want: "https://example.com/org/repo.git",
		},
		{
			name: "http credentials",
			url:  "http://user:password@example.com/org/repo.git",
			want: "http://example.com/org/repo.git",
		},
		{
			name: "ssh scheme credentials",
			url:  "ssh://git@example.com/org/repo.git",
			want: "ssh://example.com/org/repo.git",
		},
		{
			name: "scp-style ssh is unchanged",
			url:  "git@example.com:org/repo.git",
			want: "git@example.com:org/repo.git",
		},
		{
			name: "clean url",
			url:  "https://example.com/org/repo.git",
			want: "https://example.com/org/repo.git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FilterSensitiveInfo(tc.url); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestGetParentGitFolder(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := getParentGitFolder(nested)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := filepath.Join(root, ".git"); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	got, err = getParentGitFolder("")
	if err != nil {
		t.Fatalf("expected no error for empty path, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty git folder for empty path, got %q", got)
	}

	noGit := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(noGit, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = getParentGitFolder(noGit)
	if err != nil {
		t.Fatalf("expected no error for missing git folder, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty git folder for non-repository, got %q", got)
	}
}

func TestGitVersion(t *testing.T) {
	resetGitPackageState(t)
	gittest.RequireGit(t)

	major, minor, _, err := getGitVersion()
	if err != nil {
		t.Fatalf("expected git version, got %v", err)
	}
	if major <= 0 {
		t.Fatalf("expected positive major version, got %d.%d", major, minor)
	}
}

func TestCheckAvailableIntegrationWithGitDir(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Setenv("GIT_DIR", filepath.Join(repo.Path, ".git"))
	t.Setenv("GIT_WORK_TREE", repo.Path)
	resetGitPackageState(t)

	if err := CheckAvailable(); err != nil {
		t.Fatalf("expected git to be available with GIT_DIR, got %v", err)
	}
}

func TestGitIntegrationLocalGitDataFromWorktree(t *testing.T) {
	repo := gittest.NewRepository(t)
	nested := filepath.Join(repo.Path, "nested", "inside")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	resetGitPackageState(t)

	if got := GetSourceRoot(); got != repo.Path {
		t.Fatalf("expected source root %q, got %q", repo.Path, got)
	}
	if got := getSafeDirectoryConfig(); got != repo.Path {
		t.Fatalf("expected safe.directory %q, got %q", repo.Path, got)
	}

	data, err := GetLocalGitData()
	if err != nil {
		t.Fatalf("expected local git data, got %v", err)
	}

	assertCommitData(t, data.LocalCommitData, repo.Commits[1], "second commit\n\nMore details", repo.AuthorDates[1])
	if data.SourceRoot != repo.Path {
		t.Fatalf("expected source root %q, got %q", repo.Path, data.SourceRoot)
	}
	if data.RepositoryURL != "https://example.com/org/repo.git" {
		t.Fatalf("expected sanitized repository url, got %q", data.RepositoryURL)
	}
	if data.Branch != "main" {
		t.Fatalf("expected branch main, got %q", data.Branch)
	}
}

func TestGitIntegrationLocalGitDataWithGitDir(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Setenv("GIT_DIR", filepath.Join(repo.Path, ".git"))
	t.Setenv("GIT_WORK_TREE", repo.Path)
	resetGitPackageState(t)

	data, err := GetLocalGitData()
	if err != nil {
		t.Fatalf("expected local git data from GIT_DIR, got %v", err)
	}

	assertCommitData(t, data.LocalCommitData, repo.Commits[1], "second commit\n\nMore details", repo.AuthorDates[1])
	if data.SourceRoot != repo.Path {
		t.Fatalf("expected source root %q, got %q", repo.Path, data.SourceRoot)
	}
	if data.RepositoryURL != "https://example.com/org/repo.git" {
		t.Fatalf("expected sanitized repository url, got %q", data.RepositoryURL)
	}
}

func TestGitIntegrationFetchCommitData(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	commit, err := FetchCommitData(repo.Commits[0])
	if err != nil {
		t.Fatalf("expected commit data, got %v", err)
	}

	assertCommitData(t, commit, repo.Commits[0], "initial commit", repo.AuthorDates[0])
}

func TestGitIntegrationLastLocalCommitShas(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	shas := GetLastLocalGitCommitShas()
	if len(shas) < 2 {
		t.Fatalf("expected at least two commits, got %v", shas)
	}
	if shas[0] != repo.Commits[1] {
		t.Fatalf("expected latest commit first: want %q, got %q", repo.Commits[1], shas[0])
	}
	if shas[1] != repo.Commits[0] {
		t.Fatalf("expected previous commit second: want %q, got %q", repo.Commits[0], shas[1])
	}
}

func TestGitIntegrationShallowHelpersOnNormalRepository(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	shallow, err := isAShallowCloneRepository()
	if err != nil {
		t.Fatalf("expected shallow check to succeed, got %v", err)
	}
	if shallow {
		t.Fatal("expected normal repository not to be shallow")
	}

	moreThanOne, err := hasTheGitLogHaveMoreThanOneCommits()
	if err != nil {
		t.Fatalf("expected git log count check to succeed, got %v", err)
	}
	if !moreThanOne {
		t.Fatal("expected repository to have more than one commit")
	}

	unshallowed, err := UnshallowGitRepository()
	if err != nil {
		t.Fatalf("expected unshallow to be a no-op for normal repository, got %v", err)
	}
	if unshallowed {
		t.Fatal("expected normal repository not to be unshallowed")
	}
}

func TestGitIntegrationUnshallowRepository(t *testing.T) {
	repo := gittest.NewShallowRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	shallow, err := isAShallowCloneRepository()
	if err != nil {
		t.Fatalf("expected shallow check to succeed, got %v", err)
	}
	if !shallow {
		t.Fatal("expected depth-1 clone to be shallow")
	}

	commits := GetLastLocalGitCommitShas()
	if len(commits) != 1 {
		t.Fatalf("expected shallow clone to see one commit before unshallowing, got %v", commits)
	}

	unshallowed, err := UnshallowGitRepository()
	if err != nil {
		t.Fatalf("expected unshallow to succeed against local origin, got %v", err)
	}
	if !unshallowed {
		t.Fatal("expected shallow clone to be unshallowed")
	}

	shallow, err = isAShallowCloneRepository()
	if err != nil {
		t.Fatalf("expected shallow check after unshallow to succeed, got %v", err)
	}
	if shallow {
		t.Fatal("expected repository not to be shallow after unshallowing")
	}

	commits = GetLastLocalGitCommitShas()
	if len(commits) < 3 {
		t.Fatalf("expected unshallowed clone to see full local history, got %v", commits)
	}
	if commits[0] != repo.Commits[2] || commits[1] != repo.Commits[1] || commits[2] != repo.Commits[0] {
		t.Fatalf("expected latest commits %v, got %v", repo.Commits, commits[:3])
	}
}

func TestGitIntegrationFetchCommitDataFromShallowRepository(t *testing.T) {
	repo := gittest.NewShallowRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	commit, err := FetchCommitData(repo.Commits[1])
	if err != nil {
		t.Fatalf("expected missing commit data to be fetched from local origin, got %v", err)
	}

	assertCommitData(t, commit, repo.Commits[1], "second commit", repo.AuthorDates[1])
}

func TestGitIntegrationRemoteName(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	remote, err := getRemoteName()
	if err != nil {
		t.Fatalf("expected remote name, got %v", err)
	}
	if remote != "origin" {
		t.Fatalf("expected origin remote, got %q", remote)
	}

	gittest.Run(t, repo.Path, "checkout", "-b", "feature")
	gittest.Run(t, repo.Path, "branch", "--set-upstream-to=origin/main", "feature")
	resetGitPackageState(t)

	remote, err = getRemoteName()
	if err != nil {
		t.Fatalf("expected remote name from upstream, got %v", err)
	}
	if remote != "origin" {
		t.Fatalf("expected origin remote from upstream, got %q", remote)
	}
}

func TestGitIntegrationObjectCollectionAndPackFiles(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	objects := getObjectsSha([]string{repo.Commits[1]}, []string{repo.Commits[0]})
	if len(objects) == 0 {
		t.Fatal("expected objects for latest commit")
	}
	for _, object := range objects {
		if object == "" {
			t.Fatalf("expected non-empty object shas, got %v", objects)
		}
	}

	packFiles := CreatePackFiles([]string{repo.Commits[1]}, []string{repo.Commits[0]})
	if len(packFiles) == 0 {
		t.Fatal("expected pack files")
	}
	for _, packFile := range packFiles {
		info, err := os.Stat(packFile)
		if err != nil {
			t.Fatalf("expected pack file %q to exist, got %v", packFile, err)
		}
		if info.Size() == 0 {
			t.Fatalf("expected pack file %q to be non-empty", packFile)
		}
		t.Cleanup(func() {
			_ = os.RemoveAll(filepath.Dir(packFile))
		})
	}
}

func TestExecGitStringWithInput(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	resetGitPackageState(t)

	got, err := execGitStringWithInput("hello", "hash-object", "--stdin")
	if err != nil {
		t.Fatalf("expected hash-object to succeed, got %v", err)
	}

	const helloBlobSHA = "b6fc4c620b67d95f953a5c1c1230aaab5db5a1b0"
	if got != helloBlobSHA {
		t.Fatalf("expected blob sha %q, got %q", helloBlobSHA, got)
	}
}

func resetGitPackageState(t *testing.T) {
	t.Helper()

	origLookPath := LookPathFunc
	origGitVersionValue := gitVersionValue
	origShallowOnce := isAShallowCloneRepositoryOnce.Load()
	origShallowValue := isAShallowCloneRepositoryValue
	origSafeDirectoryValue := safeDirectoryValue

	LookPathFunc = exec.LookPath
	gitVersionOnce = sync.Once{}
	gitVersionValue = gitVersionData{}
	isAShallowCloneRepositoryOnce.Store(nil)
	isAShallowCloneRepositoryValue = false
	safeDirectoryOnce = sync.Once{}
	safeDirectoryValue = ""

	t.Cleanup(func() {
		LookPathFunc = origLookPath
		gitVersionOnce = sync.Once{}
		gitVersionValue = origGitVersionValue
		isAShallowCloneRepositoryOnce.Store(origShallowOnce)
		isAShallowCloneRepositoryValue = origShallowValue
		safeDirectoryOnce = sync.Once{}
		safeDirectoryValue = origSafeDirectoryValue
	})
}

func assertCommitData(t *testing.T, got LocalCommitData, wantSHA string, wantMessage string, wantAuthorDate time.Time) {
	t.Helper()

	if got.CommitSha != wantSHA {
		t.Fatalf("expected commit sha %q, got %q", wantSHA, got.CommitSha)
	}
	if got.AuthorName != gittest.AuthorName {
		t.Fatalf("expected author name %q, got %q", gittest.AuthorName, got.AuthorName)
	}
	if got.AuthorEmail != gittest.AuthorEmail {
		t.Fatalf("expected author email %q, got %q", gittest.AuthorEmail, got.AuthorEmail)
	}
	if got.CommitterName != gittest.CommitterName {
		t.Fatalf("expected committer name %q, got %q", gittest.CommitterName, got.CommitterName)
	}
	if got.CommitterEmail != gittest.CommitterEmail {
		t.Fatalf("expected committer email %q, got %q", gittest.CommitterEmail, got.CommitterEmail)
	}
	if got.CommitMessage != wantMessage {
		t.Fatalf("expected commit message %q, got %q", wantMessage, got.CommitMessage)
	}

	if !got.AuthorDate.Equal(wantAuthorDate) {
		t.Fatalf("expected author date %s, got %s", wantAuthorDate, got.AuthorDate)
	}

	wantCommitterDate := wantAuthorDate.Add(time.Second)
	if !got.CommitterDate.Equal(wantCommitterDate) {
		t.Fatalf("expected committer date %s, got %s", wantCommitterDate, got.CommitterDate)
	}
}
