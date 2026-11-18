// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"encoding/hex"
	"maps"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/env"
)

var mergeRef = regexp.MustCompile("^refs/pull/[0-9]+/merge$")
var numericPR = regexp.MustCompile("^[0-9]+$")

func fullCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeCommitSHAs(tags map[string]string) {
	for _, key := range []string{constants.GitCommitSHA, constants.GitHeadCommit, constants.GitPrBaseCommit,
		constants.GitPrBaseHeadCommit, constants.CIGitMergeCommitSHA} {
		if fullCommitSHA(tags[key]) {
			tags[key] = strings.ToLower(tags[key])
		}
	}
}

func addMergeCommitTag(tags map[string]string) {
	var sha string
	switch tags[constants.CIProviderName] {
	case "github":
		if mergeRef.MatchString(env.Get("GITHUB_REF")) {
			sha = env.Get("GITHUB_SHA")
		}
	case "gitlab":
		event := env.Get("CI_MERGE_REQUEST_EVENT_TYPE")
		if env.Get("CI_MERGE_REQUEST_IID") != "" && (event == "merged_result" || event == "merge_train") {
			sha = env.Get("CI_COMMIT_SHA")
		}
	case "azurepipelines":
		if env.Get("BUILD_REASON") == "PullRequest" && mergeRef.MatchString(env.Get("BUILD_SOURCEBRANCH")) {
			sha = env.Get("BUILD_SOURCEVERSION")
		}
	case "travisci":
		if env.Get("TRAVIS_EVENT_TYPE") == "pull_request" && numericPR.MatchString(env.Get("TRAVIS_PULL_REQUEST")) {
			sha = env.Get("TRAVIS_COMMIT")
		}
	}
	if fullCommitSHA(sha) {
		tags[constants.CIGitMergeCommitSHA] = strings.ToLower(sha)
	}
}

func userGitTags() map[string]string {
	tags := map[string]string{}
	replaceWithUserSpecificTags(tags)
	normalizeTags(tags)
	for key, value := range tags {
		if strings.TrimSpace(value) == "" {
			delete(tags, key)
		}
	}
	return tags
}

func canonicalCheckoutPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func customCheckout(root, workspace string) bool {
	return root != "" && workspace != "" && canonicalCheckoutPath(root) != canonicalCheckoutPath(workspace)
}

// applyCheckoutMetadata keeps a readable checkout as one source of Git identity.
func applyCheckoutMetadata(tags map[string]string, data localGitData, provider map[string]string) {
	user := userGitTags()
	normalizeCommitSHAs(tags)
	providerSHA := provider[constants.GitCommitSHA]
	workspace := tags[constants.CIWorkspacePath]
	custom := customCheckout(data.SourceRoot, workspace)
	branch := data.Branch
	if branch == "HEAD" {
		branch = ""
	}
	if data.CommitSha != "" {
		identity := map[string]string{
			constants.GitCommitSHA:            data.CommitSha,
			constants.GitRepositoryURL:        data.RepositoryURL,
			constants.GitBranch:               branch,
			constants.GitTag:                  data.Tag,
			constants.GitCommitMessage:        data.CommitMessage,
			constants.GitCommitAuthorName:     data.AuthorName,
			constants.GitCommitAuthorEmail:    data.AuthorEmail,
			constants.GitCommitCommitterName:  data.CommitterName,
			constants.GitCommitCommitterEmail: data.CommitterEmail,
		}
		if !data.AuthorDate.IsZero() {
			identity[constants.GitCommitAuthorDate] = data.AuthorDate.String()
		}
		if !data.CommitterDate.IsZero() {
			identity[constants.GitCommitCommitterDate] = data.CommitterDate.String()
		}
		if branch != "" {
			delete(identity, constants.GitTag)
		}
		normalizeCommitSHAs(identity)
		matched := provider[constants.CIWorkspacePath] != "" && data.SourceRoot != "" && !custom && data.Branch == "HEAD" &&
			canonicalCheckoutPath(data.SourceRoot) == canonicalCheckoutPath(provider[constants.CIWorkspacePath]) &&
			(identity[constants.GitCommitSHA] == providerSHA || identity[constants.GitCommitSHA] == provider[constants.CIGitMergeCommitSHA] ||
				identity[constants.GitCommitSHA] == provider[constants.GitHeadCommit])
		if matched {
			for _, key := range []string{constants.GitBranch, constants.GitTag, constants.GitRepositoryURL} {
				if provider[key] != "" {
					identity[key] = provider[key]
				}
			}
		}
		for key := range tags {
			if strings.HasPrefix(key, "git.") && !strings.HasPrefix(key, "git.commit.head.") && !strings.HasPrefix(key, "git.pull_request.") {
				delete(tags, key)
			}
		}
		maps.Copy(tags, identity)
	}
	if data.SourceRoot != "" {
		tags[constants.GitCheckoutRoot] = data.SourceRoot
		if workspace == "" {
			tags[constants.CIWorkspacePath] = data.SourceRoot
		}
	}
	if custom {
		tags[constants.GitCheckoutIsCustom] = "true"
	}
	collectedSHA := data.CommitSha
	if collectedSHA == "" {
		collectedSHA = tags[constants.GitCommitSHA]
	}
	if fullCommitSHA(collectedSHA) {
		collectedSHA = strings.ToLower(collectedSHA)
	}
	if sha := user[constants.GitCommitSHA]; sha != "" && sha != collectedSHA {
		for key := range tags {
			if key == constants.GitTag || (strings.HasPrefix(key, "git.commit.") && key != constants.GitCommitSHA && !strings.HasPrefix(key, "git.commit.head.")) {
				delete(tags, key)
			}
		}
	}
	if head := user[constants.GitHeadCommit]; head != "" && head != tags[constants.GitHeadCommit] {
		for key := range tags {
			if strings.HasPrefix(key, "git.commit.head.") {
				delete(tags, key)
			}
		}
	}
	maps.Copy(tags, user)
}

// checkoutOperationTags keeps outer workflow relationships out of another checkout's Git operations.
func checkoutOperationTags(tags, provider map[string]string) map[string]string {
	if tags[constants.GitCheckoutIsCustom] != "true" {
		sha := tags[constants.GitCommitSHA]
		if tags[constants.GitCheckoutRoot] == "" || sha == "" ||
			sha == provider[constants.GitCommitSHA] || sha == provider[constants.GitHeadCommit] || sha == provider[constants.CIGitMergeCommitSHA] ||
			(len(provider) == 0 && tags[constants.GitHeadCommit] == "") {
			return tags
		}
	}
	user := userGitTags()
	selected := maps.Clone(tags)
	for key := range selected {
		if (strings.HasPrefix(key, "git.commit.head.") && user[constants.GitHeadCommit] == "") ||
			(strings.HasPrefix(key, "git.pull_request.") && user[key] == "") {
			delete(selected, key)
		}
	}
	return selected
}

// GetGitCheckoutTags returns the selected revisions for requests and local Git operations.
func GetGitCheckoutTags() map[string]string {
	tags := GetCITagsReadOnly()
	ciTagsMutex.Lock()
	provider := originalProviderTags
	ciTagsMutex.Unlock()
	return checkoutOperationTags(tags, provider)
}

// GetCITagsSourceRoot returns the selected checkout root, or the CI workspace without local Git.
func GetCITagsSourceRoot() string {
	tags := GetCITagsReadOnly()
	if root := tags[constants.GitCheckoutRoot]; root != "" {
		return root
	}
	return tags[constants.CIWorkspacePath]
}
