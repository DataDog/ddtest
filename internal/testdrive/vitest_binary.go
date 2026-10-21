// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/buildinfo"
	"github.com/DataDog/ddtest/internal/onboard"
)

type runnerBinary struct {
	Path     string   `json:"path"`
	Version  string   `json:"version"`
	CIPins   []string `json:"ci_pins,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func resolveVitestRunnerBinary(root, directory, command string) (string, error) {
	if command == "ddtest" {
		return os.Executable()
	}
	if !filepath.IsAbs(command) && strings.ContainsAny(command, `/\`) {
		packageDirectory, err := localSetupDirectory(root, directory)
		if err != nil {
			return "", err
		}
		command = filepath.Join(packageDirectory, command)
	}
	command, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	return filepath.Abs(command)
}

func (t *Testdrive) inspectVitestRunnerBinary(ctx context.Context) runnerBinary {
	info := runnerBinary{Path: t.command, Version: "unverified"}
	current, err := os.Executable()
	if err == nil && canonicalWorkflowRoot(current) == canonicalWorkflowRoot(t.command) {
		info.Version = buildinfo.CurrentVersion()
	} else {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		data, err := t.executor.CombinedOutput(ctx, t.command, []string{"--version"}, map[string]string{"NODE_OPTIONS": stripDatadogNodeOptions(t.nodeOptions())})
		if err == nil {
			line := strings.TrimSpace(string(data))
			if _, version, ok := strings.Cut(line, " version "); ok && !strings.ContainsAny(version, "\r\n") {
				info.Version = version
			}
		}
	}
	root := t.repositoryRoot
	if t.workflowRoot != "" {
		root = t.workflowRoot
	}
	info.CIPins = onboard.VitestRunnerCIPins(root)
	for _, pin := range info.CIPins {
		if strings.TrimPrefix(pin, "v") != strings.TrimPrefix(info.Version, "v") {
			info.Warnings = append(info.Warnings, fmt.Sprintf("CI requests ddtest %s; local validation uses %s at %s. That CI binary was not exercised.", pin, info.Version, info.Path))
		}
	}
	return info
}
