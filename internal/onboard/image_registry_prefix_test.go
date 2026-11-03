// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A registry prefix written only as variables, such as GitLab's dependency
// proxy, names a mirror of the literal image after it when the prefix names
// a mirror or the rest is namespaced; an official-looking name behind an
// unknown registry, or a name or tag that reads a variable, is never guessed.
func TestMirroredImageReadsTheLiteralImageAfterAVariablePrefix(t *testing.T) {
	for image, want := range map[string]string{
		"${GITLAB_DEPENDENCY_PROXY}ruby:3.3":               "ruby:3.3",
		"$CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX/ruby:3.3": "ruby:3.3",
		"${REGISTRY}/library/node:22":                      "library/node:22",
		"${REGISTRY}${MIRROR}/node:22-alpine":              "node:22-alpine",
		"<< parameters.registry >>/cimg/node:22.11":        "cimg/node:22.11",
		"ruby:3.3":                              "ruby:3.3",
		"$RUBY_IMAGE":                           "$RUBY_IMAGE",
		"${IMAGE}:3.3":                          "${IMAGE}:3.3",
		"${REGISTRY}/ruby:${VERSION}":           "${REGISTRY}/ruby:${VERSION}",
		"${REGISTRY}-ruby:3.3":                  "${REGISTRY}-ruby:3.3",
		"${REGISTRY}:5000/ruby:3.3":             "${REGISTRY}:5000/ruby:3.3",
		"$$REGISTRY/node:22":                    "$$REGISTRY/node:22",
		"${NODE_IMAGE:-node:22}":                "${NODE_IMAGE:-node:22}",
		"$CI_REGISTRY_IMAGE/ruby${V}:3":         "$CI_REGISTRY_IMAGE/ruby${V}:3",
		"$PREFIXruby:3.3":                       "$PREFIXruby:3.3",
		"${GITLAB_DEPENDENCY_PROXY}":            "${GITLAB_DEPENDENCY_PROXY}",
		"${GITLAB_DEPENDENCY_PROXY}/":           "${GITLAB_DEPENDENCY_PROXY}/",
		"  ${REGISTRY}/library/node:22.11.0   ": "library/node:22.11.0",
		"${REGISTRY}/myteam/ruby:3.3":           "myteam/ruby:3.3",
		"$CI_REGISTRY/group/project:tag":        "group/project:tag",
		// Without its own /, only a dependency proxy variable is a
		// registry: IMAGE_PREFIX=custom- makes custom-node:22.
		"${IMAGE_PREFIX}node:22":                      "${IMAGE_PREFIX}node:22",
		"$IMAGE_PREFIX" + "node:22-alpine":            "$IMAGE_PREFIX" + "node:22-alpine",
		"${REGISTRY}${GITLAB_DEPENDENCY_PROXY}ruby:3": "${REGISTRY}${GITLAB_DEPENDENCY_PROXY}ruby:3",
		"<< parameters.prefix >>node:22":              "<< parameters.prefix >>node:22",
		"${GITLAB_DEPENDENCY_PROXY_ADDRESS}node:22":   "node:22",
		"${GITLAB_DEPENDENCY_PROXY}/ruby:3.3":         "ruby:3.3",
		"${DOCKER_HUB_MIRROR}/node:22":                "node:22",
		"$DOCKERHUB/ruby:3.3":                         "ruby:3.3",
		// An unknown registry may store any image at an official-looking
		// path: the registry is part of the image's name.
		"${REGISTRY}/node:22":               "${REGISTRY}/node:22",
		"${REGISTRY}/ruby:3.3-slim":         "${REGISTRY}/ruby:3.3-slim",
		"$CI_REGISTRY/node:22":              "$CI_REGISTRY/node:22",
		"$CI_REGISTRY_IMAGE/ruby:3.3":       "$CI_REGISTRY_IMAGE/ruby:3.3",
		"<< parameters.registry >>/node:22": "<< parameters.registry >>/node:22",
		"${REGISTRY}${PROJECT}/node:22":     "${REGISTRY}${PROJECT}/node:22",
	} {
		require.Equal(t, want, mirroredImage(image), image)
	}
	for image, node := range map[string]string{
		"${GITLAB_DEPENDENCY_PROXY}node:22":                       "22",
		"${REGISTRY}/library/node:20.11":                          "20.11",
		"${REGISTRY}/cimg/node:22.11":                             "22.11",
		"${REGISTRY}/node:${NODE}":                                "",
		"${NODE_IMAGE}":                                           "",
		"${REGISTRY}/myteam/node:22":                              "",
		"$CI_DEPENDENCY_PROXY_DIRECT_GROUP_IMAGE_PREFIX/node:lts": "",
		"${IMAGE_PREFIX}node:22":                                  "",
		"${REGISTRY}/node:22":                                     "",
		"$CI_REGISTRY_IMAGE/node:22":                              "",
		"${DOCKER_HUB_MIRROR}/node:22":                            "22",
	} {
		require.Equal(t, node, imageNode(image), image)
	}
	for image, want := range map[string][2]string{
		"${GITLAB_DEPENDENCY_PROXY}ruby:3.3":                    {"3.3", ""},
		"$CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX/ruby:3.2-slim": {"3.2", ""},
		"${REGISTRY}/library/ruby:3.1.4":                        {"3.1.4", ""},
		"${GITLAB_DEPENDENCY_PROXY}jruby:9.4":                   {"", "JRuby"},
		"${GITLAB_DEPENDENCY_PROXY}ruby:${RUBY_VERSION}":        {"", ""},
		"${RUBY_IMAGE}:3.3":                                     {"", ""},
		"${REGISTRY}/ruby:3.3":                                  {"", ""},
		"$CI_REGISTRY_IMAGE/jruby:9.4":                          {"", ""},
	} {
		version, engine := rubyImageRuntime(image)
		require.Equal(t, want, [2]string{version, engine}, image)
	}
	require.Equal(t, "lts", javascriptImageRuntime(runtimeJob{Image: "${REGISTRY}/library/node:lts"}), "a floating tag is still not the user's to state")
	require.Empty(t, javascriptImageRuntime(runtimeJob{Image: "${REGISTRY}/node:lts"}))
	require.Equal(t, "latest", rubyImageSelection(runtimeJob{Image: "${GITLAB_DEPENDENCY_PROXY}ruby"}))
	require.True(t, imageWithoutBash("${GITLAB_DEPENDENCY_PROXY}node:22-alpine"))
	require.True(t, imageWithoutDownloader("$CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX/ruby:3.3-slim"))
	require.False(t, imageWithoutDownloader("${REGISTRY}/myteam/ruby:3.3-slim"), "only official slim images are known to lack curl")
	require.False(t, imageWithoutDownloader("${REGISTRY}/ruby:3.3-slim"), "an unknown registry's image is not the official one")
	require.True(t, imageWithoutDownloader("${REGISTRY}/library/ruby:3.3-slim"))
	// GitLab can't tell whether an unknown registry's image has Bash and curl.
	require.Contains(t, gitLabShellProblem("${REGISTRY}/node:22"), "depends on a variable ddtest cannot resolve")
	require.Empty(t, gitLabShellProblem("${GITLAB_DEPENDENCY_PROXY}node:22"))
	require.Empty(t, gitLabShellProblem("$CI_REGISTRY/group/project:tag"))
}

