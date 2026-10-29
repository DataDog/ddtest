// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// freshshellDockerfile is freshshell/fresh's Dockerfile, shortened: a
// slim Ruby image, packages installed in a heredoc RUN, the checkout copied
// to the WORKDIR and a non-root user.
const freshshellDockerfile = `# syntax=docker/dockerfile:1.4

FROM ruby:3.4.7-slim-trixie AS ruby

WORKDIR /app
SHELL ["/bin/bash", "-c"]

RUN \
  --mount=type=cache,id=fresh-apt-cache,sharing=locked,target=/var/cache/apt \
  <<SH
  set -euo pipefail
  apt-get update --yes
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    perl git build-essential
SH

COPY Gemfile* ./
COPY . ./

USER user
`

// pipelineSchemaDockerfile is buildkite/pipeline-schema's test/Dockerfile.
const pipelineSchemaDockerfile = "FROM node:18\n\nWORKDIR /app/test\n\nCOPY package.json package-lock.json /app/test/\nRUN npm install\n\nCOPY . /app/test\n\nCMD [\"npm\", \"test\"]\n"

// ddtest reads the stage a build produces: its base image through stage
// aliases, build arguments with literal defaults, its last WORKDIR, the copy
// of the build context and the packages its RUN instructions install.
func TestBuildkiteReadsDockerfiles(t *testing.T) {
	for _, tc := range []struct {
		name, dockerfile, target string
		overridden               []string
		want                     buildkiteDockerfile
	}{
		{name: "freshshell/fresh", dockerfile: freshshellDockerfile, want: buildkiteDockerfile{image: "ruby:3.4.7-slim-trixie", workdir: "/app", contextCopy: "/app"}},
		{name: "buildkite/pipeline-schema", dockerfile: pipelineSchemaDockerfile, want: buildkiteDockerfile{image: "node:18", workdir: "/app/test", contextCopy: "/app/test"}},
		{name: "a stage alias chain", dockerfile: "FROM node:20-slim AS base\nRUN apt-get update && apt-get install -y curl=7.88.1-10\nWORKDIR /srv\nFROM base AS deps\nRUN npm ci\nFROM deps\nWORKDIR app\n", want: buildkiteDockerfile{image: "node:20-slim", workdir: "/srv/app", downloader: true}},
		{name: "an argument with a literal default", dockerfile: "ARG RUBY_VERSION=3.3\nARG VARIANT=slim\nFROM ruby:${RUBY_VERSION}-${VARIANT}\n", want: buildkiteDockerfile{image: "ruby:3.3-slim"}},
		{name: "an argument without a default", dockerfile: "ARG RUBY_VERSION\nFROM ruby:${RUBY_VERSION}\n", want: buildkiteDockerfile{problem: "whose base image ruby:${RUBY_VERSION} reads the build argument RUBY_VERSION, which has no literal default ddtest can resolve"}},
		{name: "an argument the build overrides", dockerfile: "ARG RUBY_VERSION=3.3\nFROM ruby:$RUBY_VERSION\n", overridden: []string{"RUBY_VERSION"}, want: buildkiteDockerfile{problem: "whose base image ruby:$RUBY_VERSION reads the build argument RUBY_VERSION, which the build overrides with a value ddtest does not read"}},
		{name: "the build's target", dockerfile: "FROM node:22 AS test\nWORKDIR /test\nFROM nginx:1.27 AS production\n", target: "test", want: buildkiteDockerfile{image: "node:22", workdir: "/test"}},
		{name: "a missing target", dockerfile: "FROM node:22\n", target: "test", want: buildkiteDockerfile{problem: "which has no stage test for the build's target"}},
		{name: "scratch", dockerfile: "FROM scratch\nCOPY app /app\n", want: buildkiteDockerfile{problem: "which builds FROM scratch, an image without a shell"}},
		{name: "no FROM", dockerfile: "# empty\n", want: buildkiteDockerfile{problem: "which has no FROM instruction ddtest can read"}},
		{name: "Alpine with Bash and curl", dockerfile: "FROM --platform=$BUILDPLATFORM node:22-alpine\n# tools\nRUN apk add --no-cache \\\n    # the installer's\n    bash \\\n    curl\n", want: buildkiteDockerfile{image: "node:22-alpine", bash: true, downloader: true}},
		{name: "a heredoc continuing its install", dockerfile: "FROM debian:12-slim\nRUN <<-SH\n\tapt-get install -y \\\n\t  git curl\n\tSH\nWORKDIR /w\n", want: buildkiteDockerfile{image: "debian:12-slim", workdir: "/w", downloader: true}},
		{name: "wget with dnf", dockerfile: "FROM fedora:41\nRUN dnf install -y wget && dnf clean all\n", want: buildkiteDockerfile{image: "fedora:41", downloader: true}},
		{name: "a WORKDIR read from ENV", dockerfile: "FROM node:22\nENV APP_HOME=/home/app\nWORKDIR $APP_HOME/web\n", want: buildkiteDockerfile{image: "node:22", workdir: "/home/app/web"}},
		{name: "a WORKDIR ddtest cannot resolve", dockerfile: "FROM node:22\nARG HOME_DIR\nWORKDIR ${HOME_DIR}\n", want: buildkiteDockerfile{image: "node:22", workdirProblem: "its WORKDIR ${HOME_DIR} reads $HOME_DIR, which ddtest cannot resolve"}},
		{name: "a copy from another stage", dockerfile: "FROM node:22 AS build\nFROM node:22-slim\nWORKDIR /app\nCOPY --from=build . /app\n", want: buildkiteDockerfile{image: "node:22-slim", workdir: "/app"}},
		{name: "curl mentioned without an install", dockerfile: "FROM node:22-slim\nRUN echo curl wget bash\n", want: buildkiteDockerfile{image: "node:22-slim"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overridden := map[string]bool{}
			for _, name := range tc.overridden {
				overridden[name] = true
			}
			require.Equal(t, tc.want, readBuildkiteDockerfile(tc.dockerfile, tc.target, overridden))
		})
	}
}

