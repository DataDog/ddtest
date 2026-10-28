// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitLabSetupEntry is the README's setup entry with the given variables.
func gitLabSetupEntry(variables string) string {
	return `    - |
      ` + variables + ` \
      SRC=$(curl -fsSL https://github.com/DataDog/test-optimization-gitlab-script/releases/download/v1.2.10/script.sh); \
      echo "$SRC" | sha256sum | grep -q '^c82030bdbc7cbbbb139751885ca0392a7191c858f5a080ae364e0ec16218b783' && \
        source <(echo "$SRC") || \
        echo "ERROR: SHA256 mismatch. Datadog Test Optimization autoinstrumentation not enabled." >&2
`
}

const gitLabJestSetup = `LANGUAGES="js" SITE="datadoghq.com" API_KEY="$DD_API_KEY" JS_TRACER_VERSION="5.133.0"`

const gitLabRubySetup = `LANGUAGES="ruby" SITE="datadoghq.com" API_KEY="$DD_API_KEY" RUBY_TRACER_VERSION="1.42.0"`

// gitLabJobs reads root's GitLab configuration, keyed by job name, and the
// file each job belongs to.
func gitLabJobs(t *testing.T, root string) (map[string]runtimeJob, map[string]string) {
	t.Helper()
	workflows, err := gitLabCI{}.readWorkflows(root)
	require.NoError(t, err)
	jobs, files := map[string]runtimeJob{}, map[string]string{}
	for _, workflow := range workflows {
		for name, job := range workflow.Jobs {
			jobs[name], files[name] = job, workflow.Path
		}
	}
	return jobs, files
}

// runs lists a job's commands, without steps that only changed state.
func runs(job runtimeJob) []string {
	var commands []string
	for _, step := range job.Steps {
		if step.Run != "" {
			commands = append(commands, step.Run)
		}
	}
	return commands
}

// unresolved lists the resolution errors of root's GitLab configuration.
func unresolved(t *testing.T, root string) []string {
	t.Helper()
	jobs, _ := gitLabJobs(t, root)
	var errors []string
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		for _, step := range jobs[name].Steps {
			if step.ResolutionError != "" {
				errors = append(errors, step.ResolutionError)
			}
		}
	}
	return errors
}

func TestGitLabConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	files, err := gitLabCI{}.configurationFiles(root)
	require.NoError(t, err)
	require.Empty(t, files)
	writeFile(t, root, ".gitlab-ci.yml", "invalid: [")
	files, err = gitLabCI{}.configurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{".gitlab-ci.yml"}, files, "listing never parses")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "gitlab", selected)
	_, err = gitLabCI{}.readWorkflows(root)
	require.ErrorContains(t, err, "parse .gitlab-ci.yml")
}

// Every file that defines jobs is read: local includes in each form, nested
// includes and patterns. Each file becomes its own workflow.
func TestGitLabIncludesReadEveryFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "include:\n  - ci/strings.yml\n  - local: /ci/mapping.yml\n  - local: ci/more/*.yml\nroot:\n  script: [npm test]\n")
	writeFile(t, root, "ci/strings.yml", "strings:\n  script: [npm test]\n")
	writeFile(t, root, "ci/mapping.yml", "include: ci/nested.yml\nmapping:\n  script: [npm test]\n")
	writeFile(t, root, "ci/nested.yml", "nested:\n  script: [npm test]\n")
	writeFile(t, root, "ci/more/a.yml", "a:\n  script: [npm test]\n")
	writeFile(t, root, "ci/more/b.yml", "b:\n  script: [npm test]\n")
	jobs, files := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"root": ".gitlab-ci.yml", "strings": "ci/strings.yml", "mapping": "ci/mapping.yml", "nested": "ci/nested.yml", "a": "ci/more/a.yml", "b": "ci/more/b.yml"}, files)
	for name, job := range jobs {
		require.Equal(t, []string{"npm test"}, runs(job), name)
	}
	require.Empty(t, unresolved(t, root))
	useCIProvider(t, "gitlab")
	require.ElementsMatch(t, []string{".gitlab-ci.yml", "ci/strings.yml", "ci/mapping.yml", "ci/nested.yml", "ci/more/a.yml", "ci/more/b.yml"}, CIConfigurationFiles(root))
}

