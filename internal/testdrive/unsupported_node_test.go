package testdrive

import (
	"bytes"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// Rows on a Node no dd-trace release supports are ddtest's own decision: they
// count as excluded, never block completion, and the handoff names them.
func TestUnsupportedNodeRowsAreReportedAsNotTried(t *testing.T) {
	for _, legacyRows := range []bool{false, true} {
		root := t.TempDir()
		result := completeValidationFixture("jest")
		jobs := []onboard.RuntimeFinding{{Workflow: ".github/workflows/ci.yml", Job: "test", Step: 3, Node: "24", Status: onboard.StatusCompatible}}
		if legacyRows {
			for _, node := range []string{"16", "17"} {
				jobs = append(jobs, onboard.RuntimeFinding{Workflow: ".github/workflows/legacy.yml", Job: "test", Step: 4, Node: node, Status: onboard.StatusExcluded, Code: onboard.CodeUnsupportedNode, Reason: onboard.UnsupportedNodeReason(node)})
			}
		}
		result.CIRuntime = &onboard.RuntimeCheck{Status: onboard.StatusCompatible, Jobs: jobs}
		var output bytes.Buffer
		require.NoError(t, finishValidation(&output, root, result))
		report := readValidationReport(t, root)
		baseline := report.Summary.Status
		if !legacyRows {
			continue
		}
		require.Equal(t, baseline, report.Summary.Status)
		require.Equal(t, []string{".github/workflows/legacy.yml / test on Node 16, 17"}, report.Summary.CIScope.NotTried)
		require.ElementsMatch(t, []string{"16", "17"}, report.Summary.CIScope.Excluded)
		require.Empty(t, report.Summary.CIScope.Unverified)
		require.Contains(t, report.Summary.FinalResponse, "Not tried: .github/workflows/legacy.yml / test on Node 16, 17 (no dd-trace release supports them; ddtest skipped them on its own, no approval needed).")
	}
}
