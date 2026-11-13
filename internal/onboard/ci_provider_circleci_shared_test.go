// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// springBootSharedJob is spring-boot-microservice-best-practices' CircleCI
// configuration at 896d6f72df3bfae9cd0e0104adaee50c7efa4a87, whose
// parameterized gradle_execute_task job runs the Acceptance Tests
// (gradle_task: test -Pe2e) and, for other invocations, Jacoco
// (clean jacocoTestCoverageVerification) and PIT (pitest).
func springBootSharedJob(t *testing.T) (root, config string) {
	t.Helper()
	data, err := os.ReadFile("testdata/spring-boot-circleci.yml")
	require.NoError(t, err)
	root = springBootRepository(t, springBootScripts)
	writeFile(t, root, circleCIFile, string(data))
	return root, string(data)
}

// The edits a person makes to the configuration, each one exactly once.
func editCircleConfig(t *testing.T, config string, edits ...[2]string) string {
	t.Helper()
	for _, edit := range edits {
		require.Equal(t, 1, strings.Count(config, edit[0]), edit[0])
		config = strings.Replace(config, edit[0], edit[1], 1)
	}
	return config
}

const (
	springBootTestStep     = "          name: Run Unit and E2E tests\n          command: ./gradlew test\n"
	springBootSharedStep   = "          name: << parameters.step_name >>\n          command: ./gradlew << parameters.gradle_task >>\n"
	springBootParameters   = "      artifact_path:\n        type: string\n    steps:\n"
	springBootAcceptance   = "          name: Acceptance Tests\n"
	springBootWithGuard    = "      artifact_path:\n        type: string\n      datadog_test_optimization:\n        type: boolean\n        default: false\n    steps:\n"
	springBootAcceptanceOn = "          name: Acceptance Tests\n          datadog_test_optimization: true\n"
)

// springBootJobs are the jobs of the configuration, as the CI check reads
// them: the setup's step in each, and the environment of the test command.
func springBootJobs(t *testing.T, root string) map[string]runtimeJob {
	t.Helper()
	jobs := circleJobs(t, root)
	for _, name := range []string{"workflow-all-jobs/Acceptance Tests", "workflow-all-jobs/Jacoco Code Coverage", "workflow-all-jobs/Mutation Tests"} {
		require.Contains(t, jobs, name)
	}
	return jobs
}

func circleSetupSteps(job runtimeJob) int {
	count := 0
	for _, step := range job.Steps {
		if step.Setup != nil {
			count++
		}
	}
	return count
}

