// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package impactedtests

import (
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

// A test process waits for the worker's impacted tests as long as ddto's
// --git-upload-timeout lets the worker fetch the base commit.
func TestImpactedStateWaitFollowsTheUploadTimeout(t *testing.T) {
	for wait, want := range map[time.Duration]time.Duration{
		0:               cliStateWait,
		time.Second:     cliStateWait,
		2 * time.Minute: 2 * time.Minute,
	} {
		if got := impactedStateWait(&utils.CLIGitRecord{UploadWaitMs: wait.Milliseconds()}); got != want {
			t.Errorf("upload wait %s: %s; want %s", wait, got, want)
		}
	}
}
