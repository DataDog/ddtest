// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const buildkiteFile = ".buildkite/pipeline.yml"

// buildkiteRepository is a Jest repository whose only CI is the Buildkite
// pipeline; the test runs in it with Buildkite selected.
func buildkiteRepository(t *testing.T, pipeline string) string {
	t.Helper()
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, buildkiteFile, pipeline)
	t.Chdir(root)
	useCIProvider(t, "buildkite")
	return root
}

// buildkiteJobs reads the jobs of one pipeline file.
func buildkiteJobs(t *testing.T, root, file string) map[string]runtimeJob {
	t.Helper()
	workflows, err := buildkite{}.readWorkflows(root)
	require.NoError(t, err)
	for _, workflow := range workflows {
		if workflow.Path == file {
			return workflow.Jobs
		}
	}
	t.Fatalf("no workflow %s in %v", file, workflows)
	return nil
}

// buildkiteWorkflow reads one pipeline file.
func buildkiteWorkflow(t *testing.T, root, file string) ciWorkflow {
	t.Helper()
	workflows, err := buildkite{}.readWorkflows(root)
	require.NoError(t, err)
	for _, workflow := range workflows {
		if workflow.Path == file {
			return workflow
		}
	}
	t.Fatalf("no workflow %s", file)
	return ciWorkflow{}
}

// buildkiteSnippetText is ddtest's guarded snippet for js, as a commands
// entry indented by indent.
func buildkiteSnippetText(language, tracer, site, indent string, after ...string) string {
	return buildkiteSetupText(ciSetupRequest{Language: language, Site: site, TracerInput: language + "-tracer-version", TracerVersion: tracer, After: after}, indent)
}

func buildkiteDockerStep(label, image, commands string) string {
	return "  - label: " + label + "\n    commands:\n" + commands + "    plugins:\n      - docker#v5.12.0:\n          image: " + image + "\n          environment:\n            - DD_API_KEY\n"
}

func TestBuildkiteConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	files, err := buildkite{}.configurationFiles(root)
	require.NoError(t, err)
	require.Empty(t, files)
	writeFile(t, root, ".buildkite/docker-compose.yml", "services: {}\n")
	writeFile(t, root, ".buildkite/hooks/environment", "export X=1\n")
	writeFile(t, root, ".buildkite/renovate.json", "{}\n")
	files, err = buildkite{}.configurationFiles(root)
	require.NoError(t, err)
	require.Empty(t, files, "Compose files, hooks and other JSON are not pipelines")
	writeFile(t, root, buildkiteFile, "steps: [")
	writeFile(t, root, ".buildkite/pipeline.release.yaml", "steps: []\n")
	writeFile(t, root, ".buildkite/pipelines/tests.yml", "steps: []\n")
	writeFile(t, root, "buildkite.yml", "steps: []\n")
	files, err = buildkite{}.configurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{"buildkite.yml", ".buildkite/pipeline.release.yaml", ".buildkite/pipeline.yml", ".buildkite/pipelines/tests.yml"}, files)
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "buildkite", selected, "listing never parses")
	_, err = buildkite{}.readWorkflows(root)
	require.ErrorContains(t, err, "parse .buildkite/pipeline.yml")
}

// Every file that defines steps is a pipeline, in the mapping or the legacy
// list form; files of anchors or another tool's YAML are not.
func TestBuildkiteReadsEveryPipelineFile(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - command: npm test\n")
	writeFile(t, root, ".buildkite/pipeline.deploy.yml", "- label: deploy\n  command: ./deploy.sh\n")
	writeFile(t, root, ".buildkite/shared.yml", "definitions:\n  node: &node node:22\n")
	workflows, err := buildkite{}.readWorkflows(root)
	require.NoError(t, err)
	var paths []string
	for _, workflow := range workflows {
		paths = append(paths, workflow.Path)
	}
	require.Equal(t, []string{".buildkite/pipeline.deploy.yml", buildkiteFile}, paths)
	require.Equal(t, []string{"./deploy.sh"}, circleRuns(buildkiteJobs(t, root, ".buildkite/pipeline.deploy.yml")["deploy"]))
	require.Equal(t, []string{"npm test"}, circleRuns(buildkiteJobs(t, root, buildkiteFile)["step 1"]))
	require.Contains(t, CIConfigurationFiles(root), ".buildkite/pipeline.deploy.yml")
}

// A step's commands share one shell: each top-level statement is a step of
// the shared model, and exports, assignments and cd reach later
// statements of the same Buildkite step only.
func TestBuildkiteCommandsShareTheStepShell(t *testing.T) {
	root := buildkiteRepository(t, `env:
  FROM_PIPELINE: "1"
steps:
  - label: test
    env:
      FROM_STEP: "1"
    commands:
      - export NODE_ENV=test
      - |
        set -euo pipefail
        cd packages/app
        echo "--- :jest: Test"
        npm test
  - label: other
    command: npm test
`)
	jobs := buildkiteJobs(t, root, buildkiteFile)
	job := jobs["test"]
	require.Equal(t, map[string]string{"FROM_PIPELINE": "1", "FROM_STEP": "1"}, job.Env)
	require.Equal(t, []string{`echo "--- :jest: Test"`, "npm test"}, circleRuns(job))
	test := job.Steps[len(job.Steps)-1]
	require.Equal(t, "packages/app", test.WorkingDirectory)
	require.Equal(t, map[string]string{"NODE_ENV": "test"}, test.Env)
	require.Equal(t, buildkiteFile+":13", test.Source)
	require.Equal(t, "bash", test.Shell)
	other := jobs["other"].Steps[0]
	require.Empty(t, other.Env, "every Buildkite step starts a fresh shell")
	require.Empty(t, other.WorkingDirectory)
}

