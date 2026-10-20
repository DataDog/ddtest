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
const nodeDistributionIndex = "https://nodejs.org/dist/index.json"

// NodeResolution records a time-dependent alias resolution. setup-node resolves
// LTS aliases to a major, then may use a cached patch: an exact patch is unknown.
type NodeResolution struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	ResolvedAt time.Time `json:"resolved_at"`
	Platform   string    `json:"platform,omitempty"`
}

type nodeResolver func(context.Context, string, string) (NodeResolution, error)

// Fetch once per check, including on failure, so every job uses the same snapshot.
func setupNodeResolver(client *http.Client) nodeResolver {
	type snapshot struct {
		data []byte
		err  error
		at   time.Time
	}
	cache := map[string]snapshot{}
	return func(ctx context.Context, alias, platform string) (NodeResolution, error) {
		source := nodeVersionsManifest
		if latestNodeAlias(alias) {
			if platform == "" {
				return NodeResolution{}, fmt.Errorf("cannot determine the hosted runner platform/architecture for %q", alias)
			}
			source = nodeDistributionIndex
		}
		value, ok := cache[source]
		if !ok {
			value.data, value.err = fetchRuntimeMetadata(ctx, client, source)
			value.at = time.Now().UTC()
			cache[source] = value
		}
		if value.err != nil {
			return NodeResolution{}, value.err
		}
		result := NodeResolution{Source: source, ResolvedAt: value.at}
		var err error
		if latestNodeAlias(alias) {
			result.Platform = platform
			result.Version, err = resolveLatestNode(value.data, platform)
		} else {
			result.Version, err = resolveLTSMajor(value.data, alias)
		}
		return result, err
	}
}

func latestNodeAlias(alias string) bool {
	return slices.Contains([]string{"current", "latest", "node"}, alias)
}

// setup-node selects the newest distribution available for the runner's OS/arch.
func resolveLatestNode(data []byte, platform string) (string, error) {
	var releases []struct {
		Version string   `json:"version"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("decode Node distribution index: %w", err)
	}
	var best [3]int
	var selected string
	for _, release := range releases {
		version, precision, ok := nodeInterval(release.Version)
		if ok && precision == 3 && slices.Contains(release.Files, platform) && slices.Compare(version[:], best[:]) > 0 {
			best, selected = version, strings.TrimPrefix(release.Version, "v")
		}
	}
	if selected == "" {
		return "", fmt.Errorf("node distribution index has no release for %q", platform)
	}
	return selected, nil
}

// Unknown/self-hosted platforms stay unverified. macOS architecture must be
// explicit because the default differs between hosted labels and repository types.
func nodeDistributionPlatform(runner any, architecture string, row map[string]any) string {
	label, ok := runner.(string)
	if !ok {
		return ""
	}
	label, err := runtimeValue(label, row)
	if err != nil {
		return ""
	}
	arch, err := runtimeValue(architecture, row)
	if err != nil {
		return ""
	}
	var prefix, suffix string
	switch {
	case strings.HasPrefix(label, "ubuntu-"):
		prefix = "linux-"
	case strings.HasPrefix(label, "windows-"):
		prefix, suffix = "win-", "-exe"
	case strings.HasPrefix(label, "macos-"):
		if arch == "" {
			return ""
		}
		prefix, suffix = "osx-", "-tar"
	default:
		return ""
	}
	if arch == "" {
		arch = "x64"
		if strings.HasSuffix(label, "-arm") {
			arch = "arm64"
		}
	}
	if !slices.Contains([]string{"x64", "x86", "arm64", "arm"}, arch) {
		return ""
	}
	if arch == "arm" {
		arch = "armv7l"
	}
	return prefix + arch + suffix
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
