// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
)

// TestGitRecorderHelperProcess runs real git and appends each command, without
// the options that execGit adds before it, to DD_TEST_GIT_RECORDER.
func TestGitRecorderHelperProcess(t *testing.T) {
	logFile := os.Getenv("DD_TEST_GIT_RECORDER")
	if logFile == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	command := args
	for len(command) >= 2 && command[0] == "-c" {
		command = command[2:]
	}
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(strings.Join(command, " ") + "\n")
		_ = f.Close()
	}
	cmd := exec.Command(os.Getenv("DD_TEST_REAL_GIT"), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		os.Exit(127)
	}
	os.Exit(0)
}

// recordGitCommands routes execGit through TestGitRecorderHelperProcess and
// returns a reader for the recorded commands.
func recordGitCommands(t *testing.T) func() []string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "commands.log")
	resetGitCommandCachesForTesting(t)
	gitExecutable = testBinary
	gitExecutableArgs = []string{"-test.run=^TestGitRecorderHelperProcess$", "--"}
	t.Setenv("DD_TEST_GIT_RECORDER", logFile)
	t.Setenv("DD_TEST_REAL_GIT", realGit)
	return func() []string {
		data, err := os.ReadFile(logFile)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// runFixtureGit runs real git with isolated configuration for test setup.
func runFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-c", "user.name=CI Fixture", "-c", "user.email=ci-fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "init.templateDir=", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newBaseBranchRemote creates a remote with main, dev, release/1.0 and
// feature/master branches. A pattern for "master" matches the last one in
// ls-remote, although the remote has no master branch.
func newBaseBranchRemote(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	source := filepath.Join(root, "source")
	runFixtureGit(t, root, "init", "--bare", remote)
	runFixtureGit(t, root, "init", source)
	runFixtureGit(t, source, "commit", "--allow-empty", "-m", "base")
	runFixtureGit(t, source, "branch", "dev")
	runFixtureGit(t, source, "commit", "--allow-empty", "-m", "main work")
	runFixtureGit(t, source, "branch", "feature/master")
	runFixtureGit(t, source, "branch", "release/1.0")
	runFixtureGit(t, source, "push", remote, "main", "dev", "feature/master", "release/1.0")
	path := filepath.ToSlash(remote)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // file:///C:/... on Windows
	}
	// A file:// URL uses an ordinary git transport, as a network remote does.
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// newBaseBranchClone fetches only dev, so the other base branches have no
// remote-tracking refs, and checks out a source branch.
func newBaseBranchClone(t *testing.T, remoteURL string) string {
	t.Helper()
	local := t.TempDir()
	runFixtureGit(t, local, "init")
	runFixtureGit(t, local, "remote", "add", "origin", remoteURL)
	runFixtureGit(t, local, "fetch", "--depth", "1", "origin", "dev")
	runFixtureGit(t, local, "checkout", "-b", "feature/source", "origin/dev")
	runFixtureGit(t, local, "commit", "--allow-empty", "-m", "source work")
	return local
}

// legacyCheckAndFetchBranches is the upstream per-branch algorithm, kept as an
// oracle: show-ref, then ls-remote with the branch as a pattern, then fetch.
func legacyCheckAndFetchBranches(branches []string, remoteName string) {
	for _, branch := range branches {
		if _, err := execGitString(telemetry.ShowRefCommandType, "show-ref", "--verify", "--quiet", "refs/remotes/"+remoteName+"/"+branch); err == nil {
			continue
		}
		remoteHeads, err := execGitString(telemetry.LsRemoteHeadsCommandType, "ls-remote", "--heads", remoteName, branch)
		if err != nil || remoteHeads == "" {
			continue
		}
		_, _ = execGitString(telemetry.FetchCommandType, "fetch", "--depth", "1", remoteName, branch)
	}
}

// baseBranchResult returns the remote-tracking refs and the base SHA that
// GetBaseBranchSha's step 2a and 3 select in the current repository.
func baseBranchResult(t *testing.T, remoteName string) (string, string) {
	t.Helper()
	refs, err := execGitString(telemetry.NotSpecifiedCommandsType, "for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes/"+remoteName)
	if err != nil {
		t.Fatal(err)
	}
	sourceBranch, err := getSourceBranch()
	if err != nil {
		t.Fatal(err)
	}
	remoteBranches, err := getRemoteBranches(remoteName)
	if err != nil {
		t.Fatal(err)
	}
	var candidates []string
	for _, branch := range remoteBranches {
		if branch != sourceBranch && isMainLikeBranch(branch, remoteName) {
			candidates = append(candidates, branch)
		}
	}
	metrics, err := computeBranchMetrics(candidates, sourceBranch)
	if err != nil {
		t.Fatal(err)
	}
	return refs, findBestBranch(metrics, "main", remoteName)
}

func commandsNamed(commands []string, name string) []string {
	var matched []string
	for _, command := range commands {
		if command == name || strings.HasPrefix(command, name+" ") {
			matched = append(matched, command)
		}
	}
	return matched
}

// One ls-remote lists every missing candidate; only branches the remote has,
// as exact refs/heads names, are fetched, in candidate order.
func TestCheckAndFetchBranchesListsMissingBranchesOnce(t *testing.T) {
	remoteURL := newBaseBranchRemote(t)
	t.Chdir(newBaseBranchClone(t, remoteURL))
	commands := recordGitCommands(t)

	checkAndFetchBranches(possibleBaseBranches, "origin")

	recorded := commands()
	if got := len(commandsNamed(recorded, "show-ref")); got != len(possibleBaseBranches) {
		t.Fatalf("show-ref commands = %d, want %d: %q", got, len(possibleBaseBranches), recorded)
	}
	wantListing := []string{"ls-remote --heads origin refs/heads/main refs/heads/master refs/heads/preprod refs/heads/prod refs/heads/development refs/heads/trunk"}
	if got := commandsNamed(recorded, "ls-remote"); !reflect.DeepEqual(got, wantListing) {
		t.Fatalf("ls-remote commands = %q, want %q", got, wantListing)
	}
	if got, want := commandsNamed(recorded, "fetch"), []string{"fetch --depth 1 origin main"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetch commands = %q, want %q", got, want)
	}
	if _, err := execGitString(telemetry.NotSpecifiedCommandsType, "show-ref", "--verify", "--quiet", "refs/remotes/origin/main"); err != nil {
		t.Fatalf("main was not fetched: %v", err)
	}
}

// The batched listing leaves the same remote-tracking refs and base branch as
// the upstream per-branch algorithm, without its failing fetch of master.
func TestCheckAndFetchBranchesMatchesPerBranchAlgorithm(t *testing.T) {
	remoteURL := newBaseBranchRemote(t)
	legacy, batched := newBaseBranchClone(t, remoteURL), newBaseBranchClone(t, remoteURL)

	t.Chdir(legacy)
	legacyCommands := recordGitCommands(t)
	legacyCheckAndFetchBranches(possibleBaseBranches, "origin")
	legacyRefs, legacyBase := baseBranchResult(t, "origin")
	legacyFetches := commandsNamed(legacyCommands(), "fetch")

	t.Chdir(batched)
	batchedCommands := recordGitCommands(t)
	checkAndFetchBranches(possibleBaseBranches, "origin")
	batchedRefs, batchedBase := baseBranchResult(t, "origin")
	batchedFetches := commandsNamed(batchedCommands(), "fetch")

	if legacyRefs != batchedRefs {
		t.Fatalf("remote-tracking refs differ:\nper-branch:\n%s\nbatched:\n%s", legacyRefs, batchedRefs)
	}
	if legacyBase == "" || legacyBase != batchedBase {
		t.Fatalf("base SHA per-branch=%q batched=%q", legacyBase, batchedBase)
	}
	// The pattern "master" matched refs/heads/feature/master, so the
	// per-branch algorithm also tried a fetch that git rejects.
	if want := []string{"fetch --depth 1 origin main", "fetch --depth 1 origin master"}; !reflect.DeepEqual(legacyFetches, want) {
		t.Fatalf("per-branch fetches = %q, want %q", legacyFetches, want)
	}
	if want := []string{"fetch --depth 1 origin main"}; !reflect.DeepEqual(batchedFetches, want) {
		t.Fatalf("batched fetches = %q, want %q", batchedFetches, want)
	}
}

// A pull-request base branch keeps its single exact lookup and fetch.
func TestCheckAndFetchBranchFetchesPullRequestBase(t *testing.T) {
	remoteURL := newBaseBranchRemote(t)
	t.Chdir(newBaseBranchClone(t, remoteURL))
	commands := recordGitCommands(t)

	checkAndFetchBranch("release/1.0", "origin")
	checkAndFetchBranch("dev", "origin")

	recorded := commands()
	if got, want := commandsNamed(recorded, "ls-remote"), []string{"ls-remote --heads origin refs/heads/release/1.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ls-remote commands = %q, want %q", got, want)
	}
	if got, want := commandsNamed(recorded, "fetch"), []string{"fetch --depth 1 origin release/1.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetch commands = %q, want %q", got, want)
	}
}