// Pipeline upload interpolates $NAME before any step runs: $$ is a read when
// the step runs, the top-level env is known, Buildkite's own variables are
// reads of the build, and anything else (such as the step's env) is
// unknown at upload time.
func TestBuildkiteInterpolation(t *testing.T) {
	root := buildkiteRepository(t, `env:
  SUITE: unit
  FROM_AGENT: $AGENT_VALUE
steps:
  - label: escaped
    env:
      REPORTS: reports
    commands:
      - npm test -- --outputFile=$$REPORTS/out.json
  - label: known
    command: npm test -- --selectProjects $SUITE --branch=$BUILDKITE_BRANCH
  - label: job variable
    command: npm test -- --shard=$BUILDKITE_PARALLEL_JOB --cache=$BUILDKITE_BUILD_CHECKOUT_PATH/.jest
  - label: unknown
    env:
      REPORTS: reports
    command: npm test -- --outputFile=$REPORTS/out.json
  - label: inherited
    command: npm test
`)
	jobs := buildkiteJobs(t, root, buildkiteFile)
	require.Equal(t, []string{"npm test -- --outputFile=$REPORTS/out.json"}, circleRuns(jobs["escaped"]))
	require.Equal(t, []string{"npm test -- --selectProjects unit --branch=$BUILDKITE_BRANCH"}, circleRuns(jobs["known"]))
	// Agent and job variables of the upload step differ from the test's.
	require.Contains(t, circleRuns(jobs["job variable"])[0], "the command reads $BUILDKITE_PARALLEL_JOB, $BUILDKITE_BUILD_CHECKOUT_PATH, which buildkite-agent pipeline upload replaces")
	require.Contains(t, circleRuns(jobs["unknown"])[0], "the command reads $REPORTS, which buildkite-agent pipeline upload replaces when the pipeline is uploaded")
	require.Equal(t, unresolvedCIValue("$AGENT_VALUE"), jobs["inherited"].Env["FROM_AGENT"])

	text, unknown := buildkiteInterpolate(`a $$b \$c ${KNOWN} $UNKNOWN ${BUILDKITE_BRANCH}`, map[string]string{"KNOWN": "k"})
	require.Equal(t, `a $b $c k $UNKNOWN ${BUILDKITE_BRANCH}`, text)
	require.Equal(t, []string{"UNKNOWN"}, unknown)

	// Default values follow pipeline upload's rules for known variables:
	// :- replaces an empty or missing value, - only a missing one. A
	// variable ddtest does not know stays unknown, default or not.
	known := map[string]string{"NODE_IMAGE": "node:22", "EMPTY": ""}
	for written, want := range map[string]string{
		"${NODE_IMAGE:-node:20}": "node:22",
		"${NODE_IMAGE-node:20}":  "node:22",
		"${EMPTY:-node:20}":      "node:20",
		"${EMPTY-node:20}":       "",
	} {
		text, unknown := buildkiteInterpolate(written, known)
		require.Equal(t, want, text, written)
		require.Empty(t, unknown, written)
	}
	for _, written := range []string{"${MISSING:-node:20}", "${MISSING-node:20}", "${EMPTY:?required}", "${NODE_IMAGE:0:4}"} {
		text, unknown := buildkiteInterpolate(written, known)
		require.Equal(t, written, text)
		require.Len(t, unknown, 1, written)
	}
	root = buildkiteRepository(t, "env:\n  NODE_IMAGE: node:22\nsteps:\n"+buildkiteDockerStep("test", "${NODE_IMAGE:-node:20}", buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test\n"))
	require.Equal(t, "node:22", buildkiteJobs(t, root, buildkiteFile)["test"].Image)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
}

// A matrix runs one job per combination: a list of values ({{matrix}}),
// or setup dimensions ({{matrix.name}}) with adjustments that add or skip
// combinations; the values reach labels, commands, env and plugins.
func TestBuildkiteMatrix(t *testing.T) {
	root := buildkiteRepository(t, `steps:
  - label: "node {{matrix}}"
    command: npm test
    matrix: ["20", "22"]
    plugins:
      - docker#v5.12.0:
          image: "node:{{matrix}}"
  - label: suites
    command: npm test -- --selectProjects {{matrix.suite}}
    env:
      OS: "{{matrix.os}}"
    matrix:
      setup:
        suite: [unit, e2e]
        os: [linux]
      adjustments:
        - with: {suite: e2e, os: linux}
          skip: "flaky"
        - with: {suite: smoke, os: mac}
  - label: unknown dimension
    command: npm test -- {{matrix.missing}}
    matrix: ["1"]
`)
	jobs := buildkiteJobs(t, root, buildkiteFile)
	require.Equal(t, "node:20", jobs["node 20"].Image)
	require.Equal(t, "node:{{matrix}}", jobs["node 22"].ImageTemplate)
	require.Equal(t, []string{"npm test -- --selectProjects unit"}, circleRuns(jobs["suites (linux, unit)"]))
	require.Equal(t, map[string]string{"OS": "mac"}, jobs["suites (mac, smoke)"].Env)
	require.NotContains(t, jobs, "suites (linux, e2e)")
	require.Contains(t, circleRuns(jobs["unknown dimension (1)"])[0], "reads {{matrix.missing}}, which no matrix dimension of the step defines")

	_, err := buildkiteMatrix(yamlNode(t, "setup:\n  a: x\n"))
	require.ErrorContains(t, err, "matrix dimension a is not a list")
}

// Conditions are evaluated only when every operand is a literal: if false
// and skip remove a step (or a group's steps), while conditions on the
// build and branch filters keep it as a possible CI path.
func TestBuildkiteConditions(t *testing.T) {
	root := buildkiteRepository(t, `steps:
  - label: never
    if: "false"
    command: npm test
  - label: literal
    if: '"a" == "b" || !true'
    command: npm test
  - label: build
    if: build.branch == "main" && build.env("RUN") != null
    command: npm test
  - label: branches
    branches: main
    command: npm test
  - label: skipped
    skip: "Temporarily disabled"
    command: npm test
  - label: not skipped
    skip: false
    command: npm test
  - group: Disabled
    if: false
    steps:
      - label: nested
        command: npm test
  - group: Tests
    steps:
      - label: unit
        command: npm test
`)
	jobs := buildkiteJobs(t, root, buildkiteFile)
	for name, disabled := range map[string]bool{"never": true, "literal": true, "build": false, "branches": false, "skipped": true, "not skipped": false, "Disabled / nested": true, "Tests / unit": false} {
		require.Equal(t, disabled, disabledCondition(jobs[name].If), name)
	}
	for expression, want := range map[string]gitLabTruth{"true": gitLabTrue, "!false && 1 == 1": gitLabTrue, `build.message =~ /skip/`: gitLabUnknown, `build.pull_request.labels includes "x"`: gitLabUnknown, `(build.tag != null) || false`: gitLabUnknown, `(`: gitLabUnknown} {
		require.Equal(t, want, buildkiteCondition(expression), expression)
	}
}

// Wait, block and input steps only pause the pipeline.
func TestBuildkiteGates(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - wait\n  - wait: ~\n    continue_on_failure: true\n  - block: Release\n  - input: Details\n  - \"waiter\"\n  - command: npm test\n")
	jobs := buildkiteJobs(t, root, buildkiteFile)
	require.Len(t, jobs, 1)
	require.Contains(t, jobs, "step 6")
}

// Anything ddtest cannot read stays an unresolved blocker: triggered
// pipelines, generated or uploaded steps, plugins it does not know (or reads
// from a variable), Windows agents and steps that run a container's own
// command. A trigger that never runs is not a blocker.
func TestBuildkiteUnresolvedSteps(t *testing.T) {
	for _, tc := range []struct{ name, step, reason string }{
		{"trigger", "  - trigger: deploy-tests\n", "triggers the pipeline deploy-tests, whose steps ddtest cannot read"},
		{"generated upload", "  - command: .buildkite/steps.sh | buildkite-agent pipeline upload\n", "generates or uploads steps with buildkite-agent pipeline upload"},
		{"upload of an unread file", "  - command: buildkite-agent pipeline upload ci/tests.yml\n", "generates or uploads steps with buildkite-agent pipeline upload"},
		{"upload without interpolation", "  - command: buildkite-agent pipeline upload --no-interpolation .buildkite/pipeline.yml\n", "generates or uploads steps"},
		{"unknown plugin", "  - command: npm test\n    plugins:\n      - mise#v1.1.4:\n          dir: app\n", "the plugin mise#v1.1.4 may change how the step runs its commands"},
		{"plugin from a variable", "  - command: npm test\n    plugins:\n      - $NVM_PLUGIN\n", "the plugin $NVM_PLUGIN is read from a variable ddtest cannot resolve"},
		{"windows agents", "  - command: npm test\n    agents:\n      queue: windows\n", "runs on Windows agents"},
		{"compose service command", "  - plugins:\n      - docker-compose#v5.5.0:\n          run: app\n", "runs the Docker Compose service app with its own command"},
		{"docker image command", "  - plugins:\n      - docker#v5.12.0:\n          image: node:22\n", "runs the docker plugin's image with its own command"},
		{"docker shell false", "  - command: npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          shell: false\n", "shell: false passes the commands to the container without a shell"},
		{"unreadable matrix", "  - command: npm test\n    matrix:\n      setup: 1\n", "matrix has no setup ddtest can read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRepository(t, "steps:\n"+tc.step)
			var runs []string
			for _, job := range buildkiteJobs(t, root, buildkiteFile) {
				runs = append(runs, circleRuns(job)...)
			}
			require.Contains(t, strings.Join(runs, "\n"), tc.reason)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.NotEmpty(t, scope.Unresolved, "an unresolved step is a blocker")
		})
	}
	root := buildkiteRepository(t, "steps:\n  - trigger: deploy\n    if: \"false\"\n  - command: npm test\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
}

// An upload of pipeline files ddtest reads, such as a template uploading
// .buildkite/pipeline.yml, is resolved: those files are read as pipelines.
func TestBuildkitePipelineUploadOfReadFiles(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - command: npm test\n")
	writeFile(t, root, ".buildkite/template.yml", "steps:\n  - command: buildkite-agent pipeline upload\n  - command: buildkite-agent pipeline upload .buildkite/pipeline.yml\n")
	workflow := buildkiteWorkflow(t, root, ".buildkite/template.yml")
	require.Equal(t, []string{"Buildkite uploads .buildkite/pipeline.yml with buildkite-agent pipeline upload; ddtest reads it as a pipeline of its own."}, workflow.Review)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
}

// The docker plugin runs commands in its image with sh unless its shell
// says otherwise; docker-compose in its run service's image, read from the
// Compose file. Plugins known to run no tests are reported once.
func TestBuildkitePlugins(t *testing.T) {
	root := buildkiteRepository(t, `steps:
  - label: docker
    command: npm test
    plugins:
      - artifacts#v1.9.4:
          download: coverage/*
      - https://github.com/buildkite-plugins/docker-buildkite-plugin.git#v5.12.0:
          image: node:22
          shell: ["/bin/bash", "-e", "-c"]
  - label: compose
    command: npm test
    plugins:
      - seek-oss/aws-sm#v2.3.2:
          env:
            DD_API_KEY: dd-api-key
      - docker-compose#v5.5.0:
          run: app
          config: .buildkite/docker-compose.yml
  - label: built
    command: npm test
    plugins:
      - docker-compose#v5.5.0:
          run: built
          config: .buildkite/docker-compose.yml
  - label: build only
    plugins:
      - docker-compose#v5.5.0:
          build: app
          config: .buildkite/docker-compose.yml
  - label: annotate
    plugins:
      - junit-annotate#v2.6.0:
          artifacts: tmp/*.xml
`)
	writeFile(t, root, ".buildkite/docker-compose.yml", "services:\n  app:\n    image: ${NODE_IMAGE:-node:22-slim}\n    volumes:\n      - .:/app\n    environment:\n      - DD_API_KEY\n  built:\n    build: .\n")
	workflow := buildkiteWorkflow(t, root, buildkiteFile)
	docker := workflow.Jobs["docker"]
	require.Equal(t, "node:22", docker.Image)
	require.Equal(t, "bash", docker.Steps[0].Shell)
	compose := workflow.Jobs["compose"]
	require.Empty(t, compose.Image, "the agent or a .env file may set NODE_IMAGE")
	require.Equal(t, "sh", compose.Steps[0].Shell)
	require.Empty(t, workflow.Jobs["built"].Image)
	require.NotContains(t, workflow.Jobs, "build only")
	require.NotContains(t, workflow.Jobs, "annotate")
	require.Equal(t, []string{buildkiteFile + ": Buildkite plugins and steps that only fetch secrets, artifacts or caches, log in to registries, build images or annotate results run no tests and are outside this validation: artifacts, aws-sm, docker-compose (image build), junit-annotate."}, workflow.Caveats)
	require.Equal(t, []string{".buildkite/docker-compose.yml"}, workflow.Files)
	require.Contains(t, CIConfigurationFiles(root), ".buildkite/docker-compose.yml")

	for reference, want := range map[string]string{
		"docker#v5.12.0": "buildkite-plugins/docker",
		"docker-compose": "buildkite-plugins/docker-compose",
		"buildkite-plugins/docker-buildkite-plugin#v5":                       "buildkite-plugins/docker",
		"ssh://git@github.com/Gusto/cloudsmith-auth-buildkite-plugin.git#v2": "Gusto/cloudsmith-auth",
		"git@github.com:acme/tests-buildkite-plugin.git#main":                "acme/tests",
		"./.buildkite/plugins/cache-v2":                                      "./.buildkite/plugins/cache-v2",
	} {
		require.Equal(t, want, buildkitePluginName(reference), reference)
	}
}

// The setup snippet is recognized in exactly the form ddtest writes,
// guarded or not, with the loader registration inside the guard.
func TestBuildkiteRecognizesTheSetupSnippet(t *testing.T) {
	guarded := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	unguarded := "      - |\n        " + strings.ReplaceAll(buildkiteEscape(buildkiteSnippet{site: `"datadoghq.eu"`, languages: `"js"`, tracers: []string{`DD_SET_TRACER_VERSION_JS="5.133.0"`}, installer: buildkiteInstaller, checksum: buildkiteInstallerChecksum}.shell()), "\n", "\n        ") + "\n"
	loader := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ", esmImportCommand)
	pipeline := func(snippet string) string {
		return "steps:\n" + buildkiteDockerStep("test", "node:22", "      - npm ci\n"+snippet+"      - npm test\n")
	}
	for _, tc := range []struct {
		name, snippet string
		supported     bool
		problem       string
		inputs        map[string]string
	}{
		{"guarded", guarded, true, "", map[string]string{"languages": "js", "site": "datadoghq.com", "js-tracer-version": "5.133.0", "api_key": "$DD_API_KEY"}},
		{"unguarded", unguarded, true, "", map[string]string{"languages": "js", "site": "datadoghq.eu", "js-tracer-version": "5.133.0", "api_key": "$DD_API_KEY"}},
		{"loader", loader, true, "", nil},
		{"another checksum", strings.Replace(guarded, buildkiteInstallerChecksum, strings.Repeat("0", 64), 1), false, "", nil},
		{"another installer", strings.ReplaceAll(guarded, "install_test_visibility_v15.sh", "install_test_visibility_v14.sh"), false, "", nil},
		{"one URL changed", strings.Replace(guarded, "install_test_visibility_v15.sh", "install_test_visibility_v14.sh", 1), false, "in a form ddtest does not recognize", nil},
		{"prefix condition", strings.Replace(guarded, `if [ -n "$${DD_API_KEY:-}" ]; then`, `if [ -n "$${DD_API_KEY:-}" ] && [ "$$BUILDKITE_BRANCH" = main ]; then`, 1), false, "in a form ddtest does not recognize", nil},
		{"negated guard", strings.Replace(guarded, `if [ -n "$${DD_API_KEY:-}" ]; then`, `if ! [ -n "$${DD_API_KEY:-}" ]; then`, 1), false, "in a form ddtest does not recognize", nil},
		{"a command inside the guard", strings.Replace(guarded, "\n        else\n", "\n          npm test\n        else\n", 1), false, "in a form ddtest does not recognize", nil},
		{"another provider", strings.Replace(guarded, `PROVIDER="buildkite"`, `PROVIDER="gitlab"`, 1), false, "in a form ddtest does not recognize", nil},
		{"unescaped", strings.ReplaceAll(guarded, "$$", "$"), true, "without escaping the $", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRepository(t, pipeline(tc.snippet))
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			reference := "Datadog installer " + buildkiteInstallerURL.FindStringSubmatch(tc.snippet)[1]
			if strings.Contains(tc.problem, "does not recognize") {
				// It may run anything, tests included: an unresolved command.
				runs := circleRuns(job)
				require.Len(t, runs, 3)
				require.Contains(t, runs[1], "<unresolved: Buildkite step test: The step runs Datadog's installer in a form ddtest does not recognize")
				require.Nil(t, job.Steps[1].Setup)
				require.Contains(t, job.Steps[1].Run, "install_test_visibility")
				require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
				return
			}
			require.Equal(t, []string{"npm ci", "<setup " + reference + ">", "npm test"}, circleRuns(job))
			setup := job.Steps[1].Setup
			require.Equal(t, tc.supported, setup.Supported, setup.Problem)
			if tc.problem == "" {
				require.Empty(t, setup.Problem)
			} else {
				require.Contains(t, setup.Problem, tc.problem)
			}
			if tc.inputs != nil {
				require.Equal(t, tc.inputs, setup.Inputs)
			}
			if tc.supported && tc.problem == "" {
				require.Contains(t, job.Steps[2].Env["NODE_OPTIONS"], "-r ${{ env.DD_TRACE_PACKAGE }}", "the installer's exports reach later commands")
			}
		})
	}
	// The snippet inside a command block is a statement of its own.
	block := "steps:\n  - label: test\n    command: |\n      npm ci\n" + strings.ReplaceAll(strings.TrimPrefix(guarded, "      - |\n"), "\n  ", "\n") + "      npm test\n"
	root := buildkiteRepository(t, block)
	require.Equal(t, []string{"npm ci", "<setup Datadog installer install_test_visibility_v15.sh>", "npm test"}, circleRuns(buildkiteJobs(t, root, buildkiteFile)["test"]))
}

