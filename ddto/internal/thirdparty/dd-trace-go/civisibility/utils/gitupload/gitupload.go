// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package gitupload uploads the repository's missing commits. It holds the
// SDK's integrations code, so that ddto's Git worker can run the upload once
// per invocation without the test runtime.
package gitupload

import (
	"fmt"
	"slices"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Client is the part of the CI Visibility client that the upload uses.
type Client interface {
	GetCommits(localCommits []string) ([]string, error)
	SendPackFiles(commitSha string, packFiles []string) (bytes int64, err error)
}

// SearchCommitsResponse compares the local commits with the backend's.
type SearchCommitsResponse struct {
	LocalCommits  []string
	RemoteCommits []string
	IsOk          bool
}

// Hooks are the upload's steps.
type Hooks struct {
	// GetSearchCommits retrieves the local/remote commit comparison for the current repository state.
	GetSearchCommits func() (*SearchCommitsResponse, error)
	// UnshallowGitRepository expands shallow clones before computing the final missing commits.
	UnshallowGitRepository func() (bool, error)
	// SendObjectsPackFile uploads a git packfile containing the missing commits.
	SendObjectsPackFile func(string, []string, []string) (int64, error)
}

// ClientHooks are the default steps against client.
func ClientHooks(client Client) Hooks {
	return Hooks{
		GetSearchCommits:       func() (*SearchCommitsResponse, error) { return SearchCommits(client) },
		UnshallowGitRepository: utils.UnshallowGitRepository,
		SendObjectsPackFile: func(commitSha string, include, exclude []string) (int64, error) {
			return SendObjectsPackFile(client, commitSha, include, exclude)
		},
	}
}

// Upload runs the default git upload flow with hooks.
func Upload(hooks Hooks) (bytes int64, err error) {
	// get the search commits response
	initialCommitData, err := hooks.GetSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err)
	}

	// let's check if we could retrieve commit data
	if !initialCommitData.IsOk {
		return 0, nil
	}

	// if there are no commits then we don't need to do anything
	if !initialCommitData.HasCommits() {
		log.Debug("civisibility: no commits found")
		return 0, nil
	}

	// Calculate the initial missing commits once and reuse the same ordered list if
	// the repository cannot be unshallowed. MissingCommits walks the local and
	// remote commit lists, so calling it twice here would repeat identical work.
	initialMissingCommits := initialCommitData.MissingCommits()

	// If there are not missing commits (backend has the total number of local commits already), then we are good to go
	// with it, we don't need to check if we need to unshallow or anything and just go with that.
	if len(initialMissingCommits) == 0 {
		log.Debug("civisibility: initial commit data has everything already, we don't need to upload anything")
		return 0, nil
	}

	// there's some missing commits on the backend, first we need to check if we need to unshallow before sending anything...
	hasBeenUnshallowed, err := hooks.UnshallowGitRepository()
	if err != nil || !hasBeenUnshallowed {
		if err != nil {
			log.Warn("%s", err.Error())
		}
		// if unshallowing the repository failed or if there's nothing to unshallow then we try to upload the packfiles from
		// the initial commit data

		// send the pack file with the missing commits
		return hooks.SendObjectsPackFile(initialCommitData.LocalCommits[0], initialMissingCommits, initialCommitData.RemoteCommits)
	}

	// after unshallowing the repository we need to get the search commits to calculate the missing commits again
	commitsData, err := hooks.GetSearchCommits()
	if err != nil {
		return 0, fmt.Errorf("civisibility: error getting the search commits response: %s", err)
	}

	// let's check if we could retrieve commit data
	if !commitsData.IsOk {
		return 0, nil
	}

	// send the pack file with the missing commits
	return hooks.SendObjectsPackFile(commitsData.LocalCommits[0], commitsData.MissingCommits(), commitsData.RemoteCommits)
}

// SearchCommits gets the search commits response with the local and remote commits
func SearchCommits(client Client) (*SearchCommitsResponse, error) {
	localCommits := utils.GetLastLocalGitCommitShas()
	if len(localCommits) == 0 {
		log.Debug("civisibility: no local commits found")
		return NewSearchCommitsResponse(nil, nil, false), nil
	}

	log.Debug("civisibility: local commits found: %d", len(localCommits))
	remoteCommits, err := client.GetCommits(localCommits)
	return NewSearchCommitsResponse(localCommits, remoteCommits, true), err
}

// NewSearchCommitsResponse creates a new search commits response
func NewSearchCommitsResponse(localCommits []string, remoteCommits []string, isOk bool) *SearchCommitsResponse {
	return &SearchCommitsResponse{
		LocalCommits:  localCommits,
		RemoteCommits: remoteCommits,
		IsOk:          isOk,
	}
}

// HasCommits returns true if the search commits response has commits
func (r *SearchCommitsResponse) HasCommits() bool {
	return len(r.LocalCommits) > 0
}

// MissingCommits returns the missing commits between the local and remote commits
func (r *SearchCommitsResponse) MissingCommits() []string {
	var missingCommits []string
	for _, localCommit := range r.LocalCommits {
		if !slices.Contains(r.RemoteCommits, localCommit) {
			missingCommits = append(missingCommits, localCommit)
		}
	}

	return missingCommits
}

// SendObjectsPackFile uploads one packfile chunk for the requested commit graph slice and reports the number of bytes sent.
func SendObjectsPackFile(client Client, commitSha string, commitsToInclude []string, commitsToExclude []string) (bytes int64, err error) {
	// get the pack files to send
	packFiles := utils.CreatePackFiles(commitsToInclude, commitsToExclude)
	if len(packFiles) == 0 {
		log.Debug("civisibility: no pack files to send")
		return 0, nil
	}

	// send the pack files
	log.Debug("civisibility: sending pack file with missing commits. files: %v", packFiles) //nolint:gocritic // File list logging for debugging

	// remove the pack files and their temporary directory after sending them
	defer utils.RemovePackFiles(packFiles)

	// send the pack files
	return client.SendPackFiles(commitSha, packFiles)
}
