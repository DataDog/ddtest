//go:build !linux && !darwin && !windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"os"
)

func lockWorkflowFile(*os.File) error {
	return fmt.Errorf("workflow locking is not supported on this platform")
}
