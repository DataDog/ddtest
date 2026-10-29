// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// circleRepository is a Jest repository whose only CI is the CircleCI
// configuration config; the test runs in it with CircleCI selected.
func circleRepository(t *testing.T, config string) string {
	t.Helper()
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, circleCIFile, config)
	t.Chdir(root)
	useCIProvider(t, "circleci")
	return root
}

// circleJobs reads the configuration's jobs.
func circleJobs(t *testing.T, root string) map[string]runtimeJob {
	t.Helper()
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, workflows, 1)
	require.Equal(t, circleCIFile, workflows[0].Path)
	return workflows[0].Jobs
}

// circleRuns lists a job's run commands, unresolved steps as their reason.
func circleRuns(job runtimeJob) []string {
	var commands []string
	for _, step := range job.Steps {
		switch {
		case step.Setup != nil:
			commands = append(commands, "<setup "+step.Setup.Reference+">")
		case step.ResolutionError != "":
			commands = append(commands, "<unresolved: "+step.ResolutionError+">")
		case step.Run != "":
			commands = append(commands, step.Run)
		}
	}
	return commands
}

const circleOrbStep = "      - test-optimization-circleci-orb/autoinstrument:\n          languages: js\n          site: datadoghq.com\n          js_tracer_version: 5.133.0\n"

func circleWorkflow(jobs string) string {
	return "\nworkflows:\n  main:\n    jobs:\n" + jobs
}

func TestCircleCIConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	files, err := circleCI{}.configurationFiles(root)
	require.NoError(t, err)
	require.Empty(t, files)
	writeFile(t, root, ".circleci/config.yaml", "version: 2.1\n")
	files, err = circleCI{}.configurationFiles(root)
	require.NoError(t, err)
	require.Empty(t, files, "CircleCI reads config.yml only")
	writeFile(t, root, circleCIFile, "invalid: [")
	files, err = circleCI{}.configurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{circleCIFile}, files)
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "circleci", selected, "listing never parses")
}

// Executors, their parameters, reusable commands and job environment
// resolve into each job a workflow runs.
func TestCircleCIExecutorsCommandsAndParameters(t *testing.T) {
	root := circleRepository(t, `version: 2.1
executors:
  node:
    parameters:
      tag:
        type: string
        default: "22.11"
    docker:
      - image: cimg/node:<< parameters.tag >>
        environment:
          FROM_IMAGE: "1"
    environment:
      FROM_EXECUTOR: "1"
    working_directory: ~/repo
commands:
  install:
    parameters:
      flags:
        type: string
        default: ""
    steps:
      - restore_cache:
          keys:
            - deps-{{ checksum "package-lock.json" }}
      - run: npm ci << parameters.flags >>
jobs:
  test:
    parameters:
      suite:
        type: string
        default: unit
    executor:
      name: node
      tag: "20.18"
    environment:
      SUITE: << parameters.suite >>
    steps:
      - checkout
      - install:
          flags: --ignore-scripts
      - run:
          name: Tests
          command: npm test
          environment:
            STEP: "1"
          working_directory: ~/repo/packages/app
`+circleWorkflow("      - test:\n          suite: integration\n"))
	job := circleJobs(t, root)["main/test"]
	require.Equal(t, "cimg/node:20.18", job.Image)
	require.Equal(t, "cimg/node:<< parameters.tag >>", job.ImageTemplate)
	require.Equal(t, map[string]string{"FROM_IMAGE": "1", "FROM_EXECUTOR": "1", "SUITE": "integration"}, job.Env)
	require.Equal(t, []string{"npm ci --ignore-scripts", "npm test"}, circleRuns(job))
	require.Equal(t, "packages/app", job.Steps[1].WorkingDirectory)
	require.Equal(t, map[string]string{"STEP": "1"}, job.Steps[1].Env)
	require.Equal(t, "bash", job.Steps[1].Shell)
}

// A workflow matrix runs one job per combination, without excluded ones,
// named as CircleCI names them.
func TestCircleCIMatrix(t *testing.T) {
	config := `version: 2.1
jobs:
  test:
    parameters:
      node:
        type: string
      os:
        type: string
        default: linux
    docker:
      - image: cimg/node:<< parameters.node >>
    steps:
      - run: npm test
` + circleWorkflow(`      - test:
          matrix:
            parameters:
              node: ["20.18", "22.11"]
              os: [linux, arm]
            exclude:
              - node: "20.18"
                os: arm
      - test:
          name: named-<< matrix.node >>
          matrix:
            parameters:
              node: ["18.20"]
`)
	jobs := circleJobs(t, circleRepository(t, config))
	require.ElementsMatch(t, []string{"main/test-20.18-linux", "main/test-22.11-arm", "main/test-22.11-linux", "main/named-18.20"}, slices.Collect(mapKeys(jobs)))
	require.Equal(t, "cimg/node:22.11", jobs["main/test-22.11-arm"].Image)
}

func mapKeys(jobs map[string]runtimeJob) func(func(string) bool) {
	return func(yield func(string) bool) {
		for name := range jobs {
			if !yield(name) {
				return
			}
		}
	}
}

