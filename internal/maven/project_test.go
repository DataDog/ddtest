package maven

import (
	"os"
	"path/filepath"
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
	for _, args := range [][]string{{"deploy"}, {"test", "deploy"}, {"test", "-f", "../pom.xml"}, {"verify", "--file=/outside/pom.xml"}} {
		require.Error(t, ValidateTestdriveCommand("mvn", args))
	}
	require.NoError(t, ValidateTestdriveCommand("./mvnw", []string{"-f", "module/pom.xml", "-Pci", "verify"}))
}
