// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// useCIStatements states runtimes and facts for one test.
func useCIStatements(t *testing.T, runtimes []string, facts ...string) {
	t.Helper()
	var statements []CIRuntimeStatement
	for _, value := range runtimes {
		statement, err := ParseCIRuntime(value)
		require.NoError(t, err)
		statements = append(statements, statement)
	}
	var confirmed []CIFact
	for _, value := range facts {
		fact, err := ParseCIConfirm(value)
		require.NoError(t, err)
		confirmed = append(confirmed, fact)
	}
	require.NoError(t, UseCIStatements(statements, confirmed))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
}

func TestParseCIRuntime(t *testing.T) {
	for _, tc := range []struct {
		value, job, runtime, version, err string
	}{
		{value: "node=22", runtime: "js", version: "22"},
		{value: "js=v22.11.0", runtime: "js", version: "22.11.0"},
		{value: "ruby=3.3.6", runtime: "ruby", version: "3.3.6"},
		{value: "Unit Tests:node=20", job: "Unit Tests", runtime: "js", version: "20"},
		// Job keys keep their own colons: the runtime follows the last one.
		{value: ".buildkite/pipeline.yml / :jest: Jest Tests:node=22", job: ".buildkite/pipeline.yml / :jest: Jest Tests", runtime: "js", version: "22"},
		{value: "node", err: "must be [<job>:]<runtime>=<version>"},
		{value: "node=", err: "must be [<job>:]<runtime>=<version>"},
		{value: "go=1.22", err: `names runtime "go"; supported runtimes: node, python, ruby`},
		{value: "python=3.12", runtime: "python", version: "3.12"},
		{value: "test:python=3.12.7", job: "test", runtime: "python", version: "3.12.7"},
		{value: "python=3", err: "the Python version must be numeric, such as 3.12 or 3.12.7"},
		{value: "node=lts", err: "the Node version must be numeric"},
		{value: "node=22.x", err: "the Node version must be numeric"},
		{value: "ruby=3", err: "the Ruby version must be numeric, such as 3.3 or 3.3.6"},
		{value: ":node=22", err: "names an empty job"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			statement, err := ParseCIRuntime(tc.value)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, CIRuntimeStatement{Job: tc.job, Runtime: tc.runtime, Version: tc.version}, statement)
		})
	}
	// String round-trips through the canonical runtime name.
	statement, err := ParseCIRuntime("Unit Tests:js=22")
	require.NoError(t, err)
	require.Equal(t, "Unit Tests:node=22", statement.String())
}

// Fact keys come from the selected provider: none defines any yet, and a
// registered fact enforces its value.
func TestCIConfirmFactsAreDefinedByTheProvider(t *testing.T) {
	useCIProvider(t, "buildkite")
	fact, err := ParseCIConfirm("plugin-installed")
	require.NoError(t, err)
	require.ErrorContains(t, UseCIStatements(nil, []CIFact{fact}), "--ci-confirm plugin-installed: Buildkite defines no facts to confirm")
	_, err = ParseCIConfirm("=x")
	require.ErrorContains(t, err, "must be <fact>[=<value>]")

	ciProviderFacts["buildkite"] = []ciFactDefinition{
		{Key: "plugin-installed", Statement: func(string) string { return "The plugin is installed" }},
		{Key: "plugin-mode", Values: "agentless|agent", valid: func(value string) bool { return value == "agentless" || value == "agent" }},
	}
	t.Cleanup(func() { delete(ciProviderFacts, "buildkite") })
	for _, tc := range []struct {
		facts []string
		err   string
	}{
		{facts: []string{"plugin-installed", "plugin-mode=agent"}},
		{facts: []string{"other"}, err: "--ci-confirm other is not a Buildkite fact; facts: plugin-installed, plugin-mode"},
		{facts: []string{"plugin-installed=yes"}, err: "--ci-confirm plugin-installed takes no value"},
		{facts: []string{"plugin-mode"}, err: "--ci-confirm plugin-mode needs a value: plugin-mode=agentless|agent"},
		{facts: []string{"plugin-mode=other"}, err: "--ci-confirm plugin-mode needs a value"},
		{facts: []string{"plugin-installed", "plugin-installed"}, err: "is given more than once"},
	} {
		var facts []CIFact
		for _, value := range tc.facts {
			fact, err := ParseCIConfirm(value)
			require.NoError(t, err)
			facts = append(facts, fact)
		}
		err := UseCIStatements(nil, facts)
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err, tc.facts)
			require.Empty(t, CIStatementArguments(), "a rejected statement is not kept")
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []string{"--ci-confirm", "plugin-installed", "--ci-confirm", "plugin-mode=agent"}, CIStatementArguments())
		stated, unused := StatedFacts(nil, "javascript")
		require.Equal(t, []string{"The plugin is installed (--ci-confirm)", "plugin-mode=agent (--ci-confirm)"}, stated)
		require.Empty(t, unused)
	}
	require.NoError(t, UseCIStatements(nil, nil))
}