// when/unless steps and workflows follow conditions on parameters; a
// condition on pipeline values ddtest does not know keeps its steps.
func TestCircleCIConditions(t *testing.T) {
	config := `version: 2.1
parameters:
  integration:
    type: boolean
    default: false
jobs:
  test:
    parameters:
      coverage:
        type: boolean
        default: true
      node:
        type: string
        default: "22"
    docker:
      - image: cimg/node:22.11
    steps:
      - when:
          condition: << parameters.coverage >>
          steps:
            - run: npm run coverage
      - unless:
          condition: << parameters.coverage >>
          steps:
            - run: npm run no-coverage
      - when:
          condition:
            and:
              - equal: [ "22", << parameters.node >> ]
              - not: << pipeline.parameters.integration >>
          steps:
            - run: npm run node22
      - when:
          condition:
            equal: [ main, << pipeline.git.branch >> ]
          steps:
            - run: npm run main-only
      - run:
          command: npm run on-failure
          when: on_fail
      - run:
          command: npm run always
          when: always
  integration:
    docker:
      - image: cimg/node:22.11
    steps:
      - run: npm run integration
workflows:
  main:
    jobs: [test]
  integration:
    when: << pipeline.parameters.integration >>
    jobs: [integration]
  nightly:
    when:
      equal: [ scheduled_pipeline, << pipeline.trigger_source >> ]
    jobs: [integration]
`
	jobs := circleJobs(t, circleRepository(t, config))
	require.Equal(t, []string{"npm run coverage", "npm run node22", "npm run main-only", "npm run always"}, circleRuns(jobs["main/test"]))
	require.Equal(t, "false", jobs["integration/integration"].If)
	require.Empty(t, jobs["nightly/integration"].If, "a pipeline condition ddtest cannot decide may run")
}

// Steps of type steps, inline orbs and a configuration without workflows
// resolve locally.
func TestCircleCIStepsParametersAndInlineOrbs(t *testing.T) {
	config := `version: 2
orbs:
  local:
    commands:
      test:
        steps:
          - run: npm test
    executors:
      default:
        docker:
          - image: cimg/node:22.11
commands:
  install:
    parameters:
      after:
        type: steps
        default: []
    steps:
      - run: npm ci
      - steps: << parameters.after >>
      - << parameters.after >>
jobs:
  build:
    executor: local/default
    steps:
      - install:
          after:
            - run: npm run build
      - local/test
`
	jobs := circleJobs(t, circleRepository(t, config))
	require.Equal(t, []string{"npm ci", "npm run build", "npm run build", "npm test"}, circleRuns(jobs["build/build"]))
	require.Equal(t, "cimg/node:22.11", jobs["build/build"].Image)
}

// Configuration ddtest cannot read locally stays unresolved; orbs that only
// notify, scan or upload coverage are reported, not blockers.
func TestCircleCIUnresolvedConfiguration(t *testing.T) {
	config := `version: 2.1
setup: true
orbs:
  node: acme/node@5.2.0
  slack: circleci/slack@4.12.5
  codecov: codecov/codecov@4.0.1
jobs:
  test:
    docker:
      - image: cimg/node:22.11
    steps:
      - node/install-packages
      - run: npm test -- --shard=<< pipeline.number >>
      - codecov/upload
      - slack/notify:
          event: fail
      - run:
          command: npm start
          background: true
      - run:
          command: Write-Host hello
          shell: powershell.exe
      - run:
          command: npm test
          working_directory: /tmp/elsewhere
      - missing-command
` + circleWorkflow("      - test\n      - node/test\n      - undefined-job\n      - hold:\n          type: approval\n")
	root := circleRepository(t, config)
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	workflow := workflows[0]
	steps := strings.Join(circleRuns(workflow.Jobs["main/test"]), "\n")
	for _, reason := range []string{
		"the orb command node/install-packages from acme/node@5.2.0 runs steps ddtest cannot read",
		"reads << pipeline.number >>, which only CircleCI knows",
		"runs in the background",
		"runs with the shell powershell.exe",
		"working directory /tmp/elsewhere is outside the checkout",
		"step missing-command is not a command the configuration defines",
	} {
		require.Contains(t, steps, reason)
	}
	require.NotContains(t, steps, "codecov")
	require.Contains(t, circleRuns(workflow.Jobs["main/node/test"])[0], "runs the orb job node/test from acme/node@5.2.0")
	require.Contains(t, circleRuns(workflow.Jobs["main/undefined-job"])[0], "which the configuration does not define")
	require.NotContains(t, workflow.Jobs, "main/hold")
	require.Contains(t, circleRuns(workflow.Jobs["(unresolved configuration 1)"])[0], "setup: true makes this a dynamic configuration")
	require.Equal(t, []string{".circleci/config.yml: CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: circleci/slack@4.12.5, codecov/codecov@4.0.1."}, workflow.Caveats)
	require.True(t, workflowHasTests(root, workflow))
}

// A non-test orb's job is a caveat, not a job.
func TestCircleCINonTestOrbJobs(t *testing.T) {
	config := "version: 2.1\norbs:\n  snyk: snyk/snyk@2.1.0\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npm test\n" + circleWorkflow("      - test\n      - snyk/scan\n")
	root := circleRepository(t, config)
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	require.NotContains(t, workflows[0].Jobs, "main/snyk/scan")
	require.Contains(t, workflows[0].Review, ".circleci/config.yml: workflow main runs snyk/scan from snyk/snyk@2.1.0, the Snyk security scanning orb; it runs no tests and is outside this validation.")
}

// A configuration without workflows runs its build job.
func TestCircleCIBuildJobWithoutWorkflows(t *testing.T) {
	jobs := circleJobs(t, circleRepository(t, "version: 2\njobs:\n  build:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - run: npm test\n  other:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npm run other\n"))
	require.Equal(t, []string{"build/build"}, slices.Collect(mapKeys(jobs)))
}

