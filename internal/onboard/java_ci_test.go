// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const javaJUnit5POM = `<project><artifactId>service</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`

func javaWorkflowFixture(t *testing.T, workflow string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if files == nil {
		files = map[string]string{"pom.xml": javaJUnit5POM}
	}
	files[".github/workflows/test.yml"] = workflow
	for name, contents := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0644))
	}
	return root
}

func javaStep(t *testing.T, root, run string) commandResolution {
	t.Helper()
	return resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: run}, "junit5")
}

func TestJavaResolvesMavenTestSteps(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for run, command := range map[string]string{
		"mvn -B verify":                         "mvn -B verify",
		"./mvnw test -pl service":               "./mvnw test -pl service",
		"mvn --no-transfer-progress -B package": "mvn --no-transfer-progress -B package",
		"mvn -B clean install":                  "mvn -B clean verify",
		"mvn -B deploy":                         "mvn -B verify",
		"mvn verify source:jar javadoc:jar":     "mvn verify",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Matched, run)
		require.Len(t, resolution.Tests, 1, run)
		require.Equal(t, command, resolution.Tests[0].Command, run)
		require.Equal(t, ".", resolution.Tests[0].Directory)
		if command != run {
			require.Len(t, resolution.Tests[0].StepScopeNotes, 1, run)
			require.Contains(t, resolution.Tests[0].StepScopeNotes[0], "never installs or publishes artifacts", run)
		} else {
			require.Empty(t, resolution.Tests[0].StepScopeNotes, run)
		}
	}
}

func TestJavaReportsModulesOfOtherFrameworks(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{
		"pom.xml":        `<project><modules><module>api</module><module>legacy</module><module>docs</module></modules></project>`,
		"api/pom.xml":    javaJUnit5POM,
		"legacy/pom.xml": `<project><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></project>`,
		"docs/pom.xml":   `<project/>`,
	})
	resolution := javaStep(t, root, "mvn -B verify")
	require.True(t, resolution.Matched)
	require.Equal(t, "mvn -B verify", resolution.Tests[0].Command, "one Maven build runs every module; the command is unchanged")
	require.Len(t, resolution.Tests[0].StepScopeNotes, 1)
	require.Contains(t, resolution.Tests[0].StepScopeNotes[0], "legacy (testng)")
	require.Contains(t, resolution.Tests[0].StepScopeNotes[0], "depends on its active profiles and reactor selectors", "-pl may leave the module out")
	require.NotContains(t, resolution.Tests[0].StepScopeNotes[0], "docs", "a module without tests is not reported")
	require.Equal(t, resolution.ReviewReason, resolution.Tests[0].StepScopeNotes[0])
}

func TestJavaKeepsUnresolvedStepsInScope(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for run, reason := range map[string]string{
		"mvn -B verify | tee build.log":       "could not be resolved statically",
		"mvn verify || true":                  "could not be resolved statically",
		"(cd service && mvn test)":            "could not be resolved statically",
		"cd service && mvn test":              "single explicit command",
		"mvn ${{ matrix.goals }}":             "expression was not resolved",
		"./ci/test.sh":                        "Repository script",
		"bash scripts/run-tests":              "Repository script",
		"make test":                           "Test wrapper was not resolved",
		"gradle test":                         "Test wrapper was not resolved",
		"mvn -f ../other/pom.xml verify":      "POM selection needs review",
		"mvn compile && ./ci/integration.sh":  "Repository script",
		"./ci/integration.sh | tee build.log": "Repository script",
		"mvn compile && make test":            "Test wrapper was not resolved",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.False(t, resolution.Review, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	broken := javaWorkflowFixture(t, "", map[string]string{"pom.xml": `<project><modules><module>${module}</module></modules></project>`})
	require.Contains(t, javaStep(t, broken, "mvn verify").Reason, "reactor could not be read")
}

func TestJavaLeavesNonTestStepsOutsideScope(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for run, reason := range map[string]string{
		"mvn -B spotless:check":                 "outside the test lifecycle",
		"mvn javadoc:javadoc && mvn site":       "outside the test lifecycle",
		"mvn -B -DskipTests package":            "skips tests",
		"./mvnw -Dmaven.test.skip=true install": "skips tests",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	for _, run := range []string{"echo done", "java -version", "mvn -v"} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched || resolution.Review, run)
		require.Empty(t, resolution.Reason, run)
	}
}

func TestJavaReportsTestLikeCommandsOfUninstrumentedJobs(t *testing.T) {
	root := javaWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java}\n      - run: mvn -B verify\n  docs:\n    steps:\n      - run: make test-docs\n", nil)
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
	require.Empty(t, scope.Unresolved, "a non-test job without the action is a caveat, not a blocker")
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Review, 1)
	require.Equal(t, CodeUninstrumentedJob, ci.Review[0].Code)
}

