// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunEmitsSharedScratchGuidanceBeforeConfiguredReturn(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "new setup"
		action := ""
		if configured {
			name = "already configured"
			action = "      - uses: datadog/test-visibility-github-action@v3\n"
		}
		t.Run(name, func(t *testing.T) {
			repositoryRoot := newJestRepository(t, "jobs:\n  test:\n    steps:\n"+action+"      - run: npm test\n")
			t.Chdir(repositoryRoot)
			var output bytes.Buffer
			if err := Run(&output); err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(output.String(), strings.TrimSpace(scratchCleanupInstructions)); count != 1 {
				t.Fatalf("shared scratch guidance emitted %d times; want exactly once", count)
			}
			for _, distinction := range []string{"reusable environment-owned data", "do not scan, hash, prune individual entries", "dedicated unique directory", "Report uncertain leftovers rather than deleting them"} {
				if !strings.Contains(output.String(), distinction) {
					t.Fatalf("onboarding omitted the cleanup ownership boundary %q", distinction)
				}
			}
		})
	}
}
