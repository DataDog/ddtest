// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}

func TestProjectsReadsKotlinAndGroovySettings(t *testing.T) {
	for name, settings := range map[string]string{
		"settings.gradle.kts": `pluginManagement {
    includeBuild("build-logic")
}
rootProject.name = "shop"
include(
    ":api",
    ":services:orders", // a nested project
)
project(":legacy").projectDir = file("old/legacy")
include(":legacy")
`,
		"settings.gradle": `rootProject.name = 'shop'
/* include ':ignored' */
include ':api',
        ':services:orders'
include 'legacy'
project(':legacy').projectDir = file('old/legacy')
`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, name, settings)
			writeFile(t, root, "api/build.gradle.kts", `dependencies { testImplementation("org.junit.jupiter:junit-jupiter:5.11.4") }`)
			writeFile(t, root, "old/legacy/build.gradle", `dependencies { testImplementation group: 'junit', name: 'junit', version: '4.13.2' }`)
			projects, err := Projects(root, ".")
			require.NoError(t, err)
			var paths, directories []string
			for _, project := range projects {
				paths = append(paths, project.Path)
				directories = append(directories, project.Directory)
			}
			require.ElementsMatch(t, []string{":", ":api", ":services", ":services:orders", ":legacy"}, paths, "an included path includes its parents")
			require.Contains(t, directories, filepath.Join("old", "legacy"))
			require.Equal(t, ":", projects[0].Path, "the root project comes first")
			require.Equal(t, []string{"junit4", "junit5"}, Frameworks(projects))
		})
	}
}

func TestProjectsRejectsProjectListsBuildLogicComputes(t *testing.T) {
	for _, settings := range []string{
		`file("modules").listFiles().forEach { include(":${it.name}") }`,
		`include(*modules.toTypedArray())`,
		`include ":${name}"`,
		`project(':a').projectDir = new File(parentDir, 'a')` + "\ninclude ':a'\n",
		`includeFlat 'sibling'`,
		"include ':module-' + System.getenv('MODULE')\n",
		`include(":a" + suffix)`,
	} {
		root := t.TempDir()
		writeFile(t, root, "settings.gradle", settings)
		_, err := Projects(root, ".")
		require.Error(t, err, settings)
	}
}

func TestProjectsReadsTheVersionCatalog(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle.kts", `include(":app")`)
	writeFile(t, root, "gradle/libs.versions.toml", `[versions]
junit = "5.11.4"

[libraries]
junit-jupiter = { module = "org.junit.jupiter:junit-jupiter", version.ref = "junit" }
junit-jupiter-params = { group = "org.junit.jupiter", name = "junit-jupiter-params" }
testng = "org.testng:testng:7.10.2"
spock-core = { module = "org.spockframework:spock-core", version = "2.3-groovy-4.0" }

[bundles]
spock = ["spock-core"]
`)
	writeFile(t, root, "app/build.gradle.kts", `dependencies {
    testImplementation(libs.junit.jupiter.params)
    testImplementation(libs.bundles.spock)
}`)
	projects, err := Projects(root, ".")
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Equal(t, []string{"junit5", "spock"}, projects[1].Frameworks(), "libs.junit.jupiter.params is not libs.junit.jupiter, and testng is unused")
}

func TestBuildFindsTheNearestSettings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\n")
	writeFile(t, root, "tools/standalone/build.gradle", "")
	build, err := Build(root, "app")
	require.NoError(t, err)
	require.Equal(t, ".", build, "a subproject belongs to the build whose settings include it")
	build, err = Build(root, filepath.Join("tools", "standalone"))
	require.NoError(t, err)
	require.Equal(t, ".", build, "settings above the directory still define the build")
	empty := t.TempDir()
	writeFile(t, empty, "lib/build.gradle", "")
	build, err = Build(empty, "lib")
	require.NoError(t, err)
	require.Equal(t, "lib", build, "without settings, the directory is a single-project build")
	_, err = Build(root, "..")
	require.Error(t, err)
}

