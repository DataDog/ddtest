// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package git

import (
	"fmt"
	"testing"
	"time"
)

// TestCommitMetadataKeepsEveryField reads commits whose message and people
// hold the characters a delimiter could be made of, and checks that
// GetLocalGitData and FetchCommitData return each field as it was written.
func TestCommitMetadataKeepsEveryField(t *testing.T) {
	const (
		authorName     = "Doe, Jane"
		authorEmail    = "jane.doe@example.com"
		committerName  = `Smith, John "JS" Jr.`
		committerEmail = "john.smith@example.com"
	)
	authored := time.Unix(1764000000, 0)
	committed := authored.Add(time.Minute)
	for _, tt := range []struct {
		name, message string
	}{
		{"the old delimiter", `Fix parsing of ["a","b"]`},
		{"commas", "Handle a, b, and c"},
		{"quotes", `Say "hello" and 'goodbye'`},
		{"subject and body", "Split long files\n\nThe body's first line.\nAnd its second, with \",\"."},
		{"tabs", "Align\tthe\tcolumns"},
		{"unicode", "Corrige la caché ✓ 日本語のテスト"},
		{"empty", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory, head := checkoutRepository(t)
			tree := checkoutGit(t, directory, "", "rev-parse", head+"^{tree}")
			data := fmt.Sprintf("tree %s\nparent %s\nauthor %s <%s> %d +0000\ncommitter %s <%s> %d +0000\n\n",
				tree, head, authorName, authorEmail, authored.Unix(), committerName, committerEmail, committed.Unix())
			if tt.message != "" {
				data += tt.message + "\n"
			}
			commit := checkoutGit(t, directory, data, "hash-object", "-t", "commit", "-w", "--stdin")
			checkoutGit(t, directory, "", "update-ref", "refs/heads/main", commit)

			local, err := GetLocalGitData()
			if err != nil {
				t.Fatal(err)
			}
			fetched, err := FetchCommitData(commit)
			if err != nil {
				t.Fatal(err)
			}
			want := LocalCommitData{
				CommitSha: commit, AuthorDate: authored, AuthorName: authorName, AuthorEmail: authorEmail,
				CommitterDate: committed, CommitterName: committerName, CommitterEmail: committerEmail, CommitMessage: tt.message,
			}
			for reader, data := range map[string]LocalCommitData{"GetLocalGitData": local.LocalCommitData, "FetchCommitData": fetched} {
				if data.CommitSha != want.CommitSha || !data.AuthorDate.Equal(want.AuthorDate) || data.AuthorName != want.AuthorName ||
					data.AuthorEmail != want.AuthorEmail || !data.CommitterDate.Equal(want.CommitterDate) || data.CommitterName != want.CommitterName ||
					data.CommitterEmail != want.CommitterEmail || data.CommitMessage != want.CommitMessage {
					t.Errorf("%s = %+v\nwant %+v", reader, data, want)
				}
			}
		})
	}
}
