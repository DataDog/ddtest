package maven

import (
	"os"
	"path/filepath"
	"slices"
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
	tests, err := ReadReports(root, []Project{{Directory: "one"}, {Directory: "two"}})
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
			_, err := ReadReports(root, []Project{{Directory: "."}})
			require.Error(t, err)
		})
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "TEST-old.xml"), []byte(`<testsuite><testcase name="stale"/></testsuite>`), 0600))
	_, err := ReadReports(root, []Project{{Directory: "."}})
	require.Error(t, err)
}

func TestReadReportsIgnoresTestNGDuplicateReporter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "target/surefire-reports")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "junitreports"), 0700))
	data := []byte(`<testsuite><testcase name="one" classname="example.Test"/></testsuite>`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-example.xml"), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "junitreports/TEST-example.xml"), data, 0600))
	tests, err := ReadReports(root, []Project{{Directory: "."}})
	require.NoError(t, err)
	require.Len(t, tests, 1)
}

func TestTestNGNativeMethodsReplaceLossySurefireNames(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "core/target/surefire-reports")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-TestSuite.xml"), []byte(`<testsuite><testcase name="(example.Test)" classname="example.Test"/><testcase name="(example.Test)" classname="example.Test"/><testcase name="(example.Test)" classname="example.Test"><failure type="AssertionError"/></testcase></testsuite>`), 0600))
	data := `<testng-results><suite><test><class name="example.Test"><test-method name="setup" status="PASS" is-config="true"/><test-method name="parameters" status="PASS"><params><param index="0"><value>A</value></param></params></test-method><test-method name="parameters" status="PASS"><params><param index="0"><value>B</value></param></params></test-method><test-method name="fails" status="FAIL"><exception class="AssertionError"><message>ddtest intentional failure</message></exception></test-method></class></test></suite></testng-results>`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "testng-results.xml"), []byte(data), 0600))
	tests, err := ReadReports(root, []Project{{Directory: "core"}})
	require.NoError(t, err)
	require.Len(t, tests, 3, "retain parameterized multiplicity without duplicate reporters")
	require.Equal(t, "core", tests[0].Module)
	require.Equal(t, "parameters", tests[0].Name)
	require.Equal(t, "parameters", tests[1].Name)
	require.Equal(t, "failed", tests[2].Status)
	require.Contains(t, tests[2].Failure, "ddtest intentional failure")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "TEST-Other.xml"), []byte(`<testsuite><testcase name="other" classname="Other"/></testsuite>`), 0600))
	_, err = ReadReports(root, []Project{{Directory: "core"}})
	require.ErrorContains(t, err, "outcomes disagree", "mixed providers must not silently lose tests")
}

func TestTestNGInvalidNativeResultsCannotCertifyCompatibility(t *testing.T) {
	for _, method := range []string{`name="setup" status="FAIL" is-config="true"`, `status="PASS"`, `name="test" status="UNKNOWN"`} {
		_, err := readTestNG([]byte(`<testng-results><suite><test><class name="Test"><test-method `+method+`/></class></test></suite></testng-results>`), ".")
		require.Error(t, err)
	}
	tests, err := readTestNG([]byte(`<testng-results><suite><test><class name="Test"><test-method name="disabled" status="SKIP"/></class></test></suite></testng-results>`), ".")
	require.NoError(t, err)
	require.Equal(t, "skipped", tests[0].Status)
	tests, err = readTestNG([]byte(`<testng-results><suite><test><class name="Test"><test-method name="quarantined" status="SUCCESS_PERCENTAGE_FAILURE"/></class></test></suite></testng-results>`), ".")
	require.NoError(t, err)
	require.Equal(t, "passed", tests[0].Status, "tolerated TestNG failures must match Surefire's successful result")
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

func TestTestNGPreservesProviderIdentityAndRejectsAmbiguity(t *testing.T) {
	data := `<testng-results><suite><test><class name="ExampleTest"><test-method name="row" status="PASS"><params><param index="0"><value><![CDATA[A]]></value></param></params></test-method><test-method name="row" status="PASS"><params><param index="0"><value><![CDATA[B]]></value></param></params></test-method></class></test></suite></testng-results>`
	tests, err := readTestNG([]byte(data), ".")
	require.NoError(t, err)
	require.Len(t, tests, 2)
	require.NotEqual(t, tests[0], tests[1], "data-provider rows must keep their argument identity")
	for _, methods := range []string{
		`<test-method name="row" status="PASS"/><test-method name="row" status="FAIL"/>`,
		`<test-method name="row" status="PASS"><params><param index="0"><value>A</value></param></params></test-method><test-method name="row" status="FAIL"><params><param index="0"><value>A</value></param></params></test-method>`,
	} {
		tests, err := readTestNG([]byte(`<testng-results><suite><test><class name="ExampleTest">`+methods+`</class></test></suite></testng-results>`), ".")
		require.NoError(t, err, "controlled retry probes still need their native results")
		require.True(t, slices.ContainsFunc(tests, func(test Test) bool { return test.Ambiguous }), "ordinary comparisons must remain inconclusive")
	}
}

func TestTestNGCDATAFormattingPreservesArgumentWhitespace(t *testing.T) {
	for _, value := range []string{"A", " A ", "\nA\n", "<&", ""} {
		data := `<testng-results><suite><test><class name="ExampleTest"><test-method name="row" status="PASS"><params><param index="0"><value>
                <![CDATA[` + value + `]]>
              </value></param></params></test-method></class></test></suite></testng-results>`
		tests, err := readTestNG([]byte(data), ".")
		require.NoError(t, err)
		require.Len(t, tests, 1)
		require.Equal(t, TestNGParameters(map[string]string{"0": value}), tests[0].Parameters)
	}
}

func TestTestNGRejectsUnresolvedParameterIndex(t *testing.T) {
	_, err := readTestNG([]byte(`<testng-results><suite><test><class name="ExampleTest"><test-method name="row" status="PASS"><params><param index="1"><value>A</value></param></params></test-method></class></test></suite></testng-results>`), ".")
	require.ErrorContains(t, err, "unresolved TestNG parameter identity")
}
