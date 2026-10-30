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

func TestGradleProbeCoversEveryFramework(t *testing.T) {
	drive, root := gradleFixture(t)
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	app := javaProbeTarget{directory: "app", module: ":app:test"}
	for name, path := range map[string]string{
		"junit4":    "app/src/test/java/ddtest/DdtestValidationTest.java",
		"junit5":    "app/src/test/java/ddtest/DdtestValidationTest.java",
		"testng":    "app/src/test/java/ddtest/DdtestValidationTest.java",
		"spock":     "app/src/test/groovy/ddtest/DdtestValidationTest.groovy",
		"scalatest": "app/src/test/scala/ddtest/DdtestValidationTest.scala",
		"munit":     "app/src/test/scala/ddtest/DdtestValidationTest.scala",
	} {
		probe, reason, err := build.probe(app, name, []string{"test"})
		require.NoError(t, err)
		require.Empty(t, reason, name)
		require.Equal(t, filepath.FromSlash(path), probe.files[0].path, name)
		require.Equal(t, []string{":app:test", "--tests", javaProbeClass}, probe.args[:3], name)
		require.Empty(t, probe.properties, name)
	}
	// ScalaTest suites a JUnit runner starts make the probe declare it.
	writeJavaSource(t, filepath.Join(root, "app/src/test/scala/example/CalcSpec.scala"), "package example\nimport org.scalatestplus.junit.JUnitRunner\n@org.junit.runner.RunWith(classOf[JUnitRunner])\nclass CalcSpec\n")
	probe, _, err := build.probe(app, "scalatest", nil)
	require.NoError(t, err)
	require.Contains(t, probe.files[0].source, "@org.junit.runner.RunWith(classOf[org.scalatestplus.junit.JUnitRunner])")

	// Cucumber and Karate select the probe feature through the task's runner.
	for name, runner := range map[string]string{"cucumber-java": "Cucumber runner class", "karate": "Karate runner class"} {
		_, reason, err := build.probe(app, name, nil)
		require.NoError(t, err)
		require.Contains(t, reason, "No "+runner, name)
		require.Contains(t, reason, "test sources of :app:test", name)
	}
	writeJavaSource(t, filepath.Join(root, "app/src/test/java/example/RunCucumberTest.java"), "package example;\n@org.junit.runner.RunWith(io.cucumber.junit.Cucumber.class)\n@io.cucumber.junit.CucumberOptions(glue = \"example.steps\")\npublic class RunCucumberTest {}\n")
	probe, reason, err := build.probe(app, "cucumber-java", []string{"test"})
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("app", "src", "test", "resources", "ddtest", "DdtestValidation.feature"), probe.files[0].path)
	require.Equal(t, filepath.Join("app", "src", "test", "java", "example", "steps", "DdtestValidationSteps.java"), probe.files[1].path, "the step joins the runner's glue")
	require.Equal(t, []string{":app:test", "--tests", "example.RunCucumberTest"}, probe.args[:3])
	require.Equal(t, []string{"-Dcucumber.features=classpath:ddtest/DdtestValidation.feature"}, probe.properties, "Gradle passes its own system properties to no test JVM")
	require.False(t, probe.filtered)

	// A JUnit Platform suite runs the probe beside its own features, by tag:
	// with cucumber.features, the root cucumber engine would run it too.
	writeJavaSource(t, filepath.Join(root, "app/src/test/java/example/RunCucumberTest.java"), "package example;\n@org.junit.platform.suite.api.Suite\n@org.junit.platform.suite.api.IncludeEngines(\"cucumber\")\n@org.junit.platform.suite.api.SelectClasspathResource(\"example\")\npublic class RunCucumberTest {}\n")
	_, reason, err = build.probe(app, "cucumber-java", nil)
	require.NoError(t, err)
	require.Contains(t, reason, "No Cucumber feature file was found in the test resources of :app:test")
	writeJavaSource(t, filepath.Join(root, "app/src/test/resources/example/calc.feature"), "Feature: Calculator\n")
	probe, _, err = build.probe(app, "cucumber-java", nil)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("app", "src", "test", "resources", "example", "DdtestValidation.feature"), probe.files[0].path)
	require.Contains(t, probe.files[0].source, "  @ddtest\n  Scenario: ddtestProbe")
	require.Equal(t, []string{"-Dcucumber.filter.tags=@ddtest"}, probe.properties)
	require.True(t, probe.filtered, "the suite reports the scenarios the tag excludes as skipped")

	writeJavaSource(t, filepath.Join(root, "app/src/test/java/example/KarateTest.java"), "package example;\nclass KarateTest {\n    @com.intuit.karate.junit5.Karate.Test com.intuit.karate.junit5.Karate calc() { return com.intuit.karate.junit5.Karate.run(\"classpath:example\"); }\n}\n")
	probe, reason, err = build.probe(app, "karate", []string{"test"})
	require.NoError(t, err)
	require.Empty(t, reason)
	feature := filepath.Join("app", "src", "test", "resources", "ddtest", "DdtestValidation.feature")
	require.Equal(t, feature, probe.files[0].path)
	require.Equal(t, []string{":app:test", "--tests", "example.KarateTest.calc"}, probe.args[:3], "Gradle selects a method with a dot")
	require.Equal(t, []string{"-Dkarate.options=--tags @ddtest file:" + filepath.ToSlash(filepath.Join(root, feature))}, probe.properties)
	require.Equal(t, "example.KarateTest", probe.harness)
}

func TestGradleProbeFeaturesFollowTheTaskSourceSet(t *testing.T) {
	drive, root := gradleFixture(t)
	requireWriteFile(t, filepath.Join(root, "app", "build.gradle.kts"), `testing {
    suites {
        val integrationTest by registering(JvmTestSuite::class) {
            sources {
                java { setSrcDirs(listOf("src/it/java")) }
                resources { setSrcDirs(listOf("src/it/features")) }
            }
        }
    }
}
`)
	writeJavaSource(t, filepath.Join(root, "app/src/it/java/example/KarateIT.java"), "package example;\nclass KarateIT {\n    @com.intuit.karate.junit5.Karate.Test com.intuit.karate.junit5.Karate all() { return com.intuit.karate.junit5.Karate.run(\"classpath:example\"); }\n}\n")
	build, err := drive.javaBuild(drive.args)
	require.NoError(t, err)
	probe, reason, err := build.probe(javaProbeTarget{directory: "app", module: ":app:integrationTest"}, "karate", nil)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("app", "src", "it", "features", "ddtest", "DdtestValidation.feature"), probe.files[0].path, "the feature goes among the suite's own resources")
	require.Equal(t, []string{":app:integrationTest", "--tests", "example.KarateIT.all"}, probe.args[:3])
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
