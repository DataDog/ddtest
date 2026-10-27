package maven

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectsAndFrameworks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><artifactId>parent</artifactId><modules><module>child</module></modules><dependencyManagement><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></dependencyManagement></project>`), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "child"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "child/pom.xml"), []byte(`<project><artifactId>child</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies><profiles><profile><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></profile></profiles></project>`), 0600))
	projects, err := Projects(root)
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Empty(t, projects[0].Frameworks())
	require.Equal(t, "child", projects[1].Directory)
	require.Equal(t, []string{"junit5", "testng"}, projects[1].Frameworks())
	for _, tc := range []struct{ group, artifact, name string }{
		{"junit", "junit", "junit4"}, {"org.junit.jupiter", "junit-jupiter-api", "junit5"}, {"org.testng", "testng", "testng"}, {"org.spockframework", "spock-core", "spock"}, {"io.cucumber", "cucumber-java", "cucumber-java"}, {"com.intuit.karate", "karate-junit5", "karate"}, {"org.scalatest", "scalatest_2.13", "scalatest"}, {"org.scalameta", "munit_2.13", "munit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, []string{tc.name}, (Project{Dependencies: []Dependency{{Group: tc.group, Artifact: tc.artifact}}}).Frameworks())
		})
	}
}

func TestProjectsRejectUnresolvedOrEscapingModule(t *testing.T) {
	for _, module := range []string{"../outside", "${module}"} {
		t.Run(module, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte("<project><modules><module>"+module+"</module></modules></project>"), 0600))
			_, err := Projects(root)
			require.Error(t, err)
		})
	}
}

func TestProjectsInheritOnlyMatchingLocalParents(t *testing.T) {
	for _, tc := range []struct {
		name, parent string
		inherits     bool
	}{
		{"default parent", `<parent><groupId>example</groupId><artifactId>parent</artifactId><version>1</version></parent>`, true},
		{"explicit parent", `<parent><groupId>example</groupId><artifactId>parent</artifactId><version>1</version><relativePath>../pom.xml</relativePath></parent>`, true},
		{"disabled local lookup", `<parent><groupId>example</groupId><artifactId>parent</artifactId><version>1</version><relativePath/></parent>`, false},
		{"different coordinates", `<parent><groupId>other</groupId><artifactId>parent</artifactId><version>1</version></parent>`, false},
		{"aggregator only", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "child"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><groupId>example</groupId><artifactId>parent</artifactId><version>1</version><modules><module>child</module></modules><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></project>`), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "child/pom.xml"), []byte(`<project>`+tc.parent+`<artifactId>child</artifactId></project>`), 0600))
			projects, err := Projects(root)
			require.NoError(t, err)
			if tc.inherits {
				require.Equal(t, []string{"testng"}, projects[1].Frameworks())
			} else {
				require.Empty(t, projects[1].Frameworks())
			}
		})
	}
}

func TestProjectsRejectCyclicParents(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "child"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><groupId>example</groupId><artifactId>root</artifactId><version>1</version><parent><groupId>example</groupId><artifactId>child</artifactId><version>1</version><relativePath>child/pom.xml</relativePath></parent><modules><module>child</module></modules></project>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "child/pom.xml"), []byte(`<project><artifactId>child</artifactId><parent><groupId>example</groupId><artifactId>root</artifactId><version>1</version></parent></project>`), 0600))
	_, err := Projects(root)
	require.ErrorContains(t, err, "cyclic")
}

func TestIsTestCommand(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		want    bool
	}{
		{"mvn", []string{"-B", "-Pci", "verify"}, true}, {"./mvnw", []string{"test", "-pl", "service"}, true}, {"mvn", []string{"-f", "test", "compile"}, false}, {"mvn", []string{"compile", "-Dmessage=test"}, false}, {"gradle", []string{"test"}, false}, {"mvn", []string{"clean", "package"}, true},
	} {
		require.Equal(t, tc.want, IsTestCommand(tc.command, tc.args), "%s %v", tc.command, tc.args)
	}
}

func TestValidationCannotPublishOrSelectOutsideCopy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "module"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "module/pom.xml"), []byte(`<project/>`), 0600))
	for _, args := range [][]string{
		{"deploy"}, {"test", "deploy"}, {"package", "deploy:deploy-file"},
		{"test", "org.apache.maven.plugins:maven-deploy-plugin:3.1.4:deploy-file"},
		{"test", "-f", "../pom.xml"}, {"verify", "--file=/outside/pom.xml"},
		{"test", "-f=/outside/pom.xml"}, {"test", "-f=../pom.xml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			require.Error(t, ValidateTestdriveCommand(root, "mvn", args))
		})
	}
	require.NoError(t, ValidateTestdriveCommand(root, "./mvnw", []string{"-f", "module/pom.xml", "-Pci", "verify"}))
	require.NoError(t, ValidateTestdriveCommand(root, "mvn", []string{"test", "-f=module/pom.xml"}))
}

