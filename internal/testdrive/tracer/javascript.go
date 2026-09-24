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

const resolveJavaScriptModule = "process.stdout.write(require.resolve(process.argv[1]))"

type commandExecutor interface {
	Output(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, []byte, error)
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
}

// JSTracer installs dd-trace for a JavaScript testdrive.
type JSTracer struct {
	version  string
	executor commandExecutor
}

var _ Tracer = (*JSTracer)(nil)

// NewJSTracer creates a JavaScript tracer installer.
func NewJSTracer(version string) *JSTracer {
	return &JSTracer{version: version, executor: &ext.DefaultCommandExecutor{}}
}

// Install installs dd-trace (latest, a release, or git:<ref>) and returns its absolute ci/init path.
func (j *JSTracer) Install(ctx context.Context, sessionDirectory string) (string, error) {
	version := j.version
	if version == "" {
		version = "latest"
	}
	if ref, ok := strings.CutPrefix(version, "git:"); ok {
		if ref == "" {
			return "", fmt.Errorf("tracer git ref must not be empty")
		}
		version = "git+https://github.com/DataDog/dd-trace-js.git#" + ref
	}
	packageName := "dd-trace@" + version
	installArgs := []string{
		"install",
		"--prefix", sessionDirectory,
		"--global=false",
		"--no-save",
		"--package-lock=false",
		"--no-audit",
		"--no-fund",
		packageName,
	}
	cleanEnvironment := map[string]string{"NODE_OPTIONS": "", "NPM_CONFIG_GLOBAL": "false", "npm_config_global": "false"}
	if output, err := j.executor.CombinedOutput(ctx, "npm", installArgs, cleanEnvironment); err != nil {
		return "", commandError("install "+packageName, output, err)
	}

	ciInitModule := filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init")
	output, stderr, err := j.executor.Output(ctx, "node", []string{"-e", resolveJavaScriptModule, ciInitModule}, cleanEnvironment)
	if err != nil {
		return "", commandError("resolve dd-trace/ci/init", stderr, err)
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
