// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
)

// Python reuses the active interpreter's tracer, installing into a private target only when absent.
type Python struct {
	Interpreter string
	version     string
	command     string
	prefixArgs  []string
	executor    commandExecutor
}

func NewPython(interpreter, version string) *Python {
	return &Python{version: version, Interpreter: interpreter, command: interpreter, executor: &ext.DefaultCommandExecutor{}}
}

func NewPythonForCommand(command string, args []string, fallback, version string) *Python {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	if isPythonExecutable(base) {
		return NewPython(command, version)
	}
	if (base == "uv" || base == "poetry") && len(args) > 0 && args[0] == "run" {
		python := NewPython(command, version)
		python.prefixArgs = []string{"run", "python"}
		python.Interpreter = shellquote.Join(command, "run", "python")
		return python
	}
	if strings.HasPrefix(base, "pytest") && filepath.Dir(command) != "." {
		interpreter := filepath.Join(filepath.Dir(command), "python")
		if strings.HasSuffix(base, ".exe") {
			interpreter += ".exe"
		}
		return NewPython(interpreter, version)
	}
	return NewPython(fallback, version)
}

func isPythonExecutable(base string) bool {
	base = strings.TrimSuffix(base, ".exe")
	if !strings.HasPrefix(base, "python") {
		return false
	}
	for _, character := range strings.TrimPrefix(base, "python") {
		if (character < '0' || character > '9') && character != '.' {
			return false
		}
	}
	return true
}

func (p *Python) Install(ctx context.Context, directory string) (Installation, error) {
	packageName := "ddtrace"
	if ref, ok := strings.CutPrefix(p.version, "git:"); ok {
		if ref == "" {
			return Installation{}, fmt.Errorf("tracer git ref must not be empty")
		}
		packageName += " @ git+https://github.com/DataDog/dd-trace-py.git@" + ref
	} else if p.version != "" && p.version != "latest" {
		packageName += "==" + p.version
	}
	version, err := platform.DetectPythonTracer(ctx, p.executor, p.command, p.prefixArgs)
	if err != nil {
		return Installation{}, err
	}
	if version != "" {
		return Installation{Project: true}, nil
	}
	target := filepath.Join(directory, "python-packages")
	args := append(append([]string{}, p.prefixArgs...), "-m", "pip", "install", "--disable-pip-version-check", "--target", target, packageName)
	if output, err := p.executor.CombinedOutput(ctx, p.command, args, map[string]string{"DD_FAST_BUILD": "1"}); err != nil {
		return Installation{}, commandError("install ddtrace", output, err)
	}
	bootstrap := filepath.Join(directory, "python")
	if err := os.MkdirAll(bootstrap, 0755); err != nil {
		return Installation{}, fmt.Errorf("create Python tracer bootstrap: %w", err)
	}
	encodedTarget, _ := json.Marshal(target)
	contents := "import sys\nsys.path.append(" + string(encodedTarget) + ")\n"
	if err := os.WriteFile(filepath.Join(bootstrap, "sitecustomize.py"), []byte(contents), 0600); err != nil {
		return Installation{}, fmt.Errorf("write Python tracer bootstrap: %w", err)
	}
	return Installation{Path: bootstrap}, nil
}
