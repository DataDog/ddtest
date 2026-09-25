// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const nodeVersionsManifest = "https://raw.githubusercontent.com/actions/node-versions/main/versions-manifest.json"

// NodeResolution records a time-dependent alias resolution. setup-node resolves
// LTS aliases to a major, then may use a cached patch: an exact patch is unknown.
type NodeResolution struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	ResolvedAt time.Time `json:"resolved_at"`
}

type nodeResolver func(context.Context, string) (NodeResolution, error)

// Fetch once per check, including on failure, so every job uses the same snapshot.
func ltsNodeResolver(client *http.Client) nodeResolver {
	var data []byte
	var fetchErr error
	var resolvedAt time.Time
	return func(ctx context.Context, alias string) (NodeResolution, error) {
		if resolvedAt.IsZero() {
			data, fetchErr = fetchRuntimeMetadata(ctx, client, nodeVersionsManifest)
			resolvedAt = time.Now().UTC()
		}
		if fetchErr != nil {
			return NodeResolution{}, fetchErr
		}
		version, err := resolveLTSMajor(data, alias)
		return NodeResolution{Version: version, Source: nodeVersionsManifest, ResolvedAt: resolvedAt}, err
	}
}

func resolveLTSMajor(data []byte, alias string) (string, error) {
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		LTS     string `json:"lts"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("decode setup-node version manifest: %w", err)
	}
	// Match setup-node's lts/*, lts/<codename>, and lts/-n selectors.
	selector := strings.ToLower(strings.TrimPrefix(alias, "lts/"))
	majors := map[string]int{}
	for _, release := range releases {
		version, precision, ok := nodeInterval(release.Version)
		if !release.Stable || release.LTS == "" || !ok || precision != 3 {
			continue
		}
		name := strings.ToLower(release.LTS)
		majors[name] = max(majors[name], version[0])
	}
	if major, ok := majors[selector]; ok {
		return strconv.Itoa(major), nil
	}
	index := 0
	if selector != "*" {
		var err error
		index, err = strconv.Atoi(strings.TrimPrefix(selector, "-"))
		if !strings.HasPrefix(selector, "-") || err != nil || index < 0 {
			return "", fmt.Errorf("unknown setup-node LTS alias %q", alias)
		}
	}
	versions := make([]int, 0, len(majors))
	for _, version := range majors {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	if index >= len(versions) {
		return "", fmt.Errorf("setup-node manifest has no release for %q", alias)
	}
	return strconv.Itoa(versions[len(versions)-1-index]), nil
}
