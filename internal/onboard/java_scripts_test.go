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

// Trimmed from spring-boot-microservice-best-practices (CircleCI,
// cucumber-java): the deploy, scan and health-check jobs run repository
// scripts that build and push images, scan them, deploy with Helm and probe
// the service; none starts Gradle.
var springBootScripts = map[string]string{
	"scripts/utils.sh": "#!/usr/bin/env bash\nRED='\\033[0;31m'\nprintMessage() {\n  printf \"$1$1$1 $2 $1$1$1 - $3\\n\"\n}\nprintEmojiWrappedMessage() {\n  for i in {1..150}; do\n    printf \"$1\"\n  done\n  printf \"\\n$2\\n\"\n}\n",
	"scripts/docker-build.sh": `#!/usr/bin/env bash
set -euo pipefail
source ./scripts/utils.sh

lint_docker_file() {
  printMessage "$HEART_ICON" "$HEART_ICON" "Starting hadolinting: $1"
  hadolint $1
}

build_image() {
  TAG=$([ "${CIRCLE_BRANCH}" == "main" ] && echo "0.1.${CIRCLE_BUILD_NUM}" || echo "${CIRCLE_BRANCH}" | sed 's/dependabot\/gradle//g;s/.//')
  echo "${TAG}" > version/docker-version.txt
  echo "${DOCKER_PASS}" | docker login --username "${DOCKER_USER}" --password-stdin
  docker build -t "${DOCKER_USER}/${DOCKER_IMAGE}:${TAG}" -f ci.Dockerfile .
}
push_image() {
  TAG=$(cat version/docker-version.txt)
  if [ "${CIRCLE_BRANCH}" == "main" ]; then
    docker tag "${DOCKER_USER}/${DOCKER_IMAGE}:${TAG}" "${DOCKER_USER}/${DOCKER_IMAGE}:latest"
    docker push "${DOCKER_USER}/${DOCKER_IMAGE}:${TAG}"
  else
    printEmojiWrappedMessage "${GREEN}${HEART_ICON}" "Skipping push docker image for non main branch."
  fi
}
"$@"
`,
	"scripts/health-check.sh": `#!/usr/bin/env bash
source ./scripts/utils.sh

response=$(curl -s -X GET -H "Header:Value" ${HEALTH_ENDPOINT})
response_status=$(jq -r '.status' <<<"${response}")
if [ "${response_status}" != "UP" ]; then
  printEmojiWrappedMessage "${RED}${SKULL_ICON}" "Health Check failed, the service isn't available please analyse further."
  exit 1
fi
`,
	"scripts/helm-deploy.sh": `#!/usr/bin/env bash
source ./scripts/utils.sh

set -euo pipefail

: "${EKS_NAMESPACE}"
VERSION=latest
helm upgrade \
--install \
--wait \
--namespace "${EKS_NAMESPACE}" \
"${RELEASE_NAME}" ./kubernetes/helm-chart
`,
	"scripts/vulnerability-checks.sh": `#!/usr/bin/env bash
source ./scripts/utils.sh
ALLOWED_CRITICAL_HIGH_VULNERABILITIES="${ALLOWED_CRITICAL_HIGH_VULNERABILITIES:-0,0}"
IFS=',' read -ra my_array <<< "$ALLOWED_CRITICAL_HIGH_VULNERABILITIES"
mkdir reports
time_stamp="$(date +'%d-%m-%Y-%r')"
trivy image "${DOCKER_USER}/${DOCKER_IMAGE}:${TAG}" > reports/"vulnerability-report-${time_stamp}".txt
scan_status=$(grep -m 1 Total: reports/"vulnerability-report-${time_stamp}".txt)
high_vulnerability=$(echo "$scan_status" | grep -oE 'HIGH: [0-9]+' | awk '{print $2}')
if [[ $high_vulnerability == $ALLOWED_HIGH_VULNERABILITIES ]]; then
  echo "allowed"
else
  exit 1
fi
`,
	"ci.Dockerfile": "FROM openjdk:21-slim\n\nRUN apt-get update && apt-get upgrade -y && apt-get clean && rm -rf /var/lib/apt/lists/*\n\nWORKDIR /opt\nCOPY ${APPJAR} companieshouse-*.jar\nRUN groupadd --gid 10001 rungroup && useradd --uid 10001 --gid 10001 runuser\n",
}

