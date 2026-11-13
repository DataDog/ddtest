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

// From open-eid/cdoc2-auth: the test job passes Maven a settings file from a
// GitLab file variable, which only CI provides. A settings path never selects
// tests, so the job is validated without it and the note says so.
const cdoc2Pipeline = `stages:
  - build
  - publish

build:
  stage: build
  image: maven:3.8.8-eclipse-temurin-17
  script:
    - cat $MAVEN_SETTINGS
    - mvn test -s $MAVEN_SETTINGS
  cache:
    key: "$CI_COMMIT_REF_NAME"
    paths:
      - .m2/repository

publish:
  stage: publish
  image: maven:3.8.8-eclipse-temurin-17
  script:
    - cat $MAVEN_SETTINGS
    - mvn deploy -s $MAVEN_SETTINGS
  cache:
    key: "$CI_COMMIT_REF_NAME"
    paths:
      - .m2/repository
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
      when: manual
    - if: '$CI_COMMIT_TAG =~ /^v\d+\.\d+\.\d+\-release/'
    - if: '$CI_COMMIT_BRANCH != $CI_DEFAULT_BRANCH'
      when: manual
    - when: never
`

func TestJavaCISettingsPathLeftOut(t *testing.T) {
	gitLabJavaRepository(t, cdoc2Pipeline)
	scope, err := DiscoverValidationScope(".", "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	commands := map[string]TestCommand{}
	for _, command := range scope.Commands {
		commands[command.Command] = command
	}
	require.Contains(t, commands, "mvn test")
	require.Contains(t, commands, "mvn verify", "mvn deploy runs the tests too")
	notes := strings.Join(commands["mvn test"].StepScopeNotes, " ")
	require.Contains(t, notes, "-s $MAVEN_SETTINGS")
	require.Contains(t, notes, "without it")
}

func TestJavaCISettingsOptions(t *testing.T) {
	root := javaWorkflowFixture(t, "", nil)
	for run, command := range map[string]string{
		"mvn test -s $MAVEN_SETTINGS":                    "mvn test",
		"mvn -B --settings $CI_SETTINGS verify":          "mvn -B verify",
		"mvn --settings=$CI_SETTINGS verify":             "mvn verify",
		"mvn -gs ${GLOBAL_SETTINGS} -s $SETTINGS verify": "mvn verify",
		"mvn -s \"$MAVEN_SETTINGS\" verify":              "mvn verify",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Matched, run)
		require.Equal(t, command, resolution.Tests[0].Command, run)
		require.Contains(t, strings.Join(resolution.Tests[0].StepScopeNotes, " "), "settings", run)
	}
	// Values that may select tests, or a settings value computed by a
	// command, stay unresolved.
	for _, run := range []string{
		"mvn test -Dtest=$SUITE",
		"mvn test -s $(ci/settings)",
		"mvn $GOAL -s $MAVEN_SETTINGS",
		"mvn test -s $MAVEN_SETTINGS -P $PROFILE",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.NotEmpty(t, resolution.Reason, run)
	}
}

// With file-level variables, the shared resolver expands the step's reads
// before Java resolves it; the settings path, which no variable defines,
// still leaves out.
func TestJavaCISettingsPathLeftOutWithVariables(t *testing.T) {
	gitLabJavaRepository(t, "variables:\n  MAVEN_OPTS: \"-Dmaven.repo.local=.m2/repository\"\n\n"+cdoc2Pipeline)
	scope, err := DiscoverValidationScope(".", "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	require.Contains(t, strings.Join(scope.Commands[0].StepScopeNotes, " "), "-s $MAVEN_SETTINGS")
}

// From KorAP/Kalamar-Plugin-Export: the test job's options come from a
// file-level variable of several words, and the other jobs build and push a
// Docker image or compile, outside the test lifecycle.
func kalamarPipeline(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "java-ci", "kalamar-gitlab-ci.yml"))
	require.NoError(t, err)
	return string(data)
}

