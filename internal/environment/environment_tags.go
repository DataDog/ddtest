// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package environment

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/utils/osinfo"
)

type (
	localCommitData = git.LocalCommitData
	localGitData    = git.LocalGitData
)

const (
	testCommandTag             = "test.command"
	testSessionNameTag         = "test_session.name"
	userProvidedTestServiceTag = "_dd.test.is_user_provided_service"
	gitHeadAuthorDateTag       = "git.commit.head.author.date"
	gitHeadAuthorEmailTag      = "git.commit.head.author.email"
	gitHeadAuthorNameTag       = "git.commit.head.author.name"
	gitHeadCommitterDateTag    = "git.commit.head.committer.date"
	gitHeadCommitterEmailTag   = "git.commit.head.committer.email"
	gitHeadCommitterNameTag    = "git.commit.head.committer.name"
)

var (
	// ciTags holds the CI/CD environment variable information.
	currentCiTags  map[string]string // currentCiTags holds the CI/CD tags after originalCiTags + addedTags
	originalCiTags map[string]string // originalCiTags holds the original CI/CD tags after all the CMDs
	addedTags      map[string]string // addedTags holds the tags added by the user
	ciTagsMutex    sync.Mutex

	getProviderTagsFunc = getProviderTags
	getLocalGitDataFunc = git.GetLocalGitData
	fetchCommitDataFunc = git.FetchCommitData
)

// GetCITags retrieves and caches the CI/CD tags from environment variables.
// It initializes the ciTags map if it is not already initialized.
// This function is thread-safe due to the use of a mutex.
//
// Returns:
//
//	A map[string]string containing the CI/CD tags.
func GetCITags() map[string]string {
	ciTagsMutex.Lock()
	defer ciTagsMutex.Unlock()

	// Return the current tags if they are already initialized
	if currentCiTags != nil {
		return currentCiTags
	}

	if originalCiTags == nil {
		// If the original tags are not initialized, create them
		originalCiTags = createCITagsMap()
	}

	// Create a new map with the added tags
	newTags := maps.Clone(originalCiTags)
	maps.Copy(newTags, addedTags)

	// Update the current tags
	currentCiTags = newTags
	return currentCiTags
}

// AddCITagsMap adds a new map of tags to the CI/CD tags map.
func AddCITagsMap(tags map[string]string) {
	if tags == nil {
		return
	}

	ciTagsMutex.Lock()
	defer ciTagsMutex.Unlock()

	// Add the tag to the added tags dictionary
	if addedTags == nil {
		addedTags = make(map[string]string)
	}
	maps.Copy(addedTags, tags)

	// Reset the current tags
	currentCiTags = nil
}

// ResetCITags resets the CI/CD tags to their original values.
func ResetCITags() {
	ciTagsMutex.Lock()
	defer ciTagsMutex.Unlock()

	originalCiTags = nil
	currentCiTags = nil
	addedTags = nil
}

