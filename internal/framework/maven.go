// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"context"
	"fmt"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// Maven exposes the native build command for onboarding and testdrive. Java's
// tracer owns discovery/skipping; ddtest's file-based planner is not supported.
type Maven struct {
	name     string
	platform PlatformEnvironment
}

func NewMaven(name string, platform PlatformEnvironment) *Maven {
	return &Maven{name: name, platform: platform}
}
func (m *Maven) Name() string                  { return m.name }
func (m *Maven) Platform() PlatformEnvironment { return m.platform }
func (m *Maven) Command() (string, []string) {
	if override := loadCommandOverride(); len(override) > 0 {
		return override[0], override[1:]
	}
	return maven.DefaultCommand()
}
func (m *Maven) TestPattern() string                      { return "src/test/**/*.{java,groovy,scala,feature}" }
func (m *Maven) SupportsFullTestDiscovery() bool          { return false }
func (m *Maven) SourceFileForSuite(string) (string, bool) { return "", false }
func (m *Maven) HasUnskippableMarker(string) bool         { return false }
func (m *Maven) DiscoverTestFiles(context.Context, discovery.TestFileSet) ([]string, error) {
	return nil, m.unsupported()
}
func (m *Maven) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, m.unsupported()
}
func (m *Maven) RunTests(context.Context, []string, map[string]string) error { return m.unsupported() }
func (m *Maven) unsupported() error {
	return fmt.Errorf("java supports onboard and testdrive with Maven; run native Maven tests for tracer-managed optimization (ddtest plan/run are not supported)")
}