// The setup needs Bash and curl or wget where it runs, DD_API_KEY in a
// container's environment and never in the configuration.
func TestBuildkiteSetupProblems(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	for _, tc := range []struct{ name, pipeline, problem string }{
		{"agent host", "steps:\n  - label: test\n    commands:\n" + snippet + "      - npm test\n", ""},
		{"alpine", "steps:\n" + buildkiteDockerStep("test", "node:22-alpine", snippet+"      - npm test\n"), "has no Bash"},
		{"slim", "steps:\n" + buildkiteDockerStep("test", "node:22-slim", snippet+"      - npm test\n"), "has neither curl nor wget"},
		{"slim with curl", "steps:\n" + buildkiteDockerStep("test", "node:22-slim", "      - apt-get update && apt-get install -y curl\n"+snippet+"      - npm test\n"), ""},
		{"unresolved image", "steps:\n" + buildkiteDockerStep("test", "$$NODE_IMAGE", snippet+"      - npm test\n"), "depends on a variable or matrix value ddtest cannot resolve"},
		{"key not passed", "steps:\n  - label: test\n    commands:\n" + snippet + "      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n", "DD_API_KEY never reaches the Datadog setup snippet"},
		{"key in the step env", "steps:\n  - label: test\n    env:\n      DD_API_KEY: abc123\n    commands:\n" + snippet + "      - npm test\n", "never commit the key"},
		{"key in the pipeline env", "env:\n  DD_API_KEY: $SECRET\nsteps:\n  - label: test\n    commands:\n" + snippet + "      - npm test\n", "never commit the key"},
		{"key in the plugin env", "steps:\n  - label: test\n    commands:\n" + snippet + "      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          environment:\n            - DD_API_KEY=abc123\n", "never commit the key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRepository(t, tc.pipeline)
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			index := slices.IndexFunc(job.Steps, func(step runtimeStep) bool { return step.Setup != nil })
			require.GreaterOrEqual(t, index, 0)
			if tc.problem == "" {
				require.Empty(t, job.Steps[index].Setup.Problem)
				require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
				return
			}
			require.Contains(t, job.Steps[index].Setup.Problem, tc.problem)
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
		})
	}
}

