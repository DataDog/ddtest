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

// On CircleCI the setup runs in the run step of the tests: its exports reach
// the later commands of that step only, never a later step, which an image
// without Bash would not even read through $BASH_ENV.
func TestCircleCIFallbackSetupIsComplete(t *testing.T) {
	fallback := indented(fallbackText("ruby", "1.42.0", circleSnippetForm), "            ")
	job := func(image, steps string) string {
		return "version: 2.1\njobs:\n  rspec:\n    docker:\n      - image: " + image + "\n    steps:\n      - checkout\n      - run: bundle install\n" + steps + circleWorkflow("      - rspec\n")
	}
	same := "      - run:\n          name: Tests\n          command: |\n" + fallback + "\n            bundle exec rspec\n"
	for _, image := range []string{"ruby:3.3-slim", "ruby:3.3-alpine"} {
		root := circleRubyRepository(t, job(image, same))
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), image)
		steps := circleJobs(t, root)["main/rspec"].Steps
		require.Len(t, steps, 3, "bundle install, then the setup and the tests of one run step")
		require.NotNil(t, steps[1].Setup)
		require.True(t, steps[1].Setup.shellLocal)
		require.Equal(t, "bundle exec rspec", strings.TrimSpace(steps[2].Run))
		require.Equal(t, rubyInstallerPreload, steps[2].Env["RUBYOPT"])
		require.True(t, sameShell(steps[1], steps[2]))
		require.False(t, sameShell(steps[0], steps[2]))
	}
	apart := "      - run:\n          name: Setup\n          command: |\n" + fallback + "\n      - run: bundle exec rspec\n"
	root := circleRubyRepository(t, job("ruby:3.3-slim", apart))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), "a separate step's exports never reach the tests")
	require.Empty(t, circleJobs(t, root)["main/rspec"].Steps[2].Env["RUBYOPT"])

	text := providerSetupInstructions(circleRubyRepository(t, job("ruby:3.3-slim", "      - run: bundle exec rspec\n")), circleCI{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "These jobs (.circleci/config.yml / main/rspec) run in ruby:3.3-slim, which has neither curl nor wget, so Datadog's official installer cannot run there: it is a Bash script (install_test_visibility_v15.sh) downloaded with curl or wget, and the Datadog orb also exports through $BASH_ENV, which only Bash reads.")
	require.Contains(t, text, "    command: |\n      "+fallbackComment[0]+"\n      "+fallbackComment[1]+"\n")
	require.Contains(t, text, "      <the test command>\n```")
	require.Contains(t, text, "Don't add the Datadog orb to these jobs")
	require.NotContains(t, text, circleSetupReference, "no job here can run the orb")
	require.Contains(t, text, "re-add those options right after the setup with export RUBYOPT=\"$RUBYOPT <existing options>\" in the same run step, after the setup")
	require.Contains(t, text, "Until it is available, the "+fallbackSetupName+" in those jobs logs that Test Optimization is disabled")
	require.NotContains(t, text, circleMissingKey)
}

