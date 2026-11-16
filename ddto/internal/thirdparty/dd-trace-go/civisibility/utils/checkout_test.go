// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
)

func clearGitOverrides(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DD_GIT_BRANCH", "DD_GIT_TAG", "DD_GIT_REPOSITORY_URL", "DD_GIT_COMMIT_SHA",
		"DD_GIT_COMMIT_MESSAGE", "DD_GIT_COMMIT_AUTHOR_NAME", "DD_GIT_COMMIT_AUTHOR_EMAIL",
		"DD_GIT_COMMIT_AUTHOR_DATE", "DD_GIT_COMMIT_COMMITTER_NAME", "DD_GIT_COMMIT_COMMITTER_EMAIL",
		"DD_GIT_COMMIT_COMMITTER_DATE", "DD_GIT_PULL_REQUEST_BASE_BRANCH",
		"DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA", "DD_GIT_COMMIT_HEAD_SHA"} {
		t.Setenv(key, "")
	}
}

func TestCheckoutIdentitySelection(t *testing.T) {
	head, merge := strings.Repeat("a", 40), strings.Repeat("b", 40)
	workspace := filepath.Join(t.TempDir(), "workspace")
	cases := []struct {
		name                                                                                               string
		detached, nested, mismatch, mergeCheckout, missingWorkspace, noGit, noRemote, release, branchError bool
		branch, tag, repository                                                                            string
	}{
		{name: "unreadable branch", branchError: true, repository: "https://mirror.example.com/repo.git"},
		{name: "attached branch", branch: "local-branch", repository: "https://mirror.example.com/repo.git"},
		{name: "matching detached mirror", detached: true, branch: "ci-branch", repository: "https://example.com/repo.git"},
		{name: "different detached revision", detached: true, mismatch: true, repository: "https://mirror.example.com/repo.git"},
		{name: "nested matching revision", detached: true, nested: true, repository: "https://mirror.example.com/repo.git"},
		{name: "provider merge revision", detached: true, mergeCheckout: true, branch: "ci-branch", repository: "https://example.com/repo.git"},
		{name: "no workspace proof", detached: true, missingWorkspace: true, repository: "https://mirror.example.com/repo.git"},
		{name: "unreadable Git", noGit: true, branch: "ci-branch", repository: "https://example.com/repo.git"},
		{name: "readable checkout without a remote", noRemote: true, branch: "local-branch"},
		{name: "matching detached release", detached: true, release: true, tag: "ci-release", repository: "https://example.com/repo.git"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clearGitOverrides(t)
			tags := map[string]string{
				constants.CIWorkspacePath:  workspace,
				constants.GitCommitSHA:     head,
				constants.GitBranch:        "ci-branch",
				constants.GitRepositoryURL: "https://example.com/repo.git",
				constants.GitHeadCommit:    "workflow-head",
				constants.GitPrBaseCommit:  "workflow-base",
			}
			if test.missingWorkspace {
				delete(tags, constants.CIWorkspacePath)
			}
			if test.release {
				delete(tags, constants.GitBranch)
				tags[constants.GitTag] = "ci-release"
			}
			data := localGitData{localCommitData: localCommitData{CommitSha: head, CommitMessage: "checkout message"},
				SourceRoot: workspace, Branch: "local-branch", RepositoryURL: "https://mirror.example.com/repo.git"}
			if test.detached {
				data.Branch = "HEAD"
			}
			if test.branchError {
				data.Branch = ""
			}
			if test.nested {
				data.SourceRoot = filepath.Join(workspace, "tools")
			}
			if test.mismatch {
				data.CommitSha = merge
			}
			if test.mergeCheckout {
				data.CommitSha = merge
				tags[constants.CIGitMergeCommitSHA] = merge
			}
			if test.noRemote {
				data.RepositoryURL = ""
			}
			if test.noGit {
				data = localGitData{}
			}
			applyCheckoutMetadata(tags, data, tags)
			for key, want := range map[string]string{
				constants.GitBranch: test.branch, constants.GitTag: test.tag,
				constants.GitRepositoryURL: test.repository, constants.GitHeadCommit: "workflow-head",
				constants.GitPrBaseCommit: "workflow-base",
			} {
				if got := tags[key]; got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			wantSHA := head
			if test.mismatch || test.mergeCheckout {
				wantSHA = merge
			}
			if got := tags[constants.GitCommitSHA]; got != wantSHA {
				t.Errorf("sha=%q, want %q", got, wantSHA)
			}
			if got := tags[constants.GitCheckoutRoot]; got != data.SourceRoot {
				t.Errorf("root=%q, want %q", got, data.SourceRoot)
			}
			if test.nested && tags[constants.GitCheckoutIsCustom] != "true" {
				t.Fatal("missing custom-checkout flag")
			}
			if !test.nested && tags[constants.GitCheckoutIsCustom] != "" {
				t.Fatal("unexpected custom-checkout flag")
			}
			if test.nested {
				selected := checkoutOperationTags(tags)
				if selected[constants.GitHeadCommit] != "" || selected[constants.GitPrBaseCommit] != "" {
					t.Fatal("foreign workflow revisions reached Git operations")
				}
				if tags[constants.GitHeadCommit] != "workflow-head" {
					t.Fatal("operation view modified reported workflow tags")
				}
			}
		})
	}
}

