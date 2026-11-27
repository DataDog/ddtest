// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/log"
)

func TestRemoteSelectionLeavesAuthenticationOutOfDebugLogs(t *testing.T) {
	for _, selection := range []string{"clone default", "other branch", "no selection keys", "current branch"} {
		t.Run(selection, func(t *testing.T) {
			directory, _ := checkoutRepository(t)
			checkoutGit(t, directory, "", "remote", "add", "backup", "https://example.com/backup.git")
			const localMarker = "ddtest-local-auth-marker"
			const includedMarker = "ddtest-included-auth-marker"
			const helperMarker = "ddtest-credential-helper-marker"
			checkoutGit(t, directory, "", "config", "http.extraheader", "Authorization: Basic "+localMarker)
			included := "[http \"https://example.com/\"]\n extraHeader = Authorization: Basic " + includedMarker +
				"\n[credential]\n helper = " + helperMarker + "\n[feature]\n enabled\n"
			want := "backup"
			switch selection {
			case "clone default":
				included += "[clone]\n defaultRemoteName = backup\n"
			case "other branch":
				included += "[branch \"other\"]\n remote = backup\n"
			case "no selection keys":
				want = "origin"
			case "current branch":
				checkoutGit(t, directory, "", "config", "branch.main.remote", "backup")
			}
			path := filepath.Join(directory, ".git", "included settings")
			if err := os.WriteFile(path, []byte(included), 0o600); err != nil {
				t.Fatal(err)
			}
			checkoutGit(t, directory, "", "config", "include.path", path)
			if selection != "other branch" && selection != "current branch" {
				checkoutGit(t, directory, "", "checkout", "--detach", "HEAD")
			}

			logs := new(log.RecordLogger)
			defer log.UseLogger(logs)()
			defer log.SetLevel(log.GetLevel())
			log.SetLevel(log.LevelDebug)
			remote, err := getRemoteName()
			if err != nil || remote != want {
				t.Fatalf("selected remote=%q, want %q, error=%v", remote, want, err)
			}
			output := strings.Join(logs.Logs(), "\n")
			if !strings.Contains(output, "civisibility.git.command") {
				t.Fatal("debug command logging was not exercised")
			}
			for index, marker := range []string{localMarker, includedMarker, helperMarker} {
				if strings.Contains(output, marker) {
					t.Errorf("authentication marker %d reached the debug log", index)
				}
			}
		})
	}
}

func TestPackFallbackUsesPinnedObjectsDirectoryAfterChdir(t *testing.T) {
	for _, location := range []string{"default", "relative", "absolute"} {
		t.Run(location, func(t *testing.T) {
			directory, head := checkoutRepository(t)
			objects := filepath.Join(directory, ".git", "objects")
			if location != "default" {
				moved := filepath.Join(directory, "moved objects")
				if err := os.Rename(objects, moved); err != nil {
					t.Fatal(err)
				}
				objects = moved
				value := objects
				if location == "relative" {
					value = "moved objects"
				}
				t.Setenv("GIT_OBJECT_DIRECTORY", value)
			}
			data, err := getLocalGitData()
			if err != nil || data.CommitSha != head {
				t.Fatalf("initial checkout commit=%q, error=%v", data.CommitSha, err)
			}
			nested := filepath.Join(directory, "subdir")
			if err := os.Mkdir(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(nested)
			useTempDir(t, filepath.Join(t.TempDir(), "missing"))
			files := CreatePackFiles([]string{head}, nil)
			if len(files) == 0 {
				t.Fatal("objects-directory fallback produced no pack after chdir")
			}
			t.Cleanup(func() { RemovePackFiles(files) })
			if !sameDirectory(t, objects, filepath.Dir(filepath.Dir(files[0]))) {
				t.Fatal("pack fallback used a different objects directory")
			}
			index := strings.TrimSuffix(files[0], ".pack") + ".idx"
			contents := checkoutGit(t, directory, "", "verify-pack", "-v", index)
			if !strings.Contains(contents, head+" commit") {
				t.Fatal("fallback pack does not contain the selected commit")
			}
			RemovePackFiles(files)
			if len(packDirectories(t, objects)) != 0 || len(packDirectories(t, nested)) != 0 {
				t.Fatal("pack directories survived cleanup")
			}
		})
	}
}