// buildkiteComposeRepository is a Jest repository whose Buildkite step
// runs in the Compose service app of compose (at file), with extra files.
func buildkiteComposeRepository(t *testing.T, commands, file, compose string, files map[string]string) string {
	t.Helper()
	config := ""
	if file != "docker-compose.yml" {
		config = "          config: " + file + "\n"
	}
	root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+commands+"    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n"+config+"          environment:\n            - DD_API_KEY\n")
	writeFile(t, root, file, compose)
	for name, contents := range files {
		writeFile(t, root, name, contents)
	}
	return root
}

// A Compose service built from a Dockerfile runs in the image its final
// stage builds FROM, and starts in its last WORKDIR, mapped back into the
// checkout through the mounted directory (relative to the Compose file) or
// the copy of the build context.
func TestBuildkiteComposeServicesBuiltFromADockerfile(t *testing.T) {
	t.Run("freshshell/fresh", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", "version: '2'\nservices:\n  app:\n    build: .\n", map[string]string{"Dockerfile": freshshellDockerfile})
		workflow := buildkiteWorkflow(t, root, buildkiteFile)
		job := workflow.Jobs["test"]
		require.Equal(t, "ruby:3.4.7-slim-trixie", job.Image)
		require.Empty(t, job.ImageProblem)
		require.Empty(t, job.Steps[0].WorkingDirectory)
		require.ElementsMatch(t, []string{"docker-compose.yml", "Dockerfile"}, workflow.Files)
		require.Contains(t, workflow.Review, "docker-compose.yml: the Docker Compose service app does not mount the checkout, so the step runs on the copy of the repository in its image; ddtest validates the checkout.")
	})
	t.Run("buildkite/pipeline-schema", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "test/docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - \"../schema.json:/app/schema.json\"\n", map[string]string{"test/Dockerfile": pipelineSchemaDockerfile, "schema.json": "{}\n", "test/package.json": `{"scripts":{"test":"jest"}}`})
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Equal(t, "node:18", job.Image)
		require.Equal(t, "test", job.Steps[0].WorkingDirectory, "WORKDIR /app/test is where COPY . puts test/, the build context")
		require.Empty(t, job.Steps[0].ResolutionError)
	})
	for _, tc := range []struct{ name, compose, dockerfile, directory, problem string }{
		{"the WORKDIR in the mounted checkout", "services:\n  app:\n    build:\n      context: .\n      dockerfile: docker/ci.Dockerfile\n    volumes:\n      - .:/app\n", "FROM node:22\nWORKDIR /app/packages/ui\n", "packages/ui", ""},
		{"working_dir over the WORKDIR", "services:\n  app:\n    build: .\n    working_dir: /app/packages/api\n    volumes:\n      - .:/app\n", "FROM node:22\nWORKDIR /app/packages/ui\n", "packages/api", ""},
		{"a WORKDIR outside the mounted checkout", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app\n", "FROM node:22\nWORKDIR /srv\n", "", "starts in its Dockerfile's WORKDIR /srv, outside the checkout it mounts at /app"},
		{"a WORKDIR ddtest cannot resolve", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app\n", "FROM node:22\nARG HOME_DIR\nWORKDIR $HOME_DIR\n", "", "sets no working_dir, and docker/ci.Dockerfile its WORKDIR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compose := strings.Replace(tc.compose, "    build: .\n", "    build:\n      context: .\n      dockerfile: docker/ci.Dockerfile\n", 1)
			root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", compose, map[string]string{"docker/ci.Dockerfile": tc.dockerfile, "packages/ui/package.json": "{}", "packages/api/package.json": "{}"})
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			require.Equal(t, "node:22", job.Image)
			if tc.problem != "" {
				require.Contains(t, job.Steps[0].ResolutionError, tc.problem)
				return
			}
			require.Empty(t, job.Steps[0].ResolutionError)
			require.Equal(t, tc.directory, job.Steps[0].WorkingDirectory)
		})
	}
	t.Run("a mounted subdirectory over the copied context", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - ./packages/ui/src:/app/packages/ui/src\n", map[string]string{"Dockerfile": "FROM node:22\nWORKDIR /app\nCOPY . .\nWORKDIR /app/packages/ui\n", "packages/ui/src/index.js": ""})
		workflow := buildkiteWorkflow(t, root, buildkiteFile)
		require.Equal(t, "packages/ui", workflow.Jobs["test"].Steps[0].WorkingDirectory)
		require.Contains(t, workflow.Review, "docker-compose.yml: the Docker Compose service app does not mount the checkout, so the step runs on the copy of the repository in its image; ddtest validates the checkout.")
	})
	t.Run("volumes are relative to the Compose file", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", ".buildkite/docker-compose.yml", "services:\n  app:\n    image: node:22\n    working_dir: /workdir/packages/ui\n    volumes:\n      - ..:/workdir\n", map[string]string{"packages/ui/package.json": "{}"})
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Empty(t, job.Steps[0].ResolutionError)
		require.Equal(t, "packages/ui", job.Steps[0].WorkingDirectory)
		writeFile(t, root, ".buildkite/docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/workdir\n")
		require.Contains(t, buildkiteWorkflow(t, root, buildkiteFile).Review, ".buildkite/docker-compose.yml: the Docker Compose service app does not mount the checkout, so the step runs on the copy of the repository in its image; ddtest validates the checkout.", "it mounts .buildkite/ only")
	})
	for _, tc := range []struct{ name, compose, dockerfile, problem string }{
		{"a base read from an argument", "services:\n  app:\n    build: .\n", "ARG BASE\nFROM ${BASE}\n", "its Docker Compose service app is built from Dockerfile, whose base image ${BASE} reads the build argument BASE, which has no literal default ddtest can resolve"},
		{"an argument the Compose build passes", "services:\n  app:\n    build:\n      context: .\n      args:\n        NODE: \"20\"\n", "ARG NODE=22\nFROM node:${NODE}\n", "reads the build argument NODE, which the build overrides with a value ddtest does not read"},
		{"a missing Dockerfile", "services:\n  app:\n    build:\n      context: .\n      dockerfile: ci.Dockerfile\n", "FROM node:22\n", "its Docker Compose service app is built from the Dockerfile ci.Dockerfile, which is not a file ddtest can read in the repository"},
		{"a remote context", "services:\n  app:\n    build: https://github.com/acme/images.git#main:node\n", "FROM node:22\n", "its Docker Compose service app is built from the build context https://github.com/acme/images.git#main:node, which ddtest cannot read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", tc.compose, map[string]string{"Dockerfile": tc.dockerfile})
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			require.Empty(t, job.Image)
			require.Contains(t, job.ImageProblem, tc.problem)
		})
	}
	t.Run("an inline Dockerfile", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", "services:\n  app:\n    build:\n      context: .\n      dockerfile_inline: |\n        FROM node:22-bookworm\n        WORKDIR /app\n    volumes:\n      - .:/app\n", nil)
		require.Equal(t, "node:22-bookworm", buildkiteJobs(t, root, buildkiteFile)["test"].Image)
	})
}

