// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os/exec"
	"path"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

// repositoryChange is one uncommitted path outside CI workflows and ignore
// files. Agents make ddtest's prescribed edits (dev dependencies, package
// scripts, coverage config) without asking, so the report lists each one.
type repositoryChange struct {
	Path string `json:"path"`
	// Added is true for a new, untracked file; otherwise the file is modified
	// or deleted relative to HEAD.
	Added bool `json:"added,omitempty"`
}

// nonCIChanges lists the checkout's uncommitted changes outside CI workflows,
// ignore files and ddtest's own reports. It returns nothing outside Git.
func nonCIChanges(root string) []repositoryChange {
	status, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err != nil {
		return nil
	}
	var changes []repositoryChange
	configuration := onboard.CIConfigurationFiles(root)
	entries := strings.Split(string(status), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		code, name := entry[:2], entry[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the source path of a rename or copy follows
		}
		if ciOrIgnoreFile(name) || slices.Contains(configuration, name) {
			continue
		}
		changes = append(changes, repositoryChange{Path: name, Added: code == "??"})
	}
	slices.SortFunc(changes, func(a, b repositoryChange) int { return strings.Compare(a.Path, b.Path) })
	return changes
}

func ciOrIgnoreFile(name string) bool {
	if strings.HasPrefix(name, ".github/workflows/") || strings.HasPrefix(name, ".testoptimization/") {
		return true
	}
	base := path.Base(name)
	return strings.HasPrefix(base, ".") && strings.HasSuffix(base, "ignore")
}

// lockfileInstall names the reinstall that restores node_modules after a
// reverted manifest or lockfile.
var lockfileInstall = map[string]string{
	"package-lock.json":   "npm ci",
	"npm-shrinkwrap.json": "npm ci",
	"pnpm-lock.yaml":      "pnpm install --frozen-lockfile",
	"yarn.lock":           "yarn install --frozen-lockfile",
	"bun.lockb":           "bun install --frozen-lockfile",
	"bun.lock":            "bun install --frozen-lockfile",
}

// repositoryChangesHandoff lists the changes and the exact commands that
// revert them, including the reinstall a reverted lockfile needs.
func repositoryChangesHandoff(changes []repositoryChange) string {
	if len(changes) == 0 {
		return ""
	}
	var names, modified, added, reinstall []string
	for _, change := range changes {
		label := change.Path
		if change.Added {
			label += " (new)"
			added = append(added, change.Path)
		} else {
			modified = append(modified, change.Path)
		}
		names = append(names, label)
		if install, ok := lockfileInstall[path.Base(change.Path)]; ok && !slices.Contains(reinstall, install) {
			reinstall = append(reinstall, install)
		}
	}
	var revert []string
	if len(modified) > 0 {
		revert = append(revert, shellquote.Join(append([]string{"git", "checkout", "--"}, modified...)...))
	}
	if len(added) > 0 {
		revert = append(revert, shellquote.Join(append([]string{"rm", "--"}, added...)...))
	}
	revert = append(revert, reinstall...)
	return "Repository changes outside CI and ignore files (uncommitted): " + strings.Join(names, ", ") + ". To revert them: " + strings.Join(revert, " && ") + ".\n"
}