// YAML merge keys share a job's fields, as in many real configurations.
func TestCircleCIMergeKeys(t *testing.T) {
	config := "version: 2\ndefaults: &defaults\n  working_directory: ~/repo\n  docker:\n    - image: cimg/node:20.18\njobs:\n  test:\n    <<: *defaults\n    steps:\n      - checkout\n      - run: npm test\n" + circleWorkflow("      - test\n")
	job := circleJobs(t, circleRepository(t, config))["main/test"]
	require.Equal(t, "cimg/node:20.18", job.Image)
	require.Equal(t, []string{"npm test"}, circleRuns(job))
}

// Test splitting runs the inner command locally once; forms ddtest cannot
// resolve stay unresolved.
func TestCircleCITestSplitting(t *testing.T) {
	for _, tc := range []struct{ command, resolved, unresolved string }{
		{`circleci tests glob "src/**/*.test.js" | circleci tests run --command="xargs npx jest --ci" --verbose --split-by=timings`, "npx jest --ci", ""},
		{`circleci tests glob "src/**/*.test.js" | circleci tests split --split-by=timings | xargs npx jest`, "npx jest", ""},
		{"TEST_FILES=\"$(circleci tests glob \"spec/**/*_spec.rb\" | \\\n  circleci tests split --split-by=timings)\"\nnpx jest --ci \\\n  $TEST_FILES", "npx jest --ci", ""},
		{`npx jest $(circleci tests glob "src/**/*.test.js" | circleci tests split)`, "", "splits tests across parallel containers"},
		{"FILES=$(circleci tests glob a | circleci tests split)\nnpx jest --files=$FILES", "", "splits tests across parallel containers"},
		{"FILES=\"$(circleci tests glob a | circleci tests split | head -1)\"\nnpx jest $FILES", "", "splits tests across parallel containers"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			resolved, review, err := circleTestSplitting(tc.command)
			if tc.unresolved != "" {
				require.ErrorContains(t, err, tc.unresolved)
				return
			}
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(tc.resolved), resolved)
			require.Contains(t, review, "CircleCI test splitting")
		})
	}
}

// The Datadog orb's step is the setup: its parameters are the setup
// inputs, and what it exports reaches later steps through $BASH_ENV,
// overriding their own environment.
func TestCircleCIRecognizesTheOrb(t *testing.T) {
	config := `version: 2.1
orbs:
  dd: datadog/test-optimization-circleci-orb@2
jobs:
  test:
    docker:
      - image: cimg/node:22.11
    environment:
      NODE_OPTIONS: --max-old-space-size=4096
    steps:
      - run: npm ci
      - dd/autoinstrument:
          languages: js
          site: datadoghq.eu
          js_tracer_version: 5.133.0
      - run: echo "export NODE_OPTIONS=\"$NODE_OPTIONS --import $DD_TRACE_ESM_IMPORT\"" >> $BASH_ENV
      - run:
          command: npx vitest run
          environment:
            NODE_OPTIONS: --ignored
` + circleWorkflow("      - test\n")
	job := circleJobs(t, circleRepository(t, config))["main/test"]
	require.Equal(t, []string{"npm ci", "<setup datadog/test-optimization-circleci-orb@2>", "npx vitest run"}, circleRuns(job))
	setup := job.Steps[1].Setup
	require.True(t, setup.Supported)
	require.Empty(t, setup.Problem)
	require.Equal(t, map[string]string{"languages": "js", "site": "datadoghq.eu", "js-tracer-version": "5.133.0"}, setup.Inputs)
	require.Equal(t, "--max-old-space-size=4096 -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", job.Steps[3].Env["NODE_OPTIONS"])
	require.Empty(t, job.Steps[2].Run, "a step that only appends to $BASH_ENV replays nothing")
}

// The loader registration ddtest prescribes, appended to $BASH_ENV as a
// guarded statement, is followed too.
func TestCircleCIFollowsAppendedStatements(t *testing.T) {
	config := "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n" + circleOrbStep +
		"      - run:\n          command: |\n            " + circlePersist(esmImportCommand) + "\n      - run: echo 'export EXTRA=1' >> \"$BASH_ENV\" && npm run prepare\n      - run: npx vitest run\n" + circleWorkflow("      - test\n")
	job := circleJobs(t, circleRepository(t, config))["main/test"]
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", job.Steps[3].Env["NODE_OPTIONS"])
	require.Contains(t, job.Steps[2].Run, "npm run prepare", "a step that also runs commands stays")
}

func TestCircleCISetupProblems(t *testing.T) {
	exact := func(value string) bool { return value == "5.133.0" }
	for _, tc := range []struct {
		name, orb, step, image, jobExtra, site, problem string
	}{
		{name: "moving major", orb: "@2"},
		{name: "exact release", orb: "@2.2.0"},
		{name: "v1", orb: "@1", problem: "is not a supported release; declare datadog/test-optimization-circleci-orb@2"},
		{name: "minor", orb: "@2.2", problem: "is not a supported release"},
		{name: "volatile", orb: "@volatile", problem: "is not a supported release"},
		{name: "dev", orb: "@dev:alpha", problem: "is not a supported release"},
		{name: "no tracer", orb: "@2", step: "          languages: js\n", problem: "sets no js_tracer_version, so it installs the default"},
		{name: "latest tracer", orb: "@2", step: "          languages: js\n          js_tracer_version: latest\n", problem: `js_tracer_version "latest" is not an exact release`},
		{name: "other site", orb: "@2", site: "datadoghq.eu", problem: "reports to datadoghq.com, but the selected Datadog site is datadoghq.eu; set site: datadoghq.eu"},
		{name: "alpine", orb: "@2", image: "node:22-alpine", problem: "has no Bash, so CircleCI runs its steps with /bin/sh"},
		{name: "slim", orb: "@2", image: "node:22-slim", problem: "has neither curl nor wget"},
		{name: "slim with curl", orb: "@2", image: "node:22-slim", jobExtra: "      - run: apt-get update && apt-get install -y curl\n"},
		{name: "committed key", orb: "@2", jobExtra: "", image: "cimg/node:22.11\n    environment:\n      DD_API_KEY: abc", problem: "never commit the key"},
		{name: "sh shell", orb: "@2", image: "cimg/node:22.11\n    shell: /bin/sh -eo pipefail", problem: "only Bash reads the $BASH_ENV"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := tc.step
			if step == "" {
				step = "          languages: js\n          js_tracer_version: 5.133.0\n"
			}
			image := tc.image
			if image == "" {
				image = "cimg/node:22.11"
			}
			config := "version: 2.1\norbs:\n  dd: datadog/test-optimization-circleci-orb" + tc.orb + "\njobs:\n  test:\n    docker:\n      - image: " + image + "\n    steps:\n" + tc.jobExtra + "      - dd/autoinstrument:\n" + step + "      - run: npm test\n" + circleWorkflow("      - test\n")
			job := circleJobs(t, circleRepository(t, config))["main/test"]
			setup := job.Steps[len(job.Steps)-2].Setup
			require.NotNil(t, setup)
			problem := circleCI{}.setupProblem(*setup, "js-tracer-version", exact, "5.133.0", tc.site)
			if tc.problem == "" {
				require.Empty(t, problem)
			} else {
				require.Contains(t, problem, tc.problem)
			}
		})
	}
}

