// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package maven

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writePOM(t *testing.T, root, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(contents), 0600))
}

func TestProjectsReadEveryReactorModule(t *testing.T) {
	root := t.TempDir()
	writePOM(t, root, "pom.xml", `<project><artifactId>parent</artifactId><modules><module>child</module></modules>
		<dependencyManagement><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></dependencyManagement></project>`)
	writePOM(t, root, "child/pom.xml", `<project><artifactId>child</artifactId>
		<dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies>
		<profiles><profile><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></profile></profiles></project>`)

	projects, err := Projects(root)
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Equal(t, ".", projects[0].Directory)
	require.Empty(t, projects[0].Frameworks(), "dependencyManagement only pins versions")
	require.Equal(t, "child", projects[1].Directory)
	require.Equal(t, "child/pom.xml", projects[1].POM)
	require.Equal(t, []string{"junit5", "testng"}, projects[1].Frameworks())
	require.Equal(t, []string{"junit5", "testng"}, Frameworks(projects))
}

func TestFrameworksRecognizeEverySupportedDependency(t *testing.T) {
	for _, tc := range []struct{ group, artifact, name string }{
		{"junit", "junit", "junit4"},
		{"org.junit.jupiter", "junit-jupiter-api", "junit5"},
		{"org.testng", "testng", "testng"},
		{"org.spockframework", "spock-core", "spock"},
		{"io.cucumber", "cucumber-java", "cucumber-java"},
		{"io.cucumber", "cucumber-java8", "cucumber-java"},
		{"com.intuit.karate", "karate-junit5", "karate"},
		{"io.karatelabs", "karate-junit5", "karate"},
		{"org.scalatest", "scalatest_2.13", "scalatest"},
		{"org.scalameta", "munit_2.13", "munit"},
	} {
		t.Run(tc.group+":"+tc.artifact, func(t *testing.T) {
			require.Equal(t, []string{tc.name}, Project{Dependencies: []Dependency{{Group: tc.group, Artifact: tc.artifact}}}.Frameworks())
		})
	}
	require.Empty(t, Project{Dependencies: []Dependency{{Group: "org.mockito", Artifact: "mockito-core"}}}.Frameworks())
}

func TestProjectsRejectUnresolvableOrEscapingModules(t *testing.T) {
	for _, module := range []string{"../outside", "${module}", "/absolute"} {
		t.Run(module, func(t *testing.T) {
			root := t.TempDir()
			writePOM(t, root, "pom.xml", "<project><modules><module>"+module+"</module></modules></project>")
			_, err := Projects(root)
			require.ErrorContains(t, err, "cannot statically resolve")
		})
	}
}

func TestProjectsSkipMissingProfileModulesOnly(t *testing.T) {
	root := t.TempDir()
	writePOM(t, root, "pom.xml", `<project><profiles><profile><modules><module>missing</module><module>present</module></modules></profile></profiles></project>`)
	writePOM(t, root, "present/pom.xml", `<project><artifactId>present</artifactId></project>`)
	projects, err := Projects(root)
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Equal(t, "present", projects[1].Directory)

	writePOM(t, root, "pom.xml", `<project><modules><module>missing</module></modules></project>`)
	_, err = Projects(root)
	require.ErrorIs(t, err, os.ErrNotExist, "an unconditional module must exist")
}

func TestProjectsRejectPOMsLinkedOutsideTheRepository(t *testing.T) {
	for _, path := range []string{"pom.xml", "child/pom.xml"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(t.TempDir(), "pom.xml")
			require.NoError(t, os.WriteFile(outside, []byte(`<project/>`), 0600))
			if path != "pom.xml" {
				writePOM(t, root, "pom.xml", `<project><modules><module>child</module></modules></project>`)
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700))
			require.NoError(t, os.Symlink(outside, filepath.Join(root, path)))
			_, err := Projects(root)
			require.ErrorContains(t, err, "must stay inside the repository")
		})
	}
}

func TestProjectsFollowModuleLinksInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	writePOM(t, root, "pom.xml", `<project><modules><module>alias</module><module>real</module></modules></project>`)
	writePOM(t, root, "real/pom.xml", `<project><artifactId>real</artifactId></project>`)
	require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")))
	t.Chdir(root)
	projects, err := Projects(".")
	require.NoError(t, err)
	require.Len(t, projects, 2, "an alias and its target are one module")
	require.Equal(t, "real", projects[1].Directory)
}

func TestProjectsBoundTheReactor(t *testing.T) {
	root := t.TempDir()
	var modules string
	for i := range maxModules {
		name := filepath.Join("modules", string(rune('a'+i%26))+string(rune('a'+i/26)))
		modules += "<module>" + name + "</module>"
		writePOM(t, root, filepath.Join(name, "pom.xml"), `<project/>`)
	}
	writePOM(t, root, "pom.xml", "<project><modules>"+modules+"</modules></project>")
	_, err := Projects(root)
	require.ErrorContains(t, err, "exceeds")
}

func TestIsTestCommand(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		want    bool
	}{
		{"mvn", []string{"-B", "-Pci", "verify"}, true},
		{"./mvnw", []string{"test", "-pl", "service"}, true},
		{"mvn", []string{"-B", "package", "--file", "pom.xml"}, true},
		{"mvn.cmd", []string{"surefire:test"}, true},
		{"mvn", []string{"-f", "test", "compile"}, false},
		{"mvn", []string{"-P", "verify", "compile"}, false},
		{"mvn", []string{"compile", "-Dmessage=test"}, false},
		{"mvn", []string{"dependency:go-offline"}, false},
		{"gradle", []string{"test"}, false},
	} {
		require.Equal(t, tc.want, IsTestCommand(tc.command, tc.args), "%s %v", tc.command, tc.args)
	}
}
