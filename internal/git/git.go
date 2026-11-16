// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package git

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// MaxPackFileSizeInMb is the maximum size of a pack file in megabytes.
	MaxPackFileSizeInMb = 3
)

// CommandType identifies the purpose of a Git command.
type CommandType string

const (
	CommandGetRemote                 CommandType = "get_remote"
	CommandGetRemoteUpstreamTracking CommandType = "get_remote_upstream_tracking"
	CommandGetHead                   CommandType = "get_head"
	CommandGetBranch                 CommandType = "get_branch"
	CommandCheckShallow              CommandType = "check_shallow"
	CommandUnshallow                 CommandType = "unshallow"
	CommandGetLocalCommits           CommandType = "get_local_commits"
	CommandGetObjects                CommandType = "get_objects"
	CommandPackObjects               CommandType = "pack_objects"
)

// CommandTelemetry observes Git command executions.
type CommandTelemetry interface {
	Command(commandType CommandType)
	CommandError(commandType CommandType, err error)
	CommandDuration(commandType CommandType, duration time.Duration)
}

// LocalCommitData holds information about a single commit in the local Git repository.
type LocalCommitData struct {
	CommitSha      string
	AuthorDate     time.Time
	AuthorName     string
	AuthorEmail    string
	CommitterDate  time.Time
	CommitterName  string
	CommitterEmail string
	CommitMessage  string
}

// LocalGitData holds various pieces of information about the local Git repository,
// including the source root, repository URL, branch, commit SHA, author and committer details, and commit message.
type LocalGitData struct {
	LocalCommitData
	SourceRoot    string
	RepositoryURL string
	Branch        string
	Tag           string
}

// gitVersionData holds the major, minor, and patch version numbers of the Git executable.
type gitVersionData struct {
	major int
	minor int
	patch int
	err   error
}

var (
	// LookPathFunc is the function used to look up executables in PATH.
	// It can be overridden in tests.
	LookPathFunc = exec.LookPath

	// gitCommandMutex is a mutex used to synchronize access to Git commands to prevent lock errors in git
	gitCommandMutex sync.Mutex

	errGitExecutableNotFound = errors.New("git executable not found")

	// regexpSensitiveInfo is a regular expression used to match and filter out sensitive information from URLs.
	regexpSensitiveInfo = regexp.MustCompile("(https?://|ssh?://)[^/]*@")

	// Cached data

	// gitVersion is a sync.Once instance used to ensure that the Git version is only retrieved once.
	gitVersionOnce sync.Once

	// gitVersionValue holds the version of the Git executable installed on the system.
	gitVersionValue gitVersionData

	// isAShallowCloneRepositoryOnce is a sync.Once instance used to ensure that the check for a shallow clone repository is only performed once.
	isAShallowCloneRepositoryOnce atomic.Pointer[sync.Once]

	// isAShallowCloneRepositoryValue is a boolean flag indicating whether the repository is a shallow clone.
	isAShallowCloneRepositoryValue bool

	// safeDirectoryOnce is a sync.Once instance used to ensure that the safe directory is only resolved once.
	safeDirectoryOnce sync.Once

	// safeDirectoryValue holds the cached repository root path for safe.directory config.
	safeDirectoryValue  string
	gitWorkingDirectory string
)

// CommandRunner runs the Git operations used during Test Optimization and
// reports their command-level telemetry.
type CommandRunner struct {
	telemetry CommandTelemetry
}

// NewCommandRunner creates a Git command runner using commandTelemetry.
func NewCommandRunner(commandTelemetry CommandTelemetry) *CommandRunner {
	return &CommandRunner{telemetry: commandTelemetry}
}

func (r *CommandRunner) execGit(commandType CommandType, args ...string) (val []byte, err error) {
	startTime := time.Now()
	if r.telemetry != nil {
		r.telemetry.Command(commandType)
	}
	defer func() {
		if r.telemetry != nil {
			r.telemetry.CommandDuration(commandType, time.Since(startTime))
			r.telemetry.CommandError(commandType, err)
		}
	}()
	return execGit(args...)
}

