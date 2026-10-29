// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
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
	pomWith := func(artifact, dependency, surefire, plugins string) string {
		return `<project><modelVersion>4.0.0</modelVersion><groupId>example</groupId><artifactId>` + artifact + `</artifactId><version>1.0</version>
  <properties><maven.compiler.release>17</maven.compiler.release><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding></properties>
  <dependencies>` + dependency + `</dependencies>
  <build><plugins><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-surefire-plugin</artifactId><version>` + surefire + `</version></plugin>` + plugins + `</plugins></build>
</project>`
	}
	pom := func(artifact, dependency string) string { return pomWith(artifact, dependency, "3.5.3", "") }
	const jupiter = `<dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId><version>5.12.2</version><scope>test</scope></dependency>`
	// Validation requires per-test coverage of repository application classes.
	const calc = "package example;\npublic class Calc { public static int add(int a, int b) { return a + b; } }\n"
	const junit4 = `<dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>4.13.2</version><scope>test</scope></dependency>`
	const testng = `<dependency><groupId>org.testng</groupId><artifactId>testng</artifactId><version>7.10.2</version><scope>test</scope></dependency>`
	const spock = `<dependency><groupId>org.spockframework</groupId><artifactId>spock-core</artifactId><version>2.3-groovy-4.0</version><scope>test</scope></dependency><dependency><groupId>org.apache.groovy</groupId><artifactId>groovy</artifactId><version>4.0.24</version><scope>test</scope></dependency>`
	const cucumber = `<dependency><groupId>io.cucumber</groupId><artifactId>cucumber-java</artifactId><version>7.20.1</version><scope>test</scope></dependency>`
	const cucumberJUnit4 = cucumber + `<dependency><groupId>io.cucumber</groupId><artifactId>cucumber-junit</artifactId><version>7.20.1</version><scope>test</scope></dependency>` + junit4
	const cucumberPlatform = cucumber + `<dependency><groupId>io.cucumber</groupId><artifactId>cucumber-junit-platform-engine</artifactId><version>7.20.1</version><scope>test</scope></dependency><dependency><groupId>org.junit.platform</groupId><artifactId>junit-platform-suite</artifactId><version>1.11.4</version><scope>test</scope></dependency>`
	const cucumberFeature = "Feature: Calculator\n  Scenario: adds\n    When I add 2 and 2\n    Then the result is 4\n\n  Scenario: existing failure\n    When I add 2 and 2\n    Then the result is 5\n"
	const cucumberSteps = `package example;
public class StepDefinitions {
    private int result;
    @io.cucumber.java.en.When("I add {int} and {int}") public void add(int a, int b) { result = Calc.add(a, b); }
    @io.cucumber.java.en.Then("the result is {int}") public void check(int expected) { if (result != expected) throw new AssertionError("customer expected failure: " + result); }
}
`
	// Spock specifications compile with GMavenPlus from src/test/groovy.
	const gmavenplus = `<plugin><groupId>org.codehaus.gmavenplus</groupId><artifactId>gmavenplus-plugin</artifactId><version>4.1.1</version><executions><execution><goals><goal>addTestSources</goal><goal>compileTests</goal></goals></execution></executions></plugin>`
	testdrive := func(root string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, binary, append([]string{"testdrive", "--tracer-version", tracer, "--yes"}, args...)...)
		command.Dir = root
		output, err := command.CombinedOutput()
		return string(output), err
	}

	for _, tc := range []struct {
		name, framework, dependency, surefire, plugins string
		files                                          map[string]string
	}{
		{"junit5", "junit5", jupiter, "3.5.3", "", map[string]string{"src/test/java/example/ExampleTest.java": `package example;
public class ExampleTest {
    @org.junit.jupiter.api.Test void passes() { org.junit.jupiter.api.Assertions.assertEquals(4, Calc.add(2, 2)); }
    @org.junit.jupiter.api.Test void existingFailure() { org.junit.jupiter.api.Assertions.assertEquals(5, Calc.add(2, 2), "customer expected failure"); }
    @org.junit.jupiter.api.Test @org.junit.jupiter.api.Disabled("fixture") void ignored() {}
    @org.junit.jupiter.params.ParameterizedTest @org.junit.jupiter.params.provider.ValueSource(ints = {1, 2}) void positive(int value) { org.junit.jupiter.api.Assertions.assertTrue(value > 0); }
}
`}},
		{"junit4", "junit4", junit4, "3.5.3", "", map[string]string{"src/test/java/example/ExampleTest.java": `package example;
public class ExampleTest {
    @org.junit.Test public void passes() { org.junit.Assert.assertEquals(4, Calc.add(2, 2)); }
    @org.junit.Test public void existingFailure() { org.junit.Assert.assertEquals("customer expected failure", 5, Calc.add(2, 2)); }
    @org.junit.Ignore("fixture") @org.junit.Test public void ignored() {}
}
`}},
		{"testng", "testng", testng, "3.5.3", "", map[string]string{"src/test/java/example/ExampleTest.java": `package example;
public class ExampleTest {
    @org.testng.annotations.Test public void passes() { org.testng.Assert.assertEquals(Calc.add(2, 2), 4); }
    @org.testng.annotations.Test public void existingFailure() { org.testng.Assert.assertEquals(Calc.add(2, 2), 5, "customer expected failure"); }
    @org.testng.annotations.Test(enabled = false) public void ignored() {}
}
`}},
		{"spock", "spock", spock, "3.5.3", gmavenplus, map[string]string{"src/test/groovy/example/ExampleTest.groovy": `package example
class ExampleTest extends spock.lang.Specification {
    def "passes"() { expect: Calc.add(2, 2) == 4 }
    def "existing failure"() { expect: Calc.add(2, 2) == 5 }
    @spock.lang.Ignore def "ignored"() { expect: true }
}
`}},
		// Cucumber 7 needs its JUnit Platform release, which Surefire 3.2 runs.
		{"cucumber junit4 runner", "cucumber-java", cucumberJUnit4, "3.2.5", "", map[string]string{
			"src/test/java/example/RunCucumberTest.java": "package example;\n@org.junit.runner.RunWith(io.cucumber.junit.Cucumber.class)\n@io.cucumber.junit.CucumberOptions(features = \"classpath:example\", glue = \"example\")\npublic class RunCucumberTest {}\n",
			"src/test/java/example/StepDefinitions.java": cucumberSteps, "src/test/resources/example/calc.feature": cucumberFeature}},
		// The lambda backend has no step annotations; the probe registers its
		// step in a constructor.
		{"cucumber java8 lambdas", "cucumber-java", strings.Replace(cucumberJUnit4, "<artifactId>cucumber-java</artifactId>", "<artifactId>cucumber-java8</artifactId>", 1), "3.2.5", "", map[string]string{
			"src/test/java/example/RunCucumberTest.java": "package example;\n@org.junit.runner.RunWith(io.cucumber.junit.Cucumber.class)\n@io.cucumber.junit.CucumberOptions(features = \"classpath:example\", glue = \"example\")\npublic class RunCucumberTest {}\n",
			"src/test/java/example/StepDefinitions.java": "package example;\npublic class StepDefinitions implements io.cucumber.java8.En {\n    private int result;\n    public StepDefinitions() {\n        When(\"I add {int} and {int}\", (Integer a, Integer b) -> { result = Calc.add(a, b); });\n        Then(\"the result is {int}\", (Integer expected) -> { if (result != expected) throw new AssertionError(\"customer expected failure: \" + result); });\n    }\n}\n",
			"src/test/resources/example/calc.feature":    cucumberFeature}},
		{"cucumber platform suite", "cucumber-java", cucumberPlatform, "3.2.5", "", map[string]string{
			"src/test/java/example/RunCucumberTest.java": "package example;\n@org.junit.platform.suite.api.Suite\n@org.junit.platform.suite.api.IncludeEngines(\"cucumber\")\n@org.junit.platform.suite.api.SelectClasspathResource(\"example\")\n@org.junit.platform.suite.api.ConfigurationParameter(key = io.cucumber.junit.platform.engine.Constants.GLUE_PROPERTY_NAME, value = \"example\")\npublic class RunCucumberTest {}\n",
			"src/test/java/example/StepDefinitions.java": cucumberSteps, "src/test/resources/example/calc.feature": cucumberFeature}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"pom.xml": pomWith("fixture", tc.dependency, tc.surefire, tc.plugins), "src/main/java/example/Calc.java": calc}
			maps.Copy(files, tc.files)
			root := project(t, tc.framework, files)
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
			probes, err := filepath.Glob(filepath.Join(root, "src/test/*/ddtest"))
			require.NoFileExists(t, filepath.Join(root, "src/test/java/example/DdtestValidationSteps.java"), "probe steps stay in the disposable copy")
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