const kalamarDockerfile = `FROM eclipse-temurin:22-jdk-alpine AS builder
COPY . /export
WORKDIR /export
RUN apk update && \
    apk add --no-cache maven
RUN mvn clean package
RUN find target/KalamarExportPlugin-*.jar -exec mv {} KalamarExportPlugin.jar ';'
RUN rm -rf ~/.m2/repository
FROM eclipse-temurin:22-jre-alpine
WORKDIR /export
COPY --from=builder /export/KalamarExportPlugin.jar /export/
ENTRYPOINT [ "java", "-jar" ]
`

func TestJavaCIMultiWordVariable(t *testing.T) {
	pipeline := kalamarPipeline(t)
	root := gitLabJavaRepository(t, pipeline)
	writeFile(t, root, "pom.xml", strings.Replace(javaJUnit5POM, "org.junit.jupiter</groupId><artifactId>junit-jupiter", "junit</groupId><artifactId>junit", 1))
	writeFile(t, root, "Dockerfile", kalamarDockerfile)
	scope, err := DiscoverValidationScope(".", "junit4")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn --batch-mode --errors --fail-at-end --show-version clean test package", scope.Commands[0].Command)
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "security-scan / step 2 (mvn $MAVEN_CLI_OPTS clean compile): Maven goals outside the test lifecycle")
	// Moving and pushing images runs nothing.
	require.Contains(t, review, "): Runs no Maven or Gradle build or other command that can start junit4 tests")
	// The image build runs mvn clean package inside Docker, where no CI setup
	// reaches: reported, not a blocker.
	require.Contains(t, review, "build-docker / step 2")
	require.Contains(t, review, "inside the image build")
}

// Docker commands are read by the shared rules: moving images runs nothing,
// running a container may run anything, and an image build runs what its
// Dockerfile's RUN instructions run, inside the image.
func TestJavaDockerSteps(t *testing.T) {
	root := javaWorkflowFixture(t, "", map[string]string{"pom.xml": javaJUnit5POM, "Dockerfile": kalamarDockerfile, "docs/Dockerfile": "FROM nginx\nRUN echo hello\n"})
	t.Chdir(root)
	for run, want := range map[string][2]bool{
		"docker build -t app .":                       {false, true},
		"docker build -f docs/Dockerfile -t docs .":   {false, false},
		"docker push app:latest && docker tag a b":    {false, false},
		"docker run --rm maven:3 mvn test":            {true, true},
		"apk add --no-cache xz && apt-get install -y": {false, false},
	} {
		step := runtimeStep{Run: run}
		require.Equal(t, want[0], javaStepStartsTests(root, ciWorkflow{}, runtimeJob{}, step, false), run)
		require.Equal(t, want[1], javaStepStartsTests(root, ciWorkflow{}, runtimeJob{}, step, true), run)
	}
}

// javaCITestdata reads a CI file copied from a project in testdata/java-ci.
func javaCITestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "java-ci", name))
	require.NoError(t, err)
	return string(data)
}

// From cbeust/jcommander: a manually started workflow imports a GPG key and
// runs Gradle publish, with secrets and $HOME in its options. It runs no
// tests, so it is outside validation, not a blocker.
func TestJavaGradlePublishWorkflowOutside(t *testing.T) {
	publish, build := javaCITestdata(t, "jcommander-publish-maven-central.yml"), javaCITestdata(t, "jcommander-gradle.yml")
	root := t.TempDir()
	writeFile(t, root, "build.gradle.kts", "plugins { java }\ndependencies { testImplementation(\"org.testng:testng:7.0.0\") }\ntasks.test { useTestNG() }\n")
	writeFile(t, root, "gradlew", "#!/bin/sh\n")
	writeFile(t, root, ".github/workflows/publish-maven-central.yml", publish)
	writeFile(t, root, ".github/workflows/gradle.yml", build)
	t.Chdir(root)
	useCIProvider(t, "github")
	scope, err := DiscoverValidationScope(".", "testng")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./gradlew build test", scope.Commands[0].Command)
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "publish-maven-central.yml / build / step 3")
	require.Contains(t, review, "Gradle tasks outside testing")
}

