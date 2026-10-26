package testdrive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

// The specific missing-coverage reason and Cucumber's own advice reach the
// HTML card and the terminal, instead of the generic "wrap testdrive in nyc".
func TestCucumberMissingCoverageShowsItsReasonAndAdvice(t *testing.T) {
	reason := "No application source-coverage events arrived from Cucumber parallel workers."
	model := buildReport(t.TempDir(), intake.Facts{MissingCoverage: true}, false, reportRuntime{Framework: "Cucumber", CoverageReason: reason, CoverageSetup: intake.CoverageSetup{Wrapper: "nyc", Parallel: true}})
	var card *reportCard
	for i := range model.Cards {
		if model.Cards[i].Kind == intake.MissingCoverageFinding {
			card = &model.Cards[i]
		}
	}
	require.NotNil(t, card)
	require.Equal(t, intake.MissingCoverageContext+" "+reason, card.Context)
	require.Len(t, card.Advices, 1)
	require.Equal(t, "Cucumber parallel workers send no coverage", card.Advices[0].Title)

	var output bytes.Buffer
	writeRunFindings(&output, intake.Facts{MissingCoverage: true, TestEventCount: 1}, false, model.Cards...)
	require.Contains(t, output.String(), reason)
	require.Contains(t, output.String(), "Fix: Cucumber parallel workers send no coverage")
	require.False(t, strings.Contains(output.String(), "nyc ddtest testdrive"))
}
