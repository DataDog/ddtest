// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"testing"
)

// testJavaRelease is the latest dd-java-agent release tests read: Java
// setups name it instead of the offline fallback, javaRecommendedTracer.
const testJavaRelease = "1.68.1"

// Tests never read Maven Central: the latest dd-java-agent release is
// testJavaRelease.
func TestMain(m *testing.M) {
	SetJavaReleaseResolver(func(context.Context) (string, error) { return testJavaRelease, nil })
	os.Exit(m.Run())
}