// The main file overrides what it includes: same-named jobs merge deeply.
func TestGitLabIncludedJobsMergeWithTheMainFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "include: ci/test.yml\ntest:\n  variables:\n    B: main\n")
	writeFile(t, root, "ci/test.yml", "test:\n  variables:\n    A: included\n    B: included\n  script: [npm test]\n")
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"A": "included", "B": "main"}, jobs["test"].Env)
	require.Equal(t, []string{"npm test"}, runs(jobs["test"]))
}

// Configuration ddtest cannot read locally is reported as unresolved, never
// dropped: its jobs may run tests.
func TestGitLabUnreadableConfigurationStaysUnresolved(t *testing.T) {
	for _, tc := range []struct{ name, config, reason string }{
		{"remote string", "include: https://example.com/ci.yml\n", "remote include https://example.com/ci.yml cannot be read locally"},
		{"remote", "include:\n  - remote: https://example.com/ci.yml\n", "remote include https://example.com/ci.yml cannot be read locally"},
		{"project", "include:\n  - project: group/templates\n    file: /node.yml\n", "project include group/templates cannot be read locally"},
		{"template", "include:\n  - template: Nodejs.gitlab-ci.yml\n", "template include Nodejs.gitlab-ci.yml cannot be read locally"},
		{"component", "include:\n  - component: gitlab.com/org/node/test@1.0\n", "component include gitlab.com/org/node/test@1.0 cannot be read locally"},
		{"missing local", "include: ci/missing.yml\n", "included file ci/missing.yml is not a file in the repository"},
		{"variable", "include: ci/$ENVIRONMENT.yml\n", "depends on a variable"},
		{"outside", "include: ../shared.yml\n", "outside the repository"},
		{"cycle", "include: ci/a.yml\n", "include cycle"},
		{"inputs", "include:\n  - local: ci/a.yml\n    inputs:\n      stage: test\n", "include inputs"},
		{"spec header", "spec:\n  inputs:\n    stage:\n---\ntest:\n  script: [npm test]\n", "spec: inputs header"},
		{"trigger", "child:\n  trigger:\n    include: ci/child.yml\n", "triggers a downstream or child pipeline"},
		{"unknown extends", "test:\n  extends: .missing\n  script: [npm test]\n", "extends .missing, which is not defined"},
		{"extends cycle", ".a:\n  extends: .b\n.b:\n  extends: .a\ntest:\n  extends: .a\n  script: [npm test]\n", "extends cycle"},
		{"unknown reference", "test:\n  script:\n    - !reference [.missing, script]\n", "!reference [.missing, script]"},
		{"rules variables", "test:\n  rules:\n    - if: $CI_COMMIT_TAG\n      variables:\n        SUITE: release\n    - when: on_success\n  script: [npm test]\n", "rules:variables set the variables per pipeline"},
		{"after_script tests", "test:\n  script: [npm ci]\n  after_script: [npx jest]\n", "after_script runs in a separate shell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, jestWorkflow)
			writeFile(t, root, ".gitlab-ci.yml", tc.config)
			writeFile(t, root, "ci/a.yml", "include: ci/b.yml\n")
			writeFile(t, root, "ci/b.yml", "include: ci/a.yml\n")
			errors := unresolved(t, root)
			require.True(t, slices.ContainsFunc(errors, func(err string) bool { return strings.Contains(err, tc.reason) }), errors)
			useCIProvider(t, "gitlab")
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.NotEmpty(t, scope.Unresolved, "unresolved configuration is never certified")
			_, configured, err := gitLabCI{}.findTestWorkflows(root, "javascript", "jest")
			require.NoError(t, err)
			require.Empty(t, configured)
		})
	}
}

func TestGitLabExtendsMergesTemplatesInOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `.base:
  image: node:20
  variables:
    A: base
    B: base
  before_script: [npm ci]
.lint:
  script: [npm run lint]
.mid:
  extends: .base
  variables:
    B: mid
  script: [npm test]
test:
  extends: [.lint, .mid]
  variables:
    C: job
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"A": "base", "B": "mid", "C": "job"}, jobs["test"].Env)
	require.Equal(t, "node:20", jobs["test"].Image)
	require.Equal(t, []string{"npm ci", "npm test"}, runs(jobs["test"]), "the last template's script replaces earlier ones")
	require.NotContains(t, jobs, ".base", "hidden jobs are templates")
}

func TestGitLabReferenceTagsResolve(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `.setup:
  script:
    - npm ci
    - npm run build
.vars:
  variables:
    SUITE: unit
test:
  variables: !reference [.vars, variables]
  script:
    - !reference [.setup, script]
    - npm test
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, []string{"npm ci", "npm run build", "npm test"}, runs(jobs["test"]))
	require.Equal(t, map[string]string{"SUITE": "unit"}, jobs["test"].Env)
}

func TestGitLabDefaultsAndInheritance(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  GLOBAL: "1"
  OTHER: "2"
default:
  image: node:22
  before_script: [npm ci]
legacy-image:
  image: node:18
  script: [npm test]
no-defaults:
  inherit:
    default: false
    variables: false
  script: [npm test]
some-defaults:
  inherit:
    default: [image]
    variables: [OTHER]
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, "node:18", jobs["legacy-image"].Image)
	require.Equal(t, []string{"npm ci", "npm test"}, runs(jobs["legacy-image"]))
	require.Equal(t, map[string]string{"GLOBAL": "1", "OTHER": "2"}, jobs["legacy-image"].Env)
	require.Empty(t, jobs["no-defaults"].Image)
	require.Equal(t, []string{"npm test"}, runs(jobs["no-defaults"]))
	require.Empty(t, jobs["no-defaults"].Env)
	require.Equal(t, "node:22", jobs["some-defaults"].Image)
	require.Equal(t, []string{"npm test"}, runs(jobs["some-defaults"]))
	require.Equal(t, map[string]string{"OTHER": "2"}, jobs["some-defaults"].Env)
}

func TestGitLabTopLevelDefaultsStillApply(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "image: node:20\nbefore_script: [npm ci]\ntest:\n  script: [npm test]\n")
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, "node:20", jobs["test"].Image)
	require.Equal(t, []string{"npm ci", "npm test"}, runs(jobs["test"]))
}

