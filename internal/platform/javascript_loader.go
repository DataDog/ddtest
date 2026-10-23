// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FindPnPLoader follows Yarn's workspace ownership up to the checkout boundary.
// Both Jest inspection and tracer metadata must use the same project loader.
func FindPnPLoader(directory string) (string, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range []string{".pnp.cjs", ".pnp.js"} {
			path := filepath.Join(directory, name)
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() {
				return path, nil
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("inspect Yarn PnP loader: %w", sanitizeProjectError(err))
			}
		}
		if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
			return "", nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}