// The shared completeness check: the snippet selects the language, with an
// exact tracer and the selected site, before every test command in the
// same step.
func TestBuildkiteJavaScriptCompleteness(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	complete := "steps:\n" + buildkiteDockerStep("test", "node:22", "      - npm ci\n"+snippet+"      - npm test\n")
	for _, tc := range []struct {
		name, pipeline, site string
		configured           bool
	}{
		{"complete", complete, "", true},
		{"selected site", complete, "datadoghq.com", true},
		{"other site", complete, "datadoghq.eu", false},
		{"missing", strings.Replace(complete, snippet, "", 1), "", false},
		{"after the tests", strings.Replace(strings.Replace(complete, snippet, "", 1), "      - npm test\n", "      - npm test\n"+snippet, 1), "", false},
		{"in another step", "steps:\n  - label: setup\n    commands:\n" + snippet + "  - wait\n" + strings.Replace(complete, "steps:\n", "", 1)[0:0] + buildkiteDockerStep("test", "node:22", "      - npm test\n"), "", false},
		{"floating tracer", strings.Replace(complete, `DD_SET_TRACER_VERSION_JS="5.133.0"`, `DD_SET_TRACER_VERSION_JS="latest"`, 1), "", false},
		{"other language", strings.Replace(complete, `LANGUAGES="js"`, `LANGUAGES="python"`, 1), "", false},
		{"all languages", strings.Replace(complete, `LANGUAGES="js"`, `LANGUAGES="all"`, 1), "", true},
		{"disabled test step", complete + "  - label: never\n    if: \"false\"\n    command: npm test\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRepository(t, tc.pipeline)
			require.Equal(t, tc.configured, javascriptDiscovery{}.ciSetupConfigured(root, "jest", tc.site))
		})
	}
}

// planBuildkite plans the JavaScript setup in a Jest repository whose
// Buildkite pipeline is pipeline.
func planBuildkite(t *testing.T, framework, pipeline string, files map[string]string) (*SetupPlan, string, error) {
	t.Helper()
	root := buildkiteRepository(t, pipeline)
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	plan, err := PlanJavaScriptSetup(root, framework, "5.133.0", "datadoghq.com", ">=18")
	return plan, root, err
}

// onboard --apply inserts the snippet before the first test command, in
// the same step: a new commands entry before a listed command, and a second
// apply changes nothing.
func TestBuildkiteApplyInsertsTheSnippetBeforeTheFirstTest(t *testing.T) {
	pipeline := "steps:\n  - label: \":jest: Test\"\n    commands:\n      - npm ci\n      - npm test # unit tests\n      - npm run report\n"
	plan, root, err := planBuildkite(t, "jest", pipeline, nil)
	require.NoError(t, err)
	require.Equal(t, "steps:\n  - label: \":jest: Test\"\n    commands:\n      - npm ci\n"+buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test # unit tests\n      - npm run report\n", plannedFile(t, plan, buildkiteFile))
	require.Contains(t, plannedFile(t, plan, ".gitignore"), "/.testoptimization/")
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "a second apply changes nothing")
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
	_, err = PlanJavaScriptSetup(root, "jest", "5.140.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Existing Datadog setup differs")
}