const springBootCircleCI = `version: 2.1
executors:
  base-jdk21:
    docker:
      - image: openjdk:21-slim
  docker-git:
    docker:
      - image: abhisheksr01/dind-utils:latest
  helm:
    docker:
      - image: abhisheksr01/eks-helm:latest
  alpine:
    docker:
      - image: alpine:latest
  loadimpact-k6:
    docker:
      - image: loadimpact/k6
  owasp-zap:
    docker:
      - image: owasp/zap2docker-stable:latest
jobs:
  test:
    executor: base-jdk21
    steps:
      - checkout
      - run:
          name: Run Unit and E2E tests
          command: ./gradlew test
  docker_lint_build_scan_push:
    executor: docker-git
    steps:
      - checkout
      - setup_remote_docker
      - run:
          name: Lint Docker file
          command: |
            ./scripts/docker-build.sh lint_docker_file "ci.Dockerfile"
      - run:
          name: Build Image
          command: |
            ./scripts/docker-build.sh build_image
      - run:
          name: Scan Image Vulnerability
          command: ./scripts/vulnerability-checks.sh
      - run:
          name: Push Image
          command: ./scripts/docker-build.sh push_image
  helm_deploy:
    executor: helm
    parameters:
      release:
        type: string
    steps:
      - checkout
      - run:
          name: helm release
          command: |
            RELEASE_NAME="<< parameters.release >>" \
            EKS_NAMESPACE="${EKS_NAMESPACE}" \
            ./scripts/helm-deploy.sh
  health_check:
    executor: alpine
    steps:
      - checkout
      - run:
          name: Perform API Health Check
          command: ./scripts/health-check.sh
  penetration_test:
    executor: owasp-zap
    steps:
      - run:
          name: Perform Penetration Test
          command: |
            cd ../../../zap/
            mkdir wrk && zap-baseline.py -t "${HEALTH_ENDPOINT}" -g gen.conf -r penetration-test-report.html || :
            mv wrk/* ../home/zap/project/
  load_performance_test:
    executor: loadimpact-k6
    steps:
      - checkout
      - run:
          name: Load Performance Test - K6
          working_directory: ~/project/performance-testing
          command: |
            echo "Starting load performance test"
            k6 run -e MY_HOSTNAME=${defaultUrl} -u 50 -d 10s script.js > load_performance_test.txt
            echo "Completed load performance test"
workflows:
  workflow-all-jobs:
    jobs:
      - test:
          name: Unit & E2E Tests
      - docker_lint_build_scan_push:
          name: Docker Lint Build Scan Push
      - helm_deploy:
          name: Deploy app to K8S
          release: companieshouse-release
      - health_check:
          name: Health Check
      - penetration_test:
          name: Penetration Test
      - load_performance_test:
          name: Load Performance Test
`

func springBootRepository(t *testing.T, scripts map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "settings.gradle", "rootProject.name = 'companieshouse'\n")
	writeFile(t, root, "build.gradle", "dependencies { testImplementation 'io.cucumber:cucumber-java:7.20.1' }\n")
	writeFile(t, root, "gradlew", "#!/bin/sh\n")
	writeFile(t, root, "performance-testing/script.js", "export default function () {}\n")
	writeFile(t, root, circleCIFile, springBootCircleCI)
	for path, text := range scripts {
		writeFile(t, root, path, text)
	}
	t.Chdir(root)
	useCIProvider(t, "circleci")
	return root
}

// Repository scripts are read: one that builds, scans, deploys or probes and
// starts no build tool is not a possible test step. In its uninstrumented job
// it stays a review item naming the command; the k6 and ZAP jobs, whose
// commands launch no build, are caveats.
func TestJavaRepositoryScriptsThatStartNoBuildAreNotTests(t *testing.T) {
	root := springBootRepository(t, springBootScripts)
	scope, err := DiscoverValidationScope(root, "cucumber-java")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "./gradlew test", scope.Commands[0].Command)
	review := strings.Join(scope.Review, "\n")
	for _, step := range []string{"./scripts/docker-build.sh build_image", "./scripts/vulnerability-checks.sh", "./scripts/docker-build.sh push_image", "./scripts/health-check.sh", "./scripts/helm-deploy.sh", "k6 run", "zap-baseline.py"} {
		require.Contains(t, review, step)
	}
	require.Contains(t, review, "Uninstrumented CI job with no recognized Maven or Gradle test step")
	caveats := strings.Join(scope.Caveats, "\n")
	require.Contains(t, caveats, "Load Performance Test: CI job without the Datadog setup that never runs cucumber-java")
	require.Contains(t, caveats, "Penetration Test: CI job without the Datadog setup that never runs cucumber-java")
}

