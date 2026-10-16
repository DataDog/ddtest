// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"os"
)

// Native commands and their helpers share this run's cleanup boundary, even
// when a helper cannot run its own exit handler. Never change the caller's env.
func (r *validationRun) ownTemporaryDirectory(directory string, env map[string]string) error {
	temporary, err := os.MkdirTemp(directory, "tmp-")
	if err != nil {
		return fmt.Errorf("create run temporary directory: %w", err)
	}
	r.temporaryRoot = temporary
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		env[key] = r.temporaryRoot
	}
	return nil
}