// The B03 shape: a GitLab job image behind the dependency proxy selects the
// Ruby of its literal name and tag, and the setup check reads it as an image
// with Bash.
func TestGitLabDependencyProxyImageSelectsItsRuby(t *testing.T) {
	useCIProvider(t, "gitlab")
	for _, image := range []string{`"${GITLAB_DEPENDENCY_PROXY}ruby:3.3"`, "$CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX/ruby:3.3"} {
		root := gitLabRubyRepository(t, rubyJob(image, "", ""))
		check := RubyCIConfigurationForSite(root, "rspec", "")
		require.Len(t, check.Jobs, 1, image)
		require.Equal(t, "3.3", check.Jobs[0].Runtime, image)
		require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""), image)
	}
	matrix := "rspec:\n  image: \"${GITLAB_DEPENDENCY_PROXY}ruby:${RUBY_VERSION}\"\n  parallel:\n    matrix:\n      - RUBY_VERSION: ['3.1', '3.3']\n  script:\n" + gitLabSetupEntry(gitLabRubySetup) + "    - bundle exec rspec\n"
	root := gitLabRubyRepository(t, matrix)
	check := RubyCIConfigurationForSite(root, "rspec", "")
	var runtimes []string
	for _, job := range check.Jobs {
		runtimes = append(runtimes, job.Runtime)
	}
	require.ElementsMatch(t, []string{"3.1", "3.3"}, runtimes)

	// A variable image name is still the user's to state.
	root = gitLabRubyRepository(t, rubyJob("${RUBY_IMAGE}:3.3", "", ""))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
}