// In openjdk:21-slim, which has no curl, the Acceptance Tests get the setup
// without Bash or curl in their run step. That step belongs to
// gradle_execute_task, which Jacoco and PIT also run: ddtest prescribes a
// job parameter that enables the setup only for the Acceptance Tests, reads
// it per invocation, and reports a setup that reaches Jacoco and PIT.
func TestCircleCISharedJobFallbackRunsOnlyForTests(t *testing.T) {
	root, config := springBootSharedJob(t)
	text := providerSetupInstructions(root, circleCI{}, "java", "cucumber-java", "", true)
	require.Contains(t, text, "4. The workflows also run the job gradle_execute_task, whose steps include the test step of workflow-all-jobs/Acceptance Tests, for invocations that run no cucumber-java tests (workflow-all-jobs/Jacoco Code Coverage, workflow-all-jobs/Mutation Tests)")
	require.Contains(t, text, "declare a boolean parameter datadog_test_optimization (default: false) in the job gradle_execute_task, wrap the "+fallbackSetupName+" inside its run step in "+circleSetupGuard+" … fi")
	require.Contains(t, text, "jobs:\n  gradle_execute_task:\n    parameters:\n      datadog_test_optimization:\n        type: boolean\n        default: false\n    steps:\n      - run:\n          name: <the step's name>\n          command: |\n            "+circleSetupGuard+"\n              "+fallbackComment[0]+"\n")
	require.Contains(t, text, "            fi\n            <the test command>\nworkflows:\n  workflow-all-jobs:\n    jobs:\n      - gradle_execute_task:\n          name: Acceptance Tests\n          datadog_test_optimization: true\n```")

	snippet := fallbackText("java", "1.67.1", circleSnippetForm)
	tests := [2]string{springBootTestStep, "          name: Run Unit and E2E tests\n          command: |\n" + indented(snippet, "            ") + "\n            ./gradlew test\n"}

	// The setup as the job's other instructions show it, without the
	// parameter, reaches Jacoco and PIT.
	writeFile(t, root, circleCIFile, editCircleConfig(t, config, tests, [2]string{springBootSharedStep, "          name: << parameters.step_name >>\n          command: |\n" + indented(snippet, "            ") + "\n            ./gradlew << parameters.gradle_task >>\n"}))
	check := JavaCIConfiguration(root, "cucumber-java", "")
	var leaks []string
	for _, job := range check.Jobs {
		if job.Code == CodeIncompleteCISetup {
			leaks = append(leaks, job.Job)
			require.Contains(t, job.Reason, "This CircleCI job runs no cucumber-java tests, but it runs the "+fallbackSetupName+" written in the job gradle_execute_task, which workflow-all-jobs/Acceptance Tests also run before their tests")
			require.Contains(t, job.Reason, "set datadog_test_optimization: true only on the workflow entries of workflow-all-jobs/Acceptance Tests.")
		}
	}
	require.Equal(t, []string{"workflow-all-jobs/Jacoco Code Coverage", "workflow-all-jobs/Mutation Tests"}, leaks)
	require.False(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))

	// With the parameter, only the Acceptance Tests run it.
	guard := indented(circleSetupGuard+"\n"+indented(snippet, "  ")+"\nfi", "            ")
	writeFile(t, root, circleCIFile, editCircleConfig(t, config, tests,
		[2]string{springBootSharedStep, "          name: << parameters.step_name >>\n          command: |\n" + guard + "\n            ./gradlew << parameters.gradle_task >>\n"},
		[2]string{springBootParameters, springBootWithGuard},
		[2]string{springBootAcceptance, springBootAcceptanceOn}))
	jobs := springBootJobs(t, root)
	acceptance := jobs["workflow-all-jobs/Acceptance Tests"]
	require.Equal(t, 1, circleSetupSteps(acceptance))
	require.Equal(t, "./gradlew test -Pe2e", acceptance.Steps[len(acceptance.Steps)-1].Run)
	require.Contains(t, acceptance.Steps[len(acceptance.Steps)-1].Env["JAVA_TOOL_OPTIONS"], "-javaagent:")
	for _, name := range []string{"workflow-all-jobs/Jacoco Code Coverage", "workflow-all-jobs/Mutation Tests"} {
		require.Zero(t, circleSetupSteps(jobs[name]), name)
		require.Len(t, jobs[name].Steps, 1, name)
		require.Empty(t, jobs[name].Steps[0].Env["JAVA_TOOL_OPTIONS"], name)
	}
	require.Equal(t, "./gradlew pitest", jobs["workflow-all-jobs/Mutation Tests"].Steps[0].Run)
	check = JavaCIConfiguration(root, "cucumber-java", "")
	var instrumented []string
	for _, job := range check.Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
		require.Equal(t, fallbackSetupName, job.Action)
		instrumented = append(instrumented, job.Job)
	}
	require.Equal(t, []string{"workflow-all-jobs/Acceptance Tests", "workflow-all-jobs/Unit & E2E Tests"}, instrumented)
	require.True(t, slices.ContainsFunc(check.Review, func(finding RuntimeFinding) bool {
		return finding.Job == "workflow-all-jobs/Mutation Tests" && strings.Contains(finding.Reason, "CI job without the Datadog setup that never runs cucumber-java")
	}))
	require.True(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
	scope, err := DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	require.True(t, slices.ContainsFunc(scope.Caveats, func(caveat string) bool {
		return strings.HasPrefix(caveat, ".circleci/config.yml / workflow-all-jobs/Mutation Tests / step 1 (./gradlew pitest")
	}), "PIT stays a caveat: ddtest neither validates nor instruments it")
}

