// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DataDog/ddtest/internal/platform"
	"os"
	"path/filepath"
)

// Resolve through the project's loader, including Yarn PnP, without npx or any
// package installation. This runs only after the user approves check-only.
func (t *Testdrive) installedJest(ctx context.Context) (string, []string, error) {
	args := []string{}
	loader, err := platform.FindPnPLoader(t.repositoryRoot)
	if err != nil {
		return "", nil, err
	}
	if loader != "" {
		args = append(args, "--require", loader)
	}

	const resolver = `process.stdout.write(JSON.stringify(require.resolve('jest/bin/jest',{paths:[process.cwd()]})))`
	data, err := t.executor.CombinedOutput(ctx, "node", append(args, "-e", resolver), map[string]string{"NODE_OPTIONS": "", "NODE_PATH": "", "DD_TRACE_ENABLED": "false", "DD_CIVISIBILITY_ENABLED": "false"})
	var entry string
	if err == nil && json.Unmarshal(data, &entry) == nil && filepath.IsAbs(entry) {
		return "node", append(append(args, entry), t.args...), nil
	}
	// Some installations expose only a local bin. Never fall back to a download.
	entry = filepath.Join(t.repositoryRoot, "node_modules", ".bin", "jest")
	if info, statErr := os.Stat(entry); statErr == nil && !info.IsDir() {
		return "node", append(append(args, entry), t.args...), nil
	}
	return "", nil, fmt.Errorf("cannot resolve installed Jest in this project; install dependencies first, then rerun check-only")
}