// Executors ddtest cannot read, and Windows machines, can't certify the
// orb; machine executors have Bash.
func TestCircleCIExecutorProblems(t *testing.T) {
	for executor, problem := range map[string]string{
		"    executor: node/default\n":                                                                              "comes from an orb or parameter ddtest cannot read",
		"    machine:\n      image: ubuntu-2204:current\n":                                                          "",
		"    machine:\n      image: windows-server-2022-gui:current\n":                                              "Windows machine",
		"    parameters:\n      image:\n        type: string\n    docker:\n      - image: << parameters.image >>\n": "depends on a parameter ddtest cannot resolve",
	} {
		config := "version: 2.1\norbs:\n  node: acme/node@5\n  dd: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n" + executor + "    steps:\n      - dd/autoinstrument:\n          languages: js\n          js_tracer_version: 5.133.0\n      - run: npm test\n" + circleWorkflow("      - test\n")
		job := circleJobs(t, circleRepository(t, config))["main/test"]
		got := job.Steps[0].Setup.Problem
		if problem == "" {
			require.Empty(t, got, executor)
		} else {
			require.Contains(t, got, problem, executor)
		}
	}
}

// A setup under a condition ddtest cannot decide is not certified.
func TestCircleCIConditionalSetup(t *testing.T) {
	config := "version: 2.1\norbs:\n  dd: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - when:\n          condition:\n            equal: [ main, << pipeline.git.branch >> ]\n          steps:\n            - dd/autoinstrument:\n                languages: js\n                js_tracer_version: 5.133.0\n      - run: npm test\n" + circleWorkflow("      - test\n")
	job := circleJobs(t, circleRepository(t, config))["main/test"]
	require.Contains(t, job.Steps[0].Setup.Problem, "runs only under a condition ddtest cannot decide locally")
}

func TestCircleCIJavaScriptCompleteness(t *testing.T) {
	complete := "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - run: npm ci\n" + circleOrbStep + "      - run: npm test\n" + circleWorkflow("      - test\n")
	for _, tc := range []struct {
		name, config, site string
		configured         bool
	}{
		{"complete", complete, "", true},
		{"selected site", complete, "datadoghq.com", true},
		{"other site", complete, "datadoghq.eu", false},
		{"missing", strings.Replace(complete, circleOrbStep, "", 1), "", false},
		{"after the tests", strings.Replace(strings.Replace(complete, circleOrbStep, "", 1), "      - run: npm test\n", "      - run: npm test\n"+circleOrbStep, 1), "", false},
		{"other language", strings.Replace(complete, "languages: js", "languages: python", 1), "", false},
		{"all languages", strings.Replace(complete, "languages: js", "languages: all", 1), "", true},
		{"unresolved orb in the job", strings.Replace(complete, "      - run: npm ci\n", "      - node/install-packages\n", 1), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := circleRepository(t, tc.config)
			require.Equal(t, tc.configured, javascriptDiscovery{}.ciSetupConfigured(root, "jest", tc.site))
		})
	}
}

// planCircleCI plans the JavaScript setup in a Jest repository whose
// CircleCI configuration is config.
func planCircleCI(t *testing.T, framework, config string, files map[string]string) (*SetupPlan, string, error) {
	t.Helper()
	root := circleRepository(t, config)
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	plan, err := PlanJavaScriptSetup(root, framework, "5.133.0", "datadoghq.com", ">=18")
	return plan, root, err
}

func TestCircleCIApplyInsertsTheOrbBeforeTheFirstTest(t *testing.T) {
	plan, root, err := planCircleCI(t, "jest", `version: 2.1

jobs:
  test:
    docker:
      - image: cimg/node:22.11
    steps:
      - checkout
      - run: npm ci
      - run:
          name: Tests
          command: npm test # unit tests
      - run: npm run report

workflows:
  main:
    jobs:
      - test
`, nil)
	require.NoError(t, err)
	require.Equal(t, `version: 2.1

orbs:
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2

jobs:
  test:
    docker:
      - image: cimg/node:22.11
    steps:
      - checkout
      - run: npm ci
      - test-optimization-circleci-orb/autoinstrument:
          languages: js
          site: datadoghq.com
          js_tracer_version: 5.133.0
      - run:
          name: Tests
          command: npm test # unit tests
      - run: npm run report

workflows:
  main:
    jobs:
      - test
`, plannedFile(t, plan, circleCIFile))
	require.Contains(t, plannedFile(t, plan, ".gitignore"), "/.testoptimization/")
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured(), "a second apply changes nothing")
	_, err = PlanJavaScriptSetup(root, "jest", "5.140.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Existing Datadog setup differs")
}

