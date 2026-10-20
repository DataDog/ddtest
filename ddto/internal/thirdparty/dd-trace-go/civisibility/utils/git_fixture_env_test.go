// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"testing"
)

// TestMain keeps the git fixtures and the code under test away from the
// caller's repository. Git sets most of these variables for hooks; inherited,
// they would send a fixture's commits to that repository's index and branch,
// and GIT_CONFIG would receive its git config writes.
func TestMain(m *testing.M) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG"} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}