func TestCheckoutExplicitOverridesAndCanonicalSHA(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "different revision", true: "same revision"}[same], func(t *testing.T) {
			clearGitOverrides(t)
			selected, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			if same {
				selected = head
			}
			t.Setenv("DD_GIT_COMMIT_SHA", strings.ToUpper(selected))
			t.Setenv("DD_GIT_BRANCH", "user-branch")
			t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.com/user.git")
			tags := map[string]string{}
			data := localGitData{localCommitData: localCommitData{CommitSha: head, AuthorName: "checkout author", CommitMessage: "checkout message"}, Branch: "HEAD", Tag: "local-release"}
			applyCheckoutMetadata(tags, data, tags)
			if tags[constants.GitCommitSHA] != selected || tags[constants.GitBranch] != "user-branch" || tags[constants.GitRepositoryURL] != "https://example.com/user.git" {
				t.Fatalf("overrides lost: %#v", tags)
			}
			if !same && (tags[constants.GitCommitAuthorName] != "" || tags[constants.GitCommitMessage] != "" || tags[constants.GitTag] != "") {
				t.Fatal("stale checkout fields describe another selected revision")
			}
			if same && tags[constants.GitCommitAuthorName] != "checkout author" {
				t.Fatal("canonical same SHA dropped commit fields")
			}
		})
	}
}

func TestCustomCheckoutExplicitRelationships(t *testing.T) {
	clearGitOverrides(t)
	tags := map[string]string{constants.GitCheckoutIsCustom: "true", constants.GitHeadCommit: "workflow-head", constants.GitHeadMessage: "workflow message", constants.GitPrBaseCommit: "workflow-base", constants.GitPrBaseBranch: "workflow-branch"}
	original := maps.Clone(tags)
	t.Setenv("DD_GIT_COMMIT_HEAD_SHA", "")
	t.Setenv("DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA", " ")
	if selected := checkoutOperationTags(tags); selected[constants.GitHeadCommit] != "" || selected[constants.GitPrBaseCommit] != "" {
		t.Fatal("empty overrides retained foreign relationships")
	}
	t.Setenv("DD_GIT_COMMIT_HEAD_SHA", "user-head")
	t.Setenv("DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA", "user-base")
	tags[constants.GitHeadCommit] = "user-head"
	tags[constants.GitPrBaseCommit] = "user-base"
	selected := checkoutOperationTags(tags)
	if selected[constants.GitHeadCommit] != "user-head" || selected[constants.GitPrBaseCommit] != "user-base" || selected[constants.GitPrBaseBranch] != "" {
		t.Fatal("explicit relationships not isolated")
	}
	if original[constants.GitHeadCommit] != "workflow-head" {
		t.Fatal("test snapshot mutated")
	}
}

