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

// spring-boot-microservice-best-practices' scheduled Image Vulnerability
// Check job runs its scan script by the CircleCI checkout's absolute path,
// /root/project in a root image: the script is read as
// ./scripts/vulnerability-checks.sh is, and starts no Gradle build.
func TestJavaRepositoryScriptsByCheckoutPath(t *testing.T) {
	circle := func(executor, directory, command string) string {
		job := "  scan:\n    executor: " + executor + "\n"
		if directory != "" {
			job += "    working_directory: " + directory + "\n"
		}
		return "version: 2.1\nexecutors:\n  base-jdk21:\n    docker:\n      - image: openjdk:21-slim\n  trivy:\n    docker:\n      - image: aquasec/trivy:latest\n  cimg-jdk21:\n    docker:\n      - image: cimg/openjdk:21.0.0\n" +
			"jobs:\n  test:\n    executor: base-jdk21\n    steps:\n      - checkout\n      - run: ./gradlew test\n" + job +
			"    steps:\n      - checkout\n      - run:\n          name: Trivy Image Vulnerability Check\n          command: |\n" + command +
			"workflows:\n  scheduled-vulnerability-check:\n    jobs:\n      - test\n      - scan:\n          name: Image Vulnerability Check\n"
	}
	e15 := "            apk add --no-cache bash\n            /root/project/scripts/vulnerability-checks.sh\n"
	scan := map[string]string{"scripts/scan.sh": "#!/usr/bin/env bash\ntrivy image app:latest\n"}
	for _, tc := range []struct {
		name, provider, file, config string
		read                         bool
		scripts                      map[string]string
	}{
		{"CircleCI root image", "circleci", circleCIFile, circle("trivy", "", e15), true, nil},
		{"CircleCI home", "circleci", circleCIFile, circle("trivy", "", "            ~/project/scripts/vulnerability-checks.sh\n"), true, nil},
		{"CircleCI cimg image", "circleci", circleCIFile, circle("cimg-jdk21", "", "            /home/circleci/project/scripts/vulnerability-checks.sh\n"), true, nil},
		{"CircleCI absolute working directory", "circleci", circleCIFile, circle("trivy", "/src", "            bash /src/scripts/vulnerability-checks.sh\n"), true, nil},
		// The step runs in performance-testing, where the script is
		// ../scripts/scan.sh.
		{"CircleCI subdirectory step", "circleci", circleCIFile, strings.Replace(circle("trivy", "", "            /root/project/scripts/scan.sh\n"), "          command: |", "          working_directory: ~/project/performance-testing\n          command: |", 1), true, scan},
		{"GitLab", "gitlab", gitLabCIFile, "test:\n  script:\n    - ./gradlew test\nscan:\n  script:\n    - apk add --no-cache bash\n    - $CI_PROJECT_DIR/scripts/vulnerability-checks.sh\n    - bash \"${CI_PROJECT_DIR}/scripts/vulnerability-checks.sh\"\n", true, nil},
		{"GitHub variable", "github", ".github/workflows/ci.yml", "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ./gradlew test\n  scan:\n    runs-on: ubuntu-latest\n    steps:\n      - run: $GITHUB_WORKSPACE/scripts/vulnerability-checks.sh\n", true, nil},
		{"GitHub expression", "github", ".github/workflows/ci.yml", "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ./gradlew test\n  scan:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ${{ github.workspace }}/scripts/vulnerability-checks.sh\n", true, nil},
		{"Buildkite", "buildkite", ".buildkite/pipeline.yml", "steps:\n  - label: test\n    command: ./gradlew test\n  - label: scan\n    command: $$BUILDKITE_BUILD_CHECKOUT_PATH/scripts/vulnerability-checks.sh\n", true, nil},
		{"Jenkins", "jenkins", "Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('test') { steps { sh './gradlew test' } }\n    stage('scan') { steps { sh '$WORKSPACE/scripts/vulnerability-checks.sh' } }\n  }\n}\n", true, nil},
		// A script named by the checkout variable that runs Gradle's tests
		// stays a possible test step, as ./scripts/e2e.sh does.
		{"GitHub variable, script that may run tests", "github", ".github/workflows/ci.yml", "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ./gradlew test\n  scan:\n    runs-on: ubuntu-latest\n    steps:\n      - run: $GITHUB_WORKSPACE/scripts/e2e.sh\n", false, map[string]string{"scripts/e2e.sh": "#!/bin/bash\n./gradlew test -Pe2e\n"}},
		{"Jenkins, script that may run tests", "jenkins", "Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('test') { steps { sh './gradlew test' } }\n    stage('scan') { steps { sh '$WORKSPACE/scripts/e2e.sh' } }\n  }\n}\n", false, map[string]string{"scripts/e2e.sh": "#!/bin/bash\n./gradlew test -Pe2e\n"}},
		{"Buildkite, script that may run tests", "buildkite", ".buildkite/pipeline.yml", "steps:\n  - label: test\n    command: ./gradlew test\n  - label: scan\n    command: $${BUILDKITE_BUILD_CHECKOUT_PATH}/scripts/e2e.sh\n", false, map[string]string{"scripts/e2e.sh": "#!/bin/bash\n./gradlew test -Pe2e\n"}},
		// The checkout of a cimg image is in /home/circleci; GitLab's
		// /builds path depends on the runner and the project.
		{"CircleCI cimg image, root path", "circleci", circleCIFile, circle("cimg-jdk21", "", e15), false, nil},
		{"GitLab builds directory", "gitlab", gitLabCIFile, "test:\n  script:\n    - ./gradlew test\nscan:\n  script:\n    - /builds/team/app/scripts/vulnerability-checks.sh\n", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := springBootRepository(t, springBootScripts)
			for path, text := range tc.scripts {
				writeFile(t, root, path, text)
			}
			writeFile(t, root, tc.file, tc.config)
			useCIProvider(t, tc.provider)
			scope, err := DiscoverValidationScope(root, "cucumber-java")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "./gradlew test", scope.Commands[0].Command)
			unresolved := strings.Join(scope.Unresolved, "\n")
			if tc.read {
				require.Empty(t, unresolved)
				return
			}
			require.Contains(t, unresolved, "Repository script may run cucumber-java tests")
		})
	}
}

