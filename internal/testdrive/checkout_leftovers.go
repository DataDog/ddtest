// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
)

// checkoutEntries lists untracked and ignored top-level entries, with ignored
// directories collapsed, so a run that executes in the checkout can report what
// it left behind. It returns nil when Git can't list them.
func checkoutEntries(root string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries := map[string]bool{}
	for _, extra := range [][]string{nil, {"--ignored"}} {
		args := append([]string{"-C", root, "ls-files", "--others", "--exclude-standard", "--directory", "-z"}, extra...)
		data, err := exec.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			return nil
		}
		for entry := range strings.SplitSeq(string(data), "\x00") {
			if entry != "" {
				entries[entry] = true
			}
		}
	}
	return entries
}

// checkoutLeftovers names entries created during the run that remain after it.
// ddtest's own report directory is expected and never counted.
func checkoutLeftovers(before, after map[string]bool) []string {
	if before == nil || after == nil {
		return nil
	}
	var leftovers []string
	for entry := range after {
		if !before[entry] && !strings.HasPrefix(entry, constants.PlanDirectory+"/") && entry != constants.PlanDirectory {
			leftovers = append(leftovers, entry)
		}
	}
	slices.Sort(leftovers)
	return leftovers
}
