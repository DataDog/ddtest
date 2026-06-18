// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package environment

import (
	"testing"

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/git"

	"github.com/stretchr/testify/assert"
)

func TestGetCITagsCache(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	tags["key"] = "newvalue"
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
}

func TestAddCITagsMap(t *testing.T) {
	ResetCITags()
	originalCiTags = map[string]string{"key": "value"}

	// First call to initialize ciTags
	tags := GetCITags()
	assert.Equal(t, "value", tags["key"])

	nmap := map[string]string{}
	nmap["key"] = "newvalue"
	nmap["key2"] = "value2"
	AddCITagsMap(nmap)
	tags = GetCITags()
	assert.Equal(t, "newvalue", tags["key"])
	assert.Equal(t, "value2", tags["key2"])
}

func TestGetCITagsUsesGitEnrichment(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	var getLocalGitDataCalls int
	var fetchCommitDataCalls int
	var applyEnvironmentalDataCalls int

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{
			constants.CIJobName: "job-name",
			git.GitHeadCommit:   "head-sha",
		}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		getLocalGitDataCalls++
		return localGitData{
			LocalCommitData: localCommitData{
				CommitSha:     "commit-sha",
				CommitMessage: "commit-message",
			},
			SourceRoot:    "/tmp/workspace",
			RepositoryURL: "https://example.com/repo.git",
			Branch:        "main",
		}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		fetchCommitDataCalls++
		assert.Equal(t, "head-sha", commitSha)
		return localCommitData{
			CommitSha:     "head-sha",
			CommitMessage: "head-message",
		}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {
		applyEnvironmentalDataCalls++
		tags["env.applied"] = "true"
	}

	tags := GetCITags()
	assert.Equal(t, 1, getLocalGitDataCalls)
	assert.Equal(t, 1, fetchCommitDataCalls)
	assert.Equal(t, 1, applyEnvironmentalDataCalls)
	assert.Equal(t, "/tmp/workspace", tags[constants.CIWorkspacePath])
	assert.Equal(t, "https://example.com/repo.git", tags[git.GitRepositoryURL])
	assert.Equal(t, "commit-sha", tags[git.GitCommitSHA])
	assert.Equal(t, "head-message", tags[git.GitHeadMessage])
	assert.Equal(t, "true", tags["env.applied"])
}

func TestGetCITagsDoesNotAddProviderWhenProviderCannotBeDetected(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)

	originalGetProviderTagsFunc := getProviderTagsFunc
	originalGetLocalGitDataFunc := getLocalGitDataFunc
	originalFetchCommitDataFunc := fetchCommitDataFunc
	originalApplyEnvironmentalDataIfRequiredFunc := applyEnvironmentalDataIfRequiredFunc
	t.Cleanup(func() {
		getProviderTagsFunc = originalGetProviderTagsFunc
		getLocalGitDataFunc = originalGetLocalGitDataFunc
		fetchCommitDataFunc = originalFetchCommitDataFunc
		applyEnvironmentalDataIfRequiredFunc = originalApplyEnvironmentalDataIfRequiredFunc
	})

	getProviderTagsFunc = func() map[string]string {
		return map[string]string{}
	}
	getLocalGitDataFunc = func() (localGitData, error) {
		return localGitData{}, nil
	}
	fetchCommitDataFunc = func(commitSha string) (localCommitData, error) {
		return localCommitData{}, nil
	}
	applyEnvironmentalDataIfRequiredFunc = func(tags map[string]string) {}

	tags := GetCITags()
	assert.NotContains(t, tags, constants.CIProviderName)
}
