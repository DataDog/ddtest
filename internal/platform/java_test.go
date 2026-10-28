// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

const junit5POM = `<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`

func TestJavaIsDetectedOnlyWhereNoOtherPlatformIs(t *testing.T) {
	for _, tc := range []struct {
		name, platform, framework string
		files                     map[string]string
		want                      string
	}{
		{name: "Maven project", files: map[string]string{"pom.xml": junit5POM}, want: "java"},
		{name: "with a JavaScript project", files: map[string]string{"pom.xml": junit5POM, "package.json": `{"devDependencies":{"jest":"30"}}`}, want: "javascript"},
		{name: "with a Ruby project", files: map[string]string{"pom.xml": junit5POM, "Gemfile": "gem 'rspec'\n"}, want: "ruby"},
		{name: "selected platform", platform: "java", files: map[string]string{"pom.xml": junit5POM, "package.json": `{}`}, want: "java"},
		{name: "selected Maven framework", framework: "junit5", files: map[string]string{"package.json": `{"devDependencies":{"jest":"30"}}`}, want: "java"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDetectionSettings(t)
			settings.Get().Platform, settings.Get().Framework = tc.platform, tc.framework
			t.Chdir(t.TempDir())
			for name, contents := range tc.files {
				require.NoError(t, os.WriteFile(name, []byte(contents), 0600))
			}
			detected, err := DetectPlatform()
			require.NoError(t, err)
			require.Equal(t, tc.want, detected.Name())
		})
	}
}

func TestOtherPlatformsStillConflict(t *testing.T) {
	resetDetectionSettings(t)
	t.Chdir(t.TempDir())
	for name, contents := range map[string]string{"pom.xml": junit5POM, "package.json": `{"devDependencies":{"jest":"30"}}`, "Gemfile": "gem 'rspec'\n"} {
		require.NoError(t, os.WriteFile(name, []byte(contents), 0600))
	}
	_, err := DetectPlatform()
	require.ErrorContains(t, err, "found multiple platforms (javascript, ruby)")
}

func TestJavaDetectsMavenFrameworks(t *testing.T) {
	resetDetectionSettings(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("pom.xml", []byte(junit5POM), 0600))
	selected, err := NewJava().DetectFramework()
	require.NoError(t, err)
	require.Equal(t, "junit5", selected.Name())
	_, err = selected.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	require.ErrorIs(t, err, framework.ErrMavenPlanRun)

	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></project>`), 0600))
	_, err = NewJava().DetectFramework()
	require.ErrorContains(t, err, "found multiple test frameworks (junit4, testng)")
	settings.Get().Framework = "testng"
	selected, err = NewJava().DetectFramework()
	require.NoError(t, err)
	require.Equal(t, "testng", selected.Name())
	settings.Get().Framework = "jest"
	_, err = NewJava().DetectFramework()
	require.ErrorContains(t, err, "framework 'jest' is not supported by platform 'java'")
}

func TestJavaRejectsPlanAndRun(t *testing.T) {
	_, err := NewJava().CreateTagsMap(context.Background())
	require.ErrorIs(t, err, framework.ErrMavenPlanRun)
}

func writeAgentJAR(t *testing.T, path, manifest string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	entry, err := writer.Create("META-INF/MANIFEST.MF")
	require.NoError(t, err)
	_, err = entry.Write([]byte(manifest))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
}

// The manifest of dd-java-agent 1.67.1, with its CRLF line endings.
const agentManifest = "Manifest-Version: 1.0\r\nImplementation-Title: dd-java-agent\r\nImplementation-Version: 1.67.1\r\nMain-Class: datadog.trace.bootstrap.AgentBootstrap\r\nPremain-Class: datadog.trace.bootstrap.AgentPreCheck\r\n\r\n"

func TestJavaAgentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dd-java-agent.jar")
	writeAgentJAR(t, path, agentManifest)
	version, err := JavaAgentVersion(path)
	require.NoError(t, err)
	require.Equal(t, "1.67.1", version)

	writeAgentJAR(t, path, "Implementation-Title: other-agent\r\nImplementation-Version: 1.0\r\nPremain-Class: datadog.trace.bootstrap.AgentPreCheck\r\n")
	_, err = JavaAgentVersion(path)
	require.ErrorContains(t, err, "is not the Datadog Java agent")
	require.NoError(t, os.WriteFile(path, []byte("not a JAR"), 0600))
	_, err = JavaAgentVersion(path)
	require.Error(t, err)
}

func TestJavaDetectTracerReusesTracerFolder(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("DD_TRACER_FOLDER", directory)
	_, err := NewJava().DetectTracer(context.Background(), TracerOptions{})
	require.Error(t, err, "an empty tracer folder has no agent")
	writeAgentJAR(t, filepath.Join(directory, "dd-java-agent.jar"), agentManifest)
	path, err := NewJava().DetectTracer(context.Background(), TracerOptions{})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "dd-java-agent.jar"), path)
}

func TestJavaTracerInstallCommand(t *testing.T) {
	directory := t.TempDir()
	for version, url := range map[string]string{
		"":       "https://dtdg.co/latest-java-tracer",
		"latest": "https://dtdg.co/latest-java-tracer",
		"1.67.1": "https://repo.maven.apache.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar",
	} {
		command, args, err := NewJava().TracerInstallCommand(TracerOptions{Version: version, Directory: directory})
		require.NoError(t, err)
		require.Equal(t, "curl", command)
		require.Equal(t, url, args[len(args)-1])
		require.Contains(t, args, filepath.Join(directory, "dd-java-agent.jar"))
	}
	for _, version := range []string{"git:main", "../1.67.1", "1.67.1;echo", "1.67"} {
		_, _, err := NewJava().TracerInstallCommand(TracerOptions{Version: version})
		require.Error(t, err, version)
	}
}
