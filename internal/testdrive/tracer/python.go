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
	"github.com/kballard/go-shellquote"
)

const PythonVersion = "4.15.1"

// Python installs into a private target, keeping the active interpreter and
// the customer's dependencies available when pytest runs.
type Python struct {
	Interpreter string
	command     string
	prefixArgs  []string
	executor    commandExecutor
}

func NewPython(interpreter string) *Python {
	return &Python{Interpreter: interpreter, command: interpreter, executor: &ext.DefaultCommandExecutor{}}
}

func NewPythonForCommand(command string, args []string, fallback string) *Python {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	if isPythonExecutable(base) {
		return NewPython(command)
	}
	if (base == "uv" || base == "poetry") && len(args) > 0 && args[0] == "run" {
		python := NewPython(command)
		python.prefixArgs = []string{"run", "python"}
		python.Interpreter = shellquote.Join(command, "run", "python")
		return python
	}
	if strings.HasPrefix(base, "pytest") && filepath.Dir(command) != "." {
		interpreter := filepath.Join(filepath.Dir(command), "python")
		if strings.HasSuffix(base, ".exe") {
			interpreter += ".exe"
		}
		return NewPython(interpreter)
	}
	return NewPython(fallback)
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

func (p *Python) Install(ctx context.Context, directory string) (string, error) {
	target := filepath.Join(directory, "python-packages")
	args := append(append([]string{}, p.prefixArgs...), "-m", "pip", "install", "--disable-pip-version-check", "--target", target, "ddtrace=="+PythonVersion)
	if output, err := p.executor.CombinedOutput(ctx, p.command, args, nil); err != nil {
		return "", commandError("install ddtrace", output, err)
	}
	bootstrap := filepath.Join(directory, "python")
	if err := os.MkdirAll(bootstrap, 0755); err != nil {
		return "", fmt.Errorf("create Python tracer bootstrap: %w", err)
	}
	encodedTarget, _ := json.Marshal(target)
	contents := "import sys\nsys.path.append(" + string(encodedTarget) + ")\n"
	if err := os.WriteFile(filepath.Join(bootstrap, "sitecustomize.py"), []byte(contents), 0600); err != nil {
		return "", fmt.Errorf("write Python tracer bootstrap: %w", err)
	}
	return bootstrap, nil
}