func TestValidationAllowsPOMAliasesWithinTheRepository(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "module"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project/>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "module/pom.xml"), []byte(`<project/>`), 0600))
	require.NoError(t, os.Symlink("module", filepath.Join(root, "alias")))
	require.NoError(t, os.Symlink(filepath.Join(root, "module/pom.xml"), filepath.Join(root, "alias.xml")))
	for _, args := range [][]string{{"test"}, {"test", "-f", "."}, {"test", "--file", "alias"}, {"test", "-f=alias.xml"}, {"test", "--file=module/pom.xml"}, {"-ff", "test"}, {"-fae", "test"}, {"-fn", "test"}} {
		require.NoError(t, ValidateTestdriveCommand(root, "mvn", args), "%v", args)
	}
}

func TestProjectsRejectSymlinkedRootAndModulePOMs(t *testing.T) {
	for _, path := range []string{"pom.xml", "child/pom.xml"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(t.TempDir(), "pom.xml")
			require.NoError(t, os.WriteFile(outside, []byte(`<project/>`), 0600))
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700))
			if path != "pom.xml" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><modules><module>child</module></modules></project>`), 0600))
			}
			require.NoError(t, os.Symlink(outside, filepath.Join(root, path)))
			_, err := Projects(root)
			require.ErrorContains(t, err, "POM must stay inside")
		})
	}
}

func TestProjectsAllowUnavailableProfileCandidates(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><profiles><profile><id>optional</id><modules><module>missing</module><module>empty</module></modules></profile></profiles></project>`), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "empty"), 0700))
	projects, err := Projects(root)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><modules><module>missing</module></modules></project>`), 0600))
	_, err = Projects(root)
	require.Error(t, err, "unconditional modules remain required")
}

func TestValidationRejectsImplicitMavenArguments(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project/>`), 0600))
	for _, args := range []string{"deploy", "-f /outside/pom.xml", "-B -Pci"} {
		t.Run(args, func(t *testing.T) {
			t.Setenv("MAVEN_ARGS", args)
			require.ErrorContains(t, ValidateTestdriveCommand(root, "mvn", []string{"test"}), "MAVEN_ARGS")
		})
	}
	t.Setenv("MAVEN_ARGS", "")
	require.NoError(t, os.Mkdir(filepath.Join(root, ".mvn"), 0700))
	config := filepath.Join(root, ".mvn/maven.config")
	for _, args := range []string{"-f\n/outside/pom.xml", "-f../pom.xml", "-B\n-Pci"} {
		require.NoError(t, os.WriteFile(config, []byte(args), 0600))
		require.ErrorContains(t, ValidateTestdriveCommand(root, "mvn", []string{"test"}), "maven.config")
	}
	require.NoError(t, os.WriteFile(config, []byte("# comment only\n\n"), 0600))
	require.NoError(t, ValidateTestdriveCommand(root, "mvn", []string{"test"}))
	// A selected subproject can inherit configuration from its own .mvn directory.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "child/.mvn"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "child/pom.xml"), []byte(`<project/>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "child/.mvn/maven.config"), []byte("-f\n/outside/pom.xml"), 0600))
	require.ErrorContains(t, ValidateTestdriveCommand(root, "mvn", []string{"-f", "child", "test"}), "maven.config")
}

func TestValidationChecksPOMAliasAndInheritedConfiguration(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"real", "aliases/.mvn"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "real/pom.xml"), []byte(`<project/>`), 0600))
	require.NoError(t, os.Symlink("../real/pom.xml", filepath.Join(root, "aliases/pom.xml")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "aliases/.mvn/maven.config"), []byte("--file=/outside/pom.xml"), 0600))
	require.ErrorContains(t, ValidateTestdriveCommand(root, "mvn", []string{"-f", "aliases/pom.xml", "test"}), "maven.config")
	require.NoError(t, os.Remove(filepath.Join(root, "aliases/.mvn/maven.config")))
	for _, key := range []string{"MAVEN_BASEDIR", "MAVEN_PROJECTBASEDIR"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "/outside")
			require.ErrorContains(t, ValidateTestdriveCommand(root, "mvn", []string{"-f", "real", "test"}), key)
		})
	}
	parent := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(parent, ".mvn"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(parent, ".mvn/maven.config"), []byte("--file=/outside/pom.xml"), 0600))
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(child, "pom.xml"), []byte(`<project/>`), 0600))
	require.ErrorContains(t, ValidateTestdriveCommand(child, "mvn", []string{"test"}), "maven.config")
}
