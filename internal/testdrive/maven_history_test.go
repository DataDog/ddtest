package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mavenFailureFixture(t *testing.T, root, session string) {
	t.Helper()
	result := validationResult{Framework: "testng", Session: session, HTMLReportCurrent: true, Compatibility: verdict{Status: "suspected regression", Code: "native_outcome_mismatch", Differences: []string{"instrumented-only failure"}},
		Runs: []runSummary{(validationRun{Role: roleBaseline, Command: "mvn test", Tests: []jestTest{{Name: "test", Status: "passed"}}}).summary(), (validationRun{Role: roleReporting, Command: "mvn test", Instrumented: true, ExitCode: 1, Tests: []jestTest{{Name: "test", Status: "failed"}}}).summary()}}
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0700))
	require.NoError(t, os.WriteFile(htmlReportPath(root), []byte(session+` failed comparison HTML <a href="testdrive.json">JSON</a>`), 0600))
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
}

func TestMavenPreviousFailureSurvivesMatchingFullReruns(t *testing.T) {
	root := t.TempDir()
	mavenFailureFixture(t, root, "failed-pair")
	history, err := preserveMavenFailure(root, false)
	require.NoError(t, err)
	require.Equal(t, "failed-pair", history.Session)
	require.Nil(t, history.Result, "compact current reports do not embed growing history")
	archivePath := filepath.Join(filepath.Dir(validationPath(root)), mavenFailureJSON)
	before, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	var archived mavenFailure
	require.NoError(t, json.Unmarshal(before, &archived))
	require.Equal(t, "suspected regression", archived.Result.Compatibility.Status)
	require.Nil(t, archived.Result.PreviousFailure)
	require.NoError(t, os.WriteFile(htmlReportPath(root), []byte("later matching HTML"), 0600))
	current := validationResult{Framework: "testng", Session: "later-pair", PreviousFailure: history, HTMLReportCurrent: true, Compatibility: verdict{Status: "compatible"}, Runs: archived.Result.Runs}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, current))
	for range 3 {
		kept, err := preserveMavenFailure(root, false)
		require.NoError(t, err)
		require.Equal(t, "failed-pair", kept.Session)
	}
	after, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	require.Equal(t, before, after)
	html, err := os.ReadFile(filepath.Join(filepath.Dir(validationPath(root)), mavenFailureHTML))
	require.NoError(t, err)
	require.Equal(t, `failed-pair failed comparison HTML <a href="maven-previous-failure.json">JSON</a>`, string(html))
	require.Contains(t, readValidationReport(t, root).Summary.FinalResponse, "Previous failed comparison: failed-pair")
	// A new failure replaces the one archive slot, without nesting the old pair.
	mavenFailureFixture(t, root, "new-failure")
	replaced, err := preserveMavenFailure(root, false)
	require.NoError(t, err)
	require.Equal(t, "new-failure", replaced.Session)
	require.Nil(t, replaced.Result)
}

func TestMavenHistoryRefusesUnownedOrMismatchedArtifacts(t *testing.T) {
	for _, mode := range []string{"symlink", "unowned", "mismatch", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			mavenFailureFixture(t, root, "failed")
			original, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			directory := filepath.Dir(validationPath(root))
			switch mode {
			case "symlink":
				outside := filepath.Join(t.TempDir(), "customer.txt")
				require.NoError(t, os.WriteFile(outside, []byte("customer data"), 0600))
				require.NoError(t, os.Symlink(outside, filepath.Join(directory, mavenFailureJSON)))
			case "unowned":
				require.NoError(t, os.WriteFile(filepath.Join(directory, mavenFailureJSON), []byte("customer data"), 0600))
			case "mismatch":
				_, err := preserveMavenFailure(root, false)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(directory, mavenFailureHTML), []byte("wrong HTML"), 0600))
			case "oversized":
				require.NoError(t, os.Truncate(htmlReportPath(root), (32<<20)+1))
			}
			_, err = preserveMavenFailure(root, false)
			require.Error(t, err)
			unchanged, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			require.Equal(t, original, unchanged, "a failed archive must not replace the current result")
		})
	}
}

func TestMavenHistoryCheckOnlyDoesNotArchiveCurrentFailure(t *testing.T) {
	root := t.TempDir()
	mavenFailureFixture(t, root, "failed")
	history, err := preserveMavenFailure(root, true)
	require.NoError(t, err)
	require.Nil(t, history)
	require.NoFileExists(t, filepath.Join(filepath.Dir(validationPath(root)), mavenFailureJSON))
}

func TestMavenReportingCrashRemainsARegressionAcrossReruns(t *testing.T) {
	for _, reportError := range []string{"no native test results under target", "parse native report: unexpected EOF"} {
		t.Run(reportError, func(t *testing.T) {
			root := t.TempDir()
			baseline := validationRun{Role: roleBaseline, Command: "mvn test", Tests: []jestTest{{File: ". / Suite", Name: "test", Status: "passed"}}}
			reporting := validationRun{Role: roleReporting, Command: "mvn test", Instrumented: true, ExitCode: 134, ResultError: reportError, Diagnostic: "instrumented JVM crashed before writing reports"}
			comparison := compareMavenNative(baseline, reporting)
			require.Equal(t, "suspected regression", comparison.Status)
			require.Equal(t, "native_outcome_mismatch", comparison.Code)
			result := validationResult{Framework: "junit5", Session: "crash", Compatibility: comparison, Runs: []runSummary{baseline.summary(), reporting.summary()}, HTMLReportCurrent: true}
			require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0700))
			require.NoError(t, os.WriteFile(htmlReportPath(root), []byte(`JVM crash <a href="testdrive.json">JSON</a>`), 0600))
			require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
			history, err := preserveMavenFailure(root, false)
			require.NoError(t, err)
			require.Equal(t, "crash", history.Session)
			archive := filepath.Join(filepath.Dir(validationPath(root)), mavenFailureJSON)
			before, err := os.ReadFile(archive)
			require.NoError(t, err)
			require.Contains(t, string(before), reportError)
			require.Contains(t, string(before), "instrumented JVM crashed")
			result.Session, result.Compatibility, result.PreviousFailure = "later", compareMavenNative(baseline, baseline), history
			matching := baseline
			matching.Role, matching.Instrumented = roleReporting, true
			result.Runs = []runSummary{baseline.summary(), matching.summary()}
			require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
			_, err = preserveMavenFailure(root, false)
			require.NoError(t, err)
			after, err := os.ReadFile(archive)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}