// A script that starts Gradle's tests, directly, through a function it
// dispatches to, a script it sources or runs, a container or a launcher,
// stays a possible test step; so does one ddtest can't read.
func TestJavaRepositoryScriptsThatMayStartTestsStayInScope(t *testing.T) {
	for name, scripts := range map[string]map[string]string{
		"gradle test":          {"scripts/health-check.sh": "#!/bin/bash\n./gradlew test -Pe2e\n"},
		"dispatched function":  {"scripts/docker-build.sh": "#!/bin/bash\nbuild_image() {\n  ./gradlew check\n}\n\"$@\"\n"},
		"sourced script":       {"scripts/utils.sh": "./gradlew test\n"},
		"nested script":        {"scripts/health-check.sh": "#!/bin/bash\n./scripts/e2e.sh\n", "scripts/e2e.sh": "mvn verify\n"},
		"unreadable script":    {"scripts/health-check.sh": "#!/bin/bash\n./scripts/missing.sh\n"},
		"container run":        {"scripts/health-check.sh": "#!/bin/bash\ndocker run --rm app ./gradlew test\n"},
		"Dockerfile RUN":       {"ci.Dockerfile": "FROM gradle:8-jdk21\nRUN gradle test\n"},
		"package script":       {"scripts/health-check.sh": "#!/bin/bash\nnpm test\n"},
		"computed command":     {"scripts/health-check.sh": "#!/bin/bash\n$CHECK\n"},
		"eval":                 {"scripts/health-check.sh": "#!/bin/bash\neval \"$CHECK\"\n"},
		"directory change":     {"scripts/health-check.sh": "#!/bin/bash\ncd tools\n./run.sh\n"},
		"another interpreter":  {"scripts/health-check.sh": "#!/usr/bin/env python3\nprint('ok')\n"},
		"Python launcher":      {"scripts/health-check.sh": "#!/bin/bash\npython3 ci/run_tests.py\n"},
		"Node launcher":        {"scripts/health-check.sh": "#!/bin/bash\nnode ci/run-tests.js\n"},
		"undefined dispatched": {"scripts/docker-build.sh": "#!/bin/bash\n\"$@\"\n"},
		"Maven Daemon":         {"scripts/health-check.sh": "#!/bin/bash\nmvnd test\n"},
		"unknown executable":   {"scripts/health-check.sh": "#!/bin/bash\nacme-ci run\n"},
	} {
		t.Run(name, func(t *testing.T) {
			merged := map[string]string{}
			for path, text := range springBootScripts {
				merged[path] = text
			}
			for path, text := range scripts {
				merged[path] = text
			}
			root := springBootRepository(t, merged)
			scope, err := DiscoverValidationScope(root, "cucumber-java")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Repository script may run cucumber-java tests")
		})
	}
}

// An interpreter runs code ddtest doesn't read, which may start Maven or
// Gradle: a script or inline code it runs may start Java tests, as a launcher
// does. Version and help queries, and package management, don't.
func TestJavaInterpreterLaunchers(t *testing.T) {
	root := t.TempDir()
	for command, may := range map[string]bool{
		"python3 ci/run_tests.py":        true,
		"python -c 'import os'":          true,
		"python3 -m ci.run_tests":        true,
		"python3.12 -u ci/run_tests.py":  true,
		"node ci/run-tests.js":           true,
		"bundle exec rake ci":            true,
		"perl ci/run_tests.pl":           true,
		"groovy ci/RunTests.groovy":      true,
		"node -v":                        false,
		"python3 -m pip install -U pip":  false,
		"python -m venv .venv":           false,
		"zap-baseline.py -t https://app": false,
	} {
		require.Equal(t, may, javaShellMayRunTests(root, root, command, nil, false, 0), command)
	}
	// A CI step that runs one is a possible test step, so its job is not
	// outside cucumber-java.
	workflow := ciWorkflow{Path: gitLabCIFile}
	job := runtimeJob{Steps: []runtimeStep{{Run: "python3 ci/run_tests.py"}}}
	outside, _ := javaDiscovery{}.outsideFrameworkJob(root, workflow, job, nil, "cucumber-java")
	require.False(t, outside)
}

// Only commands ddtest knows start no tests are excluded: Maven Daemon is
// read as Maven, by its goals, and an executable ddtest doesn't know may
// start tests.
func TestJavaScriptCommandClassification(t *testing.T) {
	root := t.TempDir()
	for command, may := range map[string]bool{
		"mvnd test":               true,
		"mvnd.cmd verify":         true,
		"mvnd.sh -B package":      true,
		"mvnd -v":                 false,
		"mvnd deploy -DskipTests": false,
		"mvnd help:evaluate -q -DforceStdout -Dexpression=project.version": false,
		"acme-ci run":                        true,
		"./build/out/runner":                 true,
		"helm upgrade --install app ./chart": false,
		"trivy image app:latest":             false,
		"checkov -d ./":                      false,
		"kubectl rollout status deploy/app":  false,
		"k6 run script.js":                   false,
		"read -r line":                       false,
	} {
		require.Equal(t, may, javaShellMayRunTests(root, root, command, nil, false, 0), command)
	}
}
