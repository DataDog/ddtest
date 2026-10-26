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
