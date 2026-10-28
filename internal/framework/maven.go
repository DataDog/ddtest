// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"context"
	"errors"
	"os"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// ErrMavenPlanRun explains why plan and run reject Java: the Datadog Java agent
// discovers and skips tests inside the Maven build itself.
var ErrMavenPlanRun = errors.New("ddtest plan and run do not support Java; the Datadog Java agent performs Test Impact Analysis inside the Maven build, so run the project's Maven command directly")

// Maven runs a JVM test framework through the project's Maven build. Name is
// the selected framework, such as junit5; every framework shares one command.
type Maven struct {
	name     string
	platform PlatformEnvironment
}

func NewMaven(name string, platform PlatformEnvironment) *Maven {
	return &Maven{name: name, platform: platform}
}

func (m *Maven) Name() string                  { return m.name }
func (m *Maven) Platform() PlatformEnvironment { return m.platform }

// Command prefers the project's Maven wrapper, as CI usually does.
func (m *Maven) Command() (string, []string) {
	if override := loadCommandOverride(); len(override) > 0 {
		return override[0], override[1:]
	}
	if info, err := os.Stat("mvnw"); err == nil && !info.IsDir() {
		return "./mvnw", []string{"test"}
	}
	return "mvn", []string{"test"}
}

func (m *Maven) TestPattern() string                      { return "src/test/**/*" }
func (m *Maven) SupportsFullTestDiscovery() bool          { return false }
func (m *Maven) SourceFileForSuite(string) (string, bool) { return "", false }
func (m *Maven) HasUnskippableMarker(string) bool         { return false }

func (m *Maven) DiscoverTestFiles(context.Context, discovery.TestFileSet) ([]string, error) {
	return nil, ErrMavenPlanRun
}

func (m *Maven) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrMavenPlanRun
}

func (m *Maven) RunTests(context.Context, []string, map[string]string) error {
	return ErrMavenPlanRun
}
