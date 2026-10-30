// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gradleFixture is a two-project Gradle build: app tests with JUnit 5, lib with TestNG.
func gradleFixture() map[string]string {
	return map[string]string{
		"settings.gradle.kts":  "rootProject.name = \"shop\"\ninclude(\":app\", \":lib\")\n",
		"gradlew":              "#!/bin/sh\n",
		"app/build.gradle.kts": "dependencies { testImplementation(\"org.junit.jupiter:junit-jupiter:5.11.4\") }\n",
		"lib/build.gradle":     "dependencies { testImplementation 'org.testng:testng:7.10.2' }\n",
	}
}

func TestJavaResolvesGradleTestSteps(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for run, command := range map[string]string{
		"./gradlew test":                                 "./gradlew test",
		"./gradlew --no-daemon -Pci=true build":          "./gradlew --no-daemon -Pci=true build",
		"gradle :app:test --tests com.example.ApiTest":   "gradle :app:test --tests com.example.ApiTest",
		"./gradlew clean build jacocoTestReport publish": "./gradlew build",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Matched, "%s: %s", run, resolution.Reason)
		require.Len(t, resolution.Tests, 1)
		require.Equal(t, command, resolution.Tests[0].Command, run)
	}
	notes := strings.Join(javaStep(t, root, "./gradlew clean build jacocoTestReport publish").Tests[0].StepScopeNotes, "\n")
	require.Contains(t, notes, "also runs clean, jacocoTestReport, publish", "tasks outside testing are reported, and publishing never runs")
	require.Contains(t, notes, ":lib (testng)", "projects of other frameworks are reported")
}

func TestJavaGradleStepsOutsideOrNeedingReview(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for run, reason := range map[string]string{
		"./gradlew assemble javadoc": "outside " + "junit5 test scope",
		"./gradlew build -x test":    "excludes the test task",
		"./gradlew --version":        "outside junit5 test scope",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	for run, reason := range map[string]string{
		"./gradlew -b ci.gradle test":      "changes the build Gradle runs",
		"./gradlew -I ci/init.gradle test": "changes the build Gradle runs",
		"./gradlew -p ../other test":       "leaves the repository",
		"./gradlew -p /tmp/other test":     "absolute project directory",
		"mvn test && ./gradlew assemble":   "runs both Maven and Gradle",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	files := gradleFixture()
	files["settings.gradle.kts"] = "file(\"modules\").listFiles().forEach { include(\":${it.name}\") }\n"
	dynamic := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, dynamic, "./gradlew test").Reason, "could not be read statically")
	files = gradleFixture()
	files["build.gradle"] = "defaultTasks 'test'\n"
	defaults := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, defaults, "./gradlew").Reason, "default tasks could not be read statically")
	require.True(t, javaStep(t, root, "./gradlew").Review, "without default tasks, Gradle runs help")
	files = gradleFixture()
	files["app/build.gradle.kts"] = "defaultTasks(\"test\")\n"
	selected := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, selected, "./gradlew -p app").Reason, "default tasks could not be read statically", "the build -p selects declares the default tasks")
}

func TestJavaCIConfigurationChecksGradleInstrumentation(t *testing.T) {
	workflow := func(input, env string) string {
		return "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '21', distribution: temurin}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'" + input + "}\n      - run: ./gradlew test\n" + env
	}
	check := func(input, env string) RuntimeFinding {
		t.Helper()
		result := JavaCIConfiguration(javaWorkflowFixture(t, workflow(input, env), gradleFixture()), "junit5", "")
		require.Len(t, result.Jobs, 1)
		return result.Jobs[0]
	}
	for _, input := range []string{"", ", java-instrumented-build-system: gradle", ", java-instrumented-build-system: all"} {
		require.Equal(t, "not checked", check(input, "").Status, "the action instruments Gradle: %q", input)
	}
	finding := check(", java-instrumented-build-system: maven", "")
	require.Equal(t, "inconclusive", finding.Status)
	require.Contains(t, finding.Reason, "does not instrument Gradle")
	finding = check("", "        env: {GRADLE_OPTS: -Xmx2g}\n")
	require.Contains(t, finding.Reason, "sets GRADLE_OPTS, replacing the -javaagent option")
	require.Equal(t, "not checked", check("", "        env: {GRADLE_OPTS: '${{ env.GRADLE_OPTS }} -Xmx2g'}\n").Status, "a value that keeps the agent is fine")
	// The Maven variable doesn't carry the agent for a Gradle build.
	require.Equal(t, "not checked", check("", "        env: {MAVEN_OPTS: -Xmx2g}\n").Status)
}