// The declaration joins existing orbs; an alias taken by another orb gets
// a prefix; a declaration without version goes first.
func TestCircleCIApplyDeclaresTheOrb(t *testing.T) {
	job := "jobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npm test\n" + circleWorkflow("      - test\n")
	for _, tc := range []struct{ name, config, want string }{
		{"existing orbs", "version: 2.1\norbs:\n    slack: circleci/slack@4.12.5\n" + job, "orbs:\n    test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n    slack: circleci/slack@4.12.5\n"},
		{"empty orbs", "version: 2.1\norbs:\n" + job, "orbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:"},
		{"taken alias", "version: 2.1\norbs:\n  test-optimization-circleci-orb: acme/other@1\n" + job, "  datadog-test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n"},
		{"existing declaration", "version: 2.1\norbs:\n  dd: datadog/test-optimization-circleci-orb@2.2.0\n" + job, "      - dd/autoinstrument:\n"},
		{"no version", job, "orbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n\njobs:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _, err := planCircleCI(t, "jest", tc.config, nil)
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, circleCIFile), tc.want)
		})
	}
	_, _, err := planCircleCI(t, "jest", "version: 2.1\norbs:\n  dd: datadog/test-optimization-circleci-orb@1\n"+job, nil)
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Change it to datadog/test-optimization-circleci-orb@2")
}

// Vitest gets the ESM loader registration through $BASH_ENV, and the
// result is configured.
func TestCircleCIApplyRegistersTheESMLoader(t *testing.T) {
	plan, root, err := planCircleCI(t, "vitest", "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npx vitest run\n"+circleWorkflow("      - test\n"), map[string]string{"package.json": `{"devDependencies": {"vitest": "3.2.0"}}`})
	require.NoError(t, err)
	require.Contains(t, plannedFile(t, plan, circleCIFile), `      - test-optimization-circleci-orb/autoinstrument:
          languages: js
          site: datadoghq.com
          js_tracer_version: 5.133.0
      - run:
          name: Configure later steps for Datadog Test Optimization
          command: |
            echo 'if [ -n "${DD_TRACE_ESM_IMPORT:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_ESM_IMPORT"; fi' >> "$BASH_ENV"
      - run: npx vitest run
`)
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	again, err := PlanJavaScriptSetup(root, "vitest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, again.Configured())
}

// A reusable command only test jobs run is edited once; one a job without
// the tests runs too is refused, and so are anchors and matrix jobs sharing
// a step with a job ddtest does not set up.
func TestCircleCIApplySharedSteps(t *testing.T) {
	commands := "version: 2.1\ncommands:\n  test:\n    steps:\n      - run: npm ci\n      - run: npm test\n"
	jobs := "jobs:\n  unit:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - test\n  more:\n    docker:\n      - image: cimg/node:20.18\n    steps:\n      - test\n"
	t.Run("test jobs sharing a command", func(t *testing.T) {
		plan, _, err := planCircleCI(t, "jest", commands+jobs+circleWorkflow("      - unit\n      - more\n"), nil)
		require.NoError(t, err)
		config := plannedFile(t, plan, circleCIFile)
		require.Equal(t, 1, strings.Count(config, "/autoinstrument:"))
		require.Contains(t, config, "      - run: npm ci\n      - test-optimization-circleci-orb/autoinstrument:\n")
		require.Contains(t, plan.LocalImpact(), "The Datadog orb step is added to the reusable command test, which jobs main/more, main/unit run.")
	})
	t.Run("a job running the command twice", func(t *testing.T) {
		_, _, err := planCircleCI(t, "jest", commands+"jobs:\n  unit:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - test\n      - test\n"+circleWorkflow("      - unit\n"), nil)
		var gap *EditThenApplyGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, "runs this step more than once")
	})
	t.Run("an anchor", func(t *testing.T) {
		_, _, err := planCircleCI(t, "jest", "version: 2.1\njobs:\n  unit:\n    docker:\n      - image: cimg/node:22.11\n    steps: &steps\n      - run: npm test\n  more:\n    docker:\n      - image: cimg/node:22.11\n    steps: *steps\n"+circleWorkflow("      - unit\n      - more\n"), nil)
		var gap *EditThenApplyGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, "YAML anchor and alias")
	})
	t.Run("an unsupported image", func(t *testing.T) {
		_, _, err := planCircleCI(t, "jest", "version: 2.1\njobs:\n  unit:\n    docker:\n      - image: node:22-alpine\n    steps:\n      - run: npm test\n"+circleWorkflow("      - unit\n"), nil)
		var gap *EditThenApplyGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, "has no Bash")
	})
	t.Run("a step in a workflow's pre-steps", func(t *testing.T) {
		_, _, err := planCircleCI(t, "jest", "version: 2.1\njobs:\n  unit:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: echo done\n"+circleWorkflow("      - unit:\n          pre-steps:\n            - run: npm test\n"), nil)
		var gap *EditThenApplyGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, "a workflow's pre-steps, which ddtest does not edit")
	})
}

