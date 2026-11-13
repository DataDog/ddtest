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

// A command written as a block (command: |) ends with a line break, which a
// step's label leaves out: step 1 (./gradlew build -x test), not a label
// split over two lines.
func TestStepLabelsLeaveOutTheBlockLineBreak(t *testing.T) {
	require.Equal(t, "ci.yml / test / step 2 (./gradlew pitest)", commandLocation{Workflow: "ci.yml", Job: "test", Step: 2, Command: "./gradlew pitest\n"}.String())
	require.Equal(t, "echo a\necho b", StepCommandLabel("echo a\necho b\n\n"))

	root, config := springBootSharedJob(t)
	scope, err := DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	require.Contains(t, scope.Review, ".circleci/config.yml / workflow-all-jobs/Build / step 1 (./gradlew build -x test): Gradle step excludes the test tasks it requests, so it is outside cucumber-java test scope: ./gradlew build -x test\n")

	// The commands after the setup without Bash or curl in a block, too, as
	// in the job a coding agent edited.
	writeFile(t, root, circleCIFile, editCircleConfig(t, config, [2]string{springBootSharedStep, "          name: << parameters.step_name >>\n          command: |\n" + indented(fallbackText("java", "1.67.1", circleSnippetForm), "            ") + "\n            ./gradlew << parameters.gradle_task >>\n"}))
	scope, err = DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	for _, line := range append(append(scope.Caveats, scope.Review...), scope.Unresolved...) {
		require.NotContains(t, line, "\n)", line)
	}
	require.Contains(t, scope.Caveats, ".circleci/config.yml / workflow-all-jobs/Mutation Tests / step 2 (./gradlew pitest): "+javaMutationCaveat("./gradlew pitest", "cucumber-java"))
	require.True(t, strings.Contains(strings.Join(scope.Review, "\n"), "workflow-all-jobs/Jacoco Code Coverage / step 2 (./gradlew clean jacocoTestCoverageVerification): "))
}