// A command written as one block gets the snippet's lines inside the block;
// a single command becomes a list; a docker plugin receives DD_API_KEY in
// its environment list, which is added when missing.
func TestBuildkiteApplyEditsBlocksSingleCommandsAndContainers(t *testing.T) {
	snippetLines := func(indent string) string {
		return indent + strings.ReplaceAll(buildkiteEscape(buildkiteRequestSnippet(ciSetupRequest{Language: "js", Site: "datadoghq.com", TracerInput: "js-tracer-version", TracerVersion: "5.133.0"}).shell()), "\n", "\n"+indent) + "\n"
	}
	for _, tc := range []struct{ name, pipeline, want string }{
		{"block", "steps:\n  - label: test\n    command: |\n      npm ci\n      echo \"--- test\"\n      npm test\n", "steps:\n  - label: test\n    command: |\n      npm ci\n      echo \"--- test\"\n" + snippetLines("      ") + "      npm test\n"},
		{"listed block", "steps:\n  - label: test\n    commands:\n      - |\n        npm ci\n        npm test\n", "steps:\n  - label: test\n    commands:\n      - |\n        npm ci\n" + snippetLines("        ") + "        npm test\n"},
		{"single command", "steps:\n  - label: test\n    command: \"npm ci && npm test\"\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          workdir: /app\n", "steps:\n  - label: test\n    command:\n" + buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ") + "      - \"npm ci && npm test\"\n    plugins:\n      - docker#v5.12.0:\n          environment:\n            - DD_API_KEY\n          image: node:22\n          workdir: /app\n"},
		{"existing environment", "steps:\n  - label: test\n    commands:\n      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          environment:\n            - CI\n", "steps:\n  - label: test\n    commands:\n" + buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ") + "      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          environment:\n            - CI\n            - DD_API_KEY\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, root, err := planBuildkite(t, "jest", tc.pipeline, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, plannedFile(t, plan, buildkiteFile))
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
		})
	}
}

// Vitest gets the ESM loader registration inside the snippet's guard.
func TestBuildkiteApplyRegistersTheESMLoader(t *testing.T) {
	plan, root, err := planBuildkite(t, "vitest", "steps:\n  - label: test\n    commands:\n      - npx vitest run\n", map[string]string{"package.json": `{"devDependencies": {"vitest": "3.2.0"}}`})
	require.NoError(t, err)
	config := plannedFile(t, plan, buildkiteFile)
	require.Contains(t, config, "          fi\n          if [ -n \"$${DD_TRACE_ESM_IMPORT:-}\" ]; then export NODE_OPTIONS=\"$${NODE_OPTIONS:-} --import $$DD_TRACE_ESM_IMPORT\"; fi\n        else\n")
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "vitest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured())
	job := buildkiteJobs(t, root, buildkiteFile)["test"]
	require.Contains(t, job.Steps[len(job.Steps)-1].Env["NODE_OPTIONS"], "--import ${{ env.DD_TRACE_ESM_IMPORT }}")
}

// Matrix combinations share one command and get one insertion; commands
// shared through an anchor, a folded command, an image without Bash and a
// container ddtest cannot pass the key into are refused.
func TestBuildkiteApplySharedCommands(t *testing.T) {
	t.Run("matrix", func(t *testing.T) {
		plan, _, err := planBuildkite(t, "jest", "steps:\n  - label: \"node {{matrix}}\"\n    matrix: [\"20\", \"22\"]\n    commands:\n      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: \"node:{{matrix}}\"\n", nil)
		require.NoError(t, err)
		config := plannedFile(t, plan, buildkiteFile)
		require.Equal(t, 1, strings.Count(config, buildkiteInstallerChecksum))
		require.Equal(t, 1, strings.Count(config, "- DD_API_KEY"))
	})
	for _, tc := range []struct{ name, pipeline, reason string }{
		{"anchor", "steps:\n  - label: unit\n    commands: &tests\n      - npm test\n  - label: more\n    commands: *tests\n", "YAML anchor and alias"},
		{"anchored plugin", "plugins: &docker\n  - docker#v5.12.0:\n      image: node:22\nsteps:\n  - label: unit\n    commands:\n      - npm test\n    plugins: *docker\n", "DD_API_KEY never reaches"},
		{"folded", "steps:\n  - label: unit\n    command: >\n      npm ci &&\n      npm test\n", "write the test command as its own commands entry"},
		{"later statement", "steps:\n  - label: unit\n    commands:\n      - \"npm ci; npm test\"\n", "is not the first command of its entry"},
		{"alpine", "steps:\n" + buildkiteDockerStep("unit", "node:22-alpine", "      - npm test\n"), "has no Bash"},
		{"flow plugin", "steps:\n  - label: unit\n    commands:\n      - npm test\n    plugins:\n      - docker#v5.12.0: {image: node:22}\n", "add DD_API_KEY to the plugin's environment list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := planBuildkite(t, "jest", tc.pipeline, nil)
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, tc.reason)
		})
	}
}

func TestBuildkiteLanguageSupport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, buildkiteFile, "steps:\n  - command: pytest\n")
	useCIProvider(t, "buildkite")
	for language, supported := range map[string]bool{"javascript": true, "ruby": true, "python": false, "java": true} {
		err := CheckCIProviderSupport(root, language, "framework")
		if supported {
			require.NoError(t, err, language)
			continue
		}
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, err, &unsupported, language)
		require.Equal(t, "Buildkite", unsupported.Provider)
	}
}

// Several providers running tests ask the user which one to configure.
func TestBuildkiteProviderChoice(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, buildkiteFile, "steps:\n  - command: npm test\n")
	UseCIProvider("")
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "buildkite"}, choice.Providers)
	writeFile(t, root, buildkiteFile, "steps:\n  - command: echo deploy\n")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected)
}

// The committed fixture is heap/eslint-plugin-heap's pipeline: three steps
// in the docker plugin, of which one runs Jest as a single command string.
func TestBuildkiteJavaScriptFixture(t *testing.T) {
	root := copyFixture(t, "buildkite-javascript")
	useCIProvider(t, "buildkite")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "jest", scope.Commands[0].Command)
	plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	config := plannedFile(t, plan, buildkiteFile)
	require.Contains(t, config, "  - label: \":jest: Jest Tests\"\n    command:\n      - |\n        if [ -n \"$${DD_API_KEY:-}\" ]; then\n")
	require.Contains(t, config, "      - \"npm install && npm test\"\n    plugins:\n      - docker#v3.2.0:\n          environment:\n            - DD_API_KEY\n          image: \"node:22\"\n")
	require.Equal(t, 1, strings.Count(config, buildkiteInstallerChecksum))
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "a second apply changes nothing")
	// The ESLint and build steps never run Jest: they are reported, and the
	// runtime check evaluates the Jest step only.
	scope, err = DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), ".buildkite/pipeline.yml / :typescript: Build: CI job without the Datadog setup that never runs jest")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, result.Status, result)
}

