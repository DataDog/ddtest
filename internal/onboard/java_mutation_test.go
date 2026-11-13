// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spring-boot-microservice-best-practices' Mutation Tests job runs its
// parameterized Gradle job with gradle_task: pitest. PIT's mutation analysis
// writes no TEST-*.xml and excludes the e2e Cucumber classes: it is a caveat,
// not a cucumber-java test configuration that validation would run.
func TestJavaGradlePitestIsAMutationCaveat(t *testing.T) {
	root := springBootRepository(t, springBootScripts)
	writeFile(t, root, "build.gradle", `plugins {
    id 'java'
    id 'info.solidsoft.pitest' version '1.15.0'
}
dependencies { testImplementation 'io.cucumber:cucumber-java:7.20.1' }
pitest {
    junit5PluginVersion = '1.2.1'
    targetClasses = ['com.uk.companieshouse.*']
    targetTests = ['com.uk.companieshouse.*']
    outputFormats = ['HTML']
    timestampedReports = false
    excludedTestClasses = ['com.uk.companieshouse.e2e.*', 'com.uk.companieshouse.CompaniesHouseApplicationTest*']
}
`)
	writeFile(t, root, circleCIFile, `version: 2.1
executors:
  base-jdk21:
    docker:
      - image: openjdk:21-slim
    environment:
      _JAVA_OPTIONS: "-Xmx3g"
      GRADLE_OPTS: "-Dorg.gradle.daemon=false -Dorg.gradle.workers.max=2"
jobs:
  test:
    executor: base-jdk21
    description: Unit & E2E Tests
    steps:
      - checkout
      - run:
          name: Run Unit and E2E tests
          command: ./gradlew test
  gradle_execute_task:
    executor: base-jdk21
    parameters:
      step_name:
        type: string
        default: executing the task
      gradle_task:
        type: string
      artifact_path:
        type: string
    steps:
      - checkout
      - run:
          name: << parameters.step_name >>
          command: ./gradlew << parameters.gradle_task >>
      - store_artifacts:
          path: << parameters.artifact_path >>
          when: always
workflows:
  workflow-all-jobs:
    jobs:
      - test:
          name: Unit & E2E Tests
      - gradle_execute_task:
          name: Mutation Tests
          step_name: Perform Mutation Testing on Unit Tests
          gradle_task: pitest
          artifact_path: build/reports/pitest
`)
	scope, err := DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./gradlew test", scope.Commands[0].Command)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "Mutation Tests / step 1 (./gradlew pitest): PIT mutation testing (pitest) runs the tests in its own processes and writes no test reports: it is not a cucumber-java test configuration, and ddtest neither validates nor instruments its tests.")
}

// PIT's Maven goals run outside the test lifecycle; next to a test phase,
// the step validates only the phase.
func TestJavaMavenPitestIsAMutationCaveat(t *testing.T) {
	root := javaWorkflowFixture(t, `jobs:
  tests:
    steps:
      - run: mvn -B verify
  mutation:
    steps:
      - run: mvn -B org.pitest:pitest-maven:mutationCoverage
  incremental:
    steps:
      - run: ./mvnw -B test org.pitest:pitest-maven:1.15.0:scmMutationCoverage
`, nil)
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	var commands []string
	for _, entry := range scope.Commands {
		commands = append(commands, entry.Command)
	}
	require.ElementsMatch(t, []string{"mvn -B verify", "./mvnw -B test"}, commands)
	caveats := strings.Join(scope.Caveats, "\n")
	require.Contains(t, caveats, "mutation / step 1 (mvn -B org.pitest:pitest-maven:mutationCoverage): PIT mutation testing (org.pitest:pitest-maven:mutationCoverage)")
	require.Contains(t, caveats, "incremental / step 1 (./mvnw -B test org.pitest:pitest-maven:1.15.0:scmMutationCoverage): PIT mutation testing (org.pitest:pitest-maven:1.15.0:scmMutationCoverage)")
	require.NotContains(t, caveats, "tests / step 1")
}
