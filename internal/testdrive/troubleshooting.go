// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

// Run failures need findings even when the tracer sent no failed-test events.
// An ordinary test failure already explained by FailedTests gets no extra card.
func runFindingCards(facts intake.Facts, commandFailed bool, runtime reportRuntime) []reportCard {
	if runtime.SetupFailure != nil {
		return []reportCard{*runtime.SetupFailure}
	}
	noEvents := facts.TestEventCount == 0
	if !noEvents && (!commandFailed || len(facts.FailedTests) > 0) {
		return nil
	}
	card := reportCard{Kind: "no-test-events", Scope: runFindingScope, Severity: "error", Title: "No test results received", Count: 1}
	advice := intake.Advice{
		Finding: card.Kind, Title: "Verify test execution and instrumentation",
		Text: "Check the command log (test-output.txt). Confirm the command runs tests, then check that the Datadog library is loaded and supports the selected framework.",
	}
	if commandFailed && len(facts.FailedTests) == 0 {
		card.Kind, card.Title = "command-failed", "Test command failed"
		card.Context = fmt.Sprintf("Command: %s. Error: %s. No failed-test results were received to explain this failure.", runtime.Command, runtime.Error)
		advice.Finding, advice.Title = card.Kind, "Check the command error"
		advice.Text = "Inspect the command log (test-output.txt) for startup, configuration, dependency, or runner errors. Fix the reported error and rerun the testdrive."
		if noEvents {
			card.Context += " No test results were received."
		}
	} else {
		outcome := "exited successfully"
		if commandFailed {
			outcome = "failed: " + runtime.Error
		}
		card.Context = fmt.Sprintf("Command: %s. The command %s, but no test results were received. Test findings and coverage could not be evaluated.", runtime.Command, outcome)
		if strings.TrimSpace(runtime.Output) == "" {
			card.Context += " The command produced no output."
		}
		parts, err := shellquote.Split(runtime.Command)
		if err == nil && len(parts) == 1 && parts[0] == "node" && !commandFailed && strings.EqualFold(runtime.Framework, "vitest") {
			advice.Title = "No test runner was launched"
			advice.Text = "DDTest launched bare node without its test entrypoint. Update ddtest and rerun the testdrive. Vitest options belong in its configuration; select a custom file with --vitest-config."
		}
	}
	if noEvents && commandFailed && strings.EqualFold(runtime.Framework, "pytest") {
		for _, line := range strings.Split(runtime.Output, "\n") {
			_, arguments, found := strings.Cut(line, "error: unrecognized arguments:")
			if !found || !slices.Contains(strings.Fields(arguments), "--ddtrace") {
				continue
			}
			card.Kind, card.Title = "pytest-plugin-not-loaded", "pytest did not load the ddtrace plugin"
			card.Context = "pytest rejected --ddtrace because the ddtrace plugin was not loaded. Automatic instrumentation could not start with this project's pytest configuration."
			advice = intake.Advice{
				Finding: card.Kind, Title: "Load the ddtrace pytest plugin in CI",
				Text: "Check PYTEST_DISABLE_PLUGIN_AUTOLOAD, --disable-plugin-autoload, and -p no:ddtrace in the CI environment and pytest options. If autoloading is intentionally disabled, explicitly load ddtrace once, for example: python -m pytest -p ddtrace --ddtrace. Use the same plugin-loading options for testdrive and CI, then rerun testdrive.",
				URL:  "https://docs.pytest.org/en/stable/how-to/plugins.html#disabling-plugins-from-autoloading",
			}
			break
		}
	}
	card.Advices = []intake.Advice{advice}
	return []reportCard{card}
}