// Rules, when and workflow rules decide statically only when every operand
// is known; jobs that may run stay active.
func TestGitLabRulesAndWhen(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `never:
  when: never
  script: [npm test]
manual:
  when: manual
  script: [npm test]
pipeline-dependent:
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  script: [npm test]
rules-never:
  rules:
    - when: never
  script: [npm test]
changes:
  rules:
    - changes: [src/**/*]
  script: [npm test]
only-except:
  only: [main]
  except: [tags]
  script: [npm test]
needs:
  needs: [manual]
  script: [npm test]
matrix:
  parallel:
    matrix:
      - SUITE: [unit, legacy]
  rules:
    - if: $SUITE == "legacy"
      when: never
    - when: on_success
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	for name, disabled := range map[string]bool{"never": true, "manual": false, "pipeline-dependent": false, "rules-never": true, "changes": false, "only-except": false, "needs": false, "matrix: [unit]": false, "matrix: [legacy]": true} {
		require.Contains(t, jobs, name)
		require.Equal(t, disabled, disabledCondition(jobs[name].If), name)
	}
}

func TestGitLabWorkflowRulesCanDisableEveryJob(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "workflow:\n  rules:\n    - when: never\ntest:\n  script: [npm test]\n")
	jobs, _ := gitLabJobs(t, root)
	require.True(t, disabledCondition(jobs["test"].If))
	writeFile(t, root, ".gitlab-ci.yml", "workflow:\n  rules:\n    - if: $CI_COMMIT_BRANCH\ntest:\n  script: [npm test]\n")
	jobs, _ = gitLabJobs(t, root)
	require.False(t, disabledCondition(jobs["test"].If))
}

func TestGitLabConditions(t *testing.T) {
	env := map[string]string{"A": "x", "EMPTY": "", "DYNAMIC": "$OTHER"}
	for expression, want := range map[string]gitLabTruth{
		`$A == "x"`:                    gitLabTrue,
		`$A != "x"`:                    gitLabFalse,
		`$A == 'y' || $A == "x"`:       gitLabTrue,
		`$A == "x" && $UNKNOWN == "y"`: gitLabUnknown,
		`$A == "y" && $UNKNOWN == "y"`: gitLabFalse,
		`($A == "y" || $A == "x")`:     gitLabTrue,
		`$A`:                           gitLabTrue,
		`$EMPTY`:                       gitLabFalse,
		`$UNKNOWN`:                     gitLabUnknown,
		`$DYNAMIC == "z"`:              gitLabUnknown,
		`$A =~ /x/`:                    gitLabUnknown,
		`$A == null`:                   gitLabFalse,
		`$EMPTY == null`:               gitLabFalse,
		`$EMPTY == ""`:                 gitLabTrue,
		`$A ==`:                        gitLabUnknown,
	} {
		require.Equal(t, want, gitLabCondition(expression, env), expression)
	}
}

func TestGitLabParallelMatrix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  NODE: "22"
test:
  image: node:${NODE}
  parallel:
    matrix:
      - NODE: ["20", "22"]
        SUITE: unit
      - NODE: "24"
        SUITE: [e2e, smoke]
  script: [npm test]
split:
  parallel: 3
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	require.ElementsMatch(t, []string{"test: [20, unit]", "test: [22, unit]", "test: [24, e2e]", "test: [24, smoke]", "split"}, slices.Collect(maps.Keys(jobs)))
	require.Equal(t, "node:20", jobs["test: [20, unit]"].Image)
	require.Equal(t, map[string]string{"SUITE": "smoke"}, jobs["test: [24, smoke]"].Env, "NODE only selects the image")
}

func TestGitLabVariables(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  BASE: base
  DERIVED: "${BASE}/derived"
  LITERAL:
    value: "$BASE"
    expand: false
  DESCRIBED:
    value: described
    description: shown in the UI
  WORKSPACE: $CI_PROJECT_DIR/build
  UNKNOWN: $CI_COMMIT_SHA
test:
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"BASE": "base", "DERIVED": "base/derived", "LITERAL": "$BASE", "DESCRIBED": "described", "WORKSPACE": "${{ github.workspace }}/build", "UNKNOWN": unresolvedCIValue("$CI_COMMIT_SHA")}, jobs["test"].Env, "an unknown read stays unresolved; expand: false stays literal")
}

// A variable that only locates a declared cache directory configures the
// runner, not the tests.
func TestGitLabCacheLocationsAreNotTestEnvironment(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  npm_config_cache: $CI_PROJECT_DIR/.npm
  PIP_CACHE_DIR: .cache/pip
  OUTPUT: $CI_PROJECT_DIR/out
default:
  cache:
    - key: npm
      paths: [.npm/]
    - key: pip
      paths: [.cache]
test:
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"OUTPUT": "${{ github.workspace }}/out"}, jobs["test"].Env)
}

// Script entries share one shell: literal exports and cd reach later steps;
// state ddtest cannot follow makes them unresolved.
func TestGitLabScriptsShareTheShell(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `test:
  variables:
    APP: web
  before_script:
    - export SUITE=unit
    - LOCAL=1
    - cd $CI_PROJECT_DIR/packages/$APP
  script:
    - set -euo pipefail
    - npm ci && cd tests
    - npm test
conditional:
  script:
    - if [ -n "$CI" ]; then export SUITE=ci; fi
    - npm test
outside:
  script:
    - cd /opt/app
    - npm test
dynamic:
  script:
    - export VERSION=$(cat VERSION)
    - npm test
`)
	jobs, _ := gitLabJobs(t, root)
	test := jobs["test"]
	require.Equal(t, []string{"npm ci && cd tests", "npm test"}, runs(test))
	last := test.Steps[len(test.Steps)-1]
	require.Equal(t, "packages/web/tests", last.WorkingDirectory)
	require.Equal(t, map[string]string{"SUITE": "unit"}, last.Env, "LOCAL=1 stays a shell variable")
	require.Empty(t, last.ResolutionError)
	require.Contains(t, jobs["conditional"].Steps[1].ResolutionError, "exports variables conditionally")
	require.Contains(t, jobs["outside"].Steps[1].ResolutionError, "outside the repository")
	require.Equal(t, unresolvedCIValue("$(…)"), jobs["dynamic"].Steps[1].Env["VERSION"], "a computed value stays unresolved")
}

func TestGitLabAfterScriptCleanupIsOutsideTests(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  script: [npm test]\n  after_script: [rm -rf tmp]\n")
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, []string{"npm test"}, runs(jobs["test"]))
}

func TestGitLabRecognizesTheSetupScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  DD_SITE: datadoghq.eu
test:
  variables:
    NODE_OPTIONS: --max-old-space-size=4096
  script:
`+gitLabSetupEntry(`LANGUAGES="js" API_KEY="$DD_API_KEY" JS_TRACER_VERSION="5.133.0"`)+`    - export NODE_OPTIONS="$NODE_OPTIONS --import $DD_TRACE_ESM_IMPORT"
    - npx vitest run
from-variables:
  variables:
    LANGUAGES: ruby
    API_KEY: $DD_API_KEY
  script:
`+gitLabSetupEntry(`RUBY_TRACER_VERSION="1.42.0"`)+`    - bundle exec rspec
tampered:
  script:
`+strings.Replace(gitLabSetupEntry(gitLabJestSetup), "c82030", "000000", 1)+`    - npm test
unpinned:
  script:
`+strings.Replace(gitLabSetupEntry(gitLabJestSetup), "v1.2.10", "latest", 1)+`    - npm test
unchecked:
  script:
    - LANGUAGES="js" API_KEY="$DD_API_KEY" source <(curl -fsSL https://github.com/DataDog/test-optimization-gitlab-script/releases/download/v1.2.10/script.sh)
    - npm test
`)
	jobs, _ := gitLabJobs(t, root)
	setup := jobs["test"].Steps[0].Setup
	require.NotNil(t, setup)
	require.Equal(t, ciSetup{Reference: "test-optimization-gitlab-script v1.2.10", Pinned: true, Supported: true, DefaultLatest: true, shaped: true, Inputs: map[string]string{"languages": "js", "api_key": "$DD_API_KEY", "site": "datadoghq.eu", "js-tracer-version": "5.133.0"}}, *setup)
	test := jobs["test"].Steps[2]
	require.Equal(t, "npx vitest run", test.Run)
	require.Equal(t, "--max-old-space-size=4096 -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", test.Env["NODE_OPTIONS"], "the installer appends its preload to the job's options")

	ruby := jobs["from-variables"]
	require.Equal(t, map[string]string{"languages": "ruby", "api_key": "$DD_API_KEY", "site": "datadoghq.eu", "ruby-tracer-version": "1.42.0"}, ruby.Steps[0].Setup.Inputs)
	require.Equal(t, rubyInstallerPreload, ruby.Steps[1].Env["RUBYOPT"])

	require.Nil(t, jobs["unchecked"].Steps[0].Setup)
	require.Contains(t, jobs["unchecked"].Steps[0].ResolutionError, "combines the Datadog setup script with other commands")
	for name, want := range map[string]ciSetup{
		"tampered": {Pinned: true},
		"unpinned": {Pinned: false},
	} {
		setup := jobs[name].Steps[0].Setup
		require.NotNil(t, setup, name)
		require.Equal(t, want.Pinned, setup.Pinned, name)
		require.False(t, setup.Supported, name)
	}
}

