package maven

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadReportsPreservesModulesOutcomesAndDuplicateCases(t *testing.T) {
	root := t.TempDir()
	for _, module := range []string{"one", "two"} {
		dir := filepath.Join(root, module, "target/surefire-reports")
		require.NoError(t, os.MkdirAll(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-example.xml"), []byte(`<testsuite name="example.Tests"><testcase name="passes" classname="example.Tests"/><testcase name="parameters[1]" classname="example.Tests"/><testcase name="parameters[2]" classname="example.Tests"/><testcase name="fails" classname="example.Tests"><failure type="AssertionError" message="expected 2"/></testcase><testcase name="ignored" classname="example.Tests"><skipped/></testcase><testcase name="broken" classname="example.Tests"><error type="IllegalStateException" message="fixture"/></testcase></testsuite>`), 0600))
	}
	tests, err := ReadReports(root)
	require.NoError(t, err)
	require.Len(t, tests, 12)
	require.Equal(t, "one", tests[0].Module)
	require.Equal(t, "two", tests[6].Module)
	require.Equal(t, "failed", tests[3].Status)
	require.Contains(t, tests[3].Failure, "expected 2")
	require.Equal(t, "skipped", tests[4].Status)
	require.Equal(t, "failed", tests[5].Status)
}

func TestReadReportsRequiresValidFreshNativeResults(t *testing.T) {
	for _, data := range []string{"<broken", `<testsuite/>`, `<testsuite><testcase/></testsuite>`, `<unrelated/>`} {
		t.Run(data, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "target/failsafe-reports")
			require.NoError(t, os.MkdirAll(dir, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-example.xml"), []byte(data), 0600))
			_, err := ReadReports(root)
			require.Error(t, err)
		})
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "TEST-old.xml"), []byte(`<testsuite><testcase name="stale"/></testsuite>`), 0600))
	_, err := ReadReports(root)
	require.Error(t, err)
}

func TestReadReportsIgnoresTestNGDuplicateReporter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "target/surefire-reports")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "junitreports"), 0700))
	data := []byte(`<testsuite><testcase name="one" classname="example.Test"/></testsuite>`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-example.xml"), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "junitreports/TEST-example.xml"), data, 0600))
	tests, err := ReadReports(root)
	require.NoError(t, err)
	require.Len(t, tests, 1)
}

func TestKarateNativeScenariosAndErrors(t *testing.T) {
	data := []byte(`{"name":"feature","relativePath":"example/feature.feature","scenarioResults":[{"name":"works","failed":false},{"name":"fails","failed":true,"stepResults":[{"result":{"status":"failed","errorMessage":"ddtest intentional failure"}}]}]}`)
	tests, err := readKarate(data, "module")
	require.NoError(t, err)
	require.Len(t, tests, 2)
	require.Equal(t, "[example/feature] feature", tests[0].Suite)
	require.Equal(t, "skipped", tests[0].Status)
	require.Equal(t, "failed", tests[1].Status)
	require.Contains(t, tests[1].Failure, "ddtest intentional failure")
	_, err = readKarate([]byte(`{}`), ".")
	require.Error(t, err)
}
