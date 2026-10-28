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
	for _, module := range []string{"../outside", "../../outside", "${module}", "/absolute"} {
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

func TestCommandPOM(t *testing.T) {
	for _, tc := range []struct {
		args []string
		pom  string
	}{
		{[]string{"test"}, "pom.xml"},
		{[]string{"-f", "service/pom.xml", "verify"}, "service/pom.xml"},
		{[]string{"--file", "service", "test"}, "service"},
		{[]string{"--file=service/pom.xml", "test"}, "service/pom.xml"},
		{[]string{"-fservice", "test"}, "service"},
		{[]string{"-fae", "-ff", "-fn", "test"}, "pom.xml"},
	} {
		pom, err := CommandPOM(tc.args)
		require.NoError(t, err, "%v", tc.args)
		require.Equal(t, tc.pom, pom, "%v", tc.args)
	}
	for _, args := range [][]string{{"-f", "../pom.xml", "test"}, {"--file=/outside/pom.xml"}, {"-f"}, {"-f", "a", "-f", "b"}} {
		_, err := CommandPOM(args)
		require.Error(t, err, "%v", args)
	}
}

func TestTestInvocation(t *testing.T) {
	for _, tc := range []struct {
		args, validated, omitted []string
	}{
		{[]string{"-B", "verify"}, []string{"-B", "verify"}, nil},
		{[]string{"-B", "clean", "install"}, []string{"-B", "clean", "verify"}, []string{"install"}},
		{[]string{"verify", "deploy"}, []string{"verify"}, []string{"deploy"}},
		{[]string{"--no-transfer-progress", "verify", "source:jar", "javadoc:jar"}, []string{"--no-transfer-progress", "verify"}, []string{"source:jar", "javadoc:jar"}},
		{[]string{"-pl", "service", "-am", "test", "-Dtest=Example*"}, []string{"-pl", "service", "-am", "test", "-Dtest=Example*"}, nil},
		{[]string{"-f", "install", "test"}, []string{"-f", "install", "test"}, nil},
		{[]string{"spotless:check"}, nil, []string{"spotless:check"}},
		// Option values are never goals, and never stand in for one.
		{[]string{"--color", "always", "test"}, []string{"--color", "always", "test"}, nil},
		{[]string{"-D", "skipTests=false", "verify"}, []string{"-D", "skipTests=false", "verify"}, nil},
		{[]string{"--define", "skipTests=false", "verify"}, []string{"--define", "skipTests=false", "verify"}, nil},
		{[]string{"-gt", "toolchains.xml", "-emp", "secret", "-ep", "secret", "test"}, []string{"-gt", "toolchains.xml", "-emp", "secret", "-ep", "secret", "test"}, nil},
		{[]string{"-P", "verify", "install"}, []string{"-P", "verify", "verify"}, []string{"install"}},
	} {
		validated, omitted := TestInvocation(tc.args)
		require.Equal(t, tc.validated, validated, "%v", tc.args)
		require.Equal(t, tc.omitted, omitted, "%v", tc.args)
	}
}

func TestSkipsTests(t *testing.T) {
	for _, args := range [][]string{{"-DskipTests", "package"}, {"-DskipTests=true"}, {"-Dmaven.test.skip=true", "verify"}, {"-Dmaven.test.skip"}, {"-D", "skipTests=true", "verify"}, {"--define", "maven.test.skip=true", "verify"}, {"--define=skipTests", "verify"}} {
		require.True(t, SkipsTests(args), "%v", args)
	}
	for _, args := range [][]string{{"verify"}, {"-DskipTests=false", "test"}, {"-DskipITs", "verify"}, {"-D", "skipTests=false", "verify"}, {"-P", "skipTests", "verify"}} {
		require.False(t, SkipsTests(args), "%v", args)
	}
}

func TestGoals(t *testing.T) {
	require.Equal(t, []string{"clean", "verify", "javadoc:jar"}, Goals([]string{"-B", "clean", "-pl", "service", "verify", "-Dtest=Example", "javadoc:jar"}))
	require.Empty(t, Goals([]string{"-v"}))
	require.Empty(t, Goals([]string{"-f", "pom.xml", "--version"}))
}

func TestMetadata(t *testing.T) {
	for _, args := range [][]string{{"-v"}, {"--version"}, {"-B", "--help"}} {
		require.True(t, Metadata(args), "%v", args)
	}
	for _, args := range [][]string{{"-B"}, {"-v", "verify"}, {"-V"}} {
		require.False(t, Metadata(args), "%v", args)
	}
}

func TestProjectsReadTheDefaultGoal(t *testing.T) {
	root := t.TempDir()
	writePOM(t, root, "pom.xml", `<project><build><defaultGoal>clean verify</defaultGoal></build></project>`)
	projects, err := Projects(root)
	require.NoError(t, err)
	require.Equal(t, "clean verify", projects[0].DefaultGoal)
}

func TestProjectsAcceptSiblingModulesInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	writePOM(t, root, "build/pom.xml", `<project><modules><module>../core</module></modules></project>`)
	writePOM(t, root, "core/pom.xml", `<project><artifactId>core</artifactId></project>`)
	projects, err := ProjectsFrom(root, "build/pom.xml")
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Equal(t, "core", projects[1].Directory)
}