// createCITagsMap creates a map of CI/CD tags by extracting information from environment variables and the local Git repository.
// It also adds OS and runtime information to the tags.
//
// Returns:
//
//	A map[string]string containing the extracted CI/CD tags.
func createCITagsMap() map[string]string {
	localTags := getProviderTagsFunc()

	// Populate runtime values
	localTags[constants.OSPlatform] = runtime.GOOS
	localTags[constants.OSVersion] = osinfo.OSVersion()
	localTags[constants.OSArchitecture] = runtime.GOARCH
	localTags[constants.RuntimeName] = runtime.Compiler
	localTags[constants.RuntimeVersion] = runtime.Version()
	slog.Debug("testoptimization: os platform", "platform", runtime.GOOS)
	slog.Debug("testoptimization: os architecture", "architecture", runtime.GOARCH)
	slog.Debug("testoptimization: runtime version", "version", runtime.Version())

	// Get command line test command
	var cmd string
	if len(os.Args) == 1 {
		cmd = filepath.Base(os.Args[0])
	} else {
		cmd = fmt.Sprintf("%s %s ", filepath.Base(os.Args[0]), strings.Join(os.Args[1:], " "))
	}

	// Filter out some parameters to make the command more stable.
	cmd = regexp.MustCompile(`(?si)-test.gocoverdir=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`(?si)-test.v=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = regexp.MustCompile(`(?si)-test.testlogfile=(.*)\s`).ReplaceAllString(cmd, "")
	cmd = strings.TrimSpace(cmd)
	localTags[testCommandTag] = cmd
	slog.Debug("testoptimization: test command", "command", cmd)

	// Populate the test session name
	if testSessionName, ok := os.LookupEnv(constants.TestOptimizationTestSessionNameEnvironmentVariable); ok {
		localTags[testSessionNameTag] = testSessionName
	} else if jobName, ok := localTags[constants.CIJobName]; ok {
		localTags[testSessionNameTag] = fmt.Sprintf("%s-%s", jobName, cmd)
	} else {
		localTags[testSessionNameTag] = cmd
	}
	slog.Debug("testoptimization: test session name", "testSessionName", localTags[testSessionNameTag])

	// Check if the user provided the test service
	if ddService := os.Getenv("DD_SERVICE"); ddService != "" {
		localTags[userProvidedTestServiceTag] = "true"
	} else {
		localTags[userProvidedTestServiceTag] = "false"
	}

	// Populate missing git data
	gitData, _ := getLocalGitDataFunc()

	// Populate Git metadata from the local Git repository if not already present in localTags
	if _, ok := localTags[constants.CIWorkspacePath]; !ok {
		localTags[constants.CIWorkspacePath] = gitData.SourceRoot
	}
	if _, ok := localTags[constants.GitRepositoryURL]; !ok {
		localTags[constants.GitRepositoryURL] = gitData.RepositoryURL
	}
	if _, ok := localTags[constants.GitCommitSHA]; !ok {
		localTags[constants.GitCommitSHA] = gitData.CommitSha
	}
	if _, ok := localTags[constants.GitBranch]; !ok {
		localTags[constants.GitBranch] = gitData.Branch
	}

	// If the commit SHA matches, populate additional Git metadata
	if localTags[constants.GitCommitSHA] == gitData.CommitSha {
		if _, ok := localTags[constants.GitCommitAuthorDate]; !ok {
			localTags[constants.GitCommitAuthorDate] = gitData.AuthorDate.String()
		}
		if _, ok := localTags[constants.GitCommitAuthorName]; !ok {
			localTags[constants.GitCommitAuthorName] = gitData.AuthorName
		}
		if _, ok := localTags[constants.GitCommitAuthorEmail]; !ok {
			localTags[constants.GitCommitAuthorEmail] = gitData.AuthorEmail
		}
		if _, ok := localTags[constants.GitCommitCommitterDate]; !ok {
			localTags[constants.GitCommitCommitterDate] = gitData.CommitterDate.String()
		}
		if _, ok := localTags[constants.GitCommitCommitterName]; !ok {
			localTags[constants.GitCommitCommitterName] = gitData.CommitterName
		}
		if _, ok := localTags[constants.GitCommitCommitterEmail]; !ok {
			localTags[constants.GitCommitCommitterEmail] = gitData.CommitterEmail
		}
		if _, ok := localTags[constants.GitCommitMessage]; !ok {
			localTags[constants.GitCommitMessage] = gitData.CommitMessage
		}
	}

	// If the head commit SHA is available, populate additional Git head metadata
	if headCommitSha, ok := localTags[constants.GitHeadCommit]; ok {
		if headCommitData, err := fetchCommitDataFunc(headCommitSha); err != nil {
			slog.Warn("testoptimization: failed to fetch head commit data", "headCommitSha", headCommitSha, "error", err.Error())
		} else if headCommitSha == headCommitData.CommitSha {
			localTags[gitHeadAuthorDateTag] = headCommitData.AuthorDate.String()
			localTags[gitHeadAuthorNameTag] = headCommitData.AuthorName
			localTags[gitHeadAuthorEmailTag] = headCommitData.AuthorEmail
			localTags[gitHeadCommitterDateTag] = headCommitData.CommitterDate.String()
			localTags[gitHeadCommitterNameTag] = headCommitData.CommitterName
			localTags[gitHeadCommitterEmailTag] = headCommitData.CommitterEmail
			localTags[constants.GitHeadMessage] = headCommitData.CommitMessage
		} else {
			slog.Warn("testoptimization: head commit SHA does not match fetched commit SHA", "headCommitSha", headCommitSha, "fetchedCommitSha", headCommitData.CommitSha)
		}
	}

	slog.Debug("testoptimization: workspace directory", "path", localTags[constants.CIWorkspacePath])
	slog.Debug("testoptimization: common tags created", "items", len(localTags))
	return localTags
}
