// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// ErrJVMPlanRun explains why plan and run reject Java: the Datadog Java agent
// discovers and skips tests inside the Maven or Gradle build itself.
var ErrJVMPlanRun = errors.New("ddtest plan and run do not support Java; the Datadog Java agent performs Test Impact Analysis inside the Maven or Gradle build, so run the project's build command directly")

// JVM runs a JVM test framework through the project's Maven or Gradle build.
// Name is the selected framework, such as junit5; every framework of a build
// shares one command.
type JVM struct {
	name     string
	platform PlatformEnvironment
}

func NewJVM(name string, platform PlatformEnvironment) *JVM {
	return &JVM{name: name, platform: platform}
}

func (j *JVM) Name() string                  { return j.name }
func (j *JVM) Platform() PlatformEnvironment { return j.platform }

// Command runs the build the working directory declares, through its wrapper
// when it has one, as CI usually does: Maven when there is a POM, else Gradle
// when there are Gradle settings or build files.
func (j *JVM) Command() (string, []string) {
	if override := loadCommandOverride(); len(override) > 0 {
		return override[0], override[1:]
	}
	if !regularFile("pom.xml") && slices.ContainsFunc(append(slices.Clone(gradle.SettingsFiles), gradle.BuildFiles...), regularFile) {
		if regularFile("gradlew") {
			return "./gradlew", []string{"test"}
		}
		return "gradle", []string{"test"}
	}
	if regularFile("mvnw") {
		return "./mvnw", []string{"test"}
	}
	return "mvn", []string{"test"}
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (j *JVM) TestPattern() string                      { return "src/test/**/*" }
func (j *JVM) SupportsFullTestDiscovery() bool          { return false }
func (j *JVM) SourceFileForSuite(string) (string, bool) { return "", false }
func (j *JVM) HasUnskippableMarker(string) bool         { return false }

func (j *JVM) DiscoverTestFiles(context.Context, discovery.TestFileSet) ([]string, error) {
	return nil, ErrJVMPlanRun
}

func (j *JVM) DiscoverTests(context.Context, discovery.TestFileSet) ([]testoptimization.Test, error) {
	return nil, ErrJVMPlanRun
}

func (j *JVM) RunTests(context.Context, []string, map[string]string) error {
	return ErrJVMPlanRun
}
