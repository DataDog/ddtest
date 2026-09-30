// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		return j.ResolveRegistryTracer(ctx, selected.Requested)
	}
	if err != nil {
		return selected, err
	}
	return decodeJSSelection(selected, data)
}

// ResolveRegistryTracer reads an advisory candidate without changing or probing
// the installed project tracer. Releases are resolved from npm, never pinned here.
func (j *JavaScript) ResolveRegistryTracer(ctx context.Context, selector string) (selected JSSelection, resolveErr error) {
	selected = JSSelection{Requested: selector, Source: "fallback"}
	cache, err := os.MkdirTemp("", "ddtest-npm-metadata-")
	if err != nil {
		return selected, fmt.Errorf("create temporary npm metadata cache; check TMPDIR permissions and free space: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(cache); err != nil {
			resolveErr = errors.Join(resolveErr, fmt.Errorf("remove npm metadata cache %s: %w", cache, err))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// CLI options override inherited cache settings without changing the customer's
	// registry, proxy or credentials. Metadata reads should fail promptly offline.
	args := []string{"view", "dd-trace@" + selector, "version", "engines", "--json", "--fetch-retries=0", "--fetch-timeout=10000", "--cache=" + cache}
	data, stderr, err := j.executor.Output(ctx, "npm", args, map[string]string{"NODE_OPTIONS": ""})
	if err != nil {
		return selected, registryMetadataError(ctx, stderr, err)
	}
	return decodeJSSelection(selected, data)
}

func registryMetadataError(ctx context.Context, stderr []byte, err error) error {
	if cause := ctx.Err(); cause != nil {
		return fmt.Errorf("resolve dd-trace metadata interrupted; no version selected and compatibility remains unverified: %w", errors.Join(cause, err))
	}
	message := string(stderr)
	for _, code := range []string{"ENOTFOUND", "EAI_AGAIN", "ENETUNREACH", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "FETCH_ERROR"} {
		if strings.Contains(message, code) {
			return fmt.Errorf("resolve dd-trace metadata: registry network failure (%s); check network/sandbox access and configured registry/proxy, then rerun preflight; compatibility is unverified: %w", code, err)
		}
	}
	for _, code := range []string{"EACCES", "EPERM", "EROFS", "ENOSPC"} {
		if strings.Contains(message, code) {
			return fmt.Errorf("resolve dd-trace metadata: filesystem failure (%s); check TMPDIR and npm configuration permissions/free space, then rerun preflight; compatibility is unverified: %w", code, err)
		}
	}
	return fmt.Errorf("resolve dd-trace metadata failed; verify the selector, registry and npm authentication, then rerun preflight; no fallback version was guessed: %w", err)
}

func decodeJSSelection(selected JSSelection, data []byte) (JSSelection, error) {
	var manifest struct {
		Version string `json:"version"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
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