// spring-boot-microservice-best-practices runs its Gradle tests in
// openjdk:21-slim, which has Bash but no curl: the setup downloads
// dd-java-agent with the JDK and validates like the orb would.
func TestCircleCIFallbackJavaSetup(t *testing.T) {
	root := springBootRepository(t, springBootScripts)
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
    steps:
      - checkout
      - run:
          name: Run Unit and E2E tests
          command: ./gradlew test
workflows:
  workflow-all-jobs:
    jobs:
      - test:
          name: Unit & E2E Tests
`)
	text := providerSetupInstructions(root, circleCI{}, "java", "cucumber-java", "", true)
	require.Contains(t, text, "run in openjdk:21-slim, which has neither curl nor wget")
	require.Contains(t, text, "with the job's own curl or wget (or else its JDK)")
	require.Contains(t, text, `export JAVA_TOOL_OPTIONS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${JAVA_TOOL_OPTIONS:-}"`)
	require.Contains(t, text, "The setup downloads the dd-java-agent release it names from Maven Central with curl or wget, or else with the job's JDK, checks its SHA256 and adds -javaagent for later commands of the same run step to JAVA_TOOL_OPTIONS")
	require.NotContains(t, text, "cimg/openjdk:21")
	require.NotContains(t, text, "change only the image")

	config, err := os.ReadFile(filepath.Join(root, circleCIFile))
	require.NoError(t, err)
	written := strings.Replace(string(config), "          command: ./gradlew test\n", "          command: |\n"+indented(fallbackText("java", "1.67.1", circleSnippetForm), "            ")+"\n            ./gradlew test\n", 1)
	writeFile(t, root, circleCIFile, written)
	check := JavaCIConfiguration(root, "cucumber-java", "")
	require.Len(t, check.Jobs, 1, check.Reason)
	require.Equal(t, fallbackSetupName, check.Jobs[0].Action)
	require.Equal(t, "1.67.1", check.Jobs[0].TracerRequested)
	require.Equal(t, "not checked", check.Jobs[0].Status, check.Jobs[0].Reason)
	require.True(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
	require.Equal(t, []string{"Recommended: add Bash and curl to the image of .circleci/config.yml / workflow-all-jobs/Unit & E2E Tests, then switch those jobs to Datadog's official installer; Datadog's official installation script cannot run in that image today, so they use ddtest's equivalent Datadog setup without Bash or curl. Only the user changes the image: ddtest and the agent never do."}, FallbackHumanActions(&check))
	require.Contains(t, CIProviderAPIKeyActionFor(&check, true), "the "+fallbackSetupName+" in those jobs logs that Test Optimization is disabled")
	scope, err := DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	require.Contains(t, scope.Caveats, ".circleci/config.yml / workflow-all-jobs/Unit & E2E Tests: Datadog's official installation script cannot run in image openjdk:21-slim, which has neither curl nor wget, so ddtest prescribes its equivalent Datadog setup without Bash or curl there. ddtest does not change CI images, and the agent must not either: tell the user that it's recommended that they add Bash and curl to the image themselves and then switch these jobs to Datadog's official installer, which is simpler and more reliable.")
}

// The commands before the setup in its run step share its shell: a cd and
// exports there reach the setup and the tests after it, as in one shell.
func TestCircleCIFallbackKeepsTheStepShellState(t *testing.T) {
	fallback := indented(fallbackText("ruby", "1.42.0", circleSnippetForm), "            ")
	config := "version: 2.1\njobs:\n  rspec:\n    docker:\n      - image: ruby:3.3-slim\n    steps:\n      - checkout\n      - run:\n          name: Tests\n          command: |\n            cd app\n            export SPEC_PROFILE=ci\n" + fallback + "\n            bundle exec rspec\n" + circleWorkflow("      - rspec\n")
	root := circleRubyRepository(t, config)
	writeFile(t, root, "app/Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n")
	steps := circleJobs(t, root)["main/rspec"].Steps
	require.Len(t, steps, 3, "the commands before the setup, the setup and the tests")
	require.Empty(t, steps[0].Run, "the cd and export only change the shell")
	require.NotNil(t, steps[1].Setup)
	require.Equal(t, "app", steps[1].WorkingDirectory)
	tests := steps[2]
	require.Equal(t, "bundle exec rspec", strings.TrimSpace(tests.Run))
	require.Empty(t, tests.ResolutionError)
	require.Equal(t, "app", tests.WorkingDirectory)
	require.Equal(t, "ci", tests.Env["SPEC_PROFILE"])
	require.Equal(t, rubyInstallerPreload, tests.Env["RUBYOPT"])

	// A later step starts a new shell, in the job's directory.
	root = circleRubyRepository(t, strings.Replace(config, "            bundle exec rspec\n", "            bundle exec rspec\n      - run: bundle exec rspec\n", 1))
	steps = circleJobs(t, root)["main/rspec"].Steps
	require.Len(t, steps, 4)
	require.Empty(t, steps[3].WorkingDirectory)
	require.Empty(t, steps[3].Env["SPEC_PROFILE"])
	require.Empty(t, steps[3].Env["RUBYOPT"])
}

// circleJavaFallbackConfig is spring-boot's test job in image, with steps.
func circleJavaFallbackConfig(image, steps string) string {
	return "version: 2.1\njobs:\n  test:\n    docker:\n      - image: " + image + "\n    steps:\n      - checkout\n" + steps + "workflows:\n  workflow-all-jobs:\n    jobs:\n      - test:\n          name: Unit & E2E Tests\n"
}

// The Java setup without Bash or curl exports JAVA_TOOL_OPTIONS into its own
// run step's shell only: tests in a later run step, or in a second one after
// the run step that ran it with its tests, run untraced.
func TestCircleCIFallbackJavaSetupExpiresWithItsShell(t *testing.T) {
	fallback := indented(fallbackText("java", "1.67.1", circleSnippetForm), "            ")
	for name, steps := range map[string]string{
		"separate step": "      - run:\n          name: Setup\n          command: |\n" + fallback + "\n      - run: ./gradlew test\n",
		"second tests":  "      - run:\n          name: Unit\n          command: |\n" + fallback + "\n            ./gradlew test\n      - run: ./gradlew test -Pe2e\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := springBootRepository(t, springBootScripts)
			writeFile(t, root, circleCIFile, circleJavaFallbackConfig("openjdk:21-slim", steps))
			check := JavaCIConfiguration(root, "cucumber-java", "")
			last := check.Jobs[len(check.Jobs)-1]
			require.Equal(t, "inconclusive", last.Status, last.Reason)
			require.Contains(t, last.Reason, "runs in another shell than this test command")
			require.False(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
		})
	}
}