func TestGitLabSetupProblems(t *testing.T) {
	exact := func(value string) bool { return value == "1.42.0" }
	complete := ciSetup{Reference: "test-optimization-gitlab-script v1.2.10", Pinned: true, Supported: true, Inputs: map[string]string{"languages": "ruby", "api_key": "$DD_API_KEY", "ruby-tracer-version": "1.42.0"}}
	require.Empty(t, gitLabCI{}.setupProblem(complete, "ruby-tracer-version", exact, "1.42.0", ""))
	require.Empty(t, gitLabCI{}.setupProblem(complete, "ruby-tracer-version", exact, "1.42.0", "datadoghq.com"), "SITE defaults to datadoghq.com")
	with := func(change func(*ciSetup)) ciSetup {
		setup := complete
		setup.Inputs = maps.Clone(complete.Inputs)
		change(&setup)
		return setup
	}
	for want, setup := range map[string]ciSetup{
		"not a supported, checksum-verified release": with(func(s *ciSetup) { s.Supported = false }),
		"has no API_KEY": with(func(s *ciSetup) { delete(s.Inputs, "api_key") }),
		"API_KEY is not a CI/CD variable reference":            with(func(s *ciSetup) { s.Inputs["api_key"] = "0123456789abcdef" }),
		`RUBY_TRACER_VERSION "latest" is not an exact release`: with(func(s *ciSetup) { s.Inputs["ruby-tracer-version"] = "latest" }),
		`set SITE="datadoghq.eu"`:                              with(func(s *ciSetup) { s.Inputs["site"] = "datadoghq.com" }),
	} {
		require.Contains(t, gitLabCI{}.setupProblem(setup, "ruby-tracer-version", exact, "1.42.0", "datadoghq.eu"), want)
	}
}

