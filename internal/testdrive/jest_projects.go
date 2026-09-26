// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

type projectProbeCheck struct {
	Project               string `json:"project"`
	Root                  string `json:"root"`
	DiscoveryCommand      string `json:"discovery_command"`
	ProbeDiscoveryCommand string `json:"probe_discovery_command,omitempty"`
	Probe                 string `json:"probe,omitempty"`
	Discovered            bool   `json:"discovered"`
	Excluded              bool   `json:"excluded,omitempty"`
}

// Replace explicit project selectors only after preserving their scope. Jest
// accumulates repeated --selectProjects flags, which would run extra projects.
func projectArgs(command string, args []string, name string) ([]string, bool) {
	var kept, selected, ignored []string
	for i := 0; i < len(args); i++ {
		flag, value, equals := strings.Cut(args[i], "=")
		var target *[]string
		switch flag {
		case "--selectProjects", "--select-projects":
			target = &selected
		case "--ignoreProjects", "--ignore-projects":
			target = &ignored
		default:
			kept = append(kept, args[i])
			continue
		}
		if equals {
			*target = append(*target, value)
		} else {
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				*target = append(*target, args[i])
			}
		}
	}
	if (len(selected) > 0 && !slices.Contains(selected, name)) || slices.Contains(ignored, name) {
		return nil, false
	}
	return appendJestArgs(command, kept, "--selectProjects", name), true
}

func (t *Testdrive) discoverJestTests(ctx context.Context, probe string) ([]string, string, error) {
	args := appendJestArgs(t.command, t.args, "--listTests", "--json")
	if probe != "" {
		args = append(args, "--runTestsByPath", probe)
	}
	command := shellquote.Join(append([]string{t.command}, args...)...)
	env := testEnvironment("", "http://127.0.0.1:1", "project-discovery")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"] = "false"
	env["DD_TRACE_ENABLED"] = "false"
	data, err := t.executor.CombinedOutput(ctx, t.command, args, env)
	if err != nil {
		return nil, command, fmt.Errorf("jest --listTests failed: %w; %s", err, commandDiagnostic(data))
	}
	// Package managers may print banners before the JSON array.
	for i, value := range data {
		var paths []string
		if value == '[' && json.NewDecoder(strings.NewReader(string(data[i:]))).Decode(&paths) == nil {
			slices.Sort(paths)
			return slices.Compact(paths), command, nil
		}
	}
	return nil, command, fmt.Errorf("jest --listTests did not return a JSON path array")
}