// A step without an image runs on the agent host, whose Node ddtest cannot
// know: the runtime check reports it for review instead of certifying it.
func TestBuildkiteAgentHostRuntimeIsUnknown(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test\n")
	require.Empty(t, buildkiteJobs(t, root, buildkiteFile)["test"].Image)
	check := CheckCIRuntimes(t.Context(), root, "jest")
	require.NotEqual(t, StatusCompatible, check.Status)
	var reasons []string
	for _, job := range check.Jobs {
		reasons = append(reasons, job.Status+": "+job.Code)
	}
	require.NotContains(t, strings.Join(reasons, "\n"), StatusCompatible+":")
}

// yamlNode parses text into its document's root node.
func yamlNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(text), &document))
	return document.Content[0]
}

// The image attribute selects the container of hosted and Kubernetes
// agents; buildkite-agent artifact, annotate and meta-data commands run
// nothing ddtest replays; an assignment reading a value only pipeline upload
// knows leaves later statements unresolved.
func TestBuildkiteImagesRecordsAndUploadValues(t *testing.T) {
	root := buildkiteRepository(t, `image: node:20
steps:
  - label: pipeline image
    commands:
      - buildkite-agent artifact download "dist/*" .
      - npm test
      - buildkite-agent annotate --style success < report.md
  - label: step image
    image: node:22
    command: npm test
  - label: entrypoint
    command: npm test
    plugins:
      - docker#v5.12.0:
          image: node:22
          entrypoint: /usr/local/bin/run
  - label: no checkout
    command: npm test
    plugins:
      - docker#v5.12.0:
          image: node:22
          mount-checkout: false
  - label: upload value
    env:
      SUITE: unit
    commands:
      - export JEST_SUITE=$SUITE
      - npm test
`)
	jobs := buildkiteJobs(t, root, buildkiteFile)
	require.Equal(t, "node:20", jobs["pipeline image"].Image)
	require.Equal(t, []string{"npm test"}, circleRuns(jobs["pipeline image"]))
	require.Equal(t, "node:22", jobs["step image"].Image)
	require.Contains(t, circleRuns(jobs["entrypoint"])[0], "the docker plugin's entrypoint /usr/local/bin/run receives the commands")
	require.Contains(t, circleRuns(jobs["no checkout"])[0], "the docker plugin does not mount the checkout")
	runs := circleRuns(jobs["upload value"])
	require.Len(t, runs, 2)
	require.Contains(t, runs[1], "the command reads $SUITE", "the export's value reaches the tests")
}

// A Compose service's environment passes DD_API_KEY from the agent into the
// container, as the plugin's environment list does.
func TestBuildkiteComposeServicePassesTheKey(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	for _, tc := range []struct{ name, environment, problem string }{
		{"service list", "    environment:\n      - DD_API_KEY\n", ""},
		{"service mapping", "    environment:\n      DD_API_KEY: ${DD_API_KEY}\n", ""},
		{"literal in the service", "    environment:\n      DD_API_KEY: abc123\n", "never commit the key"},
		{"not passed", "", "DD_API_KEY never reaches the Datadog setup snippet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+snippet+"      - npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n")
			writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n"+tc.environment)
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			require.Equal(t, "node:22", job.Image)
			setup := job.Steps[0].Setup
			require.NotNil(t, setup)
			if tc.problem == "" {
				require.Empty(t, setup.Problem)
				return
			}
			require.Contains(t, setup.Problem, tc.problem)
		})
	}
}

// Findings of the PR review: each case leaves the step unresolved, refuses
// the setup, or reads what Buildkite reads.
func TestBuildkiteReviewFindings(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	t.Run("an empty key in the container's environment", func(t *testing.T) {
		for _, tc := range []struct{ name, plugin, compose string }{
			{"docker list", "      - docker#v5.12.0:\n          image: node:22\n          environment:\n            - DD_API_KEY=\n", ""},
			{"compose list", "      - docker-compose#v5.5.0:\n          run: app\n", "    environment:\n      - DD_API_KEY=\n"},
			{"compose mapping", "      - docker-compose#v5.5.0:\n          run: app\n", "    environment:\n      DD_API_KEY: \"\"\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+snippet+"      - npm test\n    plugins:\n"+tc.plugin)
				writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n"+tc.compose)
				setup := buildkiteJobs(t, root, buildkiteFile)["test"].Steps[0].Setup
				require.NotNil(t, setup)
				require.Contains(t, setup.Problem, "DD_API_KEY set to an empty value")
				require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
			})
		}
		root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+snippet+"      - npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n")
		writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    environment:\n      DD_API_KEY:\n")
		require.Empty(t, buildkiteJobs(t, root, buildkiteFile)["test"].Steps[0].Setup.Problem, "a null value passes the agent's key through")
	})
	t.Run("uploads resolve from the working directory", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - command: npm test\n")
		writeFile(t, root, "packages/app/.buildkite/pipeline.yml", "steps:\n  - command: npm test\n")
		writeFile(t, root, ".buildkite/upload.yml", "steps:\n  - commands:\n      - cd packages/app\n      - buildkite-agent pipeline upload .buildkite/pipeline.yml\n")
		require.Contains(t, circleRuns(buildkiteJobs(t, root, ".buildkite/upload.yml")["step 1"])[0], "generates or uploads steps with buildkite-agent pipeline upload")
	})
	t.Run("a quoted upload path is literal", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - command: npm test\n")
		writeFile(t, root, ".buildkite/upload.yml", "steps:\n  - command: buildkite-agent pipeline upload \".buildkite/pipeline.yml\"\n  - command: buildkite-agent pipeline upload \"$$FILE\"\n")
		jobs := buildkiteJobs(t, root, ".buildkite/upload.yml")
		require.Empty(t, circleRuns(jobs["step 1"]))
		require.Contains(t, circleRuns(jobs["step 2"])[0], "generates or uploads steps")
	})
	t.Run("an upload without arguments reads Buildkite's default file", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - command: npm test\n")
		require.NoError(t, os.Remove(filepath.Join(root, buildkiteFile)))
		writeFile(t, root, "buildkite/pipeline.yaml", "steps:\n  - command: npm test\n")
		writeFile(t, root, ".buildkite/upload.yml", "steps:\n  - command: buildkite-agent pipeline upload\n")
		workflow := buildkiteWorkflow(t, root, ".buildkite/upload.yml")
		require.Empty(t, circleRuns(workflow.Jobs["step 1"]))
		require.Equal(t, []string{"Buildkite uploads buildkite/pipeline.yaml with buildkite-agent pipeline upload; ddtest reads it as a pipeline of its own."}, workflow.Review)
		// With two default files the upload fails: ddtest cannot tell.
		writeFile(t, root, "buildkite.yml", "steps:\n  - command: npm test\n")
		require.Contains(t, circleRuns(buildkiteJobs(t, root, ".buildkite/upload.yml")["step 1"])[0], "generates or uploads steps")
	})
	t.Run("a record command running a substitution", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n      - buildkite-agent annotate \"$$(npm test)\"\n")
		require.Equal(t, []string{`buildkite-agent annotate "$(npm test)"`}, circleRuns(buildkiteJobs(t, root, buildkiteFile)["test"]))
	})
	t.Run("an unrecognized installer statement running tests", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n      - curl -fsSL https://install.datadoghq.com/scripts/install_test_visibility_v15.sh | bash && npm test\n")
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Contains(t, job.Steps[0].Run, "npm test")
		require.Contains(t, job.Steps[0].ResolutionError, "in a form ddtest does not recognize")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
	})
	t.Run("Windows rows of a matrix", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: \"test {{matrix.os}}\"\n    command: npm test\n    agents:\n      queue: \"{{matrix.os}}\"\n    matrix:\n      setup:\n        os: [linux, windows]\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Equal(t, []string{"npm test"}, circleRuns(jobs["test linux"]))
		require.Contains(t, circleRuns(jobs["test windows"])[0], "runs on Windows agents")
	})
	t.Run("Compose images read the step's env", func(t *testing.T) {
		root := buildkiteRepository(t, "env:\n  NODE_IMAGE: node:20\nsteps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n")
		writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: ${NODE_IMAGE:-node:22}\n")
		require.Equal(t, "node:20", buildkiteJobs(t, root, buildkiteFile)["test"].Image)
	})
	t.Run("Compose files named by matrix values", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: \"test {{matrix}}\"\n    command: npm test\n    matrix: [a, b]\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n          config: \"compose.{{matrix}}.yml\"\n")
		writeFile(t, root, "compose.a.yml", "services:\n  app:\n    image: node:20\n")
		writeFile(t, root, "compose.b.yml", "services:\n  app:\n    image: node:22\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Equal(t, "node:20", jobs["test a"].Image)
		require.Equal(t, "node:22", jobs["test b"].Image)
	})
	t.Run("matrices are bounded before they are built", func(t *testing.T) {
		values := "[" + strings.TrimSuffix(strings.Repeat("x,", 20), ",") + "]"
		node := yamlNode(t, "setup:\n  a: "+values+"\n  b: "+values+"\n  c: "+values+"\n  d: "+values+"\n  e: "+values+"\n  f: "+values+"\n")
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := buildkiteMatrix(node)
		runtime.ReadMemStats(&after)
		require.EqualError(t, err, "matrix expands to more than 200 jobs")
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20), "64 million combinations are never built")
		// A skipping adjustment brings a product just over the limit back
		// to it.
		var many []string
		for i := range 67 {
			many = append(many, strconv.Itoa(i))
		}
		rows, err := buildkiteMatrix(yamlNode(t, "setup:\n  a: ["+strings.Join(many, ", ")+"]\n  b: [x, y, z]\nadjustments:\n  - with: {a: \"0\", b: x}\n    skip: true\n"))
		require.NoError(t, err)
		require.Len(t, rows, 200)
	})
}