func TestJavaCIConfigurationNamesTheBuildToolOfAStepUnderReview(t *testing.T) {
	workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '21', distribution: temurin}\n      - run: ./gradlew test\n        if: steps.cache.outputs.hit != 'true'\n"
	result := JavaCIConfiguration(javaWorkflowFixture(t, workflow, gradleFixture()), "junit5", "")
	require.Len(t, result.Jobs, 1)
	require.True(t, strings.HasPrefix(result.Jobs[0].Reason, "Gradle CI scope needs review: cannot statically resolve CI condition"), result.Jobs[0].Reason)
}

func TestJavaGradleTestExclusionKeepsOtherTestTasksInScope(t *testing.T) {
	files := gradleFixture()
	files["app/build.gradle.kts"] += "val integrationTest = tasks.register<Test>(\"integrationTest\")\ntasks.check { dependsOn(integrationTest) }\n"
	root := javaWorkflowFixture(t, "", files)
	resolution := javaStep(t, root, "./gradlew check -x test")
	require.True(t, resolution.Matched, "check still runs integrationTest: %s", resolution.Reason)
	require.Equal(t, "./gradlew check -x test", resolution.Tests[0].Command)
	require.True(t, javaStep(t, javaWorkflowFixture(t, "", gradleFixture()), "./gradlew check -x test").Review, "without other test tasks, excluding test skips the tests")
}

func TestJavaGradleCustomTestTaskNames(t *testing.T) {
	files := gradleFixture()
	files["app/build.gradle.kts"] += "tasks.register<Test>(\"ciTests\")\n"
	root := javaWorkflowFixture(t, "", files)
	resolution := javaStep(t, root, "./gradlew ciTests spotlessCheck")
	require.True(t, resolution.Matched, "a declared Test task runs tests: %s", resolution.Reason)
	require.Equal(t, "./gradlew ciTests", resolution.Tests[0].Command)
	require.Contains(t, strings.Join(resolution.Tests[0].StepScopeNotes, " "), "also runs spotlessCheck")
	// Without the declaration, an unknown task is outside testing...
	plain := javaWorkflowFixture(t, "", gradleFixture())
	require.True(t, javaStep(t, plain, "./gradlew verifyAll").Review)
	// ...unless build logic ddtest does not read may declare it.
	files = gradleFixture()
	files["buildSrc/src/main/kotlin/conventions.gradle.kts"] = ""
	conventions := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, conventions, "./gradlew verifyAll").Reason, "may be a test task declared by build logic ddtest does not read")
	require.True(t, javaStep(t, conventions, "./gradlew spotlessCheck").Review, "conventional non-test tasks need no build read")
}

func TestJavaGradleStepsThatRunNoTests(t *testing.T) {
	files := gradleFixture()
	files["app/build.gradle.kts"] += "tasks.register<Test>(\"integrationTest\")\n"
	root := javaWorkflowFixture(t, "", files)
	for _, run := range []string{"./gradlew test --dry-run", "./gradlew -m build", "./gradlew integrationTest -x integrationTest", "./gradlew :app:test -x :app:test"} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, "%s: %s", run, resolution.Reason)
		require.False(t, resolution.Matched, run)
	}
	require.Contains(t, javaStep(t, root, "./gradlew integrationTest -x integrationTest").Reason, "excludes the test tasks it requests")
}

