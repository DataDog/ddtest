// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Runs Maven and downloads dd-java-agent and Maven Central dependencies; no
// real Datadog credentials are used.
// DDTEST_RUN_JAVA_INTEGRATION_TEST=1 go test ./internal/testdrive -run TestJavaValidationIntegration -v
func TestJavaValidationIntegration(t *testing.T) {
	if os.Getenv("DDTEST_RUN_JAVA_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_JAVA_INTEGRATION_TEST=1 with a JDK and mvn on PATH")
	}
	tracer := cmp.Or(os.Getenv("DDTEST_JAVA_TRACER_VERSION"), "1.67.1")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")

	type javaReport struct {
		Success       bool
		Preflight     struct{ Runtime string }
		Compatibility struct{ Status string }
		Features      []struct{ Name, Status string }
		Runs          []struct {
			Name       string
			ExitCode   int                          `json:"exit_code"`
			TestCounts struct{ Passed, Failed int } `json:"test_counts"`
		}
	}
	read := func(t *testing.T, root string) javaReport {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
		require.NoError(t, err)
		var report javaReport
		require.NoError(t, json.Unmarshal(data, &report), string(data))
		return report
	}
	project := func(t *testing.T, framework string, files map[string]string) string {
		t.Helper()
		root := t.TempDir()
		retainIntegrationReports(t, root, framework)
		integrationCommand(t, ctx, root, nil, "git", "init", "-q")
		integrationFile(t, root, ".gitignore", "target/\n.testoptimization/\n")
		for name, contents := range files {
			integrationFile(t, root, name, contents)
		}
		return root
	}
	pom := func(artifact, dependency string) string {
		return `<project><modelVersion>4.0.0</modelVersion><groupId>example</groupId><artifactId>` + artifact + `</artifactId><version>1.0</version>
  <properties><maven.compiler.release>17</maven.compiler.release><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding></properties>
  <dependencies>` + dependency + `</dependencies>
  <build><plugins><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>3.5.3</version></plugin></plugins></build>
</project>`
	}
	const jupiter = `<dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId><version>5.12.2</version><scope>test</scope></dependency>`
	// Validation requires per-test coverage of repository application classes.
	const calc = "package example;\npublic class Calc { public static int add(int a, int b) { return a + b; } }\n"
	const junit4 = `<dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.13.2</version><scope>test</scope></dependency>`
	testdrive := func(root string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, binary, append([]string{"testdrive", "--tracer-version", tracer, "--yes"}, args...)...)
		command.Dir = root
		output, err := command.CombinedOutput()
		return string(output), err
	}

	for _, tc := range []struct{ framework, dependency, source string }{
		{"junit5", jupiter, `package example;
public class ExampleTest {
    @org.junit.jupiter.api.Test void passes() { org.junit.jupiter.api.Assertions.assertEquals(4, Calc.add(2, 2)); }
    @org.junit.jupiter.api.Test void existingFailure() { org.junit.jupiter.api.Assertions.assertEquals(5, Calc.add(2, 2), "customer expected failure"); }
    @org.junit.jupiter.api.Test @org.junit.jupiter.api.Disabled("fixture") void ignored() {}
    @org.junit.jupiter.params.ParameterizedTest @org.junit.jupiter.params.provider.ValueSource(ints = {1, 2}) void positive(int value) { org.junit.jupiter.api.Assertions.assertTrue(value > 0); }
}
`},
		{"junit4", junit4, `package example;
public class ExampleTest {
    @org.junit.Test public void passes() { org.junit.Assert.assertEquals(4, Calc.add(2, 2)); }
    @org.junit.Test public void existingFailure() { org.junit.Assert.assertEquals("customer expected failure", 5, Calc.add(2, 2)); }
    @org.junit.Ignore("fixture") @org.junit.Test public void ignored() {}
}
`},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			root := project(t, tc.framework, map[string]string{"pom.xml": pom("fixture", tc.dependency), "src/main/java/example/Calc.java": calc, "src/test/java/example/ExampleTest.java": tc.source})
			output, err := testdrive(root, "--framework", tc.framework)
			require.NoError(t, err, output)
			require.Contains(t, output, "Local validation: PASSED")
			report := read(t, root)
			require.True(t, report.Success)
			require.NotEmpty(t, report.Preflight.Runtime)
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature.Name)
			}
			for _, run := range report.Runs[:2] {
				require.Equal(t, 1, run.TestCounts.Failed, "matching existing failures are acceptable")
				require.NotZero(t, run.ExitCode)
			}
			require.NoDirExists(t, filepath.Join(root, "target"), "Maven runs only in disposable copies")
			probes, err := filepath.Glob(filepath.Join(root, "src/test/java/ddtest"))
			require.NoError(t, err)
			require.Empty(t, probes)

			// Onboarding starts from the CI workflow. With the runner's JDK and
			// an exact agent pin, --all validates the CI step's test lifecycle
			// (install becomes verify) and certifies that CI row.
			pin := tracer
			if pin == "latest" {
				pin = "1.67.1"
			}
			workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-java@v4\n        with: {distribution: temurin, java-version: '" + localJava(t) + "'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '" + pin + "'}\n      - run: mvn -B clean install\n"
			integrationFile(t, root, ".github/workflows/test.yml", workflow)
			integrationCommand(t, ctx, root, nil, "git", "add", "-A")
			onboard := integrationCommand(t, ctx, root, nil, binary, "onboard", "--instructions", "--framework", tc.framework)
			require.Contains(t, onboard, "[.] mvn -B clean verify", "the CI step is validated through verify")
			require.Contains(t, onboard, "languages: java")
			all := exec.CommandContext(ctx, binary, "testdrive", "--framework", tc.framework, "--all", "--yes")
			all.Dir = root
			data, err := all.CombinedOutput()
			require.NoError(t, err, string(data))
			require.Contains(t, string(data), "Validation verdict: PASSED")
			require.Contains(t, string(data), "dd-java-agent "+pin+" is validated on this host's Java")
			check := exec.CommandContext(ctx, binary, "testdrive", "--framework", tc.framework, "--all", "--check-only", "--yes")
			check.Dir = root
			data, err = check.CombinedOutput()
			require.Error(t, err, string(data))
			require.Contains(t, string(data), "configuration-only checks validate no JDK")
			require.NotContains(t, string(data), "Running baseline")
		})
	}

	t.Run("no application coverage", func(t *testing.T) {
		root := project(t, "coverage", map[string]string{"pom.xml": pom("fixture", jupiter), "src/test/java/example/ExampleTest.java": "package example;\npublic class ExampleTest { @org.junit.jupiter.api.Test void passes() { org.junit.jupiter.api.Assertions.assertEquals(4, 2 + 2); } }\n"})
		output, err := testdrive(root, "--framework", "junit5")
		require.Error(t, err, output)
		require.Contains(t, output, "Java application coverage was not observed", "tests that run no application class cannot validate coverage")
		require.Equal(t, "compatible", read(t, root).Compatibility.Status)
	})

	t.Run("instrumentation-only failure", func(t *testing.T) {
		root := project(t, "regression", map[string]string{"pom.xml": pom("fixture", jupiter), "src/test/java/example/ExampleTest.java": `package example;
public class ExampleTest {
    @org.junit.jupiter.api.Test void passesWithoutTheAgent() {
        org.junit.jupiter.api.Assertions.assertNotEquals("true", System.getenv("DD_CIVISIBILITY_ENABLED"));
    }
}
`})
		output, err := testdrive(root, "--framework", "junit5")
		require.Error(t, err, output)
		require.Contains(t, output, "suspected regression")
		require.False(t, read(t, root).Success)
	})

	t.Run("incomplete reactor", func(t *testing.T) {
		root := project(t, "reactor", map[string]string{
			"pom.xml":      `<project><modelVersion>4.0.0</modelVersion><groupId>example</groupId><artifactId>parent</artifactId><version>1.0</version><packaging>pom</packaging><modules><module>core</module><module>broken</module></modules></project>`,
			"core/pom.xml": pom("core", jupiter),
			"core/src/test/java/example/CoreTest.java": "package example;\npublic class CoreTest { @org.junit.jupiter.api.Test void passes() {} }\n",
			"broken/pom.xml": pom("broken", jupiter),
			"broken/src/main/java/example/Broken.java": "package example;\npublic class Broken { int value = \"not a number\"; }\n",
		})
		output, err := testdrive(root, "--framework", "junit5")
		require.Error(t, err, output)
		report := read(t, root)
		require.Equal(t, "inconclusive", report.Compatibility.Status, output)
		require.Len(t, report.Runs, 2, "probes would rebuild the same failing reactor")
	})

	t.Run("publishing goal", func(t *testing.T) {
		root := project(t, "publishing", map[string]string{"pom.xml": pom("fixture", jupiter), "src/test/java/example/ExampleTest.java": "package example;\npublic class ExampleTest { @org.junit.jupiter.api.Test void passes() {} }\n"})
		output, err := testdrive(root, "--framework", "junit5", "--command", "mvn test deploy:deploy@publish")
		require.Error(t, err, output)
		require.Contains(t, output, "never installs or publishes artifacts")
		require.NotContains(t, output, "Running baseline")
		require.True(t, strings.Contains(output, "Validation verdict: INCOMPLETE"), output)
	})

	t.Run("agent workflow", func(t *testing.T) {
		root := project(t, "agent", map[string]string{"pom.xml": pom("fixture", jupiter), "src/main/java/example/Calc.java": calc,
			"src/test/java/example/ExampleTest.java": "package example;\npublic class ExampleTest { @org.junit.jupiter.api.Test void adds() { org.junit.jupiter.api.Assertions.assertEquals(4, Calc.add(2, 2)); } }\n"})
		pin := tracer
		if pin == "latest" {
			pin = "1.67.1"
		}
		// Another JDK major, which only an accepted substitute validates locally.
		other := "21"
		if localJava(t) == "21" {
			other = "17"
		}
		workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        java: ['" + localJava(t) + "', '" + other + "', '26-ea']\n    steps:\n      - uses: actions/setup-java@v4\n        with: {distribution: temurin, java-version: '${{ matrix.java }}'}\nACTION      - run: mvn -B clean install\n"
		integrationFile(t, root, ".github/workflows/test.yml", strings.Replace(workflow, "ACTION", "", 1))
		type agentResult struct {
			Status        string
			Code          string
			RequiredInput []string `json:"required_input"`
			ResumeCommand []string `json:"resume_command"`
			NextCommand   []string `json:"next_command"`
			FinalResponse string   `json:"final_response"`
		}
		run := func(command []string) agentResult {
			t.Helper()
			process := exec.CommandContext(ctx, command[0], command[1:]...)
			process.Dir = root
			data, _ := process.Output()
			var result agentResult
			require.NoError(t, json.Unmarshal(data, &result), string(data))
			return result
		}
		start := run([]string{binary, "onboard", "--agent", "--format", "json"})
		require.Equal(t, "ci_setup_required", start.Code, "the agent applies the CI setup first")
		require.NotEmpty(t, start.ResumeCommand)

		// The agent's edit, as the instructions prescribe.
		action := "      - uses: datadog/test-visibility-github-action@v3\n        if: ${{ !contains(matrix.java, '-ea') }}\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '" + pin + "'}\n"
		integrationFile(t, root, ".github/workflows/test.yml", strings.Replace(workflow, "ACTION", action, 1))
		decision := run(start.ResumeCommand)
		require.Equal(t, "substitute_runtime_unaccepted", decision.Code, "%+v", decision)
		require.Contains(t, decision.RequiredInput[0], "set JAVA_HOME to an existing Java "+other+" JDK")
		require.Contains(t, decision.ResumeCommand, "--accept-substitute-runtime", "the user's acceptance resumes validation")
		validation := run(decision.ResumeCommand)
		require.Equal(t, "complete_locally", validation.Status, "%+v", validation)
		report := run(validation.NextCommand)
		require.Equal(t, "complete_locally", report.Status, "%+v", report)
		require.Contains(t, report.FinalResponse, "Onboarding validation: COMPLETE LOCALLY")
		require.Contains(t, report.FinalResponse, "1 excluded from instrumentation", "the early-access JDK keeps its uninstrumented tests")
		require.Contains(t, report.FinalResponse, "runs on Java "+other+" (accepted)", "the accepted substitute is a caveat")
		require.NoDirExists(t, filepath.Join(root, "target"))
	})
}

// localJava is the major version of the JDK Maven runs on, from its release file.
func localJava(t *testing.T) string {
	t.Helper()
	home := os.Getenv("JAVA_HOME")
	if home == "" {
		path, err := exec.LookPath("java")
		require.NoError(t, err)
		path, err = filepath.EvalSymlinks(path)
		require.NoError(t, err)
		home = filepath.Dir(filepath.Dir(path))
	}
	data, err := os.ReadFile(filepath.Join(home, "release"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(data), "\n") {
		if version, ok := strings.CutPrefix(line, "JAVA_VERSION="); ok {
			major, rest, _ := strings.Cut(strings.Trim(version, `"`), ".")
			if major == "1" {
				major, _, _ = strings.Cut(rest, ".")
			}
			return major
		}
	}
	t.Fatal("JAVA_VERSION missing from the JDK release file")
	return ""
}