func TestGitLabImages(t *testing.T) {
	for image, node := range map[string]string{"node:22": "22", "node:20.11-alpine": "20.11", "docker.io/library/node:18.19.1-bookworm": "18.19.1", "cimg/node:20.1": "20.1", "node:lts": "", "node:latest": "", "node": "", "ruby:3.3": "", "node:$NODE": ""} {
		require.Equal(t, node, imageNode(image), image)
	}
	for image, want := range map[string][2]string{"ruby:3.3": {"3.3", ""}, "ruby:3.3-slim": {"3.3", ""}, "ruby:3.2.4-alpine3.19": {"3.2.4", ""}, "docker.io/library/ruby:2.7": {"2.7", ""}, "ruby:latest": {"", ""}, "ruby:head": {"", "Ruby head"}, "jruby:9.4": {"", "JRuby"}, "flavorjones/truffleruby:stable": {"", "TruffleRuby"}, "node:22": {"", ""}, "ruby:${RUBY}": {"", ""}} {
		version, engine := rubyImageRuntime(image)
		require.Equal(t, want, [2]string{version, engine}, image)
	}
}

func TestGitLabTestWorkflowsNeedTheSetupBeforeTests(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "include: ci/e2e.yml\nunit:\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - npx jest\n")
	writeFile(t, root, "ci/e2e.yml", "late:\n  script:\n    - npx jest\n"+gitLabSetupEntry(gitLabJestSetup)+"python:\n  script:\n"+gitLabSetupEntry(`LANGUAGES="python" API_KEY="$DD_API_KEY"`)+"    - npx jest\n")
	found, configured, err := gitLabCI{}.findTestWorkflows(root, "javascript", "jest")
	require.NoError(t, err)
	require.Equal(t, []string{".gitlab-ci.yml", "ci/e2e.yml"}, found)
	require.Equal(t, []string{".gitlab-ci.yml"}, configured)
}

