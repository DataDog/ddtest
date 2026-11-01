// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// sshjBuild configures every suite and moves integrationTest's sources, as
// hierynomus/sshj does.
const sshjBuild = `testing {
  suites {
    configureEach {
      useJUnitJupiter()
      targets { all { testTask.configure { include "**/*Test.*" } } }
    }
    test {
      sources { groovy { srcDirs = ['src/test/groovy'] } }
    }
    integrationTest(JvmTestSuite) {
      dependencies { implementation project() }
      sources {
        java { srcDirs = ['src/itest/java'] }
        resources { srcDirs = ['src/itest/resources'] }
      }
    }
  }
}
`

func TestTestSourceSets(t *testing.T) {
	for name, tc := range map[string]struct {
		build, task, set string
	}{
		"test task":                 {"", "test", "test"},
		"Groovy suite":              {sshjBuild, "integrationTest", "integrationTest"},
		"Kotlin suite":              {"testing { suites { val functionalTest by registering(JvmTestSuite::class) { } } }", "functionalTest", "functionalTest"},
		"Test task of a source set": {"tasks.register<Test>(\"integration\") {\n  testClassesDirs = sourceSets[\"it\"].output.classesDirs\n  classpath = sourceSets[\"it\"].runtimeClasspath\n}", "integration", "it"},
		"Groovy Test task":          {"task slow(type: Test) {\n  testClassesDirs = sourceSets.slow.output.classesDirs\n}", "slow", "slow"},
		"configured elsewhere":      {"tasks.register('e2e', Test)\ntasks.named('e2e') { testClassesDirs = sourceSets.getByName('e2e').output.classesDirs }", "e2e", "e2e"},
		"test task reconfigured":    {"tasks.test {\n  testClassesDirs = sourceSets.integrationTest.output.classesDirs\n}", "test", "integrationTest"},
		"Groovy test block":         {"test {\n  useJUnitPlatform()\n  testClassesDirs = sourceSets.it.output.classesDirs\n}", "test", "it"},
		"suite named test":          {sshjBuild, "test", "test"},
		"last assignment wins":      {"tasks.test { testClassesDirs = sourceSets.old.output.classesDirs }\ntasks.test {\n  testClassesDirs = sourceSets.it.output.classesDirs\n}", "test", "it"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "build.gradle", tc.build)
			project := Project{Path: ":", Directory: ".", BuildFile: "build.gradle"}
			set, ok := TestSourceSet(root, project, project, tc.task)
			require.True(t, ok)
			require.Equal(t, tc.set, set)
		})
	}
	root := t.TempDir()
	writeFile(t, root, "build.gradle", "tasks.register<Test>(\"ci\") { useJUnitPlatform() }\ntasks.register<Test>(\"computed\") { testClassesDirs = files(dirs) }\n")
	build := Project{Path: ":", Directory: ".", BuildFile: "build.gradle"}
	for _, task := range []string{"ci", "computed", "unknown"} {
		_, ok := TestSourceSet(root, build, build, task)
		require.False(t, ok, "%s's classes are not a source set ddtest can read", task)
	}

	// The root build may set a subproject's test classes.
	root = t.TempDir()
	writeFile(t, root, "app/build.gradle", "")
	app := Project{Path: ":app", Directory: "app", BuildFile: filepath.Join("app", "build.gradle")}
	writeFile(t, root, "build.gradle", "subprojects {\n  tasks.test { testClassesDirs = sourceSets.integrationTest.output.classesDirs }\n}\n")
	_, ok := TestSourceSet(root, build, app, "test")
	require.False(t, ok, "the root build reconfigures the subproject's test task")
	writeFile(t, root, "build.gradle", "subprojects { repositories { mavenCentral() } }\n")
	set, ok := TestSourceSet(root, build, app, "test")
	require.True(t, ok)
	require.Equal(t, "test", set)
}

func TestSourceDirectories(t *testing.T) {
	directory := func(build, set, language string) (string, bool) {
		t.Helper()
		root := t.TempDir()
		writeFile(t, root, "build.gradle", build)
		return SourceDirectory(root, Project{Path: ":", Directory: ".", BuildFile: "build.gradle"}, set, language)
	}
	for name, tc := range map[string]struct {
		build, set, language, directory string
	}{
		"default":                {"", "integrationTest", "java", filepath.Join("src", "integrationTest", "java")},
		"replaced in a suite":    {sshjBuild, "integrationTest", "java", filepath.Join("src", "itest", "java")},
		"test's Groovy":          {sshjBuild, "test", "groovy", filepath.Join("src", "test", "groovy")},
		"test's Java untouched":  {sshjBuild, "test", "java", filepath.Join("src", "test", "java")},
		"added, not replaced":    {"sourceSets { it { java { srcDir 'extra/java' } } }", "it", "java", filepath.Join("src", "it", "java")},
		"Kotlin setSrcDirs":      {"sourceSets { create(\"it\") { java { setSrcDirs(listOf(\"src/it-java\")) } } }", "it", "java", "src/it-java"},
		"one-line srcDirs":       {"sourceSets.it.java.srcDirs = ['tests/it']", "it", "java", filepath.Join("tests", "it")},
		"brace inside a literal": {"sourceSets { it { java { srcDirs = ['src/{it}/java'] } } }", "it", "java", filepath.Join("src", "{it}", "java")},
		"last replacement wins":  {"sourceSets.test.java.srcDirs = ['src/old/java']\nsourceSets { test { java { srcDirs = ['src/test/java'] } } }", "test", "java", filepath.Join("src", "test", "java")},
		"in file order":          {"sourceSets { test { java { srcDirs = ['src/test/java'] } } }\nsourceSets.test.java.srcDirs = ['src/old/java']", "test", "java", filepath.Join("src", "old", "java")},
		"dotted in the set":      {"sourceSets { integrationTest { java.srcDirs = ['src/it-java'] } }", "integrationTest", "java", "src/it-java"},
		"last in a block":        {"sourceSets { test { java {\n  srcDirs = ['src/old/java']\n  srcDirs = ['src/test/java']\n} } }", "test", "java", filepath.Join("src", "test", "java")},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := directory(tc.build, tc.set, tc.language)
			require.True(t, ok)
			require.Equal(t, tc.directory, got)
		})
	}
	for _, build := range []string{
		"sourceSets { it { java { srcDirs = files(generated) } } }",
		"sourceSets { it { java { srcDirs = [\"$rootDir/it\"] } } }",
		"sourceSets { it { java { srcDirs = ['../shared/it'] } } }",
		"sourceSets { it { java { srcDirs = ['src/' + 'it/java'] } } }",
	} {
		_, ok := directory(build, "it", "java")
		require.False(t, ok, build)
	}
}