// A Gradle step whose task list is literal runs no tests whatever an
// expression or a variable fills in its options, as Maven's literal goals.
func TestJavaGradleNonTestTasksWithUnreadValues(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for _, run := range []string{
		"./gradlew -Psigning.secretKeyRingFile=$HOME/.gnupg/secring.gpg -Psigning.password=\"$GPG_PASSPHRASE\" -P signing.keyId=${{ secrets.GPG_KEY_ID }} publish",
		"./gradlew assemble javadoc -Pversion=${{ github.ref_name }}",
		"./gradlew publishToSonatype closeAndReleaseSonatypeStagingRepository -PsonatypeUsername=\"$SONATYPE_USER\"",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, "%s: %s", run, resolution.Reason)
		require.Contains(t, resolution.Reason, "Gradle tasks outside testing", run)
	}
	// A value that may name a task, or a test task, keeps the step in scope.
	for _, run := range []string{
		"./gradlew ${{ matrix.task }} -Pv=$HOME",
		"./gradlew $TASKS publish",
		"./gradlew -Pkey=$SIGNING_KEY test",
		"./gradlew -Pv=${{ github.ref_name }} check",
		// An unquoted variable of the job's may split into tasks.
		"./gradlew -PsonatypeUsername=$SONATYPE_USER publish",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Review, run)
		require.NotEmpty(t, resolution.Reason, run)
	}
}

// javaMUnitGradleBuild is a Gradle build whose scommons project tests with MUnit.
func javaMUnitGradleBuild(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, "settings.gradle.kts", "rootProject.name = \"commons\"\ninclude(\"scommons\")\n")
	writeFile(t, root, "gradlew", "#!/bin/sh\n")
	writeFile(t, root, "scommons/build.gradle.kts", "plugins { scala }\ndependencies { testImplementation(\"org.scalameta:munit_3:1.3.0\") }\n")
}

// From mbari-org/commons: the tests run through gradle/gradle-build-action,
// whose arguments input is the Gradle command line.
func TestJavaGradleBuildActionArguments(t *testing.T) {
	root := t.TempDir()
	javaMUnitGradleBuild(t, root)
	writeFile(t, root, ".github/workflows/gradle.yml", javaCITestdata(t, "mbari-gradle.yml"))
	t.Chdir(root)
	useCIProvider(t, "github")
	scope, err := DiscoverValidationScope(".", "munit")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./gradlew build", scope.Commands[0].Command)
	require.Equal(t, ".", scope.Commands[0].Directory)
	require.Len(t, scope.Commands[0].Locations, 1)
	require.True(t, strings.HasPrefix(scope.Commands[0].Locations[0], ".github/workflows/gradle.yml / build / step 3 "), scope.Commands[0].Locations[0])

	// The CI check finds the step and asks for the Datadog action before it.
	ci := JavaCIConfiguration(root, "munit", "")
	require.Len(t, ci.Jobs, 1)
	require.Equal(t, "./gradlew build", ci.Jobs[0].Command)
	require.Contains(t, ci.Jobs[0].Reason, "No Datadog action selects java before this test step")
}