// Python and Java keep GitHub Actions only.
func TestGitLabLanguageSupport(t *testing.T) {
	for language, supported := range map[string]bool{"javascript": true, "ruby": true, "python": false, "java": false} {
		require.Equal(t, supported, languageSupportsCIProvider(language, gitLabCI{}), language)
	}
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = 'demo'\n")
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  script: [pytest]\n")
	useCIProvider(t, "")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, CheckCIProviderSupport(root, "python", "pytest"), &unsupported)
	require.Equal(t, "GitLab CI", unsupported.Provider)
	require.NoError(t, CheckCIProviderSupport(root, "ruby", "rspec"))
}

func TestGitLabProviderChoice(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  script: [npm test]\n")
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "gitlab"}, choice.Providers)
	writeFile(t, root, ".gitlab-ci.yml", "pages:\n  script: [echo publish]\n")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected, "only GitHub Actions runs tests")
}

// A setup entry that also runs other commands would drop them: it stays a
// command, unresolved.
func TestGitLabSetupEntryWithOtherCommands(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  script:\n"+strings.TrimSuffix(gitLabSetupEntry(gitLabJestSetup), "\n")+"\n      npm test\n")
	jobs, _ := gitLabJobs(t, root)
	step := jobs["test"].Steps[0]
	require.Nil(t, step.Setup)
	require.Contains(t, step.Run, "npm test")
	require.Contains(t, step.ResolutionError, "combines the Datadog setup script with other commands")
}

func TestGitLabIncludeRules(t *testing.T) {
	for _, tc := range []struct {
		name, rules string
		loaded      bool
		gap         string
	}{
		{"always", "      - when: always\n", true, ""},
		{"never", "      - when: never\n", false, ""},
		{"pipeline dependent", "      - if: $CI_COMMIT_BRANCH == \"main\"\n", false, "has rules ddtest cannot decide locally"},
		// GitLab evaluates includes before YAML variables exist.
		{"YAML variable", "      - if: $TARGET == \"node\"\n", false, "has rules ddtest cannot decide locally"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, ".gitlab-ci.yml", "variables:\n  TARGET: node\ninclude:\n  - local: ci/node.yml\n    rules:\n"+tc.rules)
			writeFile(t, root, "ci/node.yml", "unit:\n  script: [npm test]\n")
			jobs, _ := gitLabJobs(t, root)
			_, loaded := jobs["unit"]
			require.Equal(t, tc.loaded, loaded)
			errors := strings.Join(unresolved(t, root), "\n")
			if tc.gap == "" {
				require.Empty(t, errors)
			} else {
				require.Contains(t, errors, tc.gap)
			}
		})
	}
}

func TestGitLabRepeatedIncludesMergeAgain(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "include: [ci/common.yml, ci/override.yml, ci/common.yml]\n")
	writeFile(t, root, "ci/common.yml", "unit:\n  variables:\n    SOURCE: common\n  script: [npm test]\n")
	writeFile(t, root, "ci/override.yml", "unit:\n  variables:\n    SOURCE: override\n")
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, "common", jobs["unit"].Env["SOURCE"])
}

