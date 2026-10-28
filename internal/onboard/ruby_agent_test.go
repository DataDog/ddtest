package onboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const rubyAgentWorkflow = `jobs:
  tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        ruby: ['3.3', head]
    steps:
      - uses: actions/checkout@v4
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}', bundler-cache: true}
ACTION      - run: bundle exec rspec
`

func rubyAgentFixture(t *testing.T, framework, action string) {
	t.Helper()
	t.Chdir(t.TempDir())
	gem := "rspec"
	if framework == "minitest" {
		gem = "minitest"
	}
	require.NoError(t, os.WriteFile("Gemfile", []byte("source 'https://rubygems.org'\ngem '"+gem+"'\n"), 0600))
	workflow := strings.Replace(rubyAgentWorkflow, "ACTION", action, 1)
	if framework == "minitest" {
		workflow = strings.Replace(workflow, "bundle exec rspec", "bundle exec rake test", 1)
		require.NoError(t, os.WriteFile("Rakefile", []byte("require 'rake/testtask'\nRake::TestTask.new(:test)\n"), 0600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(".github", "workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "test.yml"), []byte(workflow), 0600))
}

const rubyAgentAction = `      - uses: datadog/test-visibility-github-action@v3
        if: matrix.ruby != 'head'
        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}
`

func rubyAgentStart(t *testing.T, options StartOptions) (AgentResult, error) {
	t.Helper()
	var out bytes.Buffer
	err := StartWithOptions(&out, options)
	var state AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state), out.String())
	require.Equal(t, 2, state.SchemaVersion)
	return state, err
}

func TestRubyAgentOnboardingRequiresCISetupBeforeValidation(t *testing.T) {
	rubyAgentFixture(t, "rspec", "")
	options := StartOptions{Format: "json", Site: "datadoghq.eu", Tracer: "1.42.0", Facts: "/external/facts.json", Platform: "ruby", Timeout: "20m0s", AllowReviewedBuilds: true}
	state, err := rubyAgentStart(t, options)
	require.Error(t, err)
	require.Equal(t, "needs_input", state.Status)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Equal(t, "apply_source_edit", state.NextAction)
	require.Empty(t, state.NextCommand, "the agent edits CI first")
	command := state.ResumeCommand[1:]
	require.Equal(t, []string{"testdrive", "--framework", "rspec", "--all", "--agent", "--yes"}, command[:6])
	for _, value := range []string{"--format", "json", "--site", "datadoghq.eu", "--tracer-version", "1.42.0", "--facts", "/external/facts.json", "--platform", "ruby", "--timeout", "20m0s", "--allow-reviewed-builds"} {
		require.Contains(t, command, value)
	}
	require.Contains(t, state.Reason, AgentScope)
	require.Contains(t, state.Reason, PrescribedEditInstruction)
	require.Contains(t, state.Reason, "run resume_command once")
	require.NotEmpty(t, state.Capabilities)
	require.Contains(t, state.Instruction, "datadog/test-visibility-github-action@v3")
	require.Contains(t, state.Instruction, ".github/workflows/test.yml")
	require.Contains(t, state.Instruction, "`site: datadoghq.eu`", "the action reports to the selected site")
	// The handoff runs validation once; the instructions must not add runs.
	for _, extra := range []string{"--all --check-only", "ddtest testdrive", "PASSED"} {
		require.NotContains(t, state.Instruction, extra)
	}
}

func TestRubyAgentOnboardingIsReadyOnceTheActionIsConfigured(t *testing.T) {
	rubyAgentFixture(t, "rspec", strings.Replace(rubyAgentAction, "languages: ruby,", "languages: ruby, site: datadoghq.eu,", 1))
	var outputs []string
	for range 2 {
		var out bytes.Buffer
		require.NoError(t, StartWithOptions(&out, StartOptions{Format: "agent", Site: "datadoghq.eu"}), out.String())
		outputs = append(outputs, out.String())
	}
	require.Equal(t, outputs[0], outputs[1], "repeated onboarding returns the same handoff")
	var state AgentResult
	require.NoError(t, json.Unmarshal([]byte(outputs[0]), &state))
	require.Equal(t, "ready", state.Status)
	require.Equal(t, "run_command", state.NextAction)
	require.Equal(t, []string{"testdrive", "--framework", "rspec", "--all", "--agent", "--yes", "--site", "datadoghq.eu"}, state.NextCommand[1:])
	require.False(t, slices.Contains(state.NextCommand, "--format"), "--agent already selects the agent format")
	require.Contains(t, state.Reason, AgentScope)
	require.NotEmpty(t, state.Capabilities)
	require.Empty(t, state.Code)
}

func TestRubyAgentOnboardingRejectsCommandOverride(t *testing.T) {
	rubyAgentFixture(t, "rspec", rubyAgentAction)
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Command: "bundle exec rspec spec/models"})
	require.Error(t, err)
	require.Equal(t, "invalid_options", state.Code)
	require.Empty(t, state.NextCommand)
	require.Empty(t, state.ResumeCommand)
}

func TestRubyAgentMinitestInstructionsKeepForkSetup(t *testing.T) {
	rubyAgentFixture(t, "minitest", "")
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Equal(t, "minitest", state.ResumeCommand[3])
	require.Contains(t, state.Instruction, "ci/datadog_test_optimization.rb")
}

