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

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/osinfo"
)

type (
	localCommitData = git.LocalCommitData
	localGitData    = git.LocalGitData
)

var (
	// ciTags holds the CI/CD environment variable information.
	currentCiTags  map[string]string // currentCiTags holds the CI/CD tags after originalCiTags + addedTags
	originalCiTags map[string]string // originalCiTags holds the original CI/CD tags after all the CMDs
	addedTags      map[string]string // addedTags holds the tags added by the user
	ciTagsMutex    sync.Mutex

	getProviderTagsFunc                  = getProviderTags
	getLocalGitDataFunc                  = git.GetLocalGitData
	fetchCommitDataFunc                  = git.FetchCommitData
	applyEnvironmentalDataIfRequiredFunc = applyEnvironmentalDataIfRequired
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
	slog.Debug("civisibility: os platform", "platform", runtime.GOOS)
	slog.Debug("civisibility: os architecture", "architecture", runtime.GOARCH)
	slog.Debug("civisibility: runtime version", "version", runtime.Version())

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
	localTags[constants.TestCommand] = cmd
	slog.Debug("civisibility: test command", "command", cmd)

	// Populate the test session name
	if testSessionName, ok := os.LookupEnv(constants.TestOptimizationTestSessionNameEnvironmentVariable); ok {
		localTags[constants.TestSessionName] = testSessionName
	} else if jobName, ok := localTags[constants.CIJobName]; ok {
		localTags[constants.TestSessionName] = fmt.Sprintf("%s-%s", jobName, cmd)
	} else {
		localTags[constants.TestSessionName] = cmd
	}
	slog.Debug("civisibility: test session name", "testSessionName", localTags[constants.TestSessionName])

	// Check if the user provided the test service
	if ddService := os.Getenv("DD_SERVICE"); ddService != "" {
		localTags[constants.UserProvidedTestServiceTag] = "true"
	} else {
		localTags[constants.UserProvidedTestServiceTag] = "false"
	}

	// Populate missing git data
	gitData, _ := getLocalGitDataFunc()

	// Populate Git metadata from the local Git repository if not already present in localTags
	if _, ok := localTags[constants.CIWorkspacePath]; !ok {
		localTags[constants.CIWorkspacePath] = gitData.SourceRoot
	}
	if _, ok := localTags[git.GitRepositoryURL]; !ok {
		localTags[git.GitRepositoryURL] = gitData.RepositoryURL
	}
	if _, ok := localTags[git.GitCommitSHA]; !ok {
		localTags[git.GitCommitSHA] = gitData.CommitSha
	}
	if _, ok := localTags[git.GitBranch]; !ok {
		localTags[git.GitBranch] = gitData.Branch
	}

	// If the commit SHA matches, populate additional Git metadata
	if localTags[git.GitCommitSHA] == gitData.CommitSha {
		if _, ok := localTags[git.GitCommitAuthorDate]; !ok {
			localTags[git.GitCommitAuthorDate] = gitData.AuthorDate.String()
		}
		if _, ok := localTags[git.GitCommitAuthorName]; !ok {
			localTags[git.GitCommitAuthorName] = gitData.AuthorName
		}
		if _, ok := localTags[git.GitCommitAuthorEmail]; !ok {
			localTags[git.GitCommitAuthorEmail] = gitData.AuthorEmail
		}
		if _, ok := localTags[git.GitCommitCommitterDate]; !ok {
			localTags[git.GitCommitCommitterDate] = gitData.CommitterDate.String()
		}
		if _, ok := localTags[git.GitCommitCommitterName]; !ok {
			localTags[git.GitCommitCommitterName] = gitData.CommitterName
		}
		if _, ok := localTags[git.GitCommitCommitterEmail]; !ok {
			localTags[git.GitCommitCommitterEmail] = gitData.CommitterEmail
		}
		if _, ok := localTags[git.GitCommitMessage]; !ok {
			localTags[git.GitCommitMessage] = gitData.CommitMessage
		}
	}

	// If the head commit SHA is available, populate additional Git head metadata
	if headCommitSha, ok := localTags[git.GitHeadCommit]; ok {
		if headCommitData, err := fetchCommitDataFunc(headCommitSha); err != nil {
			slog.Warn("civisibility: failed to fetch head commit data", "headCommitSha", headCommitSha, "error", err.Error())
		} else if headCommitSha == headCommitData.CommitSha {
			localTags[git.GitHeadAuthorDate] = headCommitData.AuthorDate.String()
			localTags[git.GitHeadAuthorName] = headCommitData.AuthorName
			localTags[git.GitHeadAuthorEmail] = headCommitData.AuthorEmail
			localTags[git.GitHeadCommitterDate] = headCommitData.CommitterDate.String()
			localTags[git.GitHeadCommitterName] = headCommitData.CommitterName
			localTags[git.GitHeadCommitterEmail] = headCommitData.CommitterEmail
			localTags[git.GitHeadMessage] = headCommitData.CommitMessage
		} else {
			slog.Warn("civisibility: head commit SHA does not match fetched commit SHA", "headCommitSha", headCommitSha, "fetchedCommitSha", headCommitData.CommitSha)
		}
	}

	// Apply environmental data if is available
	applyEnvironmentalDataIfRequiredFunc(localTags)

	slog.Debug("civisibility: workspace directory", "path", localTags[constants.CIWorkspacePath])
	slog.Debug("civisibility: common tags created", "items", len(localTags))
	return localTags
}