// Statements are forwarded into follow-up commands and change the input
// fingerprint; without them, both stay as before.
func TestCIStatementsAreForwarded(t *testing.T) {
	useCIProvider(t, "buildkite")
	require.Empty(t, CIStatementsFingerprint())
	without := DDTestCommand("testdrive", "--report")
	require.NotContains(t, without, "--ci-runtime")
	useCIStatements(t, []string{"node=22", "lint:node=20"})
	command := DDTestCommand("testdrive", "--report")
	require.Equal(t, append(without, "--ci-runtime", "node=22", "--ci-runtime", "lint:node=20"), command)
	require.NotContains(t, DDTestCommand("instrument"), "--ci-runtime", "only onboard and testdrive take statements")
	require.NotEmpty(t, CIStatementsFingerprint())
	require.Equal(t, " --ci-runtime node=22 --ci-runtime lint:node=20", ciProviderFollowUpFlag(t.TempDir()))
	// The same statement twice is one; the same key with two versions is an error.
	parse := func(values ...string) []CIRuntimeStatement {
		var statements []CIRuntimeStatement
		for _, value := range values {
			statement, err := ParseCIRuntime(value)
			require.NoError(t, err)
			statements = append(statements, statement)
		}
		return statements
	}
	require.NoError(t, UseCIStatements(parse("lint:node=20", "lint:js=20"), nil))
	require.Equal(t, []string{"--ci-runtime", "lint:node=20"}, CIStatementArguments())
	require.EqualError(t, UseCIStatements(parse("lint:node=20", "lint:node=22"), nil), "--ci-runtime lint:node=20 and lint:node=22 state different versions for the same job")
	require.ErrorContains(t, UseCIStatements(parse("node=20", "node=22"), nil), "--ci-runtime node=20 and node=22 state different versions")
	require.Empty(t, CIStatementArguments(), "a rejected statement is not kept")
}

// Two keys naming the same job must agree; agreeing ones are accepted.
func TestCIRuntimeStatementsForOneJobMustAgree(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - label: unit\n    commands:\n      - npm test\n")
	useCIStatements(t, []string{"unit:node=22", ".buildkite/pipeline.yml / unit:node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	useCIStatements(t, []string{"unit:node=22", ".buildkite/pipeline.yml / unit:node=20", "unit:ruby=3.3"})
	err := CheckCIRuntimeStatements(root)
	var ambiguity *CIRuntimeAmbiguity
	require.True(t, errors.As(err, &ambiguity), err)
	require.EqualError(t, err, "--ci-runtime unit:node=22 and .buildkite/pipeline.yml / unit:node=20 state different versions for the same job, .buildkite/pipeline.yml / unit")
}

// A Buildkite step without an image runs on the agent host: ddtest asks for
// its Node, and a statement fills it, which the runtime check then uses like
// an image tag's and reports as stated.
func TestStatedNodeFillsAnAgentHostStep(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - label: test\n    commands:\n"+buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - npm test\n"+buildkiteDockerStep("lint", "node:20", "      - npm test\n"))
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".buildkite/pipeline.yml / test"}, required.Jobs)
	require.Equal(t, []string{"the Buildkite " + agentHost}, required.Hosts)
	result := CIRuntimeRequiredResult(required)
	require.Equal(t, "ci_runtime_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Equal(t, []string{"ci_runtime:.buildkite/pipeline.yml / test"}, result.RequiredInput)
	require.Contains(t, result.Instruction, "Never guess it")
	require.Contains(t, result.Instruction, "--ci-runtime node=<version>")
	require.Contains(t, result.Instruction, "--ci-runtime '.buildkite/pipeline.yml / test:node=<version>'")

	requirement := func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}
	before := checkCIRuntimes(t.Context(), root, nil, requirement, "jest")
	require.NotEqual(t, StatusCompatible, before.Status)

	useCIStatements(t, []string{"node=22"})
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	check := checkCIRuntimes(t.Context(), root, nil, requirement, "jest")
	findings := map[string]RuntimeFinding{}
	for _, finding := range check.Jobs {
		findings[finding.Job] = finding
	}
	require.Equal(t, StatusCompatible, findings["test"].Status, findings["test"])
	require.Equal(t, "22", findings["test"].Node)
	require.Equal(t, statedRuntimeSource, findings["test"].NodeSource)
	require.True(t, findings["test"].UserStated)
	// A statement never replaces what the configuration selects.
	require.Equal(t, "20", findings["lint"].Node)
	require.False(t, findings["lint"].UserStated)
	require.Empty(t, findings["lint"].RuntimeHost)

	stated, unused := StatedFacts(&check, "javascript")
	require.Equal(t, []string{"Node 22 for .buildkite/pipeline.yml / test (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version. These jobs run in a runner or agent environment the configuration does not describe, so the Bash and curl or wget the Datadog setup script needs there are not checked either."}, stated)
	require.Empty(t, unused)
	// Stated runtimes go through the same tracer check as image runtimes.
	old := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=24"}, nil
	}, "jest")
	for _, finding := range old.Jobs {
		if finding.Job == "test" {
			require.Equal(t, StatusIncompatible, finding.Status)
			require.Equal(t, "Node 22 does not satisfy >=24. Preserve test coverage and choose a compatible tracer/runtime.", finding.Reason)
		}
	}
}

