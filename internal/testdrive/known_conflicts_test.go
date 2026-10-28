package testdrive

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuspectedRegressionNamesKnownPluginConflicts(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"name":"express","devDependencies":{"mocha":"11.0.0"}}`)
	regression := pairedReport("regression", "passed")
	regression.Compatibility = verdict{Status: "suspected regression", Reason: "Uninstrumented and reporting-only results differ.", Differences: []string{"With instrumentation only: test/res.format.js › given .default: failed"}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, regression))
	got := readValidationReport(t, root)
	require.Len(t, got.Compatibility.Hints, 1)
	require.Contains(t, got.Compatibility.Hints[0], "Known dd-trace conflict (router integration)")
	require.Contains(t, got.Summary.FinalResponse, "set DD_TRACE_DISABLED_INSTRUMENTATIONS=router on the CI test step")

	require.NoError(t, finishValidation(&bytes.Buffer{}, root, pairedReport("compatible", "passed")))
	require.Empty(t, readValidationReport(t, root).Compatibility.Hints, "hints only explain differences")
	other := t.TempDir()
	requireWriteFile(t, filepath.Join(other, "package.json"), `{"name":"app","dependencies":{"fastify":"5.0.0"}}`)
	require.Empty(t, knownConflictHints(other))
}