func TestStripCommentsKeepsStrings(t *testing.T) {
	require.Equal(t, "include \"http://x//y\" \nb\n", stripComments("include \"http://x//y\" // note\nb/* c\n*/"))
}

func TestProjectsPrefersGroovyFilesLikeGradle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\n")
	writeFile(t, root, "settings.gradle.kts", "file(\"x\").listFiles().forEach { include(it.name) }\n")
	writeFile(t, root, "app/build.gradle", "dependencies { testImplementation 'org.testng:testng:7.10.2' }\n")
	writeFile(t, root, "app/build.gradle.kts", "dependencies { testImplementation(\"org.junit.jupiter:junit-jupiter:5.11.4\") }\n")
	projects, err := Projects(root, ".")
	require.NoError(t, err, "the ignored Kotlin settings are not read")
	require.Len(t, projects, 2)
	require.Equal(t, filepath.Join("app", "build.gradle"), projects[1].BuildFile)
	require.Equal(t, []string{"testng"}, projects[1].Frameworks())
}

func TestProjectsStayInsideTheRepositoryThroughSymlinks(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "build.gradle", "dependencies { testImplementation 'org.testng:testng:7.10.2' }\n")
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\nproject(':app').projectDir = file('linked')\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))
	_, err := Projects(root, ".")
	require.ErrorContains(t, err, "leaves the repository through a symlink")
	// A symlink to a directory inside the checkout is read.
	writeFile(t, root, "real/app/build.gradle", "")
	require.NoError(t, os.Remove(filepath.Join(root, "linked")))
	require.NoError(t, os.Symlink(filepath.Join(root, "real", "app"), filepath.Join(root, "linked")))
	_, err = Projects(root, ".")
	require.NoError(t, err)
}

func TestDeclaredTestTasks(t *testing.T) {
	for build, name := range map[string]string{
		`tasks.register<Test>("integrationTest") { }`:                                                   "integrationTest",
		`task ciTests(type: Test) { }`:                                                                  "ciTests",
		`tasks.register("slowTest", Test) { }`:                                                          "slowTest",
		`val e2eTest by tasks.registering(Test::class)`:                                                 "e2eTest",
		"testing {\n    suites {\n        val functional by registering(JvmTestSuite::class)\n    }\n}": "functional",
		"testing {\n    suites {\n        acceptance(JvmTestSuite) { }\n    }\n}":                       "acceptance",
	} {
		root := t.TempDir()
		writeFile(t, root, "settings.gradle", "include 'app'\n")
		writeFile(t, root, "app/build.gradle.kts", build)
		projects, err := Projects(root, ".")
		require.NoError(t, err)
		tasks := DeclaredTestTasks(root, ".", projects)
		require.Equal(t, []string{name}, tasks.Declared, build)
		require.False(t, tasks.Unread, build)
		require.True(t, tasks.Others())
		require.True(t, tasks.Runs(":app:"+name))
	}
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\n")
	writeFile(t, root, "app/build.gradle", "tasks.withType(Test) { useJUnitPlatform() }\n")
	projects, err := Projects(root, ".")
	require.NoError(t, err)
	require.False(t, DeclaredTestTasks(root, ".", projects).Others(), "configuring test tasks declares none")
	// Configuring existing test tasks or the default suite declares none.
	for _, build := range []string{
		"tasks.withType<Test> { useJUnitPlatform() }\n",
		"tasks.named<Test>(\"test\") { maxParallelForks = 2 }\n",
		"testing {\n    suites {\n        val test by getting(JvmTestSuite::class) { useJUnitJupiter() }\n    }\n}\n",
		"tasks.withType(Test::class.java) { }\n",
	} {
		writeFile(t, root, "app/build.gradle.kts", build)
		projects, err := Projects(root, ".")
		require.NoError(t, err)
		require.False(t, DeclaredTestTasks(root, ".", projects).Others(), build)
	}
	require.NoError(t, os.Remove(filepath.Join(root, "app", "build.gradle.kts")))
	// A computed name is unread, even next to a literal one.
	for _, build := range []string{
		"tasks.register<Test>(\"smokeTest\")\ntasks.register<Test>(computedName)\n",
		"tasks.register(suiteName, Test) { }\n",
	} {
		writeFile(t, root, "app/build.gradle", build)
		tasks := DeclaredTestTasks(root, ".", projects)
		require.True(t, tasks.Unread, build)
	}
	writeFile(t, root, "app/build.gradle", "tasks.register<Test>(\"smokeTest\")\ntasks.register<Test>(computedName)\n")
	require.Equal(t, []string{"smokeTest"}, DeclaredTestTasks(root, ".", projects).Declared, "the literal name is still read")
	writeFile(t, root, "app/build.gradle", "")
	writeFile(t, root, "buildSrc/src/main/kotlin/conventions.gradle.kts", "")
	require.True(t, DeclaredTestTasks(root, ".", projects).Unread, "convention plugins ddtest does not read may declare some")
}

