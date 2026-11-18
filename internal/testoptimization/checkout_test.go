// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package testoptimization

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/git/gittest"
)

type checkoutUploadClient struct {
	MockAPIClient
	t       *testing.T
	objects string
}

func (c *checkoutUploadClient) SendPackFiles(sha string, paths []string) (int64, error) {
	for _, path := range paths {
		output, err := exec.Command("git", "verify-pack", "-v", strings.TrimSuffix(path, ".pack")+".idx").CombinedOutput()
		if err != nil {
			c.t.Fatalf("verify uploaded pack: %v: %s", err, output)
		}
		c.objects += string(output)
	}
	return c.MockAPIClient.SendPackFiles(sha, paths)
}

func TestUploadUsesSelectedRevisionInsteadOfHEAD(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	withoutCIProviderEnvironment(t)
	selected, head := repo.Commits[0], repo.Commits[1]
	t.Setenv("DD_GIT_COMMIT_SHA", strings.ToUpper(selected))
	environment.ResetCITags()
	transport := &checkoutUploadClient{t: t}
	client := newTestOptimizationClient(transport, nil, nil)
	if _, err := client.uploadRepositoryChangesFromGit(); err != nil {
		t.Fatal(err)
	}
	if len(transport.GetCommitsRequests) != 1 || !slices.Equal(transport.GetCommitsRequests[0], []string{selected}) {
		t.Fatalf("searched revisions=%v", transport.GetCommitsRequests)
	}
	if transport.SentCommitSha != selected || transport.SendPackFilesCalls != 1 {
		t.Fatalf("uploaded identity=%q calls=%d", transport.SentCommitSha, transport.SendPackFilesCalls)
	}
	if !strings.Contains(transport.objects, selected+" commit") || strings.Contains(transport.objects, head+" commit") {
		t.Fatalf("pack does not match selected identity: %s", transport.objects)
	}
}

func TestUploadDoesNotInventMissingSelectedSHA(t *testing.T) {
	repo := gittest.NewRepository(t)
	t.Chdir(repo.Path)
	withoutCIProviderEnvironment(t)
	environment.AddCITagsMap(map[string]string{constants.GitCommitSHA: ""})
	transport := &MockAPIClient{}
	client := newTestOptimizationClient(transport, nil, nil)
	if _, err := client.uploadRepositoryChangesFromGit(); err != nil {
		t.Fatal(err)
	}
	if transport.GetCommitsCalls != 0 || transport.SendPackFilesCalls != 0 {
		t.Fatalf("missing identity triggered search=%d upload=%d", transport.GetCommitsCalls, transport.SendPackFilesCalls)
	}
}
