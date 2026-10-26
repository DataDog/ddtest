package testdrive

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequiredInputDropsTheDerivedConfigurationLine(t *testing.T) {
	validation := validationResult{
		Configurations: []configurationResult{{Command: "npm test", Status: "incomplete"}},
		Summary: validationSummary{BlockingChecks: []string{
			"workflow git_report_exclusions: failed",
			"configuration npm test: incomplete",
		}},
	}
	_, actionable := splitTracerOnlyBlockers(validation)
	require.Equal(t, []string{"workflow git_report_exclusions: failed"}, actionable)

	// When it is the only explanation, the configuration line stays.
	validation.Summary.BlockingChecks = []string{"configuration npm test: incomplete"}
	_, actionable = splitTracerOnlyBlockers(validation)
	require.Equal(t, []string{"configuration npm test: incomplete"}, actionable)
}