func TestTasksDeclaredWithAnotherTypeRunNoTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'lib'\n")
	writeFile(t, root, "lib/build.gradle", `for (version in versions) {
    tasks.register("testInJava-${version}", Test) { }
}
task('apiDiff', type: JapicmpTask, dependsOn: 'jar') { }
task jmh(type: me.champeau.jmh.JMHTask) { }
tasks.register('runJMH', JavaExec) { }
tasks.register<Jar>("sourcesJar")
tasks.register("slowTest", Test) { }
tasks.register("ciTests") { dependsOn(test) }
tasks.register<SmokeTest>("smoke")
tasks.register('ci', com.example.CustomTest)
`)
	projects, err := Projects(root, ".")
	require.NoError(t, err)
	tasks := DeclaredTestTasks(root, ".", projects)
	require.True(t, tasks.Unread, "a computed Test task name")
	require.Equal(t, []string{"apiDiff", "jmh", "runJMH", "sourcesJar"}, tasks.Other)
	for _, task := range []string{"apiDiff", ":lib:apiDiff", "jmh", "javadoc", "closeSonatypeStagingRepository"} {
		require.True(t, tasks.NonTest(task), task)
	}
	for _, task := range []string{"slowTest", "ciTests", "testInJava-11", "check", "smoke", "ci"} {
		require.False(t, tasks.NonTest(task), "%s may run tests", task)
	}
}

func TestProjectsReadsFileProjectDirectories(t *testing.T) {
	for _, tc := range []struct{ name, settings string }{
		{"settings.gradle", "include ':java-jwt'\nproject(':java-jwt').projectDir = new File(rootProject.projectDir, '/lib')\n"},
		{"settings.gradle", "include ':java-jwt'\nproject(':java-jwt').projectDir = new java.io.File(settingsDir, 'lib')\n"},
		{"settings.gradle.kts", "include(\":java-jwt\")\nproject(\":java-jwt\").projectDir = File(rootDir, \"lib\")\n"},
	} {
		root := t.TempDir()
		writeFile(t, root, tc.name, tc.settings)
		writeFile(t, root, "lib/build.gradle", "dependencies { testImplementation 'junit:junit:4.13.2' }\n")
		projects, err := Projects(root, ".")
		require.NoError(t, err, tc.settings)
		require.Len(t, projects, 2)
		require.Equal(t, ":java-jwt", projects[1].Path)
		require.Equal(t, "lib", projects[1].Directory, tc.settings)
		require.Equal(t, []string{"junit4"}, Frameworks(projects))
	}
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'lib'\nproject(':lib').projectDir = new File(rootDir, '../shared/lib')\n")
	_, err := Projects(root, ".")
	require.Error(t, err, "a directory outside the build")
}

func TestProjectsRejectsComputedOrAbsoluteProjectDirectories(t *testing.T) {
	for _, settings := range []string{
		"include(\":app\")\nproject(\":app\").projectDir = file(\"modules\").resolve(\"app\")\n",
		"include 'lib'\nproject(':lib').projectDir = file('/shared/lib')\n",
	} {
		root := t.TempDir()
		writeFile(t, root, "settings.gradle.kts", settings)
		_, err := Projects(root, ".")
		require.Error(t, err, settings)
	}
}

