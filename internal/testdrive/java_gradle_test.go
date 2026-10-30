// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/stretchr/testify/require"
)

// gradleFixture is a three-project Gradle build: app tests with JUnit 5, lib
// with TestNG, and the root has no tests.
func gradleFixture(t *testing.T) (*Testdrive, string) {
	t.Helper()
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "settings.gradle.kts"), "include(\":app\", \":lib\")\n")
	for project, dependency := range map[string]string{"app": "org.junit.jupiter:junit-jupiter:5.11.4", "lib": "org.testng:testng:7.10.2"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, project), 0700))
		requireWriteFile(t, filepath.Join(root, project, "build.gradle.kts"), "dependencies { testImplementation(\""+dependency+"\") }\n")
	}
	t.Setenv("GRADLE_USER_HOME", t.TempDir())
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root, framework: framework.NewJVM("junit5", nil), command: "./gradlew", args: []string{"build"}}
	return drive, root
}

func TestGradleBuildMapsTestTasksToProjects(t *testing.T) {
	drive, _ := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	require.Len(t, build.projects(), 3)
	for module, want := range map[string]string{":app:test": "app", ":lib:test": "lib", ":test": "."} {
		directory, ok := build.directory(module)
		require.True(t, ok, module)
		require.Equal(t, want, directory, module)
	}
	for _, module := range []string{":app:integrationTest", ":missing:test", "app"} {
		_, ok := build.directory(module)
		require.False(t, ok, "a probe in src/test joins only its project's test task: %s", module)
	}
}

func TestGradleProbeRunsInTheProjectThatDeclaresTheFramework(t *testing.T) {
	drive, _ := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	module, ok := javaProbeModule(build, []nativeTest{{File: ":lib:test / example.LibTest"}, {File: ":app:test / example.AppTest"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, "app", module, "the project declaring JUnit 5 is preferred")
	probe, reason, err := build.probe(module, "junit5", drive.args)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("app", "src", "test", "java", "ddtest", "DdtestValidationTest.java"), probe.files[0].path)
	require.Equal(t, ":app:test", probe.args[0])
	require.Equal(t, []string{"--tests", javaProbeClass}, probe.args[1:3])
	_, ok = javaProbeModule(build, []nativeTest{{File: ":app:integrationTest / example.AppIT"}}, "junit5")
	require.False(t, ok, "no project ran its test task")
}

func TestGradleProbeCoversClassFrameworksOnly(t *testing.T) {
	drive, _ := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	for _, name := range gradleProbeFrameworks {
		_, reason, err := build.probe("app", name, nil)
		require.NoError(t, err)
		require.Empty(t, reason, name)
	}
	for _, name := range []string{"cucumber-java", "karate", "scalatest", "munit"} {
		_, reason, err := build.probe("app", name, nil)
		require.NoError(t, err)
		require.Contains(t, reason, "not available for Gradle builds yet", name)
	}
}