// CircleCI, Buildkite and Jenkins read the same registry prefixes through
// the shared image readers.
func TestProviderImagesBehindARegistryVariable(t *testing.T) {
	t.Run("circleci", func(t *testing.T) {
		root := circleRepository(t, "version: 2.1\nparameters:\n  registry:\n    type: string\n    default: \"\"\njobs:\n  test:\n    parameters:\n      registry:\n        type: string\n    docker:\n      - image: << parameters.registry >>/cimg/node:22.11\n    steps:\n      - checkout\n      - run: npm test\n"+circleWorkflow("      - test:\n          registry: << pipeline.parameters.unknown >>\n"))
		job := circleJobs(t, root)["main/test"]
		require.Empty(t, job.ImageProblem)
		require.Equal(t, "22.11", imageNode(job.Image))
	})
	t.Run("buildkite", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: step image\n    image: \"${REGISTRY}/library/node:22\"\n    command: npm test\n  - label: unknown registry\n    image: \"${REGISTRY}/node:22\"\n    command: npm test\n  - label: docker plugin\n    command: npm test\n    plugins:\n      - docker#v5.12.0:\n          image: \"$$REGISTRY/node:22\"\n  - label: docker proxy\n    command: npm test\n    plugins:\n      - docker#v5.12.0:\n          image: \"${REGISTRY}/library/node:20\"\n  - label: variable name\n    image: \"${NODE_IMAGE}:22\"\n    command: npm test\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Equal(t, "22", imageNode(jobs["step image"].Image))
		require.Empty(t, jobs["unknown registry"].Image)
		require.Contains(t, jobs["unknown registry"].ImageProblem, "depends on a variable or matrix value ddtest cannot resolve")
		require.Equal(t, "20", imageNode(jobs["docker proxy"].Image))
		require.Empty(t, jobs["docker plugin"].Image, "an escaped $$REGISTRY is never expanded")
		require.Empty(t, jobs["variable name"].Image)
		require.Contains(t, jobs["variable name"].ImageProblem, "depends on a variable or matrix value ddtest cannot resolve")
	})
	t.Run("buildkite compose", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: compose\n    command: npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n          config: .buildkite/docker-compose.yml\n")
		writeFile(t, root, ".buildkite/docker-compose.yml", "services:\n  app:\n    image: ${DOCKER_HUB_MIRROR}/node:22-slim\n")
		require.Equal(t, "22", imageNode(buildkiteJobs(t, root, buildkiteFile)["compose"].Image))
	})
	t.Run("jenkins", func(t *testing.T) {
		jobs := jenkinsJobs(t, `pipeline {
  agent none
  stages {
    stage('Proxy') {
      agent { docker { image "${REGISTRY}/library/node:22-alpine" } }
      steps { sh 'npm test' }
    }
    stage('Param') {
      agent { docker { image "${params.MIRROR}/library/node:20" } }
      steps { sh 'npm test' }
    }
    stage('Unknown') {
      agent { docker { image "${REGISTRY}/node:22-alpine" } }
      steps { sh 'npm test' }
    }
    stage('Name') {
      agent { docker { image "${NODE_IMAGE}:22" } }
      steps { sh 'npm test' }
    }
    stage('Inside') {
      steps { script { docker.image("${env.NODE_MIRROR}/node:18").inside { sh 'npm test' } } }
    }
  }
}`)
		require.Equal(t, "22", imageNode(jobs["Proxy"].Image))
		require.Equal(t, "20", imageNode(jobs["Param"].Image))
		// A container stage whose image ddtest does not read runs the Datadog
		// setup snippet on a runtime the user states, as a host agent's.
		for _, name := range []string{"Unknown", "Name"} {
			require.Empty(t, jobs[name].Image, name)
			require.Contains(t, jobs[name].Host, "whose image ddtest does not read", name)
		}
		var inside string
		for name, job := range jobs {
			if job.Image != "" && name != "Proxy" && name != "Param" && name != "Unknown" {
				inside = job.Image
			}
		}
		require.Equal(t, "18", imageNode(inside))
	})
}