// A statement for a named job wins over one for every job; one that matches
// no job with an unknown runtime is reported as unused.
func TestCIRuntimeStatementScopeAndUnusedStatements(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n  - label: unit\n    commands:\n      - npm test\n  - label: e2e\n    commands:\n      - npm test\n")
	useCIStatements(t, []string{"node=22", "e2e:node=20", ".buildkite/pipeline.yml / missing:node=18", "ruby=3.3"})
	jobs := buildkiteJobs(t, root, buildkiteFile)
	require.Nil(t, jobs["unit"].Stated, "readWorkflows itself records nothing")
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"js": "22", "ruby": "3.3"}, workflows[0].Jobs["unit"].Stated)
	require.Equal(t, map[string]string{"js": "20", "ruby": "3.3"}, workflows[0].Jobs["e2e"].Stated)
	require.NoError(t, CheckCIRuntimeStatements(root), "an unknown job name is not a conflict")
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "jest")
	stated, unused := StatedFacts(&check, "javascript")
	require.Equal(t, []string{
		"Node 20 for .buildkite/pipeline.yml / e2e (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version. These jobs run in a runner or agent environment the configuration does not describe, so the Bash and curl or wget the Datadog setup script needs there are not checked either.",
		"Node 22 for .buildkite/pipeline.yml / unit (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version. These jobs run in a runner or agent environment the configuration does not describe, so the Bash and curl or wget the Datadog setup script needs there are not checked either.",
	}, stated)
	require.Equal(t, []string{
		"--ci-runtime .buildkite/pipeline.yml / missing:node=18 was not used: no validated Node CI job it names has a runtime ddtest could not read.",
		"--ci-runtime ruby=3.3 was not used: no validated Ruby CI job it names has a runtime ddtest could not read.",
	}, unused)
}

// A statement for a named job that contradicts the runtime its configuration
// selects is an error, never an override; an agreeing one is not.
func TestCIRuntimeStatementConflicts(t *testing.T) {
	root := buildkiteRepository(t, "steps:\n"+buildkiteDockerStep("unit", "node:22", "      - npm test\n")+buildkiteDockerStep("lts", "node:lts", "      - npm test\n")+buildkiteDockerStep("untagged", "node", "      - npm test\n"))
	for _, tc := range []struct {
		statement, err string
	}{
		{statement: "unit:node=22.11.0"},
		{statement: ".buildkite/pipeline.yml / unit:node=22"},
		{statement: "node=20"},
		{statement: "unit:node=20", err: "--ci-runtime unit:node=20 contradicts Node 22, which image node:22 selects in .buildkite/pipeline.yml / unit"},
		// A runtime image's floating tag is in the configuration too.
		{statement: "lts:node=22", err: "contradicts Node lts, which image node:lts selects"},
		{statement: "untagged:node=22", err: "contradicts Node latest, which image node selects"},
	} {
		useCIStatements(t, []string{tc.statement})
		err := CheckCIRuntimeStatements(root)
		if tc.err == "" {
			require.NoError(t, err, tc.statement)
			continue
		}
		var conflict *CIRuntimeConflict
		require.True(t, errors.As(err, &conflict), err)
		require.ErrorContains(t, err, tc.err)
	}
	// Neither job's runtime is the user's to state.
	require.NoError(t, UseCIStatements(nil, nil))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
}

