// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
)

func TestCustomCheckoutRequestIdentity(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "same root replacement"
		if nested {
			name = "nested checkout"
		}
		t.Run(name, func(t *testing.T) {
			utils.ResetCITags()
			t.Cleanup(utils.ResetCITags)
			t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
			t.Setenv("DD_API_KEY", "fixture")
			t.Setenv("DD_GIT_COMMIT_HEAD_SHA", "")
			selected := "1111111111111111111111111111111111111111"
			var received atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Data struct {
						Attributes struct {
							SHA string `json:"sha"`
						} `json:"attributes"`
					} `json:"data"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode TM request: %v", err)
				}
				if payload.Data.Attributes.SHA != selected {
					t.Errorf("test-management request SHA=%q, want %q", payload.Data.Attributes.SHA, selected)
				}
				received.Store(true)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"attributes":{"modules":{}}}}`))
			}))
			defer server.Close()
			t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", server.URL)
			root, custom := "/checkout", ""
			if nested {
				root, custom = "/checkout/tools", "true"
			}
			utils.AddCITagsMap(map[string]string{
				constants.GitCheckoutRoot: root, constants.CIWorkspacePath: "/checkout", constants.GitCheckoutIsCustom: custom,
				constants.GitCommitSHA: selected, constants.GitRepositoryURL: "https://example.com/tools.git", constants.GitBranch: "", constants.GitTag: "",
				constants.GitHeadCommit: "2222222222222222222222222222222222222222", constants.GitHeadMessage: "outer repository",
				constants.CIGitMergeCommitSHA: "3333333333333333333333333333333333333333", constants.GitPrBaseCommit: "outer-base",
			})
			got := NewClient().(*client)
			if got.workingDirectory != root || got.commitSha != selected || got.repositoryURL != "https://example.com/tools.git" {
				t.Fatalf("selected checkout=%+v", got)
			}
			if got.branchName != "" || got.headCommitSha != "" || got.headCommitMessage != "" {
				t.Errorf("foreign identity: branch=%q head=%q message=%q", got.branchName, got.headCommitSha, got.headCommitMessage)
			}
			if _, err := got.GetTestManagementTests(); err != nil {
				t.Fatal(err)
			}
			if !received.Load() {
				t.Fatal("no test-management request received")
			}
			if utils.GetCITags()[constants.CIWorkspacePath] != "/checkout" {
				t.Fatal("CI workspace changed")
			}
		})
	}
}