func TestCircleCILanguageSupport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/python:3.12\n    steps:\n      - run: pytest\n"+circleWorkflow("      - test\n"))
	useCIProvider(t, "circleci")
	for language, supported := range map[string]bool{"javascript": true, "ruby": true, "python": false, "java": false} {
		err := CheckCIProviderSupport(root, language, "framework")
		if supported {
			require.NoError(t, err, language)
			continue
		}
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, err, &unsupported, language)
		require.Equal(t, "CircleCI", unsupported.Provider)
	}
}

// The committed fixture is marp-vscode's configuration: executors with
// parameters, reusable commands with steps parameters, a coverage upload
// orb and four test jobs sharing one command.
func TestCircleCIJavaScriptFixture(t *testing.T) {
	root := copyFixture(t, "circleci-javascript")
	useCIProvider(t, "circleci")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Len(t, scope.Commands[0].locations, 4)
	require.Contains(t, scope.Caveats, ".circleci/config.yml: CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: codecov/codecov@6.0.0.")
	plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	config := plannedFile(t, plan, circleCIFile)
	require.Contains(t, config, "orbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n  codecov: codecov/codecov@6.0.0\n")
	require.Contains(t, config, "      - install\n\n      - test-optimization-circleci-orb/autoinstrument:\n          languages: js\n          site: datadoghq.com\n          js_tracer_version: 5.133.0\n      - run:\n          name: Jest\n")
	require.Equal(t, 1, strings.Count(config, "/autoinstrument:"))
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
}

// Diagnostic and type-check steps before the tests, as in Patreon/nion's
// configuration, are outside the test path on every provider: a multi-line
// version print and a tsd type check never start or prepare a test.
func TestDiagnosticAndTypeCheckStepsBeforeTestsAreOutsideTheTestPath(t *testing.T) {
	pkg := `{"scripts":{"test":"jest","typecheck":"tsd","lint:css":"stylelint \"**/*.css\""},"devDependencies":{"jest":"29.7.0","tsd":"0.31.0"}}`
	steps := []string{"|\n          node --version\n          npm --version\n", "npm ci", "npm run typecheck", "npm run lint:css", "npm test"}
	for provider, config := range map[string]string{
		"circleci": "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n" + circleRunSteps(steps, "      ") + circleWorkflow("      - test\n"),
		"github":   "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n" + circleRunSteps(steps, "      "),
	} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", pkg)
			path := circleCIFile
			if provider == "github" {
				path = ".github/workflows/test.yml"
			}
			writeFile(t, root, path, config)
			useCIProvider(t, provider)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Empty(t, scope.Commands[0].UnvalidatedSetup)
		})
	}
}

func circleRunSteps(commands []string, indent string) string {
	var text strings.Builder
	for _, command := range commands {
		text.WriteString(indent + "- run: " + command + "\n")
	}
	return text.String()
}

// Qlty's orb only installs its CLI and uploads coverage (coverage_publish,
// coverage_complete), as in presidentbeef/brakeman's configuration.
func TestCircleCIQltyCoverageOrbIsNotATest(t *testing.T) {
	config := "version: 2.1\norbs:\n  qlty: qltysh/qlty-orb@0.1\njobs:\n  upload-coverage:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - qlty/coverage_publish:\n          files: tmp/coverage.json\n" + circleWorkflow("      - upload-coverage\n")
	root := circleRepository(t, config)
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	require.Empty(t, circleRuns(workflows[0].Jobs["main/upload-coverage"]))
	require.Equal(t, []string{".circleci/config.yml: CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: qltysh/qlty-orb@0.1."}, workflows[0].Caveats)
	require.False(t, workflowHasTests(root, workflows[0]))
}

// CircleCI rejects orbs in a version 2 configuration, as Patreon/nion's is:
// apply moves it to 2.1, which reads version 2 configurations the same way,
// unless its text holds << >> sequences 2.1 would substitute; a version 2
// configuration that already declares orbs is reported.
func TestCircleCIVersion2Configurations(t *testing.T) {
	job := "jobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npm test\nworkflows:\n  version: 2\n  main:\n    jobs:\n      - test\n"
	plan, root, err := planCircleCI(t, "jest", "version: 2 # legacy\n"+job, nil)
	require.NoError(t, err)
	edited := plannedFile(t, plan, circleCIFile)
	require.True(t, strings.HasPrefix(edited, "version: 2.1 # legacy\n\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n"), edited)
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))

	_, _, err = planCircleCI(t, "jest", "version: \"2.0\"\n"+strings.Replace(job, "npm test", "npm test -- --label='<< not.a.parameter >>' # << parameters.x >>", 1), nil)
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "version 2.1")

	root = circleRepository(t, "version: 2\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n"+strings.Replace(job, "      - run: npm test\n", circleOrbStep+"      - run: npm test\n", 1))
	require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	require.Contains(t, workflows[0].Jobs["main/test"].Steps[0].Setup.Problem, "declares version 2, where CircleCI rejects orbs; set version: 2.1")
}

