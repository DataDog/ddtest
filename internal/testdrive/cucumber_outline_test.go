package testdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestCucumberOutlineTelemetryNeedsEveryExample(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "one.feature")
	run := validationRun{framework: "cucumber", root: root, Tests: []jestTest{
		{File: file, Name: "same", Status: "passed", CucumberLine: 8},
		{File: file, Name: "same", Status: "passed", CucumberLine: 9},
	}}
	telemetry := func(spans ...uint64) intake.Facts {
		var attempts []intake.TestRun
		for _, span := range spans {
			attempts = append(attempts, intake.TestRun{Status: "pass", SpanID: span})
		}
		return intake.Facts{TestEventCount: len(spans), Tests: []intake.Test{{Name: "same", SourceFile: "one.feature", Attempts: attempts}}}
	}

	run.Facts = telemetry(1, 2)
	require.Empty(t, verifyCucumberTelemetry(run).Status, "one span per example verifies both")

	// The second example's event is missing and the first one's is sent twice.
	run.Facts = telemetry(1, 1)
	check := verifyCucumberTelemetry(run)
	require.Equal(t, "inconclusive", check.Status)
	require.Contains(t, strings.Join(check.Differences, "\n"), `Duplicate telemetry: file "`+file+`", test "same", one span reported 2 times`)
	require.Contains(t, strings.Join(check.Differences, "\n"), `Missing telemetry: file "`+file+`", test "same", status "pass" (count 1)`)
}

func TestCucumberDisclosesOutlineExamplesSharingAName(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "native.ndjson")
	messages := func(names ...string) string {
		data := `{"gherkinDocument":{"feature":{"children":[{"scenario":{"id":"outline","location":{"line":3},"examples":[{"tableBody":[{"id":"row1","location":{"line":8}},{"id":"row2","location":{"line":9}}]}]}}]}}}
`
		for i, row := range []string{"row1", "row2"} {
			id := string(rune('a' + i))
			data += `{"pickle":{"id":"` + id + `","uri":"one.feature","name":"` + names[i] + `","astNodeIds":["outline","` + row + `"]}}
{"testCase":{"id":"c` + id + `","pickleId":"` + id + `"}}
{"testCaseStarted":{"id":"s` + id + `","testCaseId":"c` + id + `"}}
{"testCaseFinished":{"testCaseStartedId":"s` + id + `"}}
`
		}
		return data + `{"testRunFinished":{"success":true}}`
	}
	read := func(instrumented bool, names ...string) validationRun {
		require.NoError(t, os.WriteFile(filename, []byte(messages(names...)), 0600))
		run := validationRun{framework: "cucumber", root: root, Instrumented: instrumented}
		readCucumberResults(filename, root, &run)
		require.Empty(t, run.ResultError)
		return run
	}

	shared := read(true, "same", "same")
	require.Len(t, shared.NotExercised, 1)
	require.Contains(t, shared.NotExercised[0], `one.feature: "same", 2 examples`)
	require.Contains(t, shared.NotExercised[0], "no per-example identity")
	require.Empty(t, read(false, "same", "same").NotExercised, "only instrumented runs are checked against telemetry")
	require.Empty(t, read(true, "add 1", "add 2").NotExercised, "distinct example names are verified one by one")
}
