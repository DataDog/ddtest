// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
)

const (
	// JavaScriptVersion is the dd-trace version used by testdrive and the setup
	// that onboard will recommend.
	JavaScriptVersion       = "6.15.0"
	resolveJavaScriptModule = "process.stdout.write(require.resolve(process.argv[1]))"
)

type commandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
}

// JavaScript installs dd-trace for a JavaScript testdrive.
type JavaScript struct {
	executor commandExecutor
}

var _ Tracer = (*JavaScript)(nil)

// NewJavaScript creates a JavaScript tracer installer.
func NewJavaScript() *JavaScript {
	return &JavaScript{executor: &ext.DefaultCommandExecutor{}}
}

// Install installs the pinned dd-trace package and returns its absolute ci/init path.
func (j *JavaScript) Install(ctx context.Context, sessionDirectory string) (string, error) {
	packageName := "dd-trace@" + JavaScriptVersion
	installArgs := []string{
		"install",
		"--prefix", sessionDirectory,
		"--no-save",
		"--package-lock=false",
		"--no-audit",
		"--no-fund",
		packageName,
	}
	if output, err := j.executor.CombinedOutput(ctx, "npm", installArgs, nil); err != nil {
		return "", commandError("install "+packageName, output, err)
	}

	ciInitModule := filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init")
	output, err := j.executor.CombinedOutput(ctx, "node", []string{"-e", resolveJavaScriptModule, ciInitModule}, nil)
	if err != nil {
		return "", commandError("resolve dd-trace/ci/init", output, err)
	}

	ciInitPath := strings.TrimSpace(string(output))
	if !filepath.IsAbs(ciInitPath) {
		return "", fmt.Errorf("resolve dd-trace/ci/init: node returned non-absolute path %q", ciInitPath)
	}
	return ciInitPath, nil
}

func commandError(action string, output []byte, err error) error {
	diagnostic := strings.TrimSpace(string(output))
	if diagnostic == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s: %w", action, diagnostic, err)
}