func TestJavaScopeFollowsTheMatrix(t *testing.T) {
	root := javaWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        java: ['17', '21']
    steps:
      - uses: actions/setup-java@v4
        with: {distribution: temurin, java-version: '${{ matrix.java }}'}
      - run: ./mvnw -B verify
`, nil)
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./mvnw -B verify", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].Locations, 2, "one location per matrix row")
}

func TestJavaCIConfigurationInventoriesJDKAndTracer(t *testing.T) {
	root := javaWorkflowFixture(t, `jobs:
  tests:
    strategy:
      matrix:
        java: ['8', '17', '25-ea', 'lts']
    steps:
      - uses: actions/setup-java@v4
        with: {distribution: temurin, java-version: '${{ matrix.java }}'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.java != '25-ea'
        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}
      - run: mvn -B verify
`, nil)
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 4)
	byJava := map[string]RuntimeFinding{}
	for _, job := range ci.Jobs {
		byJava[job.Runtime] = job
	}
	require.Equal(t, "not checked", byJava["8"].Status, "complete selections are validated, not certified statically")
	require.Equal(t, "1.67.1", byJava["17"].TracerRequested)
	require.Equal(t, "not checked", byJava["17"].Status)
	require.Equal(t, "excluded", byJava["25-ea"].Status, "an early-access JDK is excluded with an if: on the action")
	require.Equal(t, "inconclusive", byJava["lts"].Status)
	require.Contains(t, byJava["lts"].Reason, "not a numeric selection")
	require.Equal(t, "inconclusive", ci.Status)
}

func TestJavaCIConfigurationRequiresCompleteSelections(t *testing.T) {
	for _, tc := range []struct{ name, steps, status, reason string }{
		{"complete", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n", "not checked", "Validate dd-java-agent 1.67.1 on Java 17"},
		{"old tracer on a new JDK", "      - uses: actions/setup-java@v4\n        with: {java-version: '21'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '0.94.0'}\n      - run: mvn verify\n", "not checked", "does not establish that this release supports this JDK"},
		{"step replaces MAVEN_OPTS", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n        env: {MAVEN_OPTS: '-Xmx2g'}\n", "inconclusive", "replacing the -javaagent option"},
		{"step extends MAVEN_OPTS", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n        env: {MAVEN_OPTS: '${{ env.MAVEN_OPTS }} -Xmx2g'}\n", "not checked", "Validate dd-java-agent"},
		{"job MAVEN_OPTS is kept by the action", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n    env: {MAVEN_OPTS: '-Xmx2g'}\n", "not checked", "Validate dd-java-agent"},
		{"step replaces JAVA_TOOL_OPTIONS", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1', java-instrumented-build-system: all}\n      - run: mvn verify\n        env: {JAVA_TOOL_OPTIONS: '-Xmx1g'}\n", "inconclusive", "sets JAVA_TOOL_OPTIONS"},
		{"MAVEN_OPTS is free with all", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1', java-instrumented-build-system: all}\n      - run: mvn verify\n        env: {MAVEN_OPTS: '-Xmx2g'}\n", "not checked", "Validate dd-java-agent"},
		{"action instruments Gradle only", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1', java-instrumented-build-system: gradle}\n      - run: mvn verify\n", "inconclusive", "does not instrument Maven"},
		{"no JDK setup", "      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n", "inconclusive", "static java-version"},
		{"no action", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - run: mvn verify\n", "inconclusive", "No Datadog action"},
		{"action for another language", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python}\n      - run: mvn verify\n", "inconclusive", "No Datadog action"},
		{"floating tracer", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}'}\n      - run: mvn verify\n", "inconclusive", "not an exact release"},
		{"old JDK", "      - uses: actions/setup-java@v4\n        with: {java-version: '1.7'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n", "inconclusive", "older than Java 8"},
		{"reporting disabled", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n        env: {DD_CIVISIBILITY_ENABLED: 'false'}\n", "inconclusive", "explicitly disabled by DD_CIVISIBILITY_ENABLED"},
		{"tracing disabled only", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n        env: {DD_TRACE_ENABLED: 'false'}\n", "not checked", "Validate dd-java-agent"},
		{"no API key", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, java-tracer-version: '1.67.1'}\n      - run: mvn verify\n", "inconclusive", "no api_key input"},
		{"unsupported action release", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v2\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn verify\n", "inconclusive", "not a supported release"},
		{"action after the tests", "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - run: mvn verify\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n", "inconclusive", "No Datadog action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ci := JavaCIConfiguration(javaWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n"+tc.steps, nil), "junit5", "")
			require.Len(t, ci.Jobs, 1)
			require.Equal(t, tc.status, ci.Jobs[0].Status)
			require.Contains(t, ci.Jobs[0].Reason, tc.reason)
			require.Equal(t, "inconclusive", ci.Status, "static selections never certify the inventory")
		})
	}
}

func TestJavaCIConfigurationKeepsUnresolvedStepsVisible(t *testing.T) {
	ci := JavaCIConfiguration(javaWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - run: mvn -B verify\n      - run: ./ci/integration.sh\n      - run: mvn spotless:check\n", nil), "junit5", "")
	require.Len(t, ci.Jobs, 2)
	require.True(t, strings.Contains(ci.Jobs[1].Reason, "Repository script"), ci.Jobs[1].Reason)
	require.Len(t, ci.Review, 1, "lint goals are listed outside scope")
	require.Equal(t, "not applicable", JavaCIConfiguration(javaWorkflowFixture(t, "jobs:\n  lint:\n    steps:\n      - run: echo ok\n", nil), "junit5", "").Status)
}

func TestJavaVariantsKeepCustomerJVMOptions(t *testing.T) {
	for _, key := range []string{"MAVEN_OPTS", "JAVA_TOOL_OPTIONS"} {
		value, handled, err := javaDiscovery{}.variantValue(ciWorkflow{}, runtimeJob{}, runtimeStep{}, nil, key, "${{ env."+key+" }} -Xmx2g")
		require.NoError(t, err)
		require.True(t, handled)
		require.Equal(t, "-Xmx2g", value, "local validation adds its own agent")
		_, _, err = javaDiscovery{}.variantValue(ciWorkflow{}, runtimeJob{Env: map[string]string{key: "-Dfile.encoding=UTF-8"}}, runtimeStep{}, nil, key, "${{ env."+key+" }} -Xmx2g")
		require.ErrorContains(t, err, "extends workflow or job JVM options")
	}
	_, handled, err := javaDiscovery{}.variantValue(ciWorkflow{}, runtimeJob{}, runtimeStep{}, nil, "CI", "true")
	require.NoError(t, err)
	require.False(t, handled)
}

func TestJavaRunsTheDefaultGoalOfGoallessCommands(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{"pom.xml": `<project><build><defaultGoal>clean install</defaultGoal></build><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></project>`})
	resolution := javaStep(t, root, "mvn -B")
	require.True(t, resolution.Matched)
	require.Equal(t, "mvn -B clean verify", resolution.Tests[0].Command)
	plain := javaWorkflowFixture(t, "", nil)
	for _, run := range []string{"mvn -B", "./mvnw --version", "mvn -v && echo done"} {
		resolution := javaStep(t, plain, run)
		require.False(t, resolution.Matched || resolution.Review, run)
		require.Empty(t, resolution.Reason, run, "without a default goal or with metadata only, Maven builds nothing")
	}
}

func TestJavaExpandsMatrixSelectedCommands(t *testing.T) {
	root := javaWorkflowFixture(t, "jobs:\n  tests:\n    strategy:\n      matrix:\n        command: ['mvn -B verify']\n    steps:\n      - run: ${{ matrix.command }}\n", nil)
	require.Contains(t, javaStep(t, root, "${{ matrix.command }}").Reason, "expression was not resolved", "an expression may select the tests")
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
}

func TestJavaKeepsRepositoryRelativeScripts(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for _, run := range []string{"ci/integration.sh", "ci/integration.sh | tee build.log", "integration.sh", "cd service && ../ci/run"} {
		require.Contains(t, javaStep(t, root, run).Reason, "Repository script", run)
	}
	for _, run := range []string{"cat target/build.log | grep ERROR", "/usr/bin/env"} {
		require.Empty(t, javaStep(t, root, run).Reason, run)
	}
}

func TestJavaConfinesTheWorkingDirectory(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "pom.xml"), []byte(javaJUnit5POM), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))
	resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "mvn verify", WorkingDirectory: "linked"}, "junit5")
	require.False(t, resolution.Matched)
	require.Contains(t, resolution.Reason, "must stay inside the repository")
}

func TestJavaSeparatedSkipTestsIsOutsideScope(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for _, run := range []string{"mvn -D skipTests=true verify", "mvn --define maven.test.skip=true install"} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, run)
		require.Contains(t, resolution.Reason, "skips tests", run)
	}
}

func TestJavaCIConfigurationNeedsTheDatadogAgent(t *testing.T) {
	steps := func(with, env string) string {
		return "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'" + with + "}\n      - run: mvn verify\n" + env
	}
	for name, tc := range map[string]struct{ workflow, reason string }{
		"another agent replaces it": {steps("", "        env: {MAVEN_OPTS: '-javaagent:/opt/jacoco.jar'}\n"), "replacing the -javaagent option"},
		"build system expression":   {steps(", java-instrumented-build-system: '${{ inputs.build_system }}'", ""), "needs review"},
	} {
		ci := JavaCIConfiguration(javaWorkflowFixture(t, tc.workflow, nil), "junit5", "")
		require.Len(t, ci.Jobs, 1, name)
		require.Equal(t, "inconclusive", ci.Jobs[0].Status, name)
		require.Contains(t, ci.Jobs[0].Reason, tc.reason, name)
	}
	kept := JavaCIConfiguration(javaWorkflowFixture(t, steps("", "        env: {MAVEN_OPTS: '-javaagent:/opt/dd-java-agent.jar -javaagent:/opt/jacoco.jar'}\n"), nil), "junit5", "")
	require.Equal(t, "not checked", kept.Jobs[0].Status, "an explicit Datadog agent keeps instrumentation")
}

func TestJavaKeepsAssignmentPrefixedMavenInScope(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for _, run := range []string{"MAVEN_OPTS=-Xmx2g mvn clean install", "env CI=true mvn -B package"} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.Contains(t, resolution.Reason, "single explicit command", run)
	}
}

func TestJavaKeepsInheritedDefaultGoalsUnresolved(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{
		"pom.xml":       `<project><build><defaultGoal>verify</defaultGoal></build><modules><module>child</module></modules></project>`,
		"child/pom.xml": `<project><parent><artifactId>parent</artifactId></parent><artifactId>child</artifactId></project>`,
	})
	require.Contains(t, javaStep(t, root, "mvn -f child/pom.xml").Reason, "may inherit a default goal")
}

func TestJavaShellReferencesInStepEnvDoNotKeepTheAgent(t *testing.T) {
	for _, value := range []string{"$MAVEN_OPTS -Xmx2g", "${MAVEN_OPTS} -Xmx2g"} {
		ci := JavaCIConfiguration(javaWorkflowFixture(t, "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, java-tracer-version: '1.67.1'}\n      - run: mvn verify\n        env: {MAVEN_OPTS: '"+value+"'}\n", nil), "junit5", "")
		require.Equal(t, "inconclusive", ci.Jobs[0].Status, value)
		require.Contains(t, ci.Jobs[0].Reason, "replacing the -javaagent option", value)
	}
}

func TestJavaResolvesPOMsFromSubdirectorySteps(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{
		"pom.xml":       `<project><modules><module>core</module></modules></project>`,
		"core/pom.xml":  javaJUnit5POM,
		"build/pom.xml": `<project><modules><module>../core</module></modules></project>`,
	})
	for _, tc := range []struct{ directory, run string }{{"build", "mvn verify"}, {"build", "mvn -f ../pom.xml verify"}, {"core", "mvn -f .. verify"}} {
		resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: tc.run, WorkingDirectory: tc.directory}, "junit5")
		require.True(t, resolution.Matched, "%s in %s: %s", tc.run, tc.directory, resolution.Reason)
		require.Equal(t, tc.directory, resolution.Tests[0].Directory)
	}
	resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "mvn -f ../../pom.xml verify", WorkingDirectory: "build"}, "junit5")
	require.Contains(t, resolution.Reason, "leaves the repository")
}

func TestJavaKeepsFullyQualifiedTestGoalsInScope(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	resolution := javaStep(t, root, "mvn -B org.apache.maven.plugins:maven-surefire-plugin:3.5.4:test")
	require.True(t, resolution.Matched)
	require.Equal(t, "mvn -B org.apache.maven.plugins:maven-surefire-plugin:3.5.4:test", resolution.Tests[0].Command)
	require.True(t, javaStep(t, root, "mvn -DskipTests=FALSE verify").Matched, "Maven reads FALSE as false")
}

func TestJavaCIConfigurationNeedsTheSelectedSite(t *testing.T) {
	workflow := "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'SITE}\n      - run: mvn verify\n"
	root := javaWorkflowFixture(t, strings.Replace(workflow, "SITE", "", 1), nil)
	require.Equal(t, "not checked", JavaCIConfiguration(root, "junit5", "").Jobs[0].Status)
	require.Equal(t, "not checked", JavaCIConfiguration(root, "junit5", "datadoghq.com").Jobs[0].Status, "the action reports to the default site")
	job := JavaCIConfiguration(root, "junit5", "datadoghq.eu").Jobs[0]
	require.Equal(t, "inconclusive", job.Status)
	require.Equal(t, CodeIncompleteCISetup, job.Code)
	require.Contains(t, job.Reason, "set site: datadoghq.eu on the action")
	root = javaWorkflowFixture(t, strings.Replace(workflow, "SITE", ", site: datadoghq.eu", 1), nil)
	require.Equal(t, "not checked", JavaCIConfiguration(root, "junit5", "datadoghq.eu").Jobs[0].Status)
}

func TestJavaDeployCredentialsDoNotBlockValidation(t *testing.T) {
	workflow := `jobs:
  build:
    steps:
      - uses: actions/setup-java@v4
        with: {java-version: '21', distribution: temurin, server-id: central, server-username: MAVEN_USERNAME, server-password: MAVEN_PASSWORD}
      - run: mvn --batch-mode deploy --file pom.xml
        env:
          MAVEN_USERNAME: ${{ secrets.MAVEN_USERNAME }}
          MAVEN_PASSWORD: ${{ secrets.MAVEN_PASSWORD }}
`
	scope, err := DiscoverValidationScope(javaWorkflowFixture(t, workflow, nil), "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved, "settings.xml credentials only deploy, which validation never runs")
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn --batch-mode verify --file pom.xml", scope.Commands[0].Command)
	require.Empty(t, scope.Commands[0].Environment["MAVEN_PASSWORD"], "no secret reaches validation")

	// Without a server-id, setup-java writes no credentials: a secret may feed the tests.
	scope, err = DiscoverValidationScope(javaWorkflowFixture(t, strings.Replace(workflow, "server-id: central, ", "", 1), nil), "junit5")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	// The action's default names, such as GITHUB_TOKEN, may serve the tests too.
	defaults := strings.NewReplacer("server-username: MAVEN_USERNAME, server-password: MAVEN_PASSWORD", "gpg-private-key: '${{ secrets.GPG_KEY }}'", "MAVEN_USERNAME: ${{ secrets.MAVEN_USERNAME }}\n          ", "", "MAVEN_PASSWORD: ${{ secrets.MAVEN_PASSWORD }}", "GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}").Replace(workflow)
	scope, err = DiscoverValidationScope(javaWorkflowFixture(t, defaults, nil), "junit5")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved, "only credentials the workflow names for settings.xml are left out")
}

func TestJavaCIConfigurationKeepsJavaSetupAcrossOtherDatadogActions(t *testing.T) {
	workflow := "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: SECOND, api_key: '${{ secrets.DD_API_KEY }}'}\n      - run: mvn verify\n"
	job := JavaCIConfiguration(javaWorkflowFixture(t, strings.Replace(workflow, "SECOND", "js", 1), nil), "junit5", "").Jobs[0]
	require.Equal(t, "not checked", job.Status, "a JavaScript action leaves the agent in MAVEN_OPTS: %s", job.Reason)
	require.Equal(t, "1.67.1", job.TracerRequested)
	job = JavaCIConfiguration(javaWorkflowFixture(t, strings.Replace(workflow, "SECOND", "java", 1), nil), "junit5", "").Jobs[0]
	require.Contains(t, job.Reason, "not an exact release", "a later Java action replaces the selection")
}

func TestJavaCIConfigurationLeavesPostTestStepsOutside(t *testing.T) {
	workflow := "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '8'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn test\n      - run: |\n          echo \"coverage = ${{ steps.jacoco.outputs.coverage }}\"\n"
	ci := JavaCIConfiguration(javaWorkflowFixture(t, workflow, nil), "junit5", "")
	require.Len(t, ci.Jobs, 1, "discovery reviews the post-test echo outside the test path: %+v", ci.Jobs)
	require.Equal(t, "not checked", ci.Jobs[0].Status)
	before := strings.Replace(workflow, "      - run: mvn test\n      - run: |\n          echo \"coverage = ${{ steps.jacoco.outputs.coverage }}\"\n", "      - run: echo ${{ steps.prepare.outputs.flags }}\n      - run: mvn test\n", 1)
	ci = JavaCIConfiguration(javaWorkflowFixture(t, before, nil), "junit5", "")
	require.Len(t, ci.Jobs, 2, "an unresolved step before the tests stays in scope")
}

func TestJavaMetadataScriptsRunNoTests(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	metadata := "ARTIFACT_ID=$(mvn help:evaluate -Dexpression=project.artifactId -q -DforceStdout)\nVERSION=$(./mvnw -q help:evaluate -Dexpression=project.version -DforceStdout)\necho \"zip=target/${ARTIFACT_ID}-${VERSION}.zip\" >> $GITHUB_OUTPUT\n"
	resolution := javaStep(t, root, metadata)
	require.False(t, resolution.Matched || resolution.Review, "help goals read the project without building it")
	require.Empty(t, resolution.Reason)
	for _, run := range []string{
		"VERSION=$(mvn help:evaluate -Dexpression=project.version -q -DforceStdout)\nmvn verify -Drevision=$VERSION",
		"VERSION=$(mvn help:evaluate -Dexpression=project.version -q -DforceStdout)\n./ci/release.sh $VERSION",
		"GOAL=test\nRESULT=$(mvn $GOAL)",
		"VERSION=$(mvn help:evaluate test -q)",
	} {
		require.NotEmpty(t, javaStep(t, root, run).Reason, "a script that may build or run tests stays unresolved: %s", run)
	}
}

func TestJavaPostTestScopeFollowsEachRowAndMavenLaunchers(t *testing.T) {
	action := "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n"
	// The test step exists only once the row is expanded; the earlier
	// unresolved setup stays in scope.
	matrix := "jobs:\n  tests:\n    strategy:\n      matrix:\n        command: ['mvn test']\n    steps:\n" + action + "      - run: echo \"MAVEN_OPTS=${{ steps.opts.outputs.value }}\" >> $GITHUB_ENV\n      - run: ${{ matrix.command }}\n"
	ci := JavaCIConfiguration(javaWorkflowFixture(t, matrix, nil), "junit5", "")
	require.Len(t, ci.Jobs, 2, "the setup before a matrix-selected test step stays in scope: %+v", ci.Jobs)
	// More Maven runs after the last literal test step stay in scope.
	launcher := "jobs:\n  tests:\n    steps:\n" + action + "      - run: mvn test\n      - run: find . -name pom.xml -exec mvn -f '{}' test \\;\n"
	ci = JavaCIConfiguration(javaWorkflowFixture(t, launcher, nil), "junit5", "")
	require.Len(t, ci.Jobs, 2, "a launcher that runs Maven may run tests: %+v", ci.Jobs)
	// A Maven call with an assignment may add goals, as MAVEN_ARGS does.
	require.NotEmpty(t, javaStep(t, javaWorkflowFixture(t, "", nil), "VERSION=$(MAVEN_ARGS=test mvn help:evaluate -Dexpression=project.version -q -DforceStdout)").Reason)
	for _, run := range []string{"export MAVEN_ARGS=test\nVERSION=$(mvn help:evaluate -Dexpression=project.version -q -DforceStdout)", "MAVEN_ARGS=test\nexport MAVEN_ARGS\nVERSION=$(mvn help:evaluate -q -DforceStdout)"} {
		require.NotEmpty(t, javaStep(t, javaWorkflowFixture(t, "", nil), run).Reason, "MAVEN_ARGS set anywhere in the script adds goals: %s", run)
	}
}

func TestJavaMetadataScriptsAndLaunchersFollowTheirEnvironmentAndMatrix(t *testing.T) {
	action := "      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n"
	metadata := "      - run: |\n          VERSION=$(mvn help:evaluate -Dexpression=project.version -q -DforceStdout)\n          echo $VERSION\n"
	for name, env := range map[string]string{"workflow": "env: {MAVEN_ARGS: test}\n", "job": "", "step": ""} {
		workflow := env + "jobs:\n  tests:\n    steps:\n" + action + metadata
		switch name {
		case "job":
			workflow = "jobs:\n  tests:\n    env: {MAVEN_ARGS: test}\n    steps:\n" + action + metadata
		case "step":
			workflow = "jobs:\n  tests:\n    steps:\n" + action + metadata + "        env: {MAVEN_ARGS: test}\n"
		}
		scope, err := DiscoverValidationScope(javaWorkflowFixture(t, workflow, nil), "junit5")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved, "%s MAVEN_ARGS adds goals to the metadata call", name)
	}
	launcher := "jobs:\n  tests:\n    strategy:\n      matrix:\n        maven: [mvn]\n    steps:\n" + action + "      - run: mvn test\n      - run: find . -name pom.xml -exec ${{ matrix.maven }} -f '{}' test \\;\n"
	ci := JavaCIConfiguration(javaWorkflowFixture(t, launcher, nil), "junit5", "")
	require.Len(t, ci.Jobs, 2, "the expanded launcher runs Maven: %+v", ci.Jobs)
}