// Review findings on the first CircleCI round.
func TestCircleCIReviewFindings(t *testing.T) {
	image := "    docker:\n      - image: cimg/node:22.11\n"
	t.Run("override-with replaces the local job", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  node: acme/node@5.2.0\n  local:\n    jobs:\n      test:\n        docker:\n          - image: cimg/node:22.11\n        steps:\n          - run: npm run inline\njobs:\n  test:\n" + image + "    steps:\n      - run: npm test\n" + circleWorkflow("      - test:\n          override-with: node/test\n      - test:\n          name: inline\n          override-with: local/test\n")
		jobs := circleJobs(t, circleRepository(t, config))
		require.Contains(t, circleRuns(jobs["main/test"])[0], "runs the orb job node/test from acme/node@5.2.0")
		require.Equal(t, []string{"npm run inline"}, circleRuns(jobs["main/inline"]))
	})
	t.Run("plain assignments in $BASH_ENV reach later commands", func(t *testing.T) {
		config := "version: 2.1\njobs:\n  test:\n" + image + "    steps:\n      - run: echo 'SUITE=integration' >> \"$BASH_ENV\"\n      - run: npm test -- --selectProjects=$SUITE\n      - run: echo 'ODD=\"a b\"' >> \"$BASH_ENV\"\n      - run: npm test -- $ODD\n" + circleWorkflow("      - test\n")
		jobs := circleJobs(t, circleRepository(t, config))
		runs := circleRuns(jobs["main/test"])
		require.Equal(t, "npm test -- --selectProjects=integration", runs[0])
		require.Contains(t, runs[1], "reads $ODD, which an earlier step assigned in $BASH_ENV")
	})
	t.Run("matches needs the whole value", func(t *testing.T) {
		config := "version: 2.1\njobs:\n  test:\n    parameters:\n      branch:\n        type: string\n        default: feature-main\n" + image + "    steps:\n      - when:\n          condition:\n            matches: { pattern: main, value: << parameters.branch >> }\n          steps:\n            - run: npm run partial\n      - when:\n          condition:\n            matches: { pattern: \"feature-.+\", value: << parameters.branch >> }\n          steps:\n            - run: npm run whole\n" + circleWorkflow("      - test\n")
		require.Equal(t, []string{"npm run whole"}, circleRuns(circleJobs(t, circleRepository(t, config))["main/test"]))
	})
	t.Run("truthiness follows types", func(t *testing.T) {
		config := "version: 2.1\nparameters:\n  label:\n    type: string\n    default: \"false\"\n  enabled:\n    type: boolean\n    default: false\njobs:\n  test:\n" + image + "    steps:\n      - when:\n          condition: \"false\"\n          steps:\n            - run: npm run quoted\n      - when:\n          condition: false\n          steps:\n            - run: npm run literal\n      - when:\n          condition: << pipeline.parameters.label >>\n          steps:\n            - run: npm run string-parameter\n      - when:\n          condition: << pipeline.parameters.enabled >>\n          steps:\n            - run: npm run boolean-parameter\n      - when:\n          condition: 0\n          steps:\n            - run: npm run zero\n" + circleWorkflow("      - test\n")
		require.Equal(t, []string{"npm run quoted", "npm run string-parameter"}, circleRuns(circleJobs(t, circleRepository(t, config))["main/test"]))
	})
	t.Run("compound splitting commands", func(t *testing.T) {
		_, _, err := circleTestSplitting(`circleci tests glob "spec/**/*_spec.rb" | circleci tests run --command="xargs bundle exec rspec --tag unit" && bundle exec rspec --tag integration`)
		require.ErrorContains(t, err, "in a form ddtest cannot resolve")
		_, _, err = circleTestSplitting(`circleci tests glob "src/*.test.js" | circleci tests split | xargs npx jest; npm run after`)
		require.ErrorContains(t, err, "in a form ddtest cannot resolve")
	})
	t.Run("a step run with sh doesn't read $BASH_ENV", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n" + image + "    steps:\n" + circleOrbStep + "      - run:\n          command: npm test\n          shell: /bin/sh -eo pipefail\n" + circleWorkflow("      - test\n")
		root := circleRepository(t, config)
		job := circleJobs(t, root)["main/test"]
		require.NotContains(t, job.Steps[1].Env, "NODE_OPTIONS")
		require.Contains(t, job.Steps[1].ResolutionError, "runs with /bin/sh, which does not read $BASH_ENV")
		require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
	})
	t.Run("$BASH_ENV changes under an undecided condition", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n" + image + "    steps:\n" + circleOrbStep + "      - when:\n          condition:\n            equal: [ main, << pipeline.git.branch >> ]\n          steps:\n            - run: " + circlePersist(esmImportCommand) + "\n      - run: npx vitest run\n" + circleWorkflow("      - test\n")
		job := circleJobs(t, circleRepository(t, config))["main/test"]
		last := job.Steps[len(job.Steps)-1]
		require.NotContains(t, last.Env["NODE_OPTIONS"], "--import")
		require.Contains(t, last.ResolutionError, "appends to $BASH_ENV only under a condition ddtest cannot decide")
	})
}

