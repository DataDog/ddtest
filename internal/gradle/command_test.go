// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTasksSkipOptionValues(t *testing.T) {
	args := strings.Fields("--no-daemon -Pprofile=ci -D org.gradle.caching=false --console plain -x javadoc :app:test --tests com.example.ApiTest check")
	require.Equal(t, []string{":app:test", "com.example.ApiTest", "check"}[0:1], Tasks(args)[0:1])
	require.Equal(t, []string{":app:test", "check"}, Tasks(args), "--tests takes a value")
}

func TestTestTasks(t *testing.T) {
	for _, task := range []string{"test", ":app:test", "check", "build", "integrationTest", ":svc:functionalTest", "jvmTest"} {
		require.True(t, TestTask(task), task)
	}
	for _, task := range []string{"assemble", "javadoc", "jacocoTestReport", "publish", "testClasses", "sonar", "dependencies", ":android-test:assembleDebugAndroidTest", "compileTestJava", "bundleReleaseAndroidTest"} {
		require.False(t, TestTask(task), task)
	}
	require.True(t, IsTestCommand([]string{"./gradlew", "--no-daemon", "build"}))
	require.False(t, IsTestCommand([]string{"./gradlew", "assemble"}))
	require.False(t, IsTestCommand([]string{"mvn", "test"}))
}

func TestSkipsTests(t *testing.T) {
	require.True(t, SkipsTests(strings.Fields("build -x test")))
	require.True(t, SkipsTests(strings.Fields("check --exclude-task :app:test")))
	require.True(t, SkipsTests(strings.Fields("build -xtest")))
	require.False(t, SkipsTests(strings.Fields("build integrationTest -x test")), "another test task still runs")
	require.False(t, SkipsTests(strings.Fields("build -x javadoc")))
}

func TestProjectDirectory(t *testing.T) {
	directory, err := ProjectDirectory(strings.Fields("-p services/api test"))
	require.NoError(t, err)
	require.Equal(t, "services/api", directory)
	directory, err = ProjectDirectory(strings.Fields("--project-dir=web test"))
	require.NoError(t, err)
	require.Equal(t, "web", directory)
	directory, err = ProjectDirectory(strings.Fields("test"))
	require.NoError(t, err)
	require.Equal(t, ".", directory)
	for _, args := range []string{"-b other.gradle test", "--settings-file ci.settings.gradle test", "-I ci.gradle test", "--include-build ../lib test"} {
		_, err := ProjectDirectory(strings.Fields(args))
		require.Error(t, err, args)
	}
}

func TestTestInvocationKeepsTestTasksAndOptions(t *testing.T) {
	validated, omitted := TestInvocation(strings.Fields("--no-daemon -Pci=true clean build jacocoTestReport sonar --tests com.example.Ignored publish test --tests com.example.ApiTest -x javadoc"))
	require.Equal(t, []string{"--no-daemon", "-Pci=true", "build", "test", "--tests", "com.example.ApiTest", "-x", "javadoc"}, validated)
	require.Equal(t, []string{"clean", "jacocoTestReport", "sonar", "publish"}, omitted, "--tests after sonar belongs to sonar")
}

func TestPublishes(t *testing.T) {
	require.True(t, Publishes(strings.Fields("build publish")))
	require.True(t, Publishes(strings.Fields(":lib:publishToMavenLocal")))
	require.True(t, Publishes(strings.Fields("uploadArchives")))
	require.False(t, Publishes(strings.Fields("build check")))
}
