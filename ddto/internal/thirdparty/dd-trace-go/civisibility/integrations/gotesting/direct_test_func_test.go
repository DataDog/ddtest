// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import "testing"

// directTestFunc reports a test function that testing runs without a
// wrapper, whose t.Helper call executeInternalTest skips, and keeps the call
// for a wrapped one.
func TestDirectTestFunc(t *testing.T) {
	instrumented := func(*testing.T) {}
	wrapper := func(t *testing.T) { instrumented(t) }
	info := &testingTInfo{}
	if directTestFunc(info, instrumented, instrumented); !info.directTestFunc {
		t.Fatal("an unwrapped function is not direct")
	}
	if directTestFunc(info, instrumented, wrapper); info.directTestFunc {
		t.Fatal("a wrapped function is direct")
	}
}
