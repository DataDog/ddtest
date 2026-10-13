// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
)

// executionCommand adds project-installed coverage instrumentation for Mocha.
// Preserve an existing NYC invocation or an inherited NYC wrapper.
func (t *Testdrive) executionCommand() (string, []string) {
	command, args := t.command, t.args
	if t.framework.Name() != "mocha" || os.Getenv("NYC_CONFIG") != "" {
		return command, args
	}
	if base := filepath.Base(command); base == "nyc" || base == "nyc.js" {
		return command, args
	}
	nyc := filepath.Join("node_modules", ".bin", "nyc")
	info, err := os.Stat(filepath.Join(t.repositoryRoot, nyc))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return command, args
	}
	wrapped := make([]string, 0, len(args)+2)
	wrapped = append(wrapped, "--", command)
	wrapped = append(wrapped, args...)
	return nyc, wrapped
}