// GitHub-hosted runners and job containers keep their own findings; a
// self-hosted runner's runtime is the user's to state, and a setup-node
// version is a conflict for a statement naming its job.
func TestGitHubSelfHostedRunnersTakeRuntimeStatements(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  hosted:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n  own:\n    runs-on: [self-hosted, linux]\n    steps:\n      - run: npm test\n  pinned:\n    runs-on: self-hosted\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: 20\n      - run: npm test\n")
	t.Chdir(root)
	useCIProvider(t, "github")
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / own"}, required.Jobs)
	require.Equal(t, []string{"the self-hosted runner self-hosted, linux"}, required.Hosts)
	useCIStatements(t, []string{"pinned:node=22"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 20, which actions/setup-node selects")
}

// A GitLab job without an image runs on the runner host: ddtest asks for its
// Ruby, and the stated Ruby is what the RubyGems checks and local validation
// compare against; a Ruby image's floating tag is not the user's to state.
func TestStatedRubyFillsAGitLabJobWithoutAnImage(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := gitLabRubyRepository(t, "rspec:\n  script:\n"+gitLabSetupEntry(gitLabRubySetup)+"    - bundle exec rspec\nlatest:\n  image: ruby:latest\n  script:\n"+gitLabSetupEntry(gitLabRubySetup)+"    - bundle exec rspec\n")
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, "Ruby", required.Runtime)
	require.Equal(t, []string{".gitlab-ci.yml / rspec"}, required.Jobs)
	require.Equal(t, []string{"the GitLab CI " + agentHost}, required.Hosts)
	before := RubyCIConfiguration(root, "rspec")
	for _, finding := range before.Jobs {
		if finding.Job == "rspec" {
			require.Contains(t, finding.Reason, "The CI Ruby runtime is unknown: it comes from the GitLab CI runner or agent environment")
		}
	}

	useCIStatements(t, []string{"ruby=2.7"})
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))
	check := RubyCIConfiguration(root, "rspec")
	findings := map[string]RuntimeFinding{}
	for _, finding := range check.Jobs {
		findings[finding.Job] = finding
	}
	require.Equal(t, "2.7", findings["rspec"].Runtime)
	require.True(t, findings["rspec"].UserStated)
	// Its RubyGems is checked like a ruby:2.7 image's.
	require.Contains(t, findings["rspec"].Reason, "gem update --system 3.4.22")
	require.Empty(t, findings["latest"].Runtime)
	require.False(t, findings["latest"].UserStated)
	require.Empty(t, findings["latest"].RuntimeHost)
	stated, _ := StatedFacts(&check, "ruby")
	require.Len(t, stated, 1)
	require.True(t, strings.HasPrefix(stated[0], "Ruby 2.7 for .gitlab-ci.yml / rspec (--ci-runtime)."), stated)
}

// CircleCI machine executors run on the host too.
func TestCircleCIMachineExecutorTakesRuntimeStatements(t *testing.T) {
	root := circleRubyRepository(t, strings.Replace(circleRubyJob("cimg/ruby:3.3", "", "", ""), "    docker:\n      - image: cimg/ruby:3.3\n", "    machine:\n      image: ubuntu-2204:current\n", 1))
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, []string{".circleci/config.yml / main/rspec"}, required.Jobs)
	useCIStatements(t, []string{"main/rspec:ruby=3.3"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))
}