func TestJavaGradleCompoundStepsReadEachBuild(t *testing.T) {
	files := gradleFixture()
	files["standalone/settings.gradle"] = "rootProject.name = 'standalone'\n"
	files["standalone/build.gradle"] = "task ciTests(type: Test)\n"
	root := javaWorkflowFixture(t, "", files)
	resolution := javaStep(t, root, "./gradlew assemble && ./gradlew -p standalone ciTests")
	require.True(t, resolution.Matched, "the second command's own build declares ciTests: %s", resolution.Reason)
	require.Equal(t, "./gradlew -p standalone ciTests", resolution.Tests[0].Command)
}

func TestJavaGradleDefaultTasksOfTheRootBuild(t *testing.T) {
	files := gradleFixture()
	files["build.gradle.kts"] = "subprojects { defaultTasks(\"test\") }\n"
	root := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, root, "./gradlew -p app").Reason, "default tasks could not be read statically")
}

func TestJavaWrapperPermissionStepRunsNoTests(t *testing.T) {
	for _, run := range []string{"chmod +x gradlew", "chmod +x ./mvnw", "chmod 755 gradlew && ls -l gradlew"} {
		resolution := javaStep(t, javaWorkflowFixture(t, "", gradleFixture()), run)
		require.False(t, resolution.Matched, run)
		require.Empty(t, resolution.Reason, "making the wrapper executable runs no tests: %s", run)
	}
	require.Contains(t, javaStep(t, javaWorkflowFixture(t, "", gradleFixture()), "chmod +x gradlew && ./gradlew test").Reason, "single explicit command", "the build it prepares is still one test step")
}

func TestJavaGradleTasksDeclaredWithAnotherType(t *testing.T) {
	files := gradleFixture()
	files["app/build.gradle.kts"] += "for (version in listOf(11, 17)) { tasks.register(\"testOn$version\", Test) }\n"
	files["lib/build.gradle"] += "task('apiDiff', type: JapicmpTask, dependsOn: 'jar') { }\n"
	root := javaWorkflowFixture(t, "", files)
	resolution := javaStep(t, root, "./gradlew assemble apiDiff check jacocoTestReport --continue")
	require.True(t, resolution.Matched, "apiDiff is declared as a JapicmpTask, never a test task: %s", resolution.Reason)
	require.Equal(t, "./gradlew check --continue", resolution.Tests[0].Command)
	require.Contains(t, javaStep(t, root, "./gradlew verifyAll").Reason, "may be a test task declared by build logic ddtest does not read", "a computed Test task name may still be any undeclared task")
	require.True(t, javaStep(t, root, "./gradlew publishToSonatype closeSonatypeStagingRepository").Review, "staging repository tasks publish")
}

func TestJavaGradleReadsFileProjectDirectories(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{
		"settings.gradle":  "include ':java-jwt'\nproject(':java-jwt').projectDir = new File(rootProject.projectDir, '/lib')\n",
		"gradlew":          "#!/bin/sh\n",
		"lib/build.gradle": "dependencies { testImplementation 'junit:junit:4.13.2' }\n",
	})
	resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "./gradlew check"}, "junit4")
	require.True(t, resolution.Matched, resolution.Reason)
}

// publishJob is a release job without the Datadog action, as jcommander's
// Maven Central workflow: secrets fill its commands' values.
const publishJob = `
  publish:
    steps:
      - uses: actions/checkout@v4
      - run: |
          cat <(echo -e "${{ secrets.GPG_SECRET_KEY }}") | gpg --batch --import
          VERSION=$(head -1 .version)
          curl -s "https://get.sdkman.io" | bash
          source "/home/runner/.sdkman/bin/sdkman-init.sh"
          sdk install java ${{ inputs.java-version }}
      - run: |
          ./gradlew --info -Psigning.password="$GPG_PASSPHRASE" -P signing.keyId=${{ secrets.GPG_KEY_ID }} publish closeSonatypeStagingRepository
          curl -u ${{ secrets.SONATYPE_USER }}:${{ secrets.SONATYPE_PASSWORD }} -X POST https://example.com/upload
`