// CircleCI's Node.js and browser-tools orbs are modeled from their source:
// installs are preparation, node/install selects the runtime, and the
// node/test and node/run jobs run the package manager's script.
func TestCircleCINodeOrb(t *testing.T) {
	image := "    docker:\n      - image: cimg/node:22.11\n"
	t.Run("commands", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  node: circleci/node@5.2.0\n  browser-tools: circleci/browser-tools@1.4.8\njobs:\n  test:\n    machine:\n      image: ubuntu-2204:current\n    steps:\n      - checkout\n      - node/install:\n          node-version: \"20.11.1\"\n      - browser-tools/install-chrome\n      - node/install-packages\n      - node/install-packages:\n          pkg-manager: yarn\n          app-dir: ~/project/web\n      - node/install-packages:\n          override-ci-command: npm install --legacy-peer-deps\n      - node/unknown-command\n      - run: npm test\n" + circleWorkflow("      - test\n")
		root := circleRepository(t, config)
		workflows, err := circleCI{}.readWorkflows(root)
		require.NoError(t, err)
		job := workflows[0].Jobs["main/test"]
		runs := circleRuns(job)
		require.Equal(t, []string{"npm ci", "yarn install --frozen-lockfile", "npm install --legacy-peer-deps"}, runs[:3])
		require.Contains(t, runs[3], "the orb command node/unknown-command from circleci/node@5.2.0 runs steps ddtest cannot read")
		require.Equal(t, "web", job.Steps[2].WorkingDirectory)
		require.Equal(t, map[string]string{"js": "20.11.1"}, job.Steps[0].Runtime)
		require.Equal(t, circleCIFile+":11", job.Steps[0].Source, "a modeled step is the configuration step that runs it")
		node, err := rowSetupNodeBefore(root, workflows[0], job, nil, len(job.Steps))
		require.NoError(t, err)
		require.Equal(t, "20.11.1", node)
		require.Contains(t, strings.Join(workflows[0].Review, "\n"), "browser-tools/install-chrome installs browsers or drivers on the CI machine")
	})
	t.Run("test and run jobs", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  node: circleci/node@5.2.0\n  node7: circleci/node@7.1.0\njobs:\n  lint:\n" + image + "    steps:\n      - run: echo lint\n" + circleWorkflow("      - node/test:\n          version: \"20.18\"\n          test-results-for: jest\n      - node7/test:\n          name: modern\n          pkg-manager: pnpm\n      - node7/run:\n          npm-run: build\n      - node7/unknown-job\n")
		root := circleRepository(t, config)
		jobs := circleJobs(t, root)
		require.Equal(t, "cimg/node:20.18", jobs["main/node/test"].Image)
		require.Equal(t, []string{"npm ci", "npm run test -- --reporters=default --reporters=jest-junit"}, circleRuns(jobs["main/node/test"]))
		require.Equal(t, "cimg/node:lts", jobs["main/modern"].Image)
		require.Equal(t, []string{"pnpm install --frozen-lockfile", "pnpm run test"}, circleRuns(jobs["main/modern"]))
		require.Equal(t, []string{"npm ci", "npm run build"}, circleRuns(jobs["main/node7/run"]))
		require.Contains(t, circleRuns(jobs["main/node7/unknown-job"])[0], "runs the orb job node7/unknown-job from circleci/node@7.1.0")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		var commands []string
		for _, entry := range scope.Commands {
			commands = append(commands, entry.Command)
		}
		require.Contains(t, commands, "jest --reporters=default --reporters=jest-junit")
	})
	t.Run("unmodeled releases stay unresolved", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  node: circleci/node@3.0.0\njobs:\n  test:\n" + image + "    steps:\n      - node/install-packages\n      - run: npm test\n" + circleWorkflow("      - test\n")
		require.Contains(t, circleRuns(circleJobs(t, circleRepository(t, config))["main/test"])[0], "runs steps ddtest cannot read")
	})
	t.Run("a test in the orb job takes the setup through its steps parameter", func(t *testing.T) {
		config := "version: 2.1\norbs:\n  node: circleci/node@5.2.0\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n" + circleWorkflow("      - node/test:\n          version: \"22.11\"\n")
		_, root, err := planCircleCI(t, "jest", config, nil)
		var gap *EditThenApplyGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, "the orb job node/test, which ddtest does not edit")
		writeFile(t, root, circleCIFile, config+"          post_install_steps:\n            - test-optimization-circleci-orb/autoinstrument:\n                languages: js\n                js_tracer_version: 5.133.0\n")
		require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
	})
}

// Patreon/nion's configuration: version 2, a YAML-anchored image, debug,
// lint, type-check and format steps before the tests, and test splitting
// through $TESTFILES. Nothing blocks, and apply moves it to version 2.1.
func TestCircleCINionFixture(t *testing.T) {
	root := copyFixture(t, "circleci-nion")
	useCIProvider(t, "circleci")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "jest --runInBand", scope.Commands[0].Command)
	require.Empty(t, scope.Commands[0].UnvalidatedSetup)
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "Check-only step before the job's jest test")
	require.Contains(t, review, "review separately: tsd")
	require.Contains(t, review, "in $TESTFILES")
	plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	config := plannedFile(t, plan, circleCIFile)
	require.Contains(t, config, "version: 2.1\n\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n")
	require.Contains(t, config, "            - test-optimization-circleci-orb/autoinstrument:\n                  languages: js\n                  site: datadoghq.com\n                  js_tracer_version: 5.133.0\n            - run:\n                  name: Test\n")
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
}

// presidentbeef/brakeman's configuration: Minitest through rake on
// cimg/ruby 3.2 to 4.0, jobs sharing steps through a YAML anchor, and Qlty's
// coverage upload orb. The test-3-2 job's multi-command step resolves, and its
// rake RUBYOPT argument, which replaces the preload, is reported with the fix.
func TestCircleCIBrakemanFixture(t *testing.T) {
	root := copyFixture(t, "circleci-brakeman")
	useCIProvider(t, "circleci")
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	require.Equal(t, "bundle exec rake", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].locations, 3)
	// test-3-2's export, rake and coverage moves resolve to one configuration.
	require.Equal(t, "bundle exec rake 'RUBYOPT=--enable-frozen-string-literal --debug-frozen-string-literal'", scope.Commands[1].Command)
	require.Equal(t, "CC_TEST_REPORTER_ID", scope.Commands[1].Environment["CC_TEST_REPORTER_ID"])
	require.Empty(t, scope.Unresolved)
	require.Contains(t, scope.Caveats, ".circleci/config.yml: CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: qltysh/qlty-orb@0.1.")
	var reasons []string
	for _, job := range RubyCIConfigurationForSite(root, "minitest", "").Jobs {
		reasons = append(reasons, job.Job+": "+job.Reason)
	}
	require.Contains(t, strings.Join(reasons, "\n"), "tests/test-3-2: bundle exec rake sets RUBYOPT='--enable-frozen-string-literal --debug-frozen-string-literal' as a rake argument")
}