// Findings of the second review round: what a container and the step's
// shell actually receive, and which files are pipelines.
func TestBuildkiteContainerAndShellFindings(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	t.Run("a pipeline file whose name mentions compose", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - command: echo lint\n")
		writeFile(t, root, ".buildkite/pipeline-compose-tests.yml", "steps:\n  - label: tests\n    command: npm test\n")
		writeFile(t, root, ".buildkite/docker-compose.ci.yml", "version: \"3\"\nservices:\n  app:\n    image: node:22\n")
		files, err := buildkite{}.configurationFiles(root)
		require.NoError(t, err)
		require.Equal(t, []string{".buildkite/pipeline-compose-tests.yml", buildkiteFile}, files)
		require.Equal(t, []string{"npm test"}, circleRuns(buildkiteJobs(t, root, ".buildkite/pipeline-compose-tests.yml")["tests"]))
	})
	t.Run("a Compose service entrypoint replacing the commands", func(t *testing.T) {
		for entrypoint, unresolved := range map[string]bool{"[sh, -c, 'npm run integration']": true, "\"\"": false, "[tini, --]": false, "docker-entrypoint.sh": false} {
			root := buildkiteRepository(t, "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n")
			writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n    entrypoint: "+entrypoint+"\n")
			runs := circleRuns(buildkiteJobs(t, root, buildkiteFile)["test"])
			if unresolved {
				require.Contains(t, runs[0], "the Docker Compose service app's entrypoint sh -c npm run integration receives the commands", entrypoint)
			} else {
				require.Equal(t, []string{"npm test"}, runs, entrypoint)
			}
		}
		root := buildkiteRepository(t, "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          entrypoint: \"\"\n")
		require.Equal(t, []string{"npm test"}, circleRuns(buildkiteJobs(t, root, buildkiteFile)["test"]), "an emptied entrypoint runs the commands")
	})
	t.Run("the step clears DD_API_KEY before the snippet", func(t *testing.T) {
		for _, clear := range []string{"export DD_API_KEY=", "unset DD_API_KEY"} {
			root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n      - "+clear+"\n"+snippet+"      - npm test\n")
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			index := slices.IndexFunc(job.Steps, func(step runtimeStep) bool { return step.Setup != nil })
			require.Contains(t, job.Steps[index].Setup.Problem, "clears DD_API_KEY", clear)
			require.NotContains(t, job.Steps[len(job.Steps)-1].Env["NODE_OPTIONS"], "DD_TRACE_PACKAGE", "no exports from a setup that skips")
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
		}
	})
	t.Run("the container's environment", func(t *testing.T) {
		root := buildkiteRepository(t, `env:
  SHARD: "1"
steps:
  - label: docker
    env:
      NOT_PASSED: "1"
    command: npm test
    plugins:
      - docker#v5.12.0:
          image: node:22
          environment:
            - NODE_ENV=production
            - SHARD
            - FROM_UPLOAD=$UNKNOWN
  - label: compose
    command: npm test
    plugins:
      - docker-compose#v5.5.0:
          run: app
          environment:
            - TZ=UTC
  - label: propagated
    env:
      FROM_STEP: "1"
    command: npm test
    plugins:
      - docker#v5.12.0:
          image: node:22
          propagate-environment: true
`)
		writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n    environment:\n      LOG: debug\n      TZ: Europe/Paris\n      SHARD_NAME: shard-${SHARD}\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Equal(t, map[string]string{"NODE_ENV": "production", "SHARD": "1", "FROM_UPLOAD": unresolvedCIValue("$UNKNOWN")}, jobs["docker"].Env)
		require.Equal(t, map[string]string{"LOG": "debug", "TZ": "UTC", "SHARD_NAME": "shard-1"}, jobs["compose"].Env)
		require.Equal(t, map[string]string{"SHARD": "1", "FROM_STEP": "1"}, jobs["propagated"].Env)
	})
	t.Run("a Compose working directory inside the mounted checkout", func(t *testing.T) {
		for directory, want := range map[string]string{"/app/packages/ui": "packages/ui", "/app": "", "/srv": "outside the checkout it mounts at /app"} {
			root := buildkiteRepository(t, "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          run: app\n")
			writeFile(t, root, "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app:cached\n    working_dir: "+directory+"\n")
			step := buildkiteJobs(t, root, buildkiteFile)["test"].Steps[0]
			if strings.Contains(want, " ") {
				require.Contains(t, step.ResolutionError, want, directory)
				continue
			}
			require.Empty(t, step.ResolutionError, directory)
			require.Equal(t, want, step.WorkingDirectory, directory)
		}
	})
	t.Run("propagate-environment passes the pipeline's key", func(t *testing.T) {
		root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+snippet+"      - npm test\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          propagate-environment: true\n")
		workflow := buildkiteWorkflow(t, root, buildkiteFile)
		require.Empty(t, workflow.Jobs["test"].Steps[0].Setup.Problem)
		require.Contains(t, strings.Join(workflow.Review, "\n"), "never a key an agent hook or secrets plugin exports")
	})
	t.Run("a step's agents override the pipeline's", func(t *testing.T) {
		root := buildkiteRepository(t, "agents:\n  queue: windows\nsteps:\n  - label: linux\n    command: npm test\n    agents:\n      queue: linux\n  - label: inherited\n    command: npm test\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Equal(t, []string{"npm test"}, circleRuns(jobs["linux"]))
		require.Contains(t, circleRuns(jobs["inherited"])[0], "runs on Windows agents")
	})
}