func (r *CommandRunner) execGitString(commandType CommandType, args ...string) (string, error) {
	out, err := r.execGit(commandType, args...)
	strOut := strings.TrimSpace(strings.Trim(string(out), "\n"))
	return strOut, err
}

func (r *CommandRunner) execGitStringWithInput(commandType CommandType, input string, args ...string) (val string, err error) {
	startTime := time.Now()
	if r.telemetry != nil {
		r.telemetry.Command(commandType)
	}
	defer func() {
		if r.telemetry != nil {
			r.telemetry.CommandDuration(commandType, time.Since(startTime))
			r.telemetry.CommandError(commandType, err)
		}
	}()
	return execGitStringWithInput(input, args...)
}

// CheckAvailable verifies that git is available and the current directory is a git repository.
// Returns an error if git is not installed or the current directory is not a git repository.
func CheckAvailable() error {
	if _, err := execGitString("rev-parse", "--git-dir"); err != nil {
		if errors.Is(err, errGitExecutableNotFound) {
			return fmt.Errorf("%w: git is required for ddtest to work", errGitExecutableNotFound)
		}
		return fmt.Errorf("current directory is not a git repository: git is required for ddtest to work")
	}

	return nil
}

// getSafeDirectoryConfig returns the repository root path to be used with git's safe.directory config.
// This is cached to avoid repeated filesystem lookups.
// Using -c safe.directory=<path> instead of modifying global config avoids config pollution
// and provides better security (only affects the single command execution).
func getSafeDirectoryConfig() string {
	safeDirectoryOnce.Do(func() {
		currentDir, err := os.Getwd()
		if err != nil {
			slog.Debug("testoptimization.git: error getting current working directory for safe.directory")
			return
		}

		gitWorkingDirectory = currentDir
		safeDirectoryValue = safeDirectoryFor(currentDir)
		slog.Debug("testoptimization.git: using safe.directory config", "path", safeDirectoryValue)
	})
	return safeDirectoryValue
}

