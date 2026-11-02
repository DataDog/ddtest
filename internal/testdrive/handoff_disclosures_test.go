package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// Trials applied DD_TRACE_DISABLED_INSTRUMENTATIONS=router on express's CI
// step, and the canonical handoff never said an integration was off.
func TestHandoffNamesDisabledInstrumentations(t *testing.T) {
	result := pairedReport("router", "passed")
	result.Configurations = []configurationResult{{
		TestCommand: onboard.TestCommand{Command: "npm run test-ci", Environment: map[string]string{"DD_TRACE_DISABLED_INSTRUMENTATIONS": "router", "DD_TRACE_HTTP_ENABLED": "true", "DD_TRACE_FS_ENABLED": "false"}},
		Status:      "passed",
	}}
	response := validationHandoff(result)
	require.Contains(t, response, "Disabled instrumentation: DD_TRACE_DISABLED_INSTRUMENTATIONS=router on the CI test step of npm run test-ci")
	require.Contains(t, response, "Disabled instrumentation: DD_TRACE_FS_ENABLED=false on the CI test step of npm run test-ci")
	require.NotContains(t, response, "DD_TRACE_HTTP_ENABLED", "an integration left enabled is not a disclosure")
}

func TestCIScopeCountsEachNodeVersionOnce(t *testing.T) {
	scope := summarizeCIScope(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{
		{Workflow: "ci.yml", Job: "test", Node: "20", Status: "compatible"},
		{Workflow: "ci.yml", Job: "test", Node: "20", Status: "excluded", Reason: "Windows runner"},
		{Workflow: "legacy.yml", Job: "test", Node: "16", Status: "excluded", Code: onboard.CodeUnsupportedNode},
		{Workflow: "ci.yml", Job: "lint", Node: "20", Status: "unverified"},
	}})
	instrumented, excluded, unverified := scope.counts()
	require.Equal(t, []int{1, 1, 0}, []int{instrumented, excluded, unverified}, "Node 20 is counted once, as instrumented")
	require.Contains(t, scope.Excluded, "20", "the per-job finding is kept")
}

func TestAPIKeyActionFollowsTheActionGuard(t *testing.T) {
	require.Equal(t, []string{onboard.CIProviderAPIKeyAction(false)}, apiKeyHumanActions(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Status: "compatible"}}}))
	require.Equal(t, []string{onboard.CIProviderAPIKeyAction(true)}, apiKeyHumanActions(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Status: "excluded", Reason: onboard.GuardedActionBranch}}}))
	require.Contains(t, onboard.CIProviderAPIKeyAction(true), "is skipped")
}

// A job that sets up the tracer by hand reads the key from the provider's
// secrets without its setup, so the action names only where to store it.
func TestAPIKeyActionForHandWrittenSetups(t *testing.T) {
	onboard.UseCIProvider("gitlab")
	t.Cleanup(func() { onboard.UseCIProvider("") })
	handWritten := onboard.RuntimeFinding{Status: "compatible", Action: "hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)"}
	actions := apiKeyHumanActions(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{handWritten, {Status: "excluded"}}})
	require.Equal(t, []string{onboard.CIProviderHandWrittenKeyAction()}, actions)
	require.Contains(t, actions[0], "Store DD_API_KEY in a masked GitLab CI/CD variable")
	require.NotContains(t, actions[0], "setup script")
	// A confirmed Datadog Agent holds the key for the jobs that report
	// through it.
	agent := onboard.RuntimeFinding{Status: "compatible", Action: handWritten.Action, AgentReporting: true}
	require.Empty(t, apiKeyHumanActions(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{agent}}))
	require.Equal(t, []string{onboard.CIProviderHandWrittenKeyAction()}, apiKeyHumanActions(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{agent, handWritten}}))
	// The setup script reads the same masked variable.
	mixed := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{handWritten, {Status: "compatible", Action: "test-optimization-gitlab-script v1.2.10"}}}
	require.Equal(t, []string{onboard.CIProviderAPIKeyAction(false)}, apiKeyHumanActions(mixed))

	// The Jenkins plugin keeps its own key, so a hand-written container
	// stage beside a plugin-instrumented one needs its credential too.
	onboard.UseCIProvider("jenkins")
	mixed = &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Status: "compatible", Action: "Datadog Jenkins plugin"}, handWritten}}
	actions = apiKeyHumanActions(mixed)
	require.Equal(t, []string{onboard.CIProviderAPIKeyAction(false), onboard.CIProviderHandWrittenKeyAction()}, actions)
	require.Contains(t, actions[0], "configure the Datadog plugin")
	require.Contains(t, actions[1], "Store DD_API_KEY in")
}

// A rerun-until-green must stay visible: trials reran an inconclusive
// validation unchanged and the final report kept no trace of it.
func TestHandoffListsEarlierStoppedAttempts(t *testing.T) {
	result := pairedReport("retried", "passed")
	result.Workflow = &WorkflowEvidence{EarlierAttempts: []string{"file_descriptors_exhausted", "validation_incomplete"}}
	require.Contains(t, validationHandoff(result), "Earlier attempts for this workflow stopped before this run: file_descriptors_exhausted, validation_incomplete.")
	result.Workflow.EarlierAttempts = nil
	require.NotContains(t, validationHandoff(result), "Earlier attempts")
}

// nion's first attempt saw a flaky baseline test and a rerun completed: the
// final response names the tests that varied, not only the earlier code.
func TestHandoffListsTestsEarlierAttemptsSawVary(t *testing.T) {
	earlier := validationResult{Configurations: []configurationResult{{Compatibility: verdict{Code: "varying_outcomes", Differences: []string{
		"Varies between repeats: test/integration.test.js › Nion fetches data on mount; baseline: passed -> failed; instrumented: passed -> passed",
		"Varies between repeats: test/integration.test.js › Nion fetches data on mount; baseline: failed -> passed; instrumented: passed -> passed",
	}}}, {Compatibility: verdict{Code: "recurring_difference", Differences: []string{"Recurring difference in both pairs: a.test.js › b; baseline: passed; instrumented: failed"}}}}}
	require.Equal(t, []string{"test/integration.test.js › Nion fetches data on mount"}, varyingTests(earlier))

	result := pairedReport("retried", "passed")
	result.Workflow = &WorkflowEvidence{EarlierAttempts: []string{"validation_incomplete"}, EarlierVaryingTests: varyingTests(earlier)}
	require.Contains(t, validationHandoff(result), "Caveat (not a blocker): an earlier attempt saw varying outcomes in: test/integration.test.js › Nion fetches data on mount; they had consistent outcomes in this run's repeated comparison, so they may be flaky.")
}