// Findings of the third review round: upload interpolation in agent
// targeting and the pipeline image, Compose paths relative to the Compose
// file, build-only Compose steps and the plugin's DD_API_KEY precedence.
func TestBuildkiteThirdReviewFindings(t *testing.T) {
	snippet := buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")
	t.Run("agent targeting reads upload-time values", func(t *testing.T) {
		root := buildkiteRepository(t, "env:\n  TARGET_QUEUE: windows-large\n  LINUX_QUEUE: linux\nsteps:\n  - label: known windows\n    command: npm test\n    agents:\n      queue: \"$TARGET_QUEUE\"\n  - label: known linux\n    command: npm test\n    agents:\n      queue: \"${LINUX_QUEUE:-default}\"\n  - label: unknown\n    command: npm test\n    agents:\n      queue: \"$QUEUE_FROM_AGENT\"\n  - label: escaped\n    command: npm test\n    agents:\n      queue: \"$$LITERAL\"\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Contains(t, circleRuns(jobs["known windows"])[0], "runs on Windows agents")
		require.Equal(t, []string{"npm test"}, circleRuns(jobs["known linux"]))
		require.Contains(t, circleRuns(jobs["unknown"])[0], "its agent targeting queue=$QUEUE_FROM_AGENT reads $QUEUE_FROM_AGENT, which buildkite-agent pipeline upload replaces with a value ddtest cannot know, so the step may run on Windows agents")
		require.Equal(t, []string{"npm test"}, circleRuns(jobs["escaped"]), "an escaped $$ is a literal queue name")
		plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
		require.NoError(t, err)
		config := plannedFile(t, plan, buildkiteFile)
		require.Contains(t, config, "  - label: unknown\n    command: npm test\n", "no Bash setup in a step that may run on Windows")
		require.Contains(t, config, "  - label: known windows\n    command: npm test\n")
		require.Equal(t, 2, strings.Count(config, buildkiteInstallerChecksum), "only the Linux steps get the setup")
	})
	t.Run("the pipeline image is interpolated once", func(t *testing.T) {
		root := buildkiteRepository(t, "env:\n  NODE_IMAGE: node:22\nimage: $$NODE_IMAGE\nsteps:\n  - label: escaped\n    commands:\n"+snippet+"      - npm test\n  - label: known\n    image: $NODE_IMAGE\n    command: npm test\n")
		jobs := buildkiteJobs(t, root, buildkiteFile)
		require.Empty(t, jobs["escaped"].Image, "$$NODE_IMAGE is the literal $NODE_IMAGE")
		require.Equal(t, "$$NODE_IMAGE", jobs["escaped"].ImageTemplate)
		setup := slices.IndexFunc(jobs["escaped"].Steps, func(step runtimeStep) bool { return step.Setup != nil })
		require.Contains(t, jobs["escaped"].Steps[setup].Setup.Problem, "its image $$NODE_IMAGE depends on a variable or matrix value ddtest cannot resolve")
		require.Equal(t, "node:22", jobs["known"].Image)
	})
	t.Run("Compose paths are relative to the Compose file", func(t *testing.T) {
		root := buildkiteComposeRepository(t, "      - npm test\n", "ci/docker-compose.yml", "services:\n  app:\n    build: .\n    working_dir: /app\n    volumes:\n      - .:/app\n", map[string]string{"ci/Dockerfile": "FROM node:20\n", "ci/package.json": `{"scripts":{"test":"jest"}}`})
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Equal(t, "ci", job.Steps[0].WorkingDirectory, ". is ci/, the Compose file's directory")
		require.Equal(t, "node:20", job.Image, "the build context is ci/ too")
		root = buildkiteComposeRepository(t, "      - npm test\n", "ci/docker-compose.yml", "services:\n  app:\n    build:\n      context: ..\n      dockerfile: docker/Dockerfile\n    volumes:\n      - ..:/src\n", map[string]string{"docker/Dockerfile": "FROM node:22\nWORKDIR /src/packages/ui\n", "packages/ui/package.json": "{}"})
		job = buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Equal(t, "node:22", job.Image, "dockerfile is relative to the context, the repository root")
		require.Equal(t, "packages/ui", job.Steps[0].WorkingDirectory)
	})
	t.Run("a build-only Compose step runs its commands on the agent", func(t *testing.T) {
		pipeline := "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v5.5.0:\n          build: app\n"
		plan, root, err := planBuildkite(t, "jest", pipeline, map[string]string{"docker-compose.yml": "services:\n  app:\n    build: .\n"})
		require.NoError(t, err)
		config := plannedFile(t, plan, buildkiteFile)
		require.Contains(t, config, snippet+"      - npm test\n")
		require.NotContains(t, config, "DD_API_KEY\n          build", "the agent host already has the key")
		require.NotContains(t, config, "environment:")
		_, err = plan.apply(true, os.Rename)
		require.NoError(t, err)
		require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"), "apply and the re-check agree")
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		require.Empty(t, job.Image)
	})
	t.Run("the plugin's DD_API_KEY overrides the Compose service's", func(t *testing.T) {
		for compose, problem := range map[string]string{
			"    environment:\n      DD_API_KEY: \"\"\n":   "",
			"    environment:\n      - DD_API_KEY=\n":      "",
			"    environment:\n      DD_API_KEY: abc123\n": "never commit the key",
		} {
			root := buildkiteComposeRepository(t, snippet+"      - npm test\n", "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n"+compose, nil)
			job := buildkiteJobs(t, root, buildkiteFile)["test"]
			setup := slices.IndexFunc(job.Steps, func(step runtimeStep) bool { return step.Setup != nil })
			if problem == "" {
				require.Empty(t, job.Steps[setup].Setup.Problem, compose)
				continue
			}
			require.Contains(t, job.Steps[setup].Setup.Problem, problem, compose)
		}
		root := buildkiteComposeRepository(t, snippet+"      - npm test\n", "docker-compose.yml", "services:\n  app:\n    image: node:22\n    volumes:\n      - .:/app\n    environment:\n      - DD_API_KEY\n", nil)
		writeFile(t, root, buildkiteFile, strings.Replace(readFixtureFile(t, root, buildkiteFile), "            - DD_API_KEY\n", "            - DD_API_KEY=\n", 1))
		job := buildkiteJobs(t, root, buildkiteFile)["test"]
		setup := slices.IndexFunc(job.Steps, func(step runtimeStep) bool { return step.Setup != nil })
		require.Contains(t, job.Steps[setup].Setup.Problem, "receives DD_API_KEY set to an empty value", "the plugin's empty value wins over the service's pass-through")
	})
}