func TestRepositoryRelativeCommands(t *testing.T) {
	checkout := []string{"$CI_PROJECT_DIR", "${CI_PROJECT_DIR}", "/root/project"}
	for text, want := range map[string]string{
		"$CI_PROJECT_DIR/scripts/a.sh":                     "./scripts/a.sh",
		`bash "${CI_PROJECT_DIR}/scripts/a.sh" x`:          `bash "./scripts/a.sh" x`,
		"cd $CI_PROJECT_DIR && /root/project/scripts/a.sh": "cd . && ./scripts/a.sh",
		"$CI_PROJECT_DIRECTORY/scripts/a.sh":               "$CI_PROJECT_DIRECTORY/scripts/a.sh",
		"/root/projects/a.sh /tmp/root/project/a.sh":       "/root/projects/a.sh /tmp/root/project/a.sh",
		"${CI_PROJECT_DIR:-/src}/a.sh":                     "${CI_PROJECT_DIR:-/src}/a.sh",
		"SCRIPT=$CI_PROJECT_DIR/a.sh $CI_PROJECT_DIR/b.sh": "SCRIPT=./a.sh ./b.sh",
	} {
		require.Equal(t, want, repositoryRelativeCommands(text, checkout, "."), text)
	}
	require.Equal(t, "../../scripts/a.sh", repositoryRelativeCommands("/root/project/scripts/a.sh", checkout, "services/api"))
	require.Equal(t, "/root/project/scripts/a.sh", repositoryRelativeCommands("/root/project/scripts/a.sh", nil, "."))
}
