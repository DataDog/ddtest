package testdrive

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

// Opt-in: uses an installed JDK/Maven and public Maven dependencies. Run with
// DDTEST_MAVEN_FRAMEWORKS=junit4,junit5,... and DD_TRACER_FOLDER pointing to a
// downloaded dd-java-agent.jar. Default make test remains offline.
func TestMavenIntegration(t *testing.T) {
	selection := os.Getenv("DDTEST_MAVEN_FRAMEWORKS")
	if selection == "" {
		t.Skip("set DDTEST_MAVEN_FRAMEWORKS and DD_TRACER_FOLDER for live Maven validation")
	}
	agent := filepath.Join(os.Getenv("DD_TRACER_FOLDER"), "dd-java-agent.jar")
	_, err := platform.JavaAgentVersion(agent)
	require.NoError(t, err)
	for _, name := range strings.Split(selection, ",") {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			fixture, err := filepath.Abs(filepath.Join("testdata/maven", name))
			require.NoError(t, err)
			root := filepath.Join(t.TempDir(), "repository")
			require.NoError(t, copyConfiguration(ctx, fixture, root, true))
			init := exec.CommandContext(ctx, "git", "init", "-q", "-b", "juan-fernandez/maven-fixture")
			init.Dir = root
			require.NoError(t, init.Run())
			session, err := NewSession()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			p := platform.NewJava()
			drive := &Testdrive{repositoryRoot: root, platform: p, framework: framework.NewMaven(name, p), language: "java", command: "mvn", args: []string{"-q", "test"}, session: session,
				startIntake: func(directory string, scenario intake.Scenario) (localIntake, error) {
					return intake.StartScenario(directory, scenario)
				}}
			baseline, err := drive.mavenRun(ctx, io.Discard, "baseline", "", intake.Scenario{}, "", nil)
			require.NoError(t, err)
			reporting, err := drive.mavenRun(ctx, io.Discard, "reporting-only", agent, intake.Scenario{}, "", nil)
			require.NoError(t, err)
			t.Logf("baseline: %+v; error: %s; diagnostic: %s", baseline.Tests, baseline.ResultError, baseline.Diagnostic)
			t.Logf("reporting: %+v; error: %s; diagnostic: %s", reporting.Tests, reporting.ResultError, reporting.Diagnostic)
			t.Logf("events: %+v", reporting.Facts.Tests)
			if reporting.ExitCode != baseline.ExitCode {
				t.Log(reporting.commandOutput)
			}
			require.Equal(t, "compatible", compareMaven(baseline, reporting).Status)
			projects, err := maven.Projects(root)
			require.NoError(t, err)
			var result validationResult
			require.NoError(t, drive.mavenFeatures(ctx, io.Discard, agent, projects, reporting, &result))
			for _, feature := range result.Features {
				t.Logf("%s: %s - %s", feature.Name, feature.Status, feature.Reason)
				if feature.Status != "passed" && feature.Status != "not supported" {
					t.Errorf("feature %s: %s", feature.Name, feature.Status)
				}
			}
			require.Len(t, result.Features, 6)
			require.NoDirExists(t, filepath.Join(root, "target"))
			require.NoDirExists(t, filepath.Join(root, "src/test/java/ddtest"))
		})
	}
}

func TestMavenVerifyWithSurefireAndFailsafe(t *testing.T) {
	if !strings.Contains(","+os.Getenv("DDTEST_MAVEN_FRAMEWORKS")+",", ",junit5,") {
		t.Skip("set DDTEST_MAVEN_FRAMEWORKS=junit5 and DD_TRACER_FOLDER for live Maven validation")
	}
	agent := filepath.Join(os.Getenv("DD_TRACER_FOLDER"), "dd-java-agent.jar")
	_, err := platform.JavaAgentVersion(agent)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	fixture, err := filepath.Abs("testdata/maven/junit5")
	require.NoError(t, err)
	root := filepath.Join(t.TempDir(), "repository")
	require.NoError(t, copyConfiguration(ctx, fixture, root, true))
	pom := filepath.Join(root, "pom.xml")
	data, err := os.ReadFile(pom)
	require.NoError(t, err)
	plugin := `<plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-failsafe-plugin</artifactId><version>3.5.3</version><executions><execution><goals><goal>integration-test</goal><goal>verify</goal></goals></execution></executions></plugin>`
	require.NoError(t, os.WriteFile(pom, []byte(strings.Replace(string(data), "</plugins>", plugin+"</plugins>", 1)), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/test/java/example/ExampleIT.java"), []byte(`package example;
public class ExampleIT {
  @org.junit.jupiter.api.Test void integration() { org.junit.jupiter.api.Assertions.assertTrue(true); }
}
`), 0600))
	init := exec.CommandContext(ctx, "git", "init", "-q", "-b", "juan-fernandez/maven-fixture")
	init.Dir = root
	require.NoError(t, init.Run())
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	p := platform.NewJava()
	drive := &Testdrive{repositoryRoot: root, platform: p, framework: framework.NewMaven("junit5", p), language: "java", command: "mvn", args: []string{"-q", "verify"}, session: session,
		startIntake: func(directory string, scenario intake.Scenario) (localIntake, error) {
			return intake.StartScenario(directory, scenario)
		},
	}
	baseline, err := drive.mavenRun(ctx, io.Discard, "baseline", "", intake.Scenario{}, "", nil)
	require.NoError(t, err)
	reporting, err := drive.mavenRun(ctx, io.Discard, "reporting-only", agent, intake.Scenario{}, "", nil)
	require.NoError(t, err)
	require.Len(t, baseline.Tests, 4, baseline.commandOutput)
	require.Equal(t, 3, executedTests(baseline), "both Surefire and Failsafe must execute their test")
	require.Equal(t, "compatible", compareMaven(baseline, reporting).Status, reporting.commandOutput)
	projects, err := maven.Projects(root)
	require.NoError(t, err)
	probe := newMavenProbe(projects[0], "junit5")
	control, err := drive.mavenRun(ctx, io.Discard, "probe-control", agent, intake.Scenario{}, "pass", &probe)
	require.NoError(t, err)
	require.Len(t, control.Tests, 1, control.commandOutput)
	require.Len(t, control.Facts.Tests, 1)
	require.NotNil(t, control.ProbeExecutions)
	require.Equal(t, 1, *control.ProbeExecutions)
	var result validationResult
	require.NoError(t, drive.mavenFeatures(ctx, io.Discard, agent, projects, reporting, &result))
	require.Len(t, result.Features, 6)
	require.True(t, featureChecksPassed("junit5", result.Features), "%+v", result.Features)
}