// execGit executes a Git command with the given arguments.
// It automatically includes -c safe.directory=<repo_root> to handle repositories
// with different ownership (common in CI environments) without modifying global config.
func execGit(args ...string) (val []byte, err error) {
	if _, err := LookPathFunc("git"); err != nil {
		return nil, errGitExecutableNotFound
	}
	gitCommandMutex.Lock()
	defer gitCommandMutex.Unlock()

	// Prepend safe.directory config if we have a known repo root
	if safeDir := getSafeDirectoryConfig(); safeDir != "" {
		args = append([]string{"-c", "safe.directory=" + safeDir}, args...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = gitWorkingDirectory
	return gitOutput(cmd)
}

// execGitString executes a Git command with the given arguments and returns the output as a string.
func execGitString(args ...string) (string, error) {
	out, err := execGit(args...)
	strOut := strings.TrimSpace(strings.Trim(string(out), "\n"))
	return strOut, err
}

// execGitStringWithInput executes a Git command with the given input and arguments and returns the output as a string.
// It automatically includes -c safe.directory=<repo_root> to handle repositories with different ownership.
func execGitStringWithInput(input string, args ...string) (val string, err error) {
	if _, err := LookPathFunc("git"); err != nil {
		return "", errGitExecutableNotFound
	}

	// Prepend safe.directory config if we have a known repo root
	if safeDir := getSafeDirectoryConfig(); safeDir != "" {
		args = append([]string{"-c", "safe.directory=" + safeDir}, args...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = gitWorkingDirectory
	cmd.Stdin = strings.NewReader(input)
	out, err := gitOutput(cmd)
	strOut := strings.TrimSpace(strings.Trim(string(out), "\n"))
	return strOut, err
}

// getGitVersion retrieves the version of the Git executable installed on the system.
func getGitVersion() (major int, minor int, patch int, err error) {
	gitVersionOnce.Do(func() {
		out, lerr := execGitString("--version")
		if lerr != nil {
			gitVersionValue = gitVersionData{err: lerr}
			return
		}
		out = strings.TrimSpace(strings.ReplaceAll(out, "git version ", ""))
		versionParts := strings.Split(out, ".")
		if len(versionParts) < 3 {
			gitVersionValue = gitVersionData{err: errors.New("invalid git version")}
			return
		}
		major, _ = strconv.Atoi(versionParts[0])
		minor, _ = strconv.Atoi(versionParts[1])
		patch, _ = strconv.Atoi(versionParts[2])
		gitVersionValue = gitVersionData{
			major: major,
			minor: minor,
			patch: patch,
			err:   nil,
		}
	})

	return gitVersionValue.major, gitVersionValue.minor, gitVersionValue.patch, gitVersionValue.err
}

// GetSourceRoot returns the absolute path to the git repository root
// (the result of `git rev-parse --show-toplevel`).
// Returns empty string if git is not available or the current directory is not in a git repo.
func GetSourceRoot() string {
	// Discovery can inspect a different candidate before metadata is selected.
	// It must not reuse the upload worker's pinned command directory.
	if _, err := LookPathFunc("git"); err != nil {
		return ""
	}
	directory, err := os.Getwd()
	if err != nil {
		return ""
	}
	args := []string{"rev-parse", "--show-toplevel"}
	if safeDir := safeDirectoryFor(directory); safeDir != "" {
		args = append([]string{"-c", "safe.directory=" + safeDir}, args...)
	}
	command := exec.Command("git", args...)
	command.Dir = directory
	out, err := gitOutput(command)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func safeDirectoryFor(directory string) string {
	gitDir, err := getParentGitFolder(directory)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(gitDir, string(filepath.Separator)+".git")
}

// GetLocalGitData retrieves information about the local Git repository from the current HEAD.
// It gathers details such as the repository URL, current branch, latest commit SHA, author and committer details, and commit message.
//
// Returns:
//
//	A LocalGitData struct populated with the retrieved Git data.
//	An error if any Git command fails or the retrieved data is incomplete.
func GetLocalGitData() (LocalGitData, error) {
	gitData := LocalGitData{}

	// Extract the absolute path to the Git directory
	slog.Debug("testoptimization.git: getting the absolute path to the Git directory")
	out, err := execGitString("rev-parse", "--show-toplevel")
	if err == nil {
		gitData.SourceRoot = out
	}

	// Extract the repository URL
	slog.Debug("testoptimization.git: getting the repository URL")
	remote, remoteErr := getRemoteName()
	if remoteErr == nil {
		out, err = execGitString("remote", "get-url", remote)
	} else {
		err = remoteErr
	}
	if err == nil {
		gitData.RepositoryURL = FilterSensitiveInfo(out)
	}

	// Extract the current branch name
	slog.Debug("testoptimization.git: getting the current branch name")
	out, err = execGitString("rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		gitData.Branch = out
	}

	if gitData.Branch == "HEAD" {
		if tags, tagErr := execGitString("tag", "--no-column", "--points-at", "HEAD"); tagErr == nil && tags != "" && !strings.Contains(tags, "\n") {
			gitData.Tag = tags
		}
	}

	// Read only the selected commit; its parents may be missing locally.
	slog.Debug("testoptimization.git: getting the latest commit details")
	out, err = execGitString("log", "--no-walk=unsorted", "--no-show-signature", "--format=%H%x00%at%x00%an%x00%ae%x00%ct%x00%cn%x00%ce%x00%B", "HEAD", "--")
	if err != nil {
		return gitData, err
	}

	// Split the output into individual components
	outArray := strings.SplitN(out, "\x00", 8)
	if len(outArray) < 8 {
		return gitData, errors.New("git log failed")
	}

	// Parse author and committer dates from Unix timestamp
	authorUnixDate, _ := strconv.ParseInt(outArray[1], 10, 64)
	committerUnixDate, _ := strconv.ParseInt(outArray[4], 10, 64)

	// Populate the LocalGitData struct with the parsed information
	gitData.CommitSha = outArray[0]
	gitData.AuthorDate = time.Unix(authorUnixDate, 0)
	gitData.AuthorName = outArray[2]
	gitData.AuthorEmail = outArray[3]
	gitData.CommitterDate = time.Unix(committerUnixDate, 0)
	gitData.CommitterName = outArray[5]
	gitData.CommitterEmail = outArray[6]
	gitData.CommitMessage = strings.Trim(outArray[7], "\n")
	return gitData, nil
}

// FetchCommitData retrieves commit data for a specific commit SHA in a shallow clone Git repository.
func FetchCommitData(commitSha string) (LocalCommitData, error) {
	commitData := LocalCommitData{}

	// let's do a first check to see if the repository is a shallow clone
	slog.Debug("testoptimization.FetchCommitData: checking if the repository is a shallow clone")
	isAShallowClone, err := isAShallowCloneRepository()
	if err != nil {
		return commitData, fmt.Errorf("testoptimization.FetchCommitData: error checking if the repository is a shallow clone: %s", err)
	}

	// if the git repo is a shallow clone, we try to fecth the commit sha data
	if isAShallowClone {
		// let's check the git version >= 2.27.0 (git --version) to see if we can unshallow the repository
		slog.Debug("testoptimization.FetchCommitData: checking the git version")
		major, minor, patch, err := getGitVersion()
		if err != nil {
			return commitData, fmt.Errorf("testoptimization.FetchCommitData: error getting the git version: %s", err)
		}
		slog.Debug("testoptimization.FetchCommitData: git version", "major", major, "minor", minor, "patch", patch)
		if major < 2 || (major == 2 && minor < 27) {
			slog.Debug("testoptimization.FetchCommitData: the git version is less than 2.27.0 we cannot unshallow the repository")
			return commitData, nil
		}

		// let's get the remote name
		remoteName, err := getRemoteName()
		if err != nil {
			return commitData, fmt.Errorf("testoptimization.FetchCommitData: error getting the remote name: %s\n%s", err, remoteName)
		}
		if remoteName == "" {
			// if the origin name is empty, we fallback to "origin"
			remoteName = "origin"
		}
		slog.Debug("testoptimization.FetchCommitData: remote name", "remoteName", remoteName)

		// let's fetch the missing commits and trees from a commit sha
		// git fetch --update-shallow --filter="blob:none" --recurse-submodules=no --no-write-fetch-head <remoteName> <commitSha>
		slog.Debug("testoptimization.FetchCommitData: fetching the missing commits and trees from the last month")
		if fetchOutput, fetchErr := execGitString(
			"fetch", "--update-shallow", "--filter=blob:none", "--recurse-submodules=no", "--no-write-fetch-head", remoteName, commitSha); fetchErr != nil {
			return commitData, fmt.Errorf("testoptimization.FetchCommitData: error: %s\n%s", fetchErr, fetchOutput)
		}
	}

	// Read only the selected commit; its parents may be missing locally.
	slog.Debug("testoptimization.git: getting the latest commit details")
	out, err := execGitString("log", "--no-walk=unsorted", "--no-show-signature", "--format=%H%x00%at%x00%an%x00%ae%x00%ct%x00%cn%x00%ce%x00%B", commitSha, "--")
	if err != nil {
		return commitData, err
	}

	// Split the output into individual components
	outArray := strings.SplitN(out, "\x00", 8)
	if len(outArray) < 8 {
		return commitData, errors.New("git log failed")
	}

	// Parse author and committer dates from Unix timestamp
	authorUnixDate, _ := strconv.ParseInt(outArray[1], 10, 64)
	committerUnixDate, _ := strconv.ParseInt(outArray[4], 10, 64)

	// Populate the LocalGitData struct with the parsed information
	commitData.CommitSha = outArray[0]
	commitData.AuthorDate = time.Unix(authorUnixDate, 0)
	commitData.AuthorName = outArray[2]
	commitData.AuthorEmail = outArray[3]
	commitData.CommitterDate = time.Unix(committerUnixDate, 0)
	commitData.CommitterName = outArray[5]
	commitData.CommitterEmail = outArray[6]
	commitData.CommitMessage = strings.Trim(outArray[7], "\n")

	slog.Debug("testoptimization.FetchCommitData: was completed successfully")
	return commitData, nil
}

// GetLastLocalGitCommitShas retrieves the commit SHAs of the last 1000 commits in the local Git repository.
func GetLastLocalGitCommitShas() []string {
	return NewCommandRunner(nil).GetLastLocalGitCommitShas()
}

// GetLastLocalGitCommitShas retrieves recent commit SHAs and records the Git command telemetry.
func (r *CommandRunner) GetLastLocalGitCommitShas() []string { return r.GetLocalGitCommitShas("") }

// GetLocalGitCommitShas enumerates the selected revision, retaining an old selected root.
func (r *CommandRunner) GetLocalGitCommitShas(commitSHA string) []string {
	commitSHA = strings.ToLower(commitSHA)
	if commitSHA != "" && !validUploadCommitSHA(commitSHA) {
		return nil
	}
	args := []string{"log", "--no-show-signature", "--format=%H", "-n", "1000", "--since=1 month ago"}
	if commitSHA != "" {
		args = append(args, commitSHA, "--")
	}
	out, err := r.execGitString(CommandGetLocalCommits, args...)
	if err != nil {
		return nil
	}
	commits := strings.Fields(out)
	if commitSHA != "" {
		if len(commits) == 0 {
			selected, readErr := execGitString("rev-parse", "--verify", commitSHA+"^{commit}")
			if readErr != nil || selected != commitSHA {
				return nil
			}
		}
		commits = append([]string{commitSHA}, slices.DeleteFunc(commits, func(sha string) bool { return sha == commitSHA })...)
	}
	if len(commits) > 1000 {
		commits = commits[:1000]
	}
	return commits
}

// UnshallowGitRepository converts a shallow clone into a complete clone by fetching all missing commits without git content (only commits and tree objects).
func UnshallowGitRepository() (bool, error) {
	return NewCommandRunner(nil).UnshallowGitRepository()
}

// UnshallowGitRepository fetches missing commit history and records the Git command telemetry.
func (r *CommandRunner) UnshallowGitRepository() (bool, error) {

	// let's do a first check to see if the repository is a shallow clone
	slog.Debug("testoptimization.unshallow: checking if the repository is a shallow clone")
	isAShallowClone, err := r.isAShallowCloneRepository()
	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error checking if the repository is a shallow clone: %s", err)
	}

	// if the git repo is not a shallow clone, we can return early
	if !isAShallowClone {
		slog.Debug("testoptimization.unshallow: the repository is not a shallow clone")
		return false, nil
	}

	// the git repo is a shallow clone, we need to double check if there are more than just 1 commit in the logs.
	slog.Debug("testoptimization.unshallow: the repository is a shallow clone, checking if there are more than one commit in the logs")
	hasMoreThanOneCommits, err := r.hasTheGitLogHaveMoreThanOneCommits()
	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error checking if the git log has more than one commit: %s", err)
	}

	// if there are more than 1 commits, we can return early
	if hasMoreThanOneCommits {
		slog.Debug("testoptimization.unshallow: the git log has more than one commits")
		return false, nil
	}

	// let's check the git version >= 2.27.0 (git --version) to see if we can unshallow the repository
	slog.Debug("testoptimization.unshallow: checking the git version")
	major, minor, patch, err := getGitVersion()
	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error getting the git version: %s", err)
	}
	slog.Debug("testoptimization.unshallow: git version", "major", major, "minor", minor, "patch", patch)
	if major < 2 || (major == 2 && minor < 27) {
		slog.Debug("testoptimization.unshallow: the git version is less than 2.27.0 we cannot unshallow the repository")
		return false, nil
	}

	// after asking for 2 logs lines, if the git log command returns just one commit sha, we reconfigure the repo
	// to ask for git commits and trees of the last month (no blobs)

	// let's get the remote name
	remoteName, err := r.getRemoteName()
	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error getting the remote name: %s\n%s", err, remoteName)
	}
	if remoteName == "" {
		// if the origin name is empty, we fallback to "origin"
		remoteName = "origin"
	}
	slog.Debug("testoptimization.unshallow: remote name", "remoteName", remoteName)

	// let's get the sha of the HEAD (git rev-parse HEAD)
	headSha, err := r.execGitString(CommandGetHead, "rev-parse", "HEAD")
	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error getting the HEAD sha: %s\n%s", err, headSha)
	}
	if headSha == "" {
		// if the HEAD is empty, we fallback to the current branch (git branch --show-current)
		headSha, err = r.execGitString(CommandGetBranch, "branch", "--show-current")
		if err != nil {
			return false, fmt.Errorf("testoptimization.unshallow: error getting the current branch: %s\n%s", err, headSha)
		}
	}
	slog.Debug("testoptimization.unshallow: HEAD sha", "headSha", headSha)

	// let's fetch the missing commits and trees from the last month
	// git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse HEAD)
	slog.Debug("testoptimization.unshallow: fetching the missing commits and trees from the last month")
	fetchOutput, err := r.execGitString(CommandUnshallow, "fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=blob:none", "--recurse-submodules=no", remoteName, headSha)

	// let's check if the last command was unsuccessful
	if err != nil {
		slog.Debug("testoptimization.unshallow: error fetching the missing commits and trees from the last month", "error", err.Error())
		// ***
		// The previous command has a drawback: if the local HEAD is a commit that has not been pushed to the remote, it will fail.
		// If this is the case, we fallback to: `git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse --abbrev-ref --symbolic-full-name @{upstream})`
		// This command will attempt to use the tracked branch for the current branch in order to unshallow.
		// ***

		// let's get the remote branch name: git rev-parse --abbrev-ref --symbolic-full-name @{upstream}
		var remoteBranchName string
		slog.Debug("testoptimization.unshallow: getting the remote branch name")
		remoteBranchName, err = r.execGitString(CommandUnshallow, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if err == nil {
			// let's try the alternative: git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName) $(git rev-parse --abbrev-ref --symbolic-full-name @{upstream})
			slog.Debug("testoptimization.unshallow: fetching the missing commits and trees from the last month using the remote branch name")
			fetchOutput, err = r.execGitString(CommandUnshallow, "fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=blob:none", "--recurse-submodules=no", remoteName, remoteBranchName)
		}
	}

	// let's check if the last command was unsuccessful
	if err != nil {
		slog.Debug("testoptimization.unshallow: error fetching the missing commits and trees from the last month", "error", err.Error())
		// ***
		// It could be that the CI is working on a detached HEAD or maybe branch tracking hasn't been set up.
		// In that case, this command will also fail, and we will finally fallback to we just unshallow all the things:
		// `git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName)`
		// ***

		// let's try the last fallback: git fetch --shallow-since="1 month ago" --update-shallow --filter="blob:none" --recurse-submodules=no $(git config --default origin --get clone.defaultRemoteName)
		slog.Debug("testoptimization.unshallow: fetching the missing commits and trees from the last month using the origin name")
		fetchOutput, err = r.execGitString(CommandUnshallow, "fetch", "--shallow-since=\"1 month ago\"", "--update-shallow", "--filter=blob:none", "--recurse-submodules=no", remoteName)
	}

	if err != nil {
		return false, fmt.Errorf("testoptimization.unshallow: error: %s\n%s", err, fetchOutput)
	}

	slog.Debug("testoptimization.unshallow: was completed successfully")
	tmpso := sync.Once{}
	isAShallowCloneRepositoryOnce.Store(&tmpso)
	return true, nil
}

// FilterSensitiveInfo removes sensitive information from a given URL using a regular expression.
// It replaces the user credentials part of the URL (if present) with an empty string.
//
// Parameters:
//
//	url - The URL string from which sensitive information should be filtered out.
//
// Returns:
//
//	The sanitized URL string with sensitive information removed.
func FilterSensitiveInfo(url string) string {
	return string(regexpSensitiveInfo.ReplaceAll([]byte(url), []byte("$1"))[:])
}

// isAShallowCloneRepository checks if the local Git repository is a shallow clone.
func isAShallowCloneRepository() (bool, error) {
	return NewCommandRunner(nil).isAShallowCloneRepository()
}

func (r *CommandRunner) isAShallowCloneRepository() (bool, error) {
	var fErr error
	var sOnce *sync.Once
	sOnce = isAShallowCloneRepositoryOnce.Load()
	if sOnce == nil {
		sOnce = &sync.Once{}
		isAShallowCloneRepositoryOnce.Store(sOnce)
	}
	sOnce.Do(func() {
		// git rev-parse --is-shallow-repository
		out, err := r.execGitString(CommandCheckShallow, "rev-parse", "--is-shallow-repository")
		if err != nil {
			isAShallowCloneRepositoryValue = false
			fErr = err
			return
		}

		isAShallowCloneRepositoryValue = strings.TrimSpace(out) == "true"
	})

	return isAShallowCloneRepositoryValue, fErr
}

// hasTheGitLogHaveMoreThanOneCommits checks if the local Git repository has more than one commit.
func hasTheGitLogHaveMoreThanOneCommits() (bool, error) {
	return NewCommandRunner(nil).hasTheGitLogHaveMoreThanOneCommits()
}

func (r *CommandRunner) hasTheGitLogHaveMoreThanOneCommits() (bool, error) {
	// git log --format=oneline -n 2
	out, err := r.execGitString(CommandCheckShallow, "log", "--format=oneline", "-n", "2")
	if err != nil || out == "" {
		return false, err
	}

	commitsCount := strings.Count(out, "\n") + 1
	return commitsCount > 1, nil
}

// getObjectsSha get the objects shas from the git repository based on the commits to include and exclude
func getObjectsSha(commitsToInclude []string, commitsToExclude []string) []string {
	return NewCommandRunner(nil).getObjectsSha(commitsToInclude, commitsToExclude)
}

func (r *CommandRunner) getObjectsSha(commitsToInclude []string, commitsToExclude []string) []string {
	// Enumerate only the selected positive revisions, with known history excluded.
	commitsToExcludeArgs := make([]string, len(commitsToExclude))
	for i, c := range commitsToExclude {
		commitsToExcludeArgs[i] = "^" + c
	}
	args := append([]string{"rev-list", "--objects", "--no-object-names", "--filter=blob:none", "--since=\"1 month ago\"", "--max-count=1000"}, append(commitsToExcludeArgs, commitsToInclude...)...)
	out, err := r.execGitString(CommandGetObjects, args...)
	if err != nil {
		return []string{}
	}

	objects := strings.Fields(out)
	if len(commitsToInclude) > 0 && !slices.Contains(objects, commitsToInclude[0]) && !slices.Contains(commitsToExclude, commitsToInclude[0]) {
		extraArgs := append([]string{"rev-list", "--objects", "--no-object-names", "--filter=blob:none", "--max-count=1"}, commitsToExcludeArgs...)
		extraArgs = append(extraArgs, commitsToInclude[0])
		extra, extraErr := r.execGitString(CommandGetObjects, extraArgs...)
		if extraErr != nil {
			return nil
		}
		for _, object := range strings.Fields(extra) {
			if !slices.Contains(objects, object) {
				objects = append(objects, object)
			}
		}
	}
	return objects
}

// CreatePackFiles creates pack files from the given commits to include and exclude.
func CreatePackFiles(commitsToInclude []string, commitsToExclude []string) []string {
	return NewCommandRunner(nil).CreatePackFiles(commitsToInclude, commitsToExclude)
}

// CreatePackFiles creates pack files and records the Git command telemetry.
func (r *CommandRunner) CreatePackFiles(commitsToInclude []string, commitsToExclude []string) []string {
	// get the objects shas to send
	objectsShas := r.getObjectsSha(commitsToInclude, commitsToExclude)
	if len(objectsShas) == 0 {
		slog.Debug("testoptimization: no objects found to send")
		return nil
	}

	// create the objects shas string
	var objectsShasString string
	for _, objectSha := range objectsShas {
		objectsShasString += objectSha + "\n"
	}

	workingDirectory := func() string {
		wd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return wd
	}

	var temporaryPath string
	var out string
	var err error

	// Git can throw a cross device error if the temporal folder is in a different drive than the .git folder (eg. symbolic link)
	// to handle this edge case, we first try with a temp folder and if we fail then we try in the working directory folder.
	for _, folder := range []string{"", workingDirectory()} {
		// get a temporary path to store the pack files
		temporaryPath, err = os.MkdirTemp(folder, ".dd-pack-objects")
		if err != nil {
			slog.Warn("testoptimization: error creating temporary directory", "folder", folder, "error", err.Error())
			continue
		}

		// git pack-objects --compression=9 --max-pack-size={MaxPackFileSizeInMb}m "{temporaryPath}"
		out, err = r.execGitStringWithInput(CommandPackObjects, objectsShasString,
			"pack-objects", "--compression=9", "--max-pack-size="+strconv.Itoa(MaxPackFileSizeInMb)+"m", temporaryPath+"/")
		if err == nil {
			break
		}
		_ = os.RemoveAll(temporaryPath)
	}

	if err != nil {
		slog.Warn("testoptimization: error creating pack files in", "temporaryPath", temporaryPath, "error", err.Error(), "output", out)
		return nil
	}

	// construct the full path to the pack files
	var packFiles []string
	for _, packFile := range strings.Split(out, "\n") {
		file := filepath.Join(temporaryPath, fmt.Sprintf("-%s.pack", packFile))

		// check if the pack file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			slog.Warn("testoptimization: pack file not found", "file", file)
			continue
		}

		packFiles = append(packFiles, file)
	}
	if len(packFiles) == 0 {
		_ = os.RemoveAll(temporaryPath)
	}

	return packFiles
}