func TestJavaPublishJobWithoutTheActionIsOutsideValidation(t *testing.T) {
	workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '21'}\n      - run: ./gradlew test\n" + publishJob
	root := javaWorkflowFixture(t, workflow, gradleFixture())
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved, "a job that only signs, downloads and publishes runs no tests")
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), ".github/workflows/test.yml / publish: CI job without the Datadog setup that never runs junit5")
	// The CI runtime check scopes the job as discovery does.
	ci := JavaCIConfiguration(root, "junit5", "")
	require.False(t, slices.ContainsFunc(ci.Jobs, func(job RuntimeFinding) bool { return job.Job == "publish" }), "no runtime finding for a job outside validation: %+v", ci.Jobs)
	require.True(t, slices.ContainsFunc(ci.Review, func(job RuntimeFinding) bool {
		return job.Job == "publish" && strings.Contains(job.Reason, "never runs junit5")
	}))

	// A step that may still run tests keeps the job's gaps in scope.
	for _, step := range []string{
		"./gradlew ${{ inputs.tasks }}",
		"./gradlew publish test",
		"mvn -B deploy",
		"./ci/release.sh",
		"cat ci/test.sh | bash",
		"git submodule foreach './gradlew test'",
		"git rebase --exec './gradlew test' HEAD~3",
		"curl -s https://example.com/install | bash -c \"$(cat ci/test.sh)\"",
		"source ci/env.sh",
		"make release",
		"${{ inputs.command }}",
		"find . -name '*.sh' -exec sh {} +",
	} {
		job := strings.Replace(publishJob, "      - uses: actions/checkout@v4\n", "      - uses: actions/checkout@v4\n      - run: "+step+"\n", 1)
		scope, err := DiscoverValidationScope(javaWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: ./gradlew test\n"+job, gradleFixture()), "junit5")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved, "%s may run tests", step)
	}
	require.False(t, javaMayRunTests("git fetch --tags && git tag -l", false), "other git commands run nothing")

	// Goals MAVEN_ARGS or .mvn/maven.config add may make a metadata command run tests.
	metadata := strings.Replace(publishJob, "          VERSION=$(head -1 .version)\n", "          VERSION=$(mvn help:evaluate -Dexpression=project.version -q -DforceStdout)\n", 1)
	maven := map[string]string{"pom.xml": javaJUnit5POM}
	scope, err = DiscoverValidationScope(javaWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: mvn verify\n"+metadata, maps.Clone(maven)), "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved, "help:evaluate reads the project")
	for name, files := range map[string]map[string]string{
		"MAVEN_ARGS":   maven,
		"maven.config": {"pom.xml": javaJUnit5POM, ".mvn/maven.config": "verify\n"},
	} {
		job := metadata
		if name == "MAVEN_ARGS" {
			job = strings.Replace(metadata, "  publish:\n", "  publish:\n    env: {MAVEN_ARGS: test}\n", 1)
		}
		scope, err := DiscoverValidationScope(javaWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: mvn verify\n"+job, maps.Clone(files)), "junit5")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved, "%s adds goals to the metadata command", name)
	}
}

func TestJavaGradleChainsWithOneTestCommand(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for run, command := range map[string]string{
		"./gradlew assemble && ./gradlew check --info --stacktrace":                                                               "./gradlew check --info --stacktrace",
		"./gradlew clean; ./gradlew test && ./gradlew publishToSonatype closeAndReleaseSonatypeStagingRepository -x check --info": "./gradlew test",
		"./gradlew spotlessCheck && ./gradlew --dry-run build && ./gradlew :app:test":                                             "./gradlew :app:test",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Matched, "%s: %s", run, resolution.Reason)
		require.Equal(t, command, resolution.Tests[0].Command, run)
		require.Contains(t, strings.Join(resolution.Tests[0].StepScopeNotes, " "), "also runs Gradle commands that run no tests: ./gradlew ", run)
	}
	for _, run := range []string{
		"./gradlew test && ./gradlew integrationTest",
		"./gradlew assemble && ./gradlew check && ./gradlew jcstress",
		"cd app && ./gradlew test",
		"./gradlew && ./gradlew test",
		"./gradlew assemble && make test",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.NotEmpty(t, resolution.Reason, run)
	}
}