func TestGitLabRuleVariables(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `variables:
  NODE_MAJOR: "20"
workflow:
  rules:
    - if: $NODE_MAJOR == "20"
      variables:
        NODE_MAJOR: "22"
    - when: always
certain:
  image: node:$NODE_MAJOR
  parallel:
    matrix:
      - ENGINE: [node, bun]
  rules:
    - if: $ENGINE == "bun"
      variables:
        RUNNER: bun
    - when: on_success
  script: [npm test]
changes-first:
  rules:
    - changes: [package.json]
    - if: $NODE_MAJOR
      variables:
        SUITE: full
  script: [npm test]
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, "node:22", jobs["certain: [node]"].Image, "workflow rules:variables apply when decidable")
	require.Equal(t, "bun", jobs["certain: [bun]"].Env["RUNNER"])
	require.NotContains(t, jobs["certain: [node]"].Env, "RUNNER")
	require.Empty(t, jobs["certain: [node]"].Steps[0].ResolutionError)
	require.Contains(t, jobs["changes-first"].Steps[0].ResolutionError, "rules:variables set the variables per pipeline")

	writeFile(t, root, ".gitlab-ci.yml", "workflow:\n  rules:\n    - if: $CI_PIPELINE_SOURCE == \"schedule\"\n      variables:\n        NODE_MAJOR: \"24\"\n    - when: always\nunit:\n  script: [npm test]\n")
	require.Contains(t, strings.Join(unresolved(t, root), "\n"), "workflow rules:variables set the variables per pipeline")
}

// A computed export stays unresolved where the tests read the environment.
func TestGitLabComputedExportsStayUnresolved(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n    - export SUITE=$(cat ci/suite)\n    - npx jest\n")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "CI environment variable SUITE requires review")
}

// A job variable reading a value ddtest does not know never reaches the
// tests as text.
func TestGitLabUnknownVariableReadsStayUnresolved(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  variables:\n    TEST_MODE: $CI_PIPELINE_SOURCE\n    API_KEY: $DD_API_KEY\n  script:\n    - npx jest\n")
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, unresolvedCIValue("$CI_PIPELINE_SOURCE"), jobs["test"].Env["TEST_MODE"])
	require.Equal(t, "$DD_API_KEY", jobs["test"].Env["API_KEY"], "setup inputs stay as written")
	useCIProvider(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "TEST_MODE requires review")
}

func TestGitLabReferencedVariables(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", `.settings:
  variables:
    BUNDLE_GEMFILE: gemfiles/rails.gemfile
.job-settings:
  variables:
    SUITE: unit
variables: !reference [.settings, variables]
test:
  variables: !reference [.job-settings, variables]
  script: [bundle exec rspec]
`)
	jobs, _ := gitLabJobs(t, root)
	require.Equal(t, map[string]string{"BUNDLE_GEMFILE": "gemfiles/rails.gemfile", "SUITE": "unit"}, jobs["test"].Env)
	require.Empty(t, unresolved(t, root))
	writeFile(t, root, ".gitlab-ci.yml", "variables: !reference [.missing, variables]\ntest:\n  script: [bundle exec rspec]\n")
	require.Contains(t, strings.Join(unresolved(t, root), "\n"), "variables: !reference [.missing, variables]")
}

// A trigger job its rules never run is not a blocker.
func TestGitLabDisabledTriggersAreNotBlockers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "child:\n  trigger:\n    include: ci/child.yml\n  rules:\n    - when: never\nlater:\n  trigger: group/project\n  when: never\nunit:\n  script: [npm test]\n")
	require.Empty(t, unresolved(t, root))
	writeFile(t, root, ".gitlab-ci.yml", "child:\n  trigger:\n    include: ci/child.yml\n  rules:\n    - if: $CI_COMMIT_TAG\n")
	require.Contains(t, strings.Join(unresolved(t, root), "\n"), "triggers a downstream or child pipeline")
}

// Jobs using GitLab run steps can't be read: they are unresolved unless
// they never run.
func TestGitLabRunStepsAreUnresolved(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "steps-job:\n  run:\n    - name: test\n      step: gitlab.com/components/npm/test@1.0\noff:\n  run:\n    - name: test\n      script: npm test\n  when: never\n")
	errors := strings.Join(unresolved(t, root), "\n")
	require.Contains(t, errors, "GitLab CI job steps-job: uses GitLab CI run steps")
	require.NotContains(t, errors, "job off")
}