func TestCIMergeCommitEvidence(t *testing.T) {
	sha := strings.Repeat("A", 40)
	for _, test := range []struct {
		name, provider string
		env            map[string]string
		merge          bool
	}{
		{"GitHub merge", "github", map[string]string{"GITHUB_SHA": sha, "GITHUB_REF": "refs/pull/42/merge"}, true},
		{"GitHub branch", "github", map[string]string{"GITHUB_SHA": sha, "GITHUB_REF": "refs/heads/main"}, false},
		{"GitHub head ref", "github", map[string]string{"GITHUB_SHA": sha, "GITHUB_REF": "refs/pull/42/head"}, false},
		{"GitLab result", "gitlab", map[string]string{"CI_COMMIT_SHA": sha, "CI_MERGE_REQUEST_IID": "42", "CI_MERGE_REQUEST_EVENT_TYPE": "merged_result"}, true},
		{"GitLab merge train", "gitlab", map[string]string{"CI_COMMIT_SHA": sha, "CI_MERGE_REQUEST_IID": "42", "CI_MERGE_REQUEST_EVENT_TYPE": "merge_train"}, true},
		{"GitLab missing PR", "gitlab", map[string]string{"CI_COMMIT_SHA": sha, "CI_MERGE_REQUEST_IID": "", "CI_MERGE_REQUEST_EVENT_TYPE": "merged_result"}, false},
		{"Travis merge", "travisci", map[string]string{"TRAVIS_COMMIT": sha, "TRAVIS_EVENT_TYPE": "pull_request", "TRAVIS_PULL_REQUEST": "42"}, true},
		{"Travis no PR", "travisci", map[string]string{"TRAVIS_COMMIT": sha, "TRAVIS_EVENT_TYPE": "pull_request", "TRAVIS_PULL_REQUEST": "false"}, false},
		{"GitLab branch", "gitlab", map[string]string{"CI_COMMIT_SHA": sha, "CI_MERGE_REQUEST_IID": "42", "CI_MERGE_REQUEST_EVENT_TYPE": "detached"}, false},
		{"Azure merge", "azurepipelines", map[string]string{"BUILD_SOURCEVERSION": sha, "BUILD_REASON": "PullRequest", "BUILD_SOURCEBRANCH": "refs/pull/42/merge"}, true},
		{"Azure branch", "azurepipelines", map[string]string{"BUILD_SOURCEVERSION": sha, "BUILD_REASON": "IndividualCI", "BUILD_SOURCEBRANCH": "refs/heads/main"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			tags := map[string]string{constants.CIProviderName: test.provider}
			addMergeCommitTag(tags)
			expected := ""
			if test.merge {
				expected = strings.ToLower(sha)
			}
			if tags[constants.CIGitMergeCommitSHA] != expected {
				t.Fatalf("merge=%q, want %q", tags[constants.CIGitMergeCommitSHA], expected)
			}
		})
	}
}

func TestReadableCheckoutDoesNotUseFileIdentity(t *testing.T) {
	clearGitOverrides(t)
	workspace := t.TempDir()
	sha := strings.Repeat("a", 40)
	fileTags := map[string]string{constants.CIWorkspacePath: workspace, constants.GitCommitSHA: sha, constants.GitBranch: "file-branch", constants.GitRepositoryURL: "https://example.com/stale.git"}
	data := localGitData{localCommitData: localCommitData{CommitSha: sha}, SourceRoot: workspace, RepositoryURL: "https://example.com/checkout.git", Branch: "HEAD"}
	applyCheckoutMetadata(fileTags, data, map[string]string{})
	if fileTags[constants.GitBranch] != "" || fileTags[constants.GitRepositoryURL] != "https://example.com/checkout.git" {
		t.Fatalf("file replaced readable checkout: %#v", fileTags)
	}
}

func TestNestedCheckoutSourceAndOwnership(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "tools")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0700); err != nil {
		t.Fatal(err)
	}
	resetCodeOwnersTestState(t, workspace)
	ResetServiceFromCodeOwnersForTesting()
	t.Cleanup(ResetServiceFromCodeOwnersForTesting)
	AddCITagsMap(map[string]string{constants.GitCheckoutRoot: root, constants.GitCheckoutIsCustom: "true"})
	writeCodeOwnersFile(t, filepath.Join(workspace, "CODEOWNERS"), "* @example/outer\n")
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @example/tools\n")
	source := filepath.Join(root, "pkg", "value_test.go")
	got := ResolveSourceFilePathFromCITags(source)
	if got.RelativePath != "pkg/value_test.go" || got.FilesystemPath != source {
		t.Fatalf("source=%+v", got)
	}
	owners := GetCodeOwners()
	if owners == nil {
		t.Fatal("no checkout ownership rules")
	}
	owner, ok := owners.Match("/pkg/value_test.go")
	if !ok || owner.FirstOwner() != "@example/tools" {
		t.Fatalf("owner=%+v", owner)
	}
	t.Setenv("DD_SERVICE", "")
	t.Setenv(ServiceFromCodeOwnersEnv, "true")
	t.Setenv(ServiceFromCodeOwnersFormatEnv, "service-$(owner)")
	registerCodeOwnersTestPackage(t, source)
	if got := ServiceFromCodeOwners(); got != "service-tools" {
		t.Fatalf("test service=%q", got)
	}
	if got := ServiceFromCodeOwnersForDirectory(filepath.Dir(source)); got != "service-tools" {
		t.Fatalf("worker service=%q", got)
	}
	if GetCITags()[constants.CIWorkspacePath] != workspace {
		t.Fatal("CI workspace changed")
	}
}
