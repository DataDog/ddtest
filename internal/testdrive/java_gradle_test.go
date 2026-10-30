// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
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
	for _, module := range []string{":app:integrationTest", ":missing:test", "app", ":app:"} {
		_, ok := build.directory(module)
		require.False(t, ok, "%s is no test task whose sources ddtest reads", module)
	}
}

func TestGradleProbeRunsInTheProjectThatDeclaresTheFramework(t *testing.T) {
	drive, _ := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	module, ok := javaProbeModule(build, []nativeTest{{File: ":lib:test / example.LibTest"}, {File: ":app:test / example.AppTest"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, javaProbeTarget{directory: "app", module: ":app:test"}, module, "the project declaring JUnit 5 is preferred")
	probe, reason, err := build.probe(module, "junit5", drive.args)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("app", "src", "test", "java", "ddtest", "DdtestValidationTest.java"), probe.files[0].path)
	require.Equal(t, ":app:test", probe.args[0])
	require.Equal(t, []string{"--tests", javaProbeClass}, probe.args[1:3])
	_, ok = javaProbeModule(build, []nativeTest{{File: ":app:integrationTest / example.AppIT"}}, "junit5")
	require.False(t, ok, "no project ran a test task whose sources ddtest reads")
}

func TestGradleProbeJoinsTheTestTaskThatRan(t *testing.T) {
	drive, root := gradleFixture(t)
	requireWriteFile(t, filepath.Join(root, "app", "build.gradle.kts"), `dependencies { testImplementation("org.junit.jupiter:junit-jupiter:5.11.4") }
testing {
    suites {
        val integrationTest by registering(JvmTestSuite::class) {
            sources { java { setSrcDirs(listOf("src/itest/java")) } }
        }
    }
}
`)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	// A CI step that runs only the suite, as ./gradlew integrationTest.
	target, ok := javaProbeModule(build, []nativeTest{{File: ":app:integrationTest / example.AppIT"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, javaProbeTarget{directory: "app", module: ":app:integrationTest"}, target)
	probe, reason, err := build.probe(target, "junit5", []string{"integrationTest"})
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("app", "src", "itest", "java", "ddtest", "DdtestValidationTest.java"), probe.files[0].path, "the probe is compiled with the suite's own sources")
	require.Equal(t, []string{":app:integrationTest", "--tests", javaProbeClass}, probe.args[:3], "and run through the suite's task")
	// Where both ran, the test task is preferred.
	target, ok = javaProbeModule(build, []nativeTest{{File: ":app:integrationTest / example.AppIT"}, {File: ":app:test / example.AppTest"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, ":app:test", target.module)
	// Sources build logic computes leave the probe nowhere to go.
	requireWriteFile(t, filepath.Join(root, "app", "build.gradle.kts"), "testing { suites { val integrationTest by registering(JvmTestSuite::class) { sources { java { setSrcDirs(generatedDirs) } } } } }\n")
	build, err = drive.javaBuild(drive.args)
	require.NoError(t, err)
	_, reason, err = build.probe(javaProbeTarget{directory: "app", module: ":app:integrationTest"}, "junit5", nil)
	require.NoError(t, err)
	require.Contains(t, reason, "java sources of :app:integrationTest are set by build logic ddtest does not read")
}

func TestGradleProbeCoversClassFrameworksOnly(t *testing.T) {
	drive, _ := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	app := javaProbeTarget{directory: "app", module: ":app:test"}
	for _, name := range gradleProbeFrameworks {
		probe, reason, err := build.probe(app, name, nil)
		require.NoError(t, err)
		require.Empty(t, reason, name)
		if name == "spock" {
			require.Equal(t, filepath.Join("app", "src", "test", "groovy", "ddtest", "DdtestValidationTest.groovy"), probe.files[0].path)
		}
	}
	for _, name := range []string{"cucumber-java", "karate", "scalatest", "munit"} {
		_, reason, err := build.probe(app, name, nil)
		require.NoError(t, err)
		require.Contains(t, reason, "not available for Gradle builds yet", name)
	}
}

func TestGradlePreflightCertifiesTheJDKGradleSelects(t *testing.T) {
	drive, root := gradleFixture(t)
	drive.configurationEnvironment = map[string]string{"JAVA_HOME": writeJDK(t, "17.0.20")}
	preflight := func() *preflightCheck {
		t.Helper()
		result := &validationResult{}
		require.NoError(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result))
		return result.Preflight
	}
	require.Equal(t, "17.0.20", preflight().Runtime, "without org.gradle.java.home, Gradle runs on JAVA_HOME")

	requireWriteFile(t, filepath.Join(root, "gradle.properties"), "org.gradle.java.home = "+writeJDK(t, "21.0.5")+"\n")
	check := preflight()
	require.Equal(t, "21.0.5", check.Runtime, "Gradle runs, and forks test JVMs, on the JDK the build selects")
	require.Contains(t, check.Verdict.Reason, "Java 21.0.5 (Java from org.gradle.java.home in "+filepath.Join(root, "gradle.properties")+")")

	requireWriteFile(t, filepath.Join(os.Getenv("GRADLE_USER_HOME"), "gradle.properties"), "org.gradle.java.home: "+writeJDK(t, "11.0.25")+"\n")
	require.Equal(t, "11.0.25", preflight().Runtime, "the user home's gradle.properties takes precedence over the build's")

	drive.configurationEnvironment["GRADLE_OPTS"] = "-Xmx1g -Dorg.gradle.java.home=" + writeJDK(t, "23.0.2")
	require.Equal(t, "23.0.2", preflight().Runtime, "the launcher's JVM options take precedence over gradle.properties")
	drive.args = []string{"build", "-Dorg.gradle.java.home=" + writeJDK(t, "25.0.1")}
	require.Equal(t, "25.0.1", preflight().Runtime, "the command line takes precedence over both")

	drive.args = []string{"build", "-Dorg.gradle.java.home=" + t.TempDir()}
	result := &validationResult{}
	require.ErrorContains(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result), "which is not a JDK")
	require.Equal(t, "inconclusive", result.Preflight.Verdict.Status)
}

func TestGradleRunsUseTheUserHomeValidationReads(t *testing.T) {
	drive, _ := gradleFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GRADLE_USER_HOME", "")
	environment := func() map[string]string {
		t.Helper()
		build, err := drive.javaBuild(drive.args)
		require.NoError(t, err)
		env := map[string]string{}
		build.environment(env)
		return env
	}
	require.Equal(t, filepath.Join(home, ".gradle"), environment()["GRADLE_USER_HOME"], "Gradle uses the home whose gradle.properties was read, not the JVM's user.home")
	t.Setenv("GRADLE_USER_HOME", "/ci/gradle")
	require.NotContains(t, environment(), "GRADLE_USER_HOME", "an environment's own home is kept")
	t.Setenv("GRADLE_USER_HOME", "")
	drive.args = []string{"build", "-g", "ci-home"}
	require.NotContains(t, environment(), "GRADLE_USER_HOME", "a command's own home is kept")
}
