// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"path/filepath"
	"strings"
	"testing"
)

// newShallowHeadClone returns a depth-1 clone of a two-commit remote and the
// base and head commits. Only the head commit is present in the clone.
func newShallowHeadClone(t *testing.T) (string, string, string) {
	t.Helper()
	remoteURL := newBaseBranchRemote(t)
	remoteDir := filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(remoteURL, "file://"), "/"))
	if filepath.VolumeName(remoteDir) == "" {
		remoteDir = string(filepath.Separator) + remoteDir
	}
	// Protocol v0 servers reject wants for unadvertised commits by default.
	runFixtureGit(t, remoteDir, "config", "uploadpack.allowAnySHA1InWant", "true")
	head := runFixtureGit(t, remoteDir, "rev-parse", "main")
	base := runFixtureGit(t, remoteDir, "rev-parse", "dev")
	local := t.TempDir()
	runFixtureGit(t, local, "init")
	runFixtureGit(t, local, "remote", "add", "origin", remoteURL)
	runFixtureGit(t, local, "fetch", "--depth", "1", "origin", "main")
	runFixtureGit(t, local, "checkout", "--detach", "FETCH_HEAD")
	return local, base, head
}

// A shallow checkout that already has the pull-request head commit reads it
// without fetching; a missing commit is still fetched first.
func TestFetchCommitDataFetchesOnlyMissingCommits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fetch bool
	}{
		{name: "present"},
		{name: "missing", fetch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, base, head := newShallowHeadClone(t)
			t.Chdir(local)
			commands := recordGitCommands(t)
			commit := head
			if tc.fetch {
				commit = base
			}

			data, err := fetchCommitData(commit)
			if err != nil {
				t.Fatal(err)
			}
			if data.CommitSha != commit || data.CommitMessage == "" || data.AuthorName != "CI Fixture" {
				t.Fatalf("commit data = %+v, want commit %s", data, commit)
			}
			recorded := commands()
			if got := commandsNamed(recorded, "cat-file"); len(got) != 1 || got[0] != "cat-file -e "+commit+"^{commit}" {
				t.Fatalf("cat-file commands = %q", got)
			}
			fetches := commandsNamed(recorded, "fetch")
			if tc.fetch && (len(fetches) != 1 || !strings.HasSuffix(fetches[0], " origin "+commit)) {
				t.Fatalf("missing commit fetches = %q, want one fetch of %s", fetches, commit)
			}
			if !tc.fetch && len(fetches) != 0 {
				t.Fatalf("present commit was fetched: %q", recorded)
			}
			if got := len(commandsNamed(recorded, "show")); got != 1 {
				t.Fatalf("show commands = %d, want 1: %q", got, recorded)
			}
		})
	}
}
