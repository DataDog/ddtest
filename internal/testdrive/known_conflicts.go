// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// A known conflict between a dd-trace integration and code that uses a package.
// Hints name the integration and the supported mitigation; they never change a verdict.
type pluginConflict struct {
	Integration string
	Packages    []string
	Detail      string
}

var knownPluginConflicts = []pluginConflict{{
	Integration: "router",
	Packages:    []string{"express", "router"},
	Detail:      "dd-trace's router integration wraps middleware next callbacks, so code that compares next by identity (such as express res.format handlers) behaves differently with instrumentation",
}}

// knownConflictHints lists conflicts for packages the repository is or depends on.
func knownConflictHints(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Name                 string            `json:"name"`
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	uses := func(name string) bool {
		if manifest.Name == name {
			return true
		}
		for _, dependencies := range []map[string]string{manifest.Dependencies, manifest.DevDependencies, manifest.PeerDependencies, manifest.OptionalDependencies} {
			if _, ok := dependencies[name]; ok {
				return true
			}
		}
		return false
	}
	var hints []string
	for _, conflict := range knownPluginConflicts {
		if slices.ContainsFunc(conflict.Packages, uses) {
			hints = append(hints, fmt.Sprintf("Known dd-trace conflict (%s integration): %s. If the differing tests exercise it, set DD_TRACE_DISABLED_INSTRUMENTATIONS=%s on the CI test step and rerun; ddtest applies that step setting locally, and settings only in your shell are reported as ambient.", conflict.Integration, conflict.Detail, conflict.Integration))
		}
	}
	return hints
}