// getParentGitFolder searches from the given directory upwards to find the nearest .git directory.
func getParentGitFolder(innerFolder string) (string, error) {
	if innerFolder == "" {
		return "", nil
	}

	dir := innerFolder
	for {
		gitDirPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitDirPath)
		if err == nil && info.IsDir() {
			return gitDirPath, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}

		parentDir := filepath.Dir(dir)
		// If we've reached the root directory, stop the loop.
		if parentDir == dir {
			break
		}
		dir = parentDir
	}

	return "", nil
}

// getRemoteName determines the remote name.
func getRemoteName() (string, error) {
	return NewCommandRunner(nil).getRemoteName()
}

func (r *CommandRunner) getRemoteName() (string, error) {
	remotes, err := r.execGitString(CommandGetRemote, "remote")
	if err != nil {
		return "origin", nil
	}
	names := strings.Fields(remotes)
	if len(names) <= 1 {
		if len(names) == 1 {
			return names[0], nil
		}
		return "origin", nil
	}
	branch, _ := r.execGitString(CommandGetRemoteUpstreamTracking, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "" && branch != "HEAD" {
		configured, _ := r.execGitString(CommandGetRemoteUpstreamTracking, "config", "--default", "", "--get", "branch."+branch+".remote")
		if configured != "." && slices.Contains(names, configured) {
			return configured, nil
		}
	}
	snapshot, err := r.execGitString(CommandGetRemote, "config", "--local", "--includes", "--null", "--list")
	if err != nil {
		return "", err
	}
	configuration := map[string]string{}
	for _, entry := range strings.Split(snapshot, "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		configuration[key] = value
	}
	if configured := configuration["clone.defaultremotename"]; slices.Contains(names, configured) {
		return configured, nil
	}
	tracking := map[string]bool{}
	for key, value := range configuration {
		if strings.HasPrefix(key, "branch.") && strings.HasSuffix(key, ".remote") && slices.Contains(names, value) {
			tracking[value] = true
		}
	}
	if len(tracking) == 1 {
		for remote := range tracking {
			return remote, nil
		}
	}
	if slices.Contains(names, "origin") {
		return "origin", nil
	}
	return names[0], nil
}

// gitOutput keeps successful stderr diagnostics out of paths and commit identities.
func gitOutput(cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return append(out, stderr.Bytes()...), err
	}
	return out, nil
}

// ResetCheckout forgets the command context for the next metadata snapshot.
// Callers must finish the previous checkout's Git operations before resetting it.
func ResetCheckout() {
	gitCommandMutex.Lock()
	defer gitCommandMutex.Unlock()
	safeDirectoryOnce = sync.Once{}
	safeDirectoryValue = ""
	gitWorkingDirectory = ""
	isAShallowCloneRepositoryOnce.Store(nil)
	isAShallowCloneRepositoryValue = false
}

func validUploadCommitSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	return strings.Trim(sha, "0123456789abcdef") == ""
}