// onboard asks for the runtime before prescribing any setup.
func TestOnboardAsksForAnUnknownCIRuntime(t *testing.T) {
	useCIProvider(t, "gitlab")
	gitLabRubyRepository(t, "rspec:\n  script:\n    - bundle exec rspec\n")
	var output bytes.Buffer
	err := StartWithOptions(&output, StartOptions{Format: "agent"})
	require.Error(t, err)
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Equal(t, "ci_runtime_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Empty(t, result.NextCommand)
	require.Empty(t, result.ResumeCommand)
	require.True(t, slices.Contains(result.RequiredInput, "ci_runtime:.gitlab-ci.yml / rspec"), result.RequiredInput)

	useCIStatements(t, []string{"ruby=3.3"})
	output.Reset()
	_ = StartWithOptions(&output, StartOptions{Format: "agent"})
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Equal(t, "ci_setup_required", result.Code)
	// DD_SITE from the environment adds --site, so look for the pair anywhere.
	flag := slices.Index(result.ResumeCommand, "--ci-runtime")
	require.GreaterOrEqual(t, flag, 0, result.ResumeCommand)
	require.Equal(t, []string{"--ci-runtime", "ruby=3.3"}, result.ResumeCommand[flag:flag+2])
}

func TestWithImplicitTag(t *testing.T) {
	require.Equal(t, "ruby:latest", withImplicitTag("ruby"))
	require.Equal(t, "registry:5000/ruby:latest", withImplicitTag("registry:5000/ruby"))
	require.Equal(t, "ruby:3.3", withImplicitTag(" ruby:3.3 "))
	require.Equal(t, "ruby@sha256:abc", withImplicitTag("ruby@sha256:abc"))
	require.Empty(t, withImplicitTag(""))
}

// An installer a condition skips, statically or on a matrix row, selects no
// runtime for that job: ddtest asks for the host's runtime, and a statement of
// a different version fills it instead of conflicting with the installer.
func TestSkippedInstallersDoNotConflictWithRuntimeStatements(t *testing.T) {
	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  disabled:
    runs-on: self-hosted
    steps:
      - uses: actions/setup-node@v4
        if: false
        with: {node-version: 20}
      - run: npm test
  partial:
    runs-on: self-hosted
    strategy:
      matrix: {runtime: [host, managed]}
    steps:
      - uses: actions/setup-node@v4
        if: matrix.runtime == 'managed'
        with: {node-version: 20}
      - run: npm test
  installed:
    runs-on: self-hosted
    strategy:
      matrix: {shard: [1, 2]}
    steps:
      - uses: actions/setup-node@v4
        if: matrix.shard > 0
        with: {node-version: 20}
      - run: npm test
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / disabled", ".github/workflows/test.yml / partial"}, required.Jobs)

	useCIStatements(t, []string{"disabled:node=22", "partial:node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "jest")
	nodes := map[string][]string{}
	for _, finding := range check.Jobs {
		if finding.UserStated {
			nodes[finding.Job] = append(nodes[finding.Job], finding.Node)
		}
	}
	require.Equal(t, map[string][]string{"disabled": {"22"}, "partial": {"22"}}, nodes)

	// An installer that runs on every row still decides the job's runtime.
	useCIStatements(t, []string{"installed:node=22"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 20, which actions/setup-node selects in .github/workflows/test.yml / installed")
}

func TestSkippedRubyInstallersDoNotConflictWithRuntimeStatements(t *testing.T) {
	useCIProvider(t, "github")
	root := rubyWorkflowFixture(t, `jobs:
  tests:
    runs-on: [self-hosted, linux]
    steps:
      - uses: ruby/setup-ruby@v1
        if: ${{ false }}
        with: {ruby-version: '3.2'}
      - run: bundle exec rspec
  pinned:
    runs-on: [self-hosted, linux]
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.2'}
      - run: bundle exec rspec
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / tests"}, required.Jobs)

	useCIStatements(t, []string{"tests:ruby=3.3"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))
	check := RubyCIConfiguration(root, "rspec")
	findings := map[string]RuntimeFinding{}
	for _, finding := range check.Jobs {
		findings[finding.Job] = finding
	}
	require.Equal(t, "3.3", findings["tests"].Runtime)
	require.True(t, findings["tests"].UserStated)

	useCIStatements(t, []string{"pinned:ruby=3.3"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Ruby 3.2, which ruby/setup-ruby selects")
}

// A self-hosted runner is recognized in runs-on's list and {group, labels}
// forms alike.
func TestSelfHostedRunnerLabelForms(t *testing.T) {
	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  list:
    runs-on: [self-hosted, linux]
    steps:
      - run: npm test
  group:
    runs-on: {group: build, labels: self-hosted}
    steps:
      - run: npm test
  labels:
    runs-on: {group: build, labels: [self-hosted, linux]}
    steps:
      - run: npm test
  hosted:
    runs-on: {group: larger-runners}
    steps:
      - run: npm test
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	hosts := map[string]string{}
	for i, job := range required.Jobs {
		hosts[job] = required.Hosts[i]
	}
	require.Equal(t, map[string]string{
		".github/workflows/test.yml / group":  "the self-hosted runner self-hosted, build",
		".github/workflows/test.yml / labels": "the self-hosted runner self-hosted, linux, build",
		".github/workflows/test.yml / list":   "the self-hosted runner self-hosted, linux",
	}, hosts)

	useCIStatements(t, []string{"node=22"})
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	for _, job := range []string{"list", "group", "labels"} {
		require.Equal(t, map[string]string{"js": "22"}, workflows[0].Jobs[job].Stated, job)
	}
	require.Nil(t, workflows[0].Jobs["hosted"].Stated)
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
}

// The runtime question reads only the repository: a Cypress action job's Node
// is the action's, not the host's, so its metadata is never fetched there.
func TestRuntimeQuestionFetchesNoActionMetadata(t *testing.T) {
	useCIProvider(t, "github")
	fetched := 0
	original := fetchCypressActionMetadata
	fetchCypressActionMetadata = func(context.Context, string) ([]byte, error) {
		fetched++
		return []byte("runs:\n  using: node20\n"), nil
	}
	t.Cleanup(func() { fetchCypressActionMetadata = original })
	root := newJestRepository(t, `jobs:
  e2e:
    runs-on: self-hosted
    steps:
      - uses: cypress-io/github-action@v6
`)
	t.Chdir(root)
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "cypress"))
	require.Zero(t, fetched)

	// The full runtime check still reads it.
	checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "cypress")
	require.Equal(t, 1, fetched)
	_, err := fetchRuntimeMetadata(withOfflineRuntimeMetadata(t.Context()), nil, "https://example.invalid/action.yml")
	require.ErrorContains(t, err, "is not read offline")
}

// Conflicts follow the runtime the tests start on: an installer after the
// first test step selects nothing they run on, and one before it still does.
func TestInstallersAfterTheTestsDoNotConflictWithRuntimeStatements(t *testing.T) {
	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  after:
    runs-on: self-hosted
    steps:
      - run: npm ci
      - run: npm test
      - uses: actions/setup-node@v4
        with: {node-version: 20}
      - run: nvm install 18
  before:
    runs-on: self-hosted
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 18}
      - uses: actions/setup-node@v4
        with: {node-version: 20}
      - run: npm ci
      - run: npm test
      - uses: actions/setup-node@v4
        with: {node-version: 22}
  skipped-test:
    runs-on: self-hosted
    steps:
      - run: npm test
        if: false
      - uses: actions/setup-node@v4
        with: {node-version: 20}
      - run: npm test
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / after"}, required.Jobs)

	useCIStatements(t, []string{"after:node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))

	// The tests run on the last selection before them.
	useCIStatements(t, []string{"before:node=22"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 20, which actions/setup-node selects in .github/workflows/test.yml / before")
	useCIStatements(t, []string{"before:node=20"})
	require.NoError(t, CheckCIRuntimeStatements(root))

	// A test step a condition skips is not where the tests start.
	useCIStatements(t, []string{"skipped-test:node=22"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 20, which actions/setup-node selects in .github/workflows/test.yml / skipped-test")
}

func TestRubyInstallersAfterTheTestsDoNotConflictWithRuntimeStatements(t *testing.T) {
	useCIProvider(t, "github")
	root := rubyWorkflowFixture(t, `jobs:
  after:
    runs-on: [self-hosted, linux]
    steps:
      - run: bundle install
      - run: bundle exec rspec
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.2'}
  before:
    runs-on: [self-hosted, linux]
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '3.2'}
      - run: bundle install
      - run: bundle exec rspec
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / after"}, required.Jobs)

	useCIStatements(t, []string{"after:ruby=3.3"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))

	useCIStatements(t, []string{"before:ruby=3.3"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Ruby 3.2, which ruby/setup-ruby selects in .github/workflows/test.yml / before")
}

// A job whose tests ddtest cannot recognize keeps every installer: a
// statement contradicting one is still a conflict.
func TestInstallersOfAJobWithoutRecognizedTestsStillConflict(t *testing.T) {
	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  build:
    runs-on: self-hosted
    steps:
      - run: npm ci
      - uses: actions/setup-node@v4
        with: {node-version: 20}
`)
	t.Chdir(root)
	useCIStatements(t, []string{"build:node=22"})
	require.ErrorContains(t, CheckCIRuntimeStatements(root), "contradicts Node 20, which actions/setup-node selects in .github/workflows/test.yml / build")
}

// A runner from the matrix is resolved per row: a job whose rows all run on
// self-hosted runners takes a statement like any self-hosted job; in a mixed
// matrix it applies only to the self-hosted rows, and GitHub-hosted rows keep
// their own findings. A runner ddtest cannot resolve is taken as written.
func TestMatrixRunnersTakeRuntimeStatementsPerRow(t *testing.T) {
	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  own:
    runs-on: ${{ matrix.runner }}
    strategy:
      matrix: {runner: [self-hosted]}
    steps:
      - uses: DataDog/test-visibility-github-action@v2
        with: {languages: js, api_key: "${{ secrets.DD_API_KEY }}"}
      - run: npm test
  mixed:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [self-hosted, ubuntu-latest]}
    steps:
      - uses: DataDog/test-visibility-github-action@v2
        with: {languages: js, api_key: "${{ secrets.DD_API_KEY }}"}
      - run: npm test
  unresolved:
    runs-on: ${{ inputs.runner }}
    steps:
      - uses: DataDog/test-visibility-github-action@v2
        with: {languages: js, api_key: "${{ secrets.DD_API_KEY }}"}
      - run: npm test
`)
	t.Chdir(root)
	requirement := func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / mixed", ".github/workflows/test.yml / own"}, required.Jobs)
	require.Equal(t, []string{"the self-hosted runner self-hosted", "the self-hosted runner self-hosted"}, required.Hosts)

	useCIStatements(t, []string{"node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"js": "22"}, workflows[0].Jobs["own"].Stated)
	require.Equal(t, map[string]string{"js": "22"}, workflows[0].Jobs["mixed"].Stated)
	require.Nil(t, workflows[0].Jobs["unresolved"].Stated)

	check := checkCIRuntimes(t.Context(), root, nil, requirement, "jest")
	findings := map[string][]RuntimeFinding{}
	for _, finding := range check.Jobs {
		findings[finding.Job] = append(findings[finding.Job], finding)
	}
	require.Len(t, findings["own"], 1)
	require.Equal(t, CodeMissingInitialization, findings["own"][0].Code, findings["own"][0])
	require.Equal(t, "22", findings["own"][0].Node)
	require.True(t, findings["own"][0].UserStated)

	// The self-hosted row runs the stated Node; the GitHub-hosted row's Node
	// stays GitHub's, unknown to ddtest, and is reported, not filled.
	require.Len(t, findings["mixed"], 2)
	var stated, hosted RuntimeFinding
	for _, finding := range findings["mixed"] {
		if finding.UserStated {
			stated = finding
		} else {
			hosted = finding
		}
	}
	require.Equal(t, "22", stated.Node)
	require.Equal(t, "the self-hosted runner self-hosted", stated.RuntimeHost)
	require.Equal(t, CodeMissingInitialization, stated.Code, stated)
	require.Empty(t, hosted.Node)
	require.Empty(t, hosted.RuntimeHost)
	require.Equal(t, StatusInconclusive, hosted.Status)
	require.Equal(t, CodeRuntimeUnknown, hosted.Code)

	// An unresolvable runner is GitHub's as written: its unknown runtime is
	// reported as inconclusive, never asked for or filled.
	require.Len(t, findings["unresolved"], 1)
	require.Equal(t, StatusInconclusive, findings["unresolved"][0].Status)
	require.Equal(t, CodeRuntimeUnknown, findings["unresolved"][0].Code)
	require.False(t, findings["unresolved"][0].UserStated)
	require.NotEqual(t, StatusCompatible, check.Status)

	statedFacts, unused := StatedFacts(&check, "javascript")
	require.Equal(t, []string{"Node 22 for .github/workflows/test.yml / mixed, .github/workflows/test.yml / own (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version."}, statedFacts)
	require.Empty(t, unused)

	// A statement naming only the unresolvable job is reported as unused.
	useCIStatements(t, []string{"unresolved:node=22"})
	check = checkCIRuntimes(t.Context(), root, nil, requirement, "jest")
	statedFacts, unused = StatedFacts(&check, "javascript")
	require.Empty(t, statedFacts)
	require.Equal(t, []string{"--ci-runtime unresolved:node=22 was not used: no validated Node CI job it names has a runtime ddtest could not read."}, unused)
}

func TestRubyMatrixRunnersTakeRuntimeStatementsPerRow(t *testing.T) {
	useCIProvider(t, "github")
	root := rubyWorkflowFixture(t, `jobs:
  mixed:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix: {os: [self-hosted, ubuntu-latest]}
    steps:
      - run: bundle exec rspec
`)
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "ruby", "rspec")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / mixed"}, required.Jobs)

	useCIStatements(t, []string{"mixed:ruby=3.3"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec"))
	check := RubyCIConfiguration(root, "rspec")
	runtimes := map[bool][]string{}
	for _, finding := range check.Jobs {
		runtimes[finding.UserStated] = append(runtimes[finding.UserStated], finding.Runtime)
	}
	require.Equal(t, map[bool][]string{true: {"3.3"}, false: {""}}, runtimes)
	stated, unused := StatedFacts(&check, "ruby")
	require.Len(t, stated, 1)
	require.True(t, strings.HasPrefix(stated[0], "Ruby 3.3 for .github/workflows/test.yml / mixed (--ci-runtime)."), stated)
	require.Empty(t, unused)
}

// A setup-node without a version installs nothing (it only caches here): the
// Node on PATH stays, so a self-hosted runner's is still the user's to state,
// and a stated one reaches the tests. A setup-node with a version, or a
// version file, still selects it, and a GitHub-hosted job is unchanged.
func TestVersionlessSetupNodeKeepsTheHostRuntime(t *testing.T) {
	useCIProvider(t, "github")
	steps := func(with string) string {
		return `    steps:
      - uses: actions/setup-node@v4
        with: {` + with + `}
      - uses: datadog/test-visibility-github-action@v3
        with:
          languages: js
      - run: npm test
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}
`
	}
	root := newJestRepository(t, "jobs:\n  own:\n    runs-on: [self-hosted, linux]\n"+steps("cache: npm")+
		"  hosted:\n    runs-on: ubuntu-latest\n"+steps("cache: npm")+
		"  pinned:\n    runs-on: [self-hosted, linux]\n"+steps("node-version: 20, cache: npm")+
		"  file:\n    runs-on: [self-hosted, linux]\n"+steps("node-version-file: .nvmrc, cache: npm"))
	writeFile(t, root, ".nvmrc", "v20.11.0\n")
	t.Chdir(root)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{".github/workflows/test.yml / own"}, required.Jobs)
	require.Equal(t, []string{"the self-hosted runner self-hosted, linux"}, required.Hosts)

	check := func() map[string]RuntimeFinding {
		result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
		}, "jest")
		findings := map[string]RuntimeFinding{}
		for _, finding := range result.Jobs {
			findings[finding.Job] = finding
		}
		return findings
	}
	findings := check()
	require.Equal(t, CodeRuntimeUnknown, findings["own"].Code)
	require.Contains(t, findings["own"].Reason, "Ask the user which Node version it runs")
	// A GitHub-hosted runner's preinstalled Node stays unknown, as without
	// the step.
	require.Equal(t, CodeRuntimeUnknown, findings["hosted"].Code)
	require.Empty(t, findings["hosted"].RuntimeHost)
	require.Empty(t, findings["hosted"].NodeSource)
	require.Equal(t, "The CI Node runtime is unknown after setup; preserve the workflow and resolve it before claiming compatibility.", findings["hosted"].Reason)
	require.Equal(t, "20", findings["pinned"].Node)
	require.Equal(t, "20.11.0", findings["file"].Node)

	useCIStatements(t, []string{"own:node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root), "a versionless setup-node selects no version to contradict")
	useCIStatements(t, []string{"node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	findings = check()
	require.Equal(t, StatusCompatible, findings["own"].Status, findings["own"])
	require.Equal(t, "22", findings["own"].Node)
	require.Equal(t, statedRuntimeSource, findings["own"].NodeSource)
	require.True(t, findings["own"].UserStated)
	require.Equal(t, CodeRuntimeUnknown, findings["hosted"].Code)
	require.False(t, findings["hosted"].UserStated)
	require.Equal(t, "20", findings["pinned"].Node)
	require.False(t, findings["pinned"].UserStated)
	require.Equal(t, "20.11.0", findings["file"].Node)
	require.False(t, findings["file"].UserStated)

	// Apply partitions rows on the same runtime.
	workflows, err := readWorkflows(root)
	require.NoError(t, err)
	for job, want := range map[string]string{"own": "22", "hosted": "", "pinned": "20", "file": "20.11.0"} {
		node, err := rowSetupNode(root, workflows[0], workflows[0].Jobs[job], nil)
		require.NoError(t, err)
		require.Equal(t, want, node, job)
	}
}

// setup-node takes node-semver selectors: a statement in the line they select
// agrees, one outside it conflicts, and one ddtest cannot evaluate is
// accepted and reported unused with that selector.
func TestSetupNodeSelectorsAgreeWithStatedVersions(t *testing.T) {
	for _, tc := range []struct {
		selector, stated  string
		agree, comparable bool
	}{
		{"20.x", "20.18.0", true, true},
		{"20.X", "20", true, true},
		{"20.*", "20.18", true, true},
		{"20.18.x", "20.18.1", true, true},
		{"20.x", "22", false, true},
		{"20.18.x", "20.17", false, true},
		{"^20", "20.18.0", true, true},
		{"^20.18", "20.19.1", true, true},
		{"^20.18", "20.17.0", false, true},
		{"^20.18", "21", false, true},
		{"^0.10", "0.10.5", true, true},
		{"^0.10", "0.11", false, true},
		{"~20.18", "20", true, true},
		{"~20.18", "20.18.3", true, true},
		{"~20.18", "20.19", false, true},
		{"~20", "20.1", true, true},
		{"~>20.18", "20.18", true, true},
		{"=20.18.0", "20.18.0", true, true},
		{"=20.18.0", "20.18.1", false, true},
		{"v20", "20.18.0", true, true},
		{"v22", "20", false, true},
		{"20", "20.18.0", true, true},
		{"20.18.0", "20", true, true},
		{"20", "22", false, true},
		{">=18 <21", "20", false, false},
		{"18 || 20", "20", false, false},
		{">=18", "20", false, false},
		{"lts/*", "20", false, false},
		{"lts/iron", "20", false, false},
		{"node", "22", false, false},
		{"latest", "22", false, false},
		{"current", "22", false, false},
		{"*", "22", false, false},
		{"x", "22", false, false},
		{"20.0.0-rc.1", "20", false, false},
	} {
		agree, comparable := nodeSelectorAgrees(tc.selector, tc.stated)
		require.Equal(t, tc.comparable, comparable, "%s vs %s", tc.selector, tc.stated)
		require.Equal(t, tc.agree, agree, "%s vs %s", tc.selector, tc.stated)
	}

	useCIProvider(t, "github")
	root := newJestRepository(t, `jobs:
  wildcard:
    runs-on: self-hosted
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '20.x'}
      - run: npm test
  range:
    runs-on: self-hosted
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '>=18 <21'}
      - run: npm test
  lts:
    runs-on: self-hosted
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 'lts/*'}
      - run: npm test
`)
	t.Chdir(root)
	for _, statement := range []string{"wildcard:node=20.18.0", "wildcard:node=20"} {
		useCIStatements(t, []string{statement})
		require.NoError(t, CheckCIRuntimeStatements(root), statement)
	}
	useCIStatements(t, []string{"wildcard:node=22"})
	err := CheckCIRuntimeStatements(root)
	var conflict *CIRuntimeConflict
	require.True(t, errors.As(err, &conflict), err)
	require.ErrorContains(t, err, "--ci-runtime wildcard:node=22 contradicts Node 20.x, which actions/setup-node selects in .github/workflows/test.yml / wildcard")

	useCIStatements(t, []string{"range:node=20", "lts:node=22"})
	require.NoError(t, CheckCIRuntimeStatements(root))
	_, unused := StatedFacts(nil, "javascript")
	require.Equal(t, []string{
		"--ci-runtime range:node=20 was not used: no validated Node CI job it names has a runtime ddtest could not read. actions/setup-node selects Node >=18 <21 in .github/workflows/test.yml / range: a selector ddtest cannot compare with a stated version, so the statement was accepted without checking that it agrees.",
		"--ci-runtime lts:node=22 was not used: no validated Node CI job it names has a runtime ddtest could not read. actions/setup-node selects Node lts/* in .github/workflows/test.yml / lts: a selector ddtest cannot compare with a stated version, so the statement was accepted without checking that it agrees.",
	}, unused)
	// A new set of statements starts without those notes.
	useCIStatements(t, []string{"range:node=20"})
	_, unused = StatedFacts(nil, "javascript")
	require.Equal(t, []string{"--ci-runtime range:node=20 was not used: no validated Node CI job it names has a runtime ddtest could not read."}, unused)
}