// The setup snippet needs Bash and curl or wget in the image a Dockerfile
// builds: the base image's, or what its RUN instructions install.
func TestBuildkiteDockerfileImagesRunTheSetup(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	for _, tc := range []struct{ name, dockerfile, problem string }{
		{"a slim base without curl", "FROM node:22-slim\nRUN apt-get update && apt-get install -y git\n", "The step's image is built from Dockerfile, whose final stage is FROM node:22-slim, which has neither curl nor wget, and no RUN instruction of the Dockerfile installs them, so the Datadog installer cannot be downloaded and its tests would run untraced. Install curl in the Dockerfile (add curl to its apt-get install packages, or RUN apt-get update && apt-get install -y --no-install-recommends curl), or build FROM the full image (without -slim)."},
		{"a slim base installing curl", "FROM node:22-slim\nRUN apt-get update \\\n  && apt-get install -y --no-install-recommends git curl\n", ""},
		{"a full base", "FROM node:22\n", ""},
		{"Alpine without Bash", "FROM node:22-alpine\n", "whose final stage is FROM node:22-alpine, which has no Bash, which the Datadog installer needs. Install bash in the Dockerfile (for Alpine: RUN apk add --no-cache bash)"},
		{"Alpine installing Bash", "FROM node:22-alpine\nRUN apk add --no-cache bash\n", ""},
		{"a base ddtest cannot resolve", "ARG BASE\nFROM $BASE\n", "The step's container cannot be checked: its Docker Compose service app is built from Dockerfile, whose base image $BASE reads the build argument BASE, which has no literal default ddtest can resolve, so ddtest cannot tell whether the image it builds has the Bash and the curl or wget the Datadog installer needs."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteComposeRepository(t, snippet+"      - npm test\n", "docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app\n", map[string]string{"Dockerfile": tc.dockerfile})
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			index := slices.IndexFunc(job.Steps, func(step runtimeStep) bool { return step.Setup != nil })
			require.GreaterOrEqual(t, index, 0)
			require.Equal(t, tc.problem == "", javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
			require.Equal(t, job.SetupImageProblem, job.Steps[index].Setup.Problem, "before the snippet is written, the job already reports it")
			if tc.problem == "" {
				require.Empty(t, job.Steps[index].Setup.Problem)
				return
			}
			require.Contains(t, job.Steps[index].Setup.Problem, tc.problem)
		})
	}
	t.Run("the instructions name the image before the snippet is written", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app\n", map[string]string{"Dockerfile": "FROM node:22-slim\n"})
		text := providerSetupInstructions(root, buildkite{}, "javascript", "jest", "", true)
		require.Contains(t, text, "These CI steps cannot run the Datadog setup snippet in their current image, so their tests would stay untraced even with it in place; changing an image or Dockerfile is not part of this setup, so tell the user and ask before making it:\n  .buildkite/pipeline.yml / test: The step's image is built from Dockerfile, whose final stage is FROM node:22-slim, which has neither curl nor wget")
		writeFile(t, root, "Dockerfile", "FROM node:22\n")
		require.NotContains(t, providerSetupInstructions(root, buildkite{}, "javascript", "jest", "", true), "These CI steps cannot run")
	})
}