// The orb exports through $BASH_ENV, which every later Bash step reads: its
// setup reaches tests in any later step.
func TestCircleCIOrbJavaSetupReachesLaterSteps(t *testing.T) {
	root := springBootRepository(t, springBootScripts)
	writeFile(t, root, circleCIFile, "version: 2.1\norbs:\n  test-optimization: "+circleSetupReference+"\njobs:\n  test:\n    docker:\n      - image: cimg/openjdk:21.0\n    steps:\n      - checkout\n      - test-optimization/autoinstrument:\n          languages: java\n          java_tracer_version: 1.67.1\n      - run: ./gradlew test\n      - run: ./gradlew test -Pe2e\nworkflows:\n  workflow-all-jobs:\n    jobs:\n      - test:\n          name: Unit & E2E Tests\n")
	check := JavaCIConfiguration(root, "cucumber-java", "")
	require.Len(t, check.Jobs, 2, check.Reason)
	for _, job := range check.Jobs {
		require.Equal(t, "not checked", job.Status, job.Reason)
	}
	require.True(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""))
}

// Without curl or wget, the setup downloads dd-java-agent by running a Java
// source file, which only a JDK 11 or later launches: one already in an
// older JDK's -slim image, or a JRE's, never installs the agent.
func TestCircleCIFallbackJavaSetupNeedsJDK11(t *testing.T) {
	fallback := indented(fallbackText("java", "1.67.1", circleSnippetForm), "            ")
	steps := "      - run:\n          name: Run Unit and E2E tests\n          command: |\n" + fallback + "\n            ./gradlew test\n"
	for image, reason := range map[string]string{
		"openjdk:8-jdk-slim":  "openjdk:8-jdk-slim has JDK 8",
		"openjdk:11-jre-slim": "has only a Java runtime (JRE)",
	} {
		root := springBootRepository(t, springBootScripts)
		writeFile(t, root, circleCIFile, circleJavaFallbackConfig(image, steps))
		check := JavaCIConfiguration(root, "cucumber-java", "")
		require.Len(t, check.Jobs, 1, check.Reason)
		require.NotEqual(t, "not checked", check.Jobs[0].Status, image)
		require.Contains(t, check.Jobs[0].Reason, reason, image)
		require.Contains(t, check.Jobs[0].Reason, fallbackUserRecommendation, image)
		require.False(t, javaDiscovery{}.ciSetupConfigured(root, "cucumber-java", ""), image)
	}
}
