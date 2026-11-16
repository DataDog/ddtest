// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
	"testing"
)

func TestCustomCheckoutRequestIdentity(t *testing.T) {
	utils.ResetCITags()
	t.Cleanup(utils.ResetCITags)
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	t.Setenv("DD_API_KEY", "fixture")
	t.Setenv("DD_GIT_COMMIT_HEAD_SHA", "")
	utils.AddCITagsMap(map[string]string{
		constants.GitCheckoutRoot:     "/checkout/tools",
		constants.CIWorkspacePath:     "/checkout",
		constants.GitCheckoutIsCustom: "true",
		constants.GitCommitSHA:        "1111111111111111111111111111111111111111",
		constants.GitRepositoryURL:    "https://example.com/tools.git",
		constants.GitBranch:           "",
		constants.GitTag:              "",
		constants.GitHeadCommit:       "2222222222222222222222222222222222222222",
		constants.GitHeadMessage:      "outer repository",
	})
	got := NewClient().(*client)
	if got.workingDirectory != "/checkout/tools" || got.commitSha != "1111111111111111111111111111111111111111" || got.repositoryURL != "https://example.com/tools.git" {
		t.Fatalf("selected checkout=%+v", got)
	}
	if got.branchName != "" || got.headCommitSha != "" || got.headCommitMessage != "" {
		t.Fatalf("foreign or invented identity: branch=%q head=%q message=%q", got.branchName, got.headCommitSha, got.headCommitMessage)
	}
	if utils.GetCITags()[constants.CIWorkspacePath] != "/checkout" {
		t.Fatal("CI workspace changed")
	}
}
