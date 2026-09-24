// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JSSelection records both the request and the exact tracer checked locally.
type JSSelection struct {
	Requested string `json:"requested"`
	Source    string `json:"source"`
	Version   string `json:"resolved_version,omitempty"`
	Node      string `json:"node_requirement,omitempty"`
}

// ResolveTestdriveTracer checks project or registry metadata before Jest runs.
// Pass the resolved Version to InstallTestdriveTracer to avoid resolving a moving
// registry tag again between the compatibility check and installation.
func (j *JavaScript) ResolveTestdriveTracer(ctx context.Context, options TracerOptions) (JSSelection, error) {
	selected := JSSelection{Requested: options.Version, Source: "fallback"}
	if selected.Requested == "" {
		selected.Requested = "latest"
	}
	path, probeErr := j.DetectTracer(ctx, options)
	if probeErr != nil {
		path = "" // The platform falls back when the project check fails.
	}
	var err error
	var data []byte
	if path != "" {
		selected.Source = "project"
		data, err = os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "package.json"))
	} else if strings.HasPrefix(selected.Requested, "git:") {
		return selected, nil // Inspect source-build metadata after installation.
	} else {
		var stderr []byte
		data, stderr, err = j.executor.Output(ctx, "npm", []string{"view", "dd-trace@" + selected.Requested, "version", "engines", "--json"}, map[string]string{"NODE_OPTIONS": ""})
		if err != nil {
			return selected, runtimeTagProbeError("resolve selected dd-trace metadata", stderr, err)
		}
	}
	if err != nil {
		return selected, err
	}
	var manifest struct {
		Version string `json:"version"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		var versions []json.RawMessage
		if json.Unmarshal(data, &versions) != nil || len(versions) == 0 {
			return selected, fmt.Errorf("decode dd-trace metadata: %w", err)
		}
		// npm view returns matching versions in registry version order.
		if err = json.Unmarshal(versions[len(versions)-1], &manifest); err != nil {
			return selected, err
		}
	}
	if manifest.Version == "" || manifest.Engines.Node == "" {
		return selected, fmt.Errorf("dd-trace metadata is missing version or engines.node")
	}
	selected.Version = manifest.Version
	selected.Node = manifest.Engines.Node
	return selected, nil
}