func TestJavaGradleActionSteps(t *testing.T) {
	root := t.TempDir()
	javaMUnitGradleBuild(t, root)
	writeFile(t, root, "nested/settings.gradle.kts", "rootProject.name = \"nested\"\n")
	writeFile(t, root, "nested/gradlew", "#!/bin/sh\n")
	writeFile(t, root, "nested/build.gradle.kts", "plugins { scala }\ndependencies { testImplementation(\"org.scalameta:munit_3:1.3.0\") }\n")
	step := func(uses string, with map[string]string) runtimeStep {
		return runtimeStep{Uses: uses, With: with}
	}
	for _, tc := range []struct {
		step      runtimeStep
		directory string
		command   string
	}{
		{step("gradle/gradle-build-action@v2", map[string]string{"arguments": "build --scan"}), ".", "./gradlew build --scan"},
		{step("gradle/gradle-build-action@v2", map[string]string{"arguments": "test", "build-root-directory": "nested"}), "nested", "./gradlew test"},
		{step("gradle/actions/setup-gradle@v3", map[string]string{"arguments": ":scommons:test\n-Pci=true"}), ".", "./gradlew :scommons:test -Pci=true"},
		{step("gradle/gradle-build-action@v2", map[string]string{"arguments": "check", "gradle-version": "wrapper"}), ".", "./gradlew check"},
	} {
		resolution := resolveJavaStep(root, ciWorkflow{Defaults: ciDefaults{Run: runDefaults{WorkingDirectory: "elsewhere"}}}, runtimeJob{}, tc.step, "munit")
		require.True(t, resolution.Matched, "%+v: %s", tc.step, resolution.Reason)
		require.Equal(t, tc.command, resolution.Tests[0].Command)
		require.Equal(t, tc.directory, resolution.Tests[0].Directory)
	}
	// Tasks that run no tests are outside scope; setup-gradle v4 no longer
	// reads arguments, so it builds nothing.
	for _, tc := range []runtimeStep{
		step("gradle/gradle-build-action@v2", map[string]string{"arguments": "assemble javadoc"}),
		step("gradle/actions/setup-gradle@v4", map[string]string{"arguments": "build"}),
	} {
		resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, tc, "munit")
		require.True(t, resolution.Review, "%+v", tc)
	}
	// A Gradle the action downloads, an expression or a release ddtest
	// can't place stays unresolved.
	for _, tc := range []runtimeStep{
		step("gradle/gradle-build-action@v2", map[string]string{"arguments": "build", "gradle-version": "8.10"}),
		step("gradle/gradle-build-action@v2", map[string]string{"arguments": "build", "gradle-executable": "tools/gradle"}),
		step("gradle/gradle-build-action@v2", map[string]string{"arguments": "${{ matrix.task }}"}),
		step("gradle/actions/setup-gradle@0123456789abcdef0123456789abcdef01234567", map[string]string{"arguments": "build"}),
		step("gradle/gradle-build-action@v2", map[string]string{"arguments": "build", "build-root-directory": "../other"}),
	} {
		resolution := resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, tc, "munit")
		require.False(t, resolution.Matched || resolution.Review, "%+v", tc)
		require.NotEmpty(t, resolution.Reason, "%+v", tc)
	}
	// Without arguments, setup-gradle only installs Gradle.
	require.Equal(t, commandResolution{}, resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, step("gradle/actions/setup-gradle@v4", nil), "munit"))
}

// A Gradle action's step runs with the workflow's, job's and step's env, as
// a run step does: validation runs its command with them.
func TestJavaGradleBuildActionEnvironment(t *testing.T) {
	root := t.TempDir()
	javaMUnitGradleBuild(t, root)
	workflow := strings.Replace(javaCITestdata(t, "mbari-gradle.yml"), "jobs:\n", "env:\n  JAVA_TOOL_OPTIONS: -Duser.timezone=UTC\njobs:\n", 1)
	workflow = strings.Replace(workflow, "    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    env:\n      TZ: UTC\n", 1)
	workflow = strings.Replace(workflow, "      with:\n        arguments: build", "      env:\n        LANG: C.UTF-8\n      with:\n        arguments: build", 1)
	writeFile(t, root, ".github/workflows/gradle.yml", workflow)
	t.Chdir(root)
	useCIProvider(t, "github")
	scope, err := DiscoverValidationScope(".", "munit")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./gradlew build", scope.Commands[0].Command)
	require.Equal(t, map[string]string{"JAVA_TOOL_OPTIONS": "-Duser.timezone=UTC", "TZ": "UTC", "LANG": "C.UTF-8"}, scope.Commands[0].Environment)
}
