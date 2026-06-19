// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// GitBranch indicates the current git branch.
	GitBranch = "git.branch"

	// GitCommitAuthorDate indicates the git commit author date related to the build.
	GitCommitAuthorDate = "git.commit.author.date"

	// GitCommitAuthorEmail indicates the git commit author email related to the build.
	GitCommitAuthorEmail = "git.commit.author.email"

	// GitCommitAuthorName indicates the git commit author name related to the build.
	GitCommitAuthorName = "git.commit.author.name"

	// GitCommitCommitterDate indicates the git commit committer date related to the build.
	GitCommitCommitterDate = "git.commit.committer.date"

	// GitCommitCommitterEmail indicates the git commit committer email related to the build.
	GitCommitCommitterEmail = "git.commit.committer.email"

	// GitCommitCommitterName indicates the git commit committer name related to the build.
	GitCommitCommitterName = "git.commit.committer.name"

	// GitCommitMessage indicates the git commit message related to the build.
	GitCommitMessage = "git.commit.message"

	// GitCommitSHA indicates the git commit SHA1 hash related to the build.
	GitCommitSHA = "git.commit.sha"

	// GitRepositoryURL indicates the git repository URL related to the build.
	GitRepositoryURL = "git.repository_url"

	// GitTag indicates the current git tag.
	GitTag = "git.tag"

	// GitHeadCommit indicates the GIT head commit hash.
	GitHeadCommit = "git.commit.head.sha"

	// GitHeadMessage indicates the GIT head commit message.
	GitHeadMessage = "git.commit.head.message"

	// GitPrBaseCommit indicates the GIT PR base commit hash.
	GitPrBaseCommit = "git.pull_request.base_branch_sha"

	// GitPrBaseHeadCommit indicates the GIT PR base branch head commit hash.
	GitPrBaseHeadCommit = "git.pull_request.base_branch_head_sha"

	// GitPrBaseBranch indicates the GIT PR base branch name.
	GitPrBaseBranch = "git.pull_request.base_branch"

	// PrNumber indicates the pull request number.
	PrNumber = "pr.number"
)