func TestRubyHumanOnboardingPrintsManualGuidance(t *testing.T) {
	rubyAgentFixture(t, "rspec", "")
	var out bytes.Buffer
	require.NoError(t, StartWithOptions(&out, StartOptions{Human: true}))
	require.False(t, json.Valid(bytes.TrimSpace(out.Bytes())))
	require.Contains(t, out.String(), "ddtest testdrive --framework rspec --all")
}

func TestRubyCISetupConfiguredNeedsAnInstrumentedStep(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		configured   bool
	}{
		{"missing", "", false},
		{"every row excluded", strings.Replace(rubyAgentAction, "matrix.ruby != 'head'", "false", 1), false},
		{"head excluded", rubyAgentAction, true},
		{"other language", strings.Replace(rubyAgentAction, "languages: ruby", "languages: js", 1), false},
		{"no API key", strings.Replace(rubyAgentAction, "api_key: '${{ secrets.DD_API_KEY }}', ", "", 1), false},
		{"no tracer version", strings.Replace(rubyAgentAction, ", ruby-tracer-version: '1.42.0'", "", 1), false},
		{"tracer requirement", strings.Replace(rubyAgentAction, "'1.42.0'", "'~> 1.42'", 1), false},
		{"tracer latest", strings.Replace(rubyAgentAction, "'1.42.0'", "latest", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rubyAgentFixture(t, "rspec", tc.action)
			root, err := os.Getwd()
			require.NoError(t, err)
			require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", ""))
		})
	}
}

func TestRubyCISetupConfiguredNeedsTheSelectedSite(t *testing.T) {
	rubyAgentFixture(t, "rspec", rubyAgentAction)
	root, err := os.Getwd()
	require.NoError(t, err)
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.eu"), "the action would report to the default site")
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Instruction, "`site: datadoghq.eu`")
	require.NotContains(t, rubyDiscovery{}.agentInstructions("rspec", ""), "site:")
	require.NotContains(t, rubyDiscovery{}.agentInstructions("rspec", "datadoghq.com"), "__SITE_INPUT__")
}

func TestRubyAgentOnboardingAsksForAScopeDecisionWhenNothingResolves(t *testing.T) {
	rubyAgentFixture(t, "rspec", "")
	workflow, err := os.ReadFile(filepath.Join(".github", "workflows", "test.yml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "test.yml"), []byte(strings.Replace(string(workflow), "bundle exec rspec", "bundle exec rake", 1)), 0600))
	require.NoError(t, os.WriteFile("Rakefile", []byte("task default: :ci\n"), 0600))
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "needs_input", state.Status)
	require.Equal(t, "scope_decision_required", state.Code, "the first answer is the scope decision, not a CI setup handoff")
	require.Equal(t, "supply_input", state.NextAction)
	require.Empty(t, state.ResumeCommand, "no validation can run before the decision")
	require.Empty(t, state.NextCommand)
	require.Empty(t, state.Instruction, "no CI setup is prescribed for steps ddtest cannot validate")
	require.Contains(t, state.Location, "bundle exec rake")
	require.Contains(t, state.Reason, "do not combine --command with --all")
	require.NotEmpty(t, state.RequiredInput)
}

func TestRubyAgentOnboardingNamesSharedStepsAndUnresolvedBlockers(t *testing.T) {
	rubyAgentFixture(t, "rspec", "")
	workflow, err := os.ReadFile(filepath.Join(".github", "workflows", "test.yml"))
	require.NoError(t, err)
	updated := strings.Replace(string(workflow), "      - run: bundle exec rspec\n", "      - run: bundle exec rake\n      - run: ./ci/integration-tests.sh\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "test.yml"), []byte(updated), 0600))
	require.NoError(t, os.WriteFile("Rakefile", []byte("task default: [:spec, :features]\n"), 0600))
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Reason, "Shared CI step: This CI step also runs Rake task(s) features")
	require.Contains(t, state.Reason, "cannot be validated and will remain blockers")
	require.Contains(t, state.Instruction, "[.] bundle exec rake spec — .github/workflows/test.yml")
	require.Contains(t, state.Instruction, "(+1 more CI locations)", "the agent listing keeps one location per configuration")
}

func TestOneLineExcerptKeepsTheScriptHeadAndReason(t *testing.T) {
	text := "ci.yml / docs / step 4 (gem install rack\ncd rack-3.2*\nbundle exec rdoc\n): Repository script could not be resolved: gem install rack\ncd rack-3.2*\n"
	require.Equal(t, "ci.yml / docs / step 4 (gem install rack …): Repository script could not be resolved: gem install rack …", OneLineExcerpt(text))
	require.Equal(t, "single line", OneLineExcerpt("single line"))
}

func TestRubyAgentOnboardingDefaultsToDDSite(t *testing.T) {
	rubyAgentFixture(t, "rspec", "")
	t.Setenv("DD_SITE", "datadoghq.eu")
	state, err := rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Instruction, "`site: datadoghq.eu`", "the action reports to DD_SITE, as onboard --apply does")
	require.Contains(t, state.ResumeCommand, "datadoghq.eu")
	rubyAgentFixture(t, "rspec", strings.Replace(rubyAgentAction, "languages: ruby,", "languages: ruby, site: datadoghq.eu,", 1))
	state, err = rubyAgentStart(t, StartOptions{Format: "json"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status, "an action already on DD_SITE is complete")
	t.Setenv("DD_SITE", "evil.com; rm -rf /")
	state, err = rubyAgentStart(t, StartOptions{Format: "json"})
	require.Error(t, err)
	require.Equal(t, "invalid_options", state.Code)
}
