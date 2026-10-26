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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

const nodeVersionsManifest = "https://raw.githubusercontent.com/actions/node-versions/main/versions-manifest.json"
const nodeDistributionIndex = "https://nodejs.org/dist/index.json"

// A time-stamped metadata resolution, not evidence of an executed CI runner.
// setup-node may reuse a different cached patch when check-latest is disabled.
type NodeResolution struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	ResolvedAt time.Time `json:"resolved_at"`
	Platform   string    `json:"platform,omitempty"`
}
type nodeResolver func(context.Context, string, string) (NodeResolution, error)

type nodeRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	LTS     string `json:"lts"`
	Files   []struct {
		Platform string `json:"platform"`
		Arch     string `json:"arch"`
	} `json:"files"`
}

// Cache success and failure so all matrix entries use one metadata snapshot.
func setupNodeResolver(client *http.Client) nodeResolver {
	type snapshot struct {
		data []byte
		err  error
		at   time.Time
	}
	cache := map[string]snapshot{}
	load := func(ctx context.Context, source string) snapshot {
		value, ok := cache[source]
		if !ok {
			value.data, value.err = fetchRuntimeMetadata(ctx, client, source)
			value.at = time.Now().UTC()
			cache[source] = value
		}
		return value
	}
	return func(ctx context.Context, selector, platform string) (NodeResolution, error) {
		if platform == "" {
			return NodeResolution{}, fmt.Errorf("cannot determine the CI platform/architecture for Node %q", selector)
		}
		if !latestNodeAlias(selector) {
			manifest := load(ctx, nodeVersionsManifest)
			version, err := "", manifest.err
			if err == nil {
				version, err = resolveManifestNode(manifest.data, selector, platform)
			}
			if err == nil {
				return NodeResolution{Version: version, Source: nodeVersionsManifest, ResolvedAt: manifest.at, Platform: platform}, nil
			}
			// setup-node needs the manifest to interpret LTS aliases. Numeric
			// selectors can instead resolve against official distribution files.
			if strings.HasPrefix(selector, "lts/") {
				return NodeResolution{}, err
			}
		}
		value := load(ctx, nodeDistributionIndex)
		if value.err != nil {
			return NodeResolution{}, value.err
		}
		constraint := selector
		if latestNodeAlias(selector) {
			constraint = "*"
		}
		version, err := resolveDistributionNode(value.data, constraint, platform)
		return NodeResolution{Version: version, Source: nodeDistributionIndex, ResolvedAt: value.at, Platform: platform}, err
	}
}
func latestNodeAlias(alias string) bool {
	return slices.Contains([]string{"current", "latest", "node"}, alias)
}

func resolveLatestNode(data []byte, platform string) (string, error) {
	return resolveDistributionNode(data, "*", platform)
}

func resolveDistributionNode(data []byte, selector, platform string) (string, error) {
	constraint, err := semver.NewConstraint(selector)
	if err != nil {
		return "", fmt.Errorf("unsupported Node selector %q: %w", selector, err)
	}
	var releases []struct {
		Version string   `json:"version"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("decode Node distribution index: %w", err)
	}
	var best *semver.Version
	for _, release := range releases {
		v, err := semver.StrictNewVersion(strings.TrimPrefix(release.Version, "v"))
		if err == nil && v.Prerelease() == "" && constraint.Check(v) && slices.Contains(release.Files, platform) && (best == nil || v.GreaterThan(best)) {
			best = v
		}
	}
	if best == nil {
		return "", fmt.Errorf("node distribution index has no release for %q on %q", selector, platform)
	}
	return best.String(), nil
}

func resolveManifestNode(data []byte, selector, platform string) (string, error) {
	var releases []nodeRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("decode setup-node manifest: %w", err)
	}
	if strings.HasPrefix(selector, "lts/") {
		names := map[uint64]string{}
		for _, r := range releases {
			v, e := semver.StrictNewVersion(r.Version)
			if e == nil && r.Stable && r.LTS != "" {
				names[v.Major()] = strings.ToLower(r.LTS)
			}
		}
		name := strings.ToLower(strings.TrimPrefix(selector, "lts/"))
		var majors []uint64
		for major, codename := range names {
			if name == "*" || strings.HasPrefix(name, "-") || name == codename {
				majors = append(majors, major)
			}
		}
		slices.Sort(majors)
		index := 0
		if strings.HasPrefix(name, "-") {
			var err error
			index, err = strconv.Atoi(strings.TrimPrefix(name, "-"))
			if err != nil || index < 0 {
				return "", fmt.Errorf("unknown LTS selector %q", selector)
			}
		}
		if index >= len(majors) {
			return "", fmt.Errorf("setup-node manifest has no release for %q", selector)
		}
		selector = strconv.FormatUint(majors[len(majors)-1-index], 10)
	}
	constraint, err := semver.NewConstraint(selector)
	if err != nil {
		return "", fmt.Errorf("unsupported Node selector %q: %w", selector, err)
	}
	var best *semver.Version
	for _, r := range releases {
		v, err := semver.StrictNewVersion(r.Version)
		if err != nil || !r.Stable || v.Prerelease() != "" || !constraint.Check(v) {
			continue
		}
		available := platform == "" // Empty platform is used only by pure alias unit tests.
		for _, file := range r.Files {
			p, a, _ := strings.Cut(platform, "-")
			a = strings.TrimSuffix(strings.TrimSuffix(a, "-exe"), "-tar")
			if p == "osx" {
				p = "darwin"
			}
			if p == "win" {
				p = "win32"
			}
			available = available || (file.Platform == p && file.Arch == a)
		}
		if available && (best == nil || v.GreaterThan(best)) {
			best = v
		}
	}
	if best == nil {
		return "", fmt.Errorf("setup-node manifest has no release for %q on %q", selector, platform)
	}
	return best.String(), nil
}

func setupNodeVersion(root string, step runtimeStep, row map[string]any) (string, error) {
	value, err := runtimeValue(step.With["node-version"], row)
	if err != nil || value != "" {
		return value, err
	}
	file, err := runtimeValue(step.With["node-version-file"], row)
	if err != nil || file == "" {
		return "", err
	}
	if !filepath.IsLocal(file) {
		return "", fmt.Errorf("node version file must be repository-relative: %s", file)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, file))
	if err != nil {
		return "", fmt.Errorf("cannot read Node version file %s", file)
	}
	if !filepath.IsLocal(repositoryPath(root, path)) {
		return "", fmt.Errorf("node version file escapes repository: %s", file)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return "", fmt.Errorf("cannot read bounded Node version file %s", file)
	}
	var manifest struct {
		Volta struct {
			Node string `json:"node"`
		} `json:"volta"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if json.Unmarshal(data, &manifest) == nil {
		if manifest.Volta.Node != "" {
			return manifest.Volta.Node, nil
		}
		if manifest.Engines.Node != "" {
			return manifest.Engines.Node, nil
		}
		return "", fmt.Errorf("node version file %s has no supported Node setting", file)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(data)), "v"), nil
}

// Unknown hosted architectures and self-hosted runners remain unverified.
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
