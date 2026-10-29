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
		`project(':a').projectDir = new File(settingsDir, 'a')` + "\ninclude ':a'\n",
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
