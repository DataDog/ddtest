// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package gitupload

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils"
)

type missingSHAClient struct{ searches, uploads int }

func (c *missingSHAClient) GetCommits([]string) ([]string, error)         { c.searches++; return nil, nil }
func (c *missingSHAClient) SendPackFiles(string, []string) (int64, error) { c.uploads++; return 0, nil }

func TestUploadDoesNotInventMissingSelectedSHA(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("DD_GIT_COMMIT_HEAD_SHA", "")
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CONFIG", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	git := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = directory
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("", "init", "--initial-branch=main")
	tree := git("", "mktree")
	now := time.Now().Unix()
	sha := git(fmt.Sprintf("tree %s\nauthor Fixture <fixture@example.com> %d +0000\ncommitter Fixture <fixture@example.com> %d +0000\n\nfixture\n", tree, now, now), "hash-object", "-t", "commit", "-w", "--stdin")
	git("", "update-ref", "refs/heads/main", sha)
	t.Chdir(directory)
	utils.ResetCITags()
	t.Cleanup(utils.ResetCITags)
	utils.AddCITagsMap(map[string]string{constants.GitCommitSHA: ""})
	client := &missingSHAClient{}
	bytes, err := Upload(ClientHooks(client))
	if err != nil || bytes != 0 || client.searches != 0 || client.uploads != 0 {
		t.Fatalf("missing identity: bytes=%d err=%v search=%d upload=%d", bytes, err, client.searches, client.uploads)
	}
}
