// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
)

// A final status keeps its value as an interface, without allocating for the
// known statuses.
func TestFinalStatusValue(t *testing.T) {
	for _, status := range []string{constants.TestStatusPass, constants.TestStatusFail, constants.TestStatusSkip, "other"} {
		if got := finalStatusValue(status); got != status {
			t.Fatalf("%q became %v", status, got)
		}
	}
	statuses := []string{constants.TestStatusPass, constants.TestStatusFail, constants.TestStatusSkip}
	var sink any
	if allocs := allocsPerRun(100, func() {
		for _, status := range statuses {
			sink = finalStatusValue(status)
		}
	}); allocs != 0 {
		t.Fatalf("known statuses allocated %v objects", allocs)
	}
	_ = sink
}