func TestProjectsReadsNoFileThatLinksOutsideTheRepository(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "build.gradle", "task ciTests(type: Test)\n")
	writeFile(t, outside, "settings.gradle", "include 'other'\n")
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0700))
	require.NoError(t, os.Symlink(filepath.Join(outside, "build.gradle"), filepath.Join(root, "app", "build.gradle")))
	_, err := Projects(root, ".")
	require.ErrorContains(t, err, "leaves the repository through a symlink", "a build file that links outside the checkout")
	linked := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(outside, "settings.gradle"), filepath.Join(linked, "settings.gradle")))
	_, err = Projects(linked, ".")
	require.ErrorContains(t, err, "leaves the repository through a symlink", "a settings file that links outside the checkout")
}

func TestProjectsRejectsDelegatedSettings(t *testing.T) {
	for settings, reason := range map[string]string{
		"apply from: \"$projectsFile\"\n":                               "apply a script ddtest cannot locate",
		"apply(from = uri(\"https://example.com/projects.gradle\"))\n":  "apply a script ddtest cannot locate",
		"apply from: 'gradle/missing.gradle'\n":                         "apply gradle/missing.gradle",
		"include 'app'\nproject(':app').buildFileName = 'app.gradle'\n": "rename project build files",
	} {
		root := t.TempDir()
		writeFile(t, root, "settings.gradle", settings)
		_, err := Projects(root, ".")
		require.ErrorContains(t, err, reason, settings)
	}
}

func TestDefaultTasksIncludeTheRootBuild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "include 'app'\n")
	writeFile(t, root, "app/build.gradle", "")
	require.False(t, DefaultTasks(root, "app"))
	writeFile(t, root, "build.gradle", "subprojects { defaultTasks 'test' }\n")
	require.True(t, DefaultTasks(root, "app"), "the root build configures the project's defaults")
	writeFile(t, root, "build.gradle", "// defaultTasks 'test'\n")
	require.False(t, DefaultTasks(root, "app"), "a comment declares nothing")
	writeFile(t, root, "app/build.gradle", "defaultTasks 'check'\n")
	require.True(t, DefaultTasks(root, "app"))
}

func TestProjectsFollowsAppliedSettingsScripts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "settings.gradle.kts", "apply(from = \"$rootDir/gradle/develocity.gradle\")\napply(from = \"gradle/projects.gradle.kts\")\ninclude(\"core\")\n")
	writeFile(t, root, "gradle/develocity.gradle", "develocity { buildScan { publishing.onlyIf { false } } }\n")
	writeFile(t, root, "gradle/projects.gradle.kts", "include(\"app\", \"lib\")\napply(from = \"gradle/more.gradle\")\n")
	writeFile(t, root, "gradle/more.gradle", "include 'tools'\n")
	projects, err := Projects(root, ".")
	require.NoError(t, err, "a script that includes nothing, and scripts that include projects, are read")
	var paths []string
	for _, project := range projects {
		paths = append(paths, project.Path)
	}
	require.ElementsMatch(t, []string{":", ":core", ":app", ":lib", ":tools"}, paths)
	writeFile(t, root, "gradle/more.gradle", "file('m').eachDir { include it.name }\n")
	_, err = Projects(root, ".")
	require.Error(t, err, "an applied script's computed includes are unreadable too")
}

func TestProjectsReadTestFrameworkSelections(t *testing.T) {
	for build, frameworks := range map[string][]string{
		sshjBuild: {"junit5"},
		"testing { suites { test { useJUnit('4.13.2') } } }":                                         {"junit4"},
		"testing { suites { val it by registering(JvmTestSuite::class) { useSpock() } } }":           {"spock"},
		"tasks.test { useTestNG() }\ndependencies { testImplementation 'org.testng:testng:7.10.2' }": {"testng"},
		"tasks.test { useJUnitPlatform() }":                                                          nil,
	} {
		root := t.TempDir()
		writeFile(t, root, "build.gradle", build)
		projects, err := Projects(root, ".")
		require.NoError(t, err)
		require.Equal(t, frameworks, Frameworks(projects), build)
	}
}
