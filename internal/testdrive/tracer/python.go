// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/ext"
)

const PythonVersion = "4.15.1"

// Python installs into a private target, keeping the active interpreter and
// the customer's dependencies available when pytest runs.
type Python struct {
	Interpreter string
	executor    commandExecutor
}

func NewPython(interpreter string) *Python {
	return &Python{Interpreter: interpreter, executor: &ext.DefaultCommandExecutor{}}
}

func (p *Python) Install(ctx context.Context, directory string) (string, error) {
	target := filepath.Join(directory, "python")
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", "--target", target, "ddtrace==" + PythonVersion}
	if output, err := p.executor.CombinedOutput(ctx, p.Interpreter, args, nil); err != nil {
		return "", commandError("install ddtrace", output, err)
	}
	return target, nil
}
