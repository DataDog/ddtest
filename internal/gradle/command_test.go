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
}

func TestRunsTests(t *testing.T) {
	none := TestTasks{}
	declared := TestTasks{Declared: []string{"integrationTest"}}
	for args, runs := range map[string]bool{
		"test":                               true,
		"build":                              true,
		"build -x test":                      false,
		"build -xtest":                       false,
		"check --exclude-task test":          false,
		"check --exclude-task :app:test":     true,  // other projects' test still runs
		":app:build -x :app:test":            false, // the project's own test is removed
		":app:build -x test":                 false, // an unqualified selector removes every project's
		"build integrationTest -x test":      true,  // another requested test task still runs
		"integrationTest -x integrationTest": false,
		":app:test -x :app:test":             false,
		":app:test -x test":                  false,
		"test -x :app:test":                  true, // other projects' test still runs
		"build -x javadoc":                   true,
		"assemble":                           false,
	} {
		require.Equal(t, runs, RunsTests(strings.Fields(args), none), args)
	}
	require.True(t, RunsTests(strings.Fields("check -x test"), declared), "check runs the build's other test tasks")
	require.True(t, RunsTests(strings.Fields("check -x test"), TestTasks{Unread: true}), "build logic ddtest does not read may add test tasks")
	require.False(t, RunsTests(strings.Fields("integrationTest -x integrationTest"), declared))
}

func TestDryRun(t *testing.T) {
	require.True(t, DryRun(strings.Fields("test --dry-run")))
	require.True(t, DryRun(strings.Fields("-m build")))
	require.False(t, DryRun(strings.Fields("test")))
}

func TestConfigurationCacheProblemsTakesAValue(t *testing.T) {
	validated, omitted := TestInvocation(strings.Fields("--configuration-cache-problems warn --update-locks org.example:lib test"), TestTask)
	require.Equal(t, []string{"--configuration-cache-problems", "warn", "--update-locks", "org.example:lib", "test"}, validated)
	require.Empty(t, omitted, "warn is the option's value, not a task")
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
	for _, args := range []string{"-b other.gradle test", "--settings-file ci.settings.gradle test", "-I ci.gradle test", "--include-build ../lib test", "-Ici/init.gradle test", "-bci.gradle test", "-cci.settings.gradle test"} {
		_, err := ProjectDirectory(strings.Fields(args))
		require.Error(t, err, args)
	}
}

func TestTestInvocationKeepsTestTasksAndOptions(t *testing.T) {
	validated, omitted := TestInvocation(strings.Fields("--no-daemon -Pci=true clean build jacocoTestReport sonar --tests com.example.Ignored publish test --tests com.example.ApiTest -x javadoc"), TestTask)
	require.Equal(t, []string{"--no-daemon", "-Pci=true", "build", "test", "--tests", "com.example.ApiTest", "-x", "javadoc"}, validated)
	require.Equal(t, []string{"clean", "jacocoTestReport", "sonar", "publish"}, omitted, "--tests after sonar belongs to sonar")
	validated, omitted = TestInvocation(strings.Fields("test --fail-fast dependencyUpdates --revision=release --offline"), TestTask)
	require.Equal(t, []string{"test", "--fail-fast", "--offline"}, validated, "a task's options go with it, Gradle's own stay")
	require.Equal(t, []string{"dependencyUpdates"}, omitted)
}

func TestPublishes(t *testing.T) {
	require.True(t, Publishes(strings.Fields("build publish")))
	require.True(t, Publishes(strings.Fields(":lib:publishToMavenLocal")))
	require.True(t, Publishes(strings.Fields("uploadArchives")))
	require.False(t, Publishes(strings.Fields("build check")))
}

func TestHelpTaskSelectorIsNotATask(t *testing.T) {
	require.Equal(t, []string{"help"}, Tasks(strings.Fields("help --task test")))
	require.False(t, RunsTests(strings.Fields("help --task test"), TestTasks{}))
}

func TestAttachedShortOptionValuesRoundTrip(t *testing.T) {
	validated, _ := TestInvocation(strings.Fields("-xjavadoc -Dk=v -Pci test"), TestTask)
	require.Equal(t, []string{"-x", "javadoc", "-Dk=v", "-P", "ci", "test"}, validated, "an attached value is kept, written as Gradle reads it")
}

func TestNonTestTasks(t *testing.T) {
	for _, task := range []string{"clean", "assemble", "javadoc", "spotlessCheck", "detekt", "ktlintCheck", "sonar", "jacocoTestReport", "publish", "dependencies", "bootJar", ":app:distZip"} {
		require.True(t, NonTestTask(task), task)
	}
	for _, task := range []string{"ciTests", "verifyAll", "test", "e2e", "nativeTest", ":native-image-tests:nativeTest"} {
		require.False(t, NonTestTask(task), task)
	}
	for _, task := range []string{"okcurl:nativeBuild", "nativeCompile", "module-tests:imageRun", "jibDockerBuild", "bootBuildImage", "shadowJar"} {
		require.True(t, NonTestTask(task), task)
	}
}

func TestValidationArgs(t *testing.T) {
	require.Equal(t,
		[]string{"--info", "-Pci=true", "build", "--rerun-tasks", "--no-build-cache", "--continue", "--no-daemon", "-Dorg.gradle.configuration-cache=false"},
		ValidationArgs(strings.Fields("--info --build-cache --daemon -Pci=true build --configuration-cache --continue")),
		"every run executes its tests, isolated from the others")
}

func TestProbeArgs(t *testing.T) {
	require.Equal(t,
		[]string{"--info", "-Pci=true", ":app:test", "--tests", "ddtest.DdtestValidationTest", "--rerun-tasks", "--no-build-cache", "--continue", "--no-daemon", "-Dorg.gradle.configuration-cache=false"},
		ProbeArgs(strings.Fields("--info -Pci=true build test --tests com.example.ApiTest -x test -x javadoc"), ":app", "ddtest.DdtestValidationTest"),
		"only the probe's project test task runs, with the command's options")
	require.Equal(t,
		[]string{"-Pci=true", "--info", ":app:test", "--tests", "ddtest.DdtestValidationTest", "--rerun-tasks", "--no-build-cache", "--continue", "--no-daemon", "-Dorg.gradle.configuration-cache=false"},
		ProbeArgs(strings.Fields("test --fail-fast --rerun -Pci=true integrationTest --debug-jvm --info"), ":app", "ddtest.DdtestValidationTest"),
		"a task's own options stay with the tasks the probe does not run; Gradle's own options are kept wherever they were")
	require.Equal(t, ":test", ProbeArgs(nil, ":", "ddtest.DdtestValidationTest")[0], "the root project's test task")
}