// In an image with curl the Acceptance Tests get the Datadog orb's step,
// which a when step on the same parameter keeps out of Jacoco and PIT.
func TestCircleCISharedJobOrbRunsOnlyForTests(t *testing.T) {
	root, config := springBootSharedJob(t)
	image := [2]string{"  base-jdk21:\n    docker:\n      - image: openjdk:21-slim\n", "  base-jdk21:\n    docker:\n      - image: cimg/openjdk:21.0\n"}
	config = editCircleConfig(t, config, image)
	writeFile(t, root, circleCIFile, config)
	text := providerSetupInstructions(root, circleCI{}, "java", "cucumber-java", "", true)
	require.NotContains(t, text, fallbackComment[0])
	require.Contains(t, text, "5. The workflows also run the job gradle_execute_task, whose steps include the test step of workflow-all-jobs/Acceptance Tests")
	require.Contains(t, text, "wrap the Datadog orb step (and the step after it that appends to $BASH_ENV, if any) in a when step on << parameters.datadog_test_optimization >>")
	require.Contains(t, text, "    steps:\n      - when:\n          condition: << parameters.datadog_test_optimization >>\n          steps:\n            - test-optimization-circleci-orb/autoinstrument:\n                languages: java\n                site: datadoghq.com\n")
	require.Contains(t, text, "workflows:\n  workflow-all-jobs:\n    jobs:\n      - gradle_execute_task:\n          name: Acceptance Tests\n          datadog_test_optimization: true\n```")

	orbs := [2]string{"orbs:\n  snyk: snyk/snyk@2.0.2\n", "orbs:\n  snyk: snyk/snyk@2.0.2\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n"}
	orb := func(indent string) string {
		return indented("- test-optimization-circleci-orb/autoinstrument:\n    languages: java\n    site: datadoghq.com\n    java_tracer_version: \"1.67.1\"", indent) + "\n"
	}
	tests := [2]string{"      - run:\n" + springBootTestStep, orb("      ") + "      - run:\n" + springBootTestStep}
	shared := "      - *restore_gradle_cache\n      - run:\n" + springBootSharedStep

	// Without the parameter, the orb step reaches Jacoco and PIT.
	writeFile(t, root, circleCIFile, editCircleConfig(t, config, orbs, tests, [2]string{shared, "      - *restore_gradle_cache\n" + orb("      ") + "      - run:\n" + springBootSharedStep}))
	check := JavaCIConfiguration(root, "cucumber-java", "")
	var leaks []string
	for _, job := range check.Jobs {
		if job.Code == CodeIncompleteCISetup {
			leaks = append(leaks, job.Job)
			require.Contains(t, job.Reason, "runs the Datadog orb step written in the job gradle_execute_task")
			require.Contains(t, job.Reason, "in a when step on << parameters.datadog_test_optimization >>")
		}
	}
	require.Equal(t, []string{"workflow-all-jobs/Jacoco Code Coverage", "workflow-all-jobs/Mutation Tests"}, leaks)
	require.False(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))

	// With it, only the Acceptance Tests run the orb step.
	when := "      - when:\n          condition: << parameters.datadog_test_optimization >>\n          steps:\n" + orb("            ")
	writeFile(t, root, circleCIFile, editCircleConfig(t, config, orbs, tests,
		[2]string{shared, "      - *restore_gradle_cache\n" + when + "      - run:\n" + springBootSharedStep},
		[2]string{springBootParameters, springBootWithGuard},
		[2]string{springBootAcceptance, springBootAcceptanceOn}))
	jobs := springBootJobs(t, root)
	require.Equal(t, 1, circleSetupSteps(jobs["workflow-all-jobs/Acceptance Tests"]))
	require.Empty(t, jobs["workflow-all-jobs/Acceptance Tests"].Steps[0].Setup.Problem)
	for _, name := range []string{"workflow-all-jobs/Jacoco Code Coverage", "workflow-all-jobs/Mutation Tests"} {
		require.Zero(t, circleSetupSteps(jobs[name]), name)
		require.Empty(t, jobs[name].Steps[0].Env["JAVA_TOOL_OPTIONS"], name)
	}
	check = JavaCIConfiguration(root, "cucumber-java", "")
	var instrumented []string
	for _, job := range check.Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
		require.Equal(t, "datadog/test-optimization-circleci-orb@2", job.Action)
		instrumented = append(instrumented, job.Job)
	}
	require.Equal(t, []string{"workflow-all-jobs/Acceptance Tests", "workflow-all-jobs/Unit & E2E Tests"}, instrumented)
	require.True(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
}

// The guard is read only around the setup ddtest writes, on values CircleCI
// wrote into the command: a guard reading a variable, or holding other
// commands, is not the setup's.
func TestCircleCIGuardedFallbackShape(t *testing.T) {
	snippet := fallbackText("java", "1.67.1", circleSnippetForm)
	pieces := func(command string) []circleRawStep {
		return circleFallbackPieces(circleRawStep{run: command, shell: "bash"})
	}
	on := pieces("./gradlew clean\nif [ \"true\" = true ]; then\n" + snippet + "\nfi\n./gradlew test\n")
	require.Len(t, on, 3)
	require.Equal(t, "./gradlew clean", on[0].run)
	require.NotNil(t, on[1].setup)
	require.True(t, on[1].setup.shaped)
	require.Empty(t, on[1].setup.Problem)
	require.Equal(t, "./gradlew test", on[2].run)

	off := pieces("if [ \"false\" = true ]; then\n" + snippet + "\nfi\n./gradlew pitest\n")
	require.Len(t, off, 1)
	require.Nil(t, off[0].setup)
	require.Equal(t, "./gradlew pitest", off[0].run)

	for _, command := range []string{
		"if [ \"$RUN_DATADOG\" = true ]; then\n" + snippet + "\nfi\n./gradlew test\n",
		"if [ \"true\" = true ]; then\n" + snippet + "\n./gradlew test\nfi\n",
		"if [ \"true\" = true ]; then\n" + snippet + "\nelse\n  echo skipped\nfi\n./gradlew test\n",
	} {
		result := pieces(command)
		require.Len(t, result, 1, command)
		require.Nil(t, result[0].setup, command)
	}
}