// The Node a Dockerfile builds FROM is the CI runtime the tracer must run
// on: the shared runtime check compares the pinned release with it.
func TestBuildkiteDockerfileNodeDecidesTheTracer(t *testing.T) {
	releases := map[string]tracerRequirement{"6.22.0": {Version: "6.22.0", Node: ">=22"}, "5.133.0": {Version: "5.133.0", Node: ">=18"}}
	resolve := func(_ context.Context, _, version string) (tracerRequirement, error) { return releases[version], nil }
	for tracer, status := range map[string]string{"6.22.0": StatusIncompatible, "5.133.0": StatusCompatible} {
		root := buildkiteComposeRepository(t, buildkiteSnippetText("js", tracer, "datadoghq.com", "      ")+"      - npm test\n", "test/docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app/test\n", map[string]string{"test/Dockerfile": pipelineSchemaDockerfile, "test/package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"29.7.0"}}`})
		result := checkCIRuntimes(t.Context(), root, nil, resolve, "jest")
		require.NotEmpty(t, result.Jobs)
		finding := result.Jobs[len(result.Jobs)-1]
		require.Equal(t, "18", finding.Node, tracer)
		require.Equal(t, "image node:18", finding.NodeSource, tracer)
		require.Equal(t, status, finding.Status, tracer+": "+finding.Reason)
	}
	t.Run("an image ddtest cannot resolve names the cause", func(t *testing.T) {
		root := buildkiteComposeRepository(t, buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test\n", "docker-compose.yml", "services:\n  app:\n    build: .\n    volumes:\n      - .:/app\n", map[string]string{"Dockerfile": "ARG NODE\nFROM node:${NODE}\n"})
		var reasons []string
		for _, job := range checkCIRuntimes(t.Context(), root, nil, resolve, "jest").Jobs {
			reasons = append(reasons, job.Reason)
		}
		require.Contains(t, strings.Join(reasons, "\n"), "its Docker Compose service app is built from Dockerfile, whose base image node:${NODE} reads the build argument NODE")
	})
}

// The tracer release manual CI edits pin is written in each provider's
// setup syntax.
func TestCIProviderTracerSetting(t *testing.T) {
	for provider, want := range map[string]string{
		"github":    "js-tracer-version: 5.133.0",
		"gitlab":    "JS_TRACER_VERSION: 5.133.0",
		"circleci":  "js_tracer_version: 5.133.0",
		"buildkite": `DD_SET_TRACER_VERSION_JS="5.133.0"`,
	} {
		useCIProvider(t, provider)
		require.Equal(t, want, CIProviderTracerSetting("js", "5.133.0"), provider)
	}
}
