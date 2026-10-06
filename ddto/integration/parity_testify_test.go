package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

func prepareTestifyFixture(t *testing.T, reference bool) (string, string) {
	t.Helper()
	return prepareTestifyFixtureWithTempDir(t, reference, t.TempDir)
}

func prepareTestifyFixtureWithTempDir(t *testing.T, reference bool, tempDir func() string) (string, string) {
	t.Helper()
	dir, driver := prepareMiniFixtureWithTempDir(t, tempDir)
	if reference {
		configureReferenceFixture(t, dir)
	}
	copyTree(t, filepath.Join("testdata", "testify"), dir)
	installExternalTestifyHelperWithTempDir(t, dir, tempDir)
	return dir, driver
}

// A separate module makes this fixture exercise the former overlay boundary.
func installExternalTestifyHelper(t *testing.T, dir string) string {
	t.Helper()
	return installExternalTestifyHelperWithTempDir(t, dir, t.TempDir)
}

func installExternalTestifyHelperWithTempDir(t *testing.T, dir string, tempDir func() string) string {
	t.Helper()
	kit := tempDir()
	for name, source := range map[string]string{
		"go.mod": "module example.com/external-testkit\n\ngo 1.26.0\nrequire github.com/stretchr/testify v1.11.1\n",
		"run.go": "package testkit\nimport(\"testing\";\"github.com/stretchr/testify/suite\")\nfunc Run(t *testing.T,s suite.TestingSuite){suite.Run(t,s)}\n",
	} {
		if err := os.WriteFile(filepath.Join(kit, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-require=example.com/external-testkit@v0.0.0", "-replace=example.com/external-testkit="+kit)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	return kit
}

// Compare the full Testify advice, not merely the two POC backends. Each binary
// is built once; policy cases exercise the selected library's original runner.
func TestCIVisibilityTestifyParity(t *testing.T) {
	runCIVisibilityTestifyParity(t, false)
}

func TestDeferredDeliveryTestifyParity(t *testing.T) {
	runCIVisibilityTestifyParity(t, true)
}

func buildTestifyParityFixture(t *testing.T, tempDir func() string) *parityFixture {
	t.Helper()
	reference := os.Getenv("ORCHESTRION_BIN")
	dir, driver := prepareTestifyFixtureWithTempDir(t, reference != "", tempDir)
	flags := []string{"-mod=mod", "-race", "-cover", "-covermode=atomic", "-coverpkg=./..."}
	bins, builds := miniPairBuilds(dir, driver, tempDir, flags...)
	oracle := bins[0]
	if reference != "" {
		var build fixtureBuild
		oracle, build = testifyReferenceBuild(dir, reference, tempDir, flags...)
		builds = append(builds, build)
	}
	buildConcurrently(t, builds...)
	return &parityFixture{dir: dir, sdk: bins[0], mini: bins[1], oracle: oracle}
}

func compileTestifyReference(t *testing.T, dir, reference string, tempDir func() string, flags ...string) string {
	t.Helper()
	oracle, build := testifyReferenceBuild(dir, reference, tempDir, flags...)
	buildConcurrently(t, build)
	return oracle
}

func testifyReferenceBuild(dir, reference string, tempDir func() string, flags ...string) (string, fixtureBuild) {
	oracle := filepath.Join(tempDir(), executableName("fixture.test"))
	args := append([]string{"go", "test"}, flags...)
	args = append(args, "-c", "-o", oracle, ".")
	return oracle, fixtureBuild{name: "Testify reference compile", dir: dir, tool: reference, env: testEnv("DD_CIVISIBILITY_ENABLED=false"), args: args}
}

func runCIVisibilityTestifyParity(t *testing.T, deferred bool) {
	fixture := sharedTestifyParity.get(t, "Testify", buildTestifyParityFixture)
	dir, oracle := fixture.dir, fixture.oracle
	run := func(method string) []string { return []string{"-test.run=^TestParitySuite$/^" + method + "$"} }
	cases := []parityCase{
		{Name: "pass-skip", Args: run("Test(Pass|Skip)"), MinTests: 3},
		{Name: "method-filter", Args: []string{"-test.run=^TestParitySuite$", "-testify.m=^TestPass$"}, MinTests: 2},
		{Name: "count-shuffle", Args: append(run("TestPass"), "-test.count=2", "-test.shuffle=42"), MinTests: 4},
		{Name: "nested", Args: run("TestNested"), MinTests: 3},
		{Name: "assert-failure", Args: run("TestAssertFailure"), WantExit: 1, MinTests: 2},
		{Name: "require-failure", Args: run("TestRequireFailure"), WantExit: 1, MinTests: 2},
		{Name: "panic", Args: run("TestPanic"), WantExit: 1, MinTests: 2},
		{Name: "lifecycle-stats", Args: []string{"-test.run=^TestParityLifecycle$"}, MinTests: 4},
		{Name: "aliases-dot-helpers", Args: []string{"-test.run=^TestParity(Alias|Dot|Helper)$/^TestPass$"}, MinTests: 6},
		{Name: "external-module-helper", Args: []string{"-test.run=^TestParityExternalHelper$/^TestPass$"}, MinTests: 2},
		{Name: "internal-package", Args: []string{"-test.run=^TestParityInternal$"}, MinTests: 2},
		{Name: "custom-and-shadowed-run", Args: []string{"-test.run=^TestParity(CustomRun|ShadowedAlias)$"}, MinTests: 3},
		{Name: "parallel-suites", Args: []string{"-test.run=^TestParityParallel(A|B)$/^TestPass$", "-test.parallel=2"}, MinTests: 4},
		{Name: "coverage-helpers", Args: []string{"-test.run=^TestParityHelper$/^TestPass$"}, Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 2},
		{Name: "coverage", Args: run("Test(Pass|Nested)"), Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 4},
		{Name: "itr-parent", Args: run("TestManaged"), Policy: policySettings{ITR: true, SkippableTarget: "TestParitySuite", SkippableSuite: "testify_cases_test.go"}, RequiredTag: "test.skipped_by_itr", RequiredValue: "true", MinTests: 1},
		// The pinned SDK applies ITR to top-level testing.M entries, not Testify methods.
		{Name: "itr-method-sdk-limit", Args: run("TestManaged"), Policy: policySettings{ITR: true, SkippableTarget: "TestParitySuite/TestManaged", SkippableSuite: "testify_cases_test.go/ParitySuite"}, RequiredTag: "test.status", RequiredValue: "fail", WantExit: 1, MinTests: 2},
	}
	for _, mode := range []string{"in_process", "process"} {
		env := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}
		cases = append(cases, parityCase{Name: "atr-" + mode, Args: run("TestFlaky"), Env: env, Policy: policySettings{Retry: true}, MinTests: 3}, parityCase{Name: "atr-coverage-" + mode, Args: run("TestFlaky"), Env: env, Policy: policySettings{Retry: true, Coverage: true}, Coverage: true, MinTests: 3})
		cases = append(cases, parityCase{Name: "efd-" + mode, Args: run("TestPass"), Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, Policy: policySettings{EFD: true}, MinTests: 3})
	}
	suiteName := "testify_cases_test.go/ParitySuite"
	for _, tc := range []parityCase{
		{Name: "disabled", Policy: policySettings{Disabled: true}, RequiredTag: "test.test_management.is_test_disabled", RequiredValue: "true"},
		{Name: "quarantined", Policy: policySettings{Quarantined: true}, RequiredTag: "test.test_management.is_quarantined", RequiredValue: "true"},
		{Name: "attempt-to-fix", Policy: policySettings{AttemptToFix: true}, RequiredTag: "test.test_management.is_attempt_to_fix", RequiredValue: "true", WantExit: 1},
	} {
		tc.Args = run("TestManaged")
		tc.MinTests = 2
		tc.Policy.ManagementTarget = "TestParitySuite/TestManaged"
		tc.Policy.ManagementSuite = suiteName
		cases = append(cases, tc)
	}
	var results []parityResult
	var pocSDKWallNS int64
	if deferred {
		selected := map[string]bool{"pass-skip": true, "external-module-helper": true, "parallel-suites": true, "panic": true, "coverage-helpers": true, "atr-coverage-in_process": true, "atr-coverage-process": true}
		kept := cases[:0]
		for _, tc := range cases {
			if selected[tc.Name] {
				tc.Env = append(tc.Env, "DD_CIVISIBILITY_DEFERRED_DELIVERY=true")
				kept = append(kept, tc)
			}
		}
		cases = kept
	}
	for _, tc := range cases {
		tc.Features = []string{"testify", tc.Name}
		t.Run(tc.Name, func(t *testing.T) {
			want, sdk := runParityCase(t, dir, oracle, tc)
			got, mini := runParityCase(t, dir, fixture.mini, tc)
			requireNoMiniLeftovers(t, mini)
			normalizeTestifyEvents(t, want.events)
			normalizeTestifyEvents(t, got.events)
			row := assertParityCase(t, tc, want, got, sdk, mini)
			if tc.Coverage {
				assertTestifyCoverageFilenames(t, &want.miniWireCapture, &got.miniWireCapture)
			}
			if !tc.Policy.Retry && !tc.Policy.EFD && !tc.Policy.Disabled && !tc.Policy.Quarantined && !tc.Policy.AttemptToFix && !tc.Policy.ITR && canonicalTestifyDiagnostic(sdk.out) != canonicalTestifyDiagnostic(mini.out) {
				t.Fatalf("native Testify output differs\nSDK %s\nMini %s", sdk.out, mini.out)
			}
			results = append(results, row)
			if tc.Name == "pass-skip" {
				if row.SDK != (eventCounts{1, 1, 2, 3, 0}) {
					t.Fatalf("unexpected Testify hierarchy: %+v", row.SDK)
				}
				// The sdk backend must also contain the advice; otherwise this comparison
				// would only prove the Mini variant, leaving the driver's default broken.
				poc, pocResult := runParityCase(t, dir, fixture.sdk, tc)
				normalizeTestifyEvents(t, poc.events)
				assertParityCase(t, tc, want, poc, sdk, pocResult)
				pocSDKWallNS = pocResult.wall.Nanoseconds()
			}
		})
	}
	// Native -run may select only some subtests. Export complete evidence only
	// when every required case ran and passed.
	if len(results) != len(cases) || t.Failed() {
		return
	}
	evidence := "testify"
	if deferred {
		evidence += "-deferred"
	}
	writeParityEvidence(t, evidence, map[string]any{"timing": results[0].Timing, "poc_sdk_wall_ns": pocSDKWallNS, "status": "passed", "sdk": results[0].SDK, "sdk_with_orchestrion": results[0].SDK, "sdk_with_poc": results[0].SDK, "mini": results[0].Mini, "external_callers": true, "scope": "Testify registration, original runner, lifecycle, aliases, local and external helpers, race/coverage and CI policies", "scenarios": results})
}

// Testify embeds library paths and debug.Stack in assertion/panic messages.
// Normalize only runtime relocation and generated stack arguments/addresses;
// retain the application's panic text, function names, source lines and errors.
var testifyGoroutineID = regexp.MustCompile(`goroutine [0-9]+`)
var testifyProgramCounter = regexp.MustCompile(` \+0x[0-9a-f]+`)

func canonicalTestifyDiagnostic(text string) string {
	sdkPath := "/internal/civisibility/"
	miniPath := "/internal/thirdparty/dd-trace-go/civisibility/"
	lines := strings.Split(text, "\n")
	stack := false
	for i, line := range lines {
		if strings.Contains(line, "goroutine ") && strings.Contains(line, "[running]") {
			stack = true
		}
		for _, path := range []string{sdkPath, miniPath} {
			if pos := strings.Index(line, path); pos >= 0 {
				prefix := line[:pos]
				start := strings.LastIndexAny(prefix, " \t") + 1
				line = prefix[:start] + "ci-runtime/civisibility/" + line[pos+len(path):]
			}
		}
		line = strings.ReplaceAll(line, "github.com/DataDog/dd-trace-go/v2/internal/civisibility/", "ci-runtime/civisibility/")
		line = strings.ReplaceAll(line, "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/", "ci-runtime/civisibility/")
		if stack {
			line = testifyGoroutineID.ReplaceAllString(line, "goroutine ID")
			line = testifyProgramCounter.ReplaceAllString(line, "")
			if !strings.Contains(line, ".go:") {
				if pos := strings.LastIndex(line, "("); pos >= 0 && strings.HasSuffix(line, ")") {
					line = line[:pos] + "()"
				}
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}
func normalizeTestifyEvents(t *testing.T, events []map[string]any) {
	t.Helper()
	for _, event := range events {
		content := event["content"].(map[string]any)
		if meta, ok := content["meta"].(map[string]any); ok {
			if message, ok := meta["error.message"].(string); ok {
				meta["error.message"] = canonicalTestifyDiagnostic(message)
			}
		}
	}
}

func TestTestifyDiagnosticNormalizationRetainsApplicationErrors(t *testing.T) {
	a := "test panicked: application 0x123\ngoroutine 12 [running]:\nexample.com/app.(*Suite).Test(0x123)\n\t/work/app_test.go:42 +0x123\n"
	b := strings.ReplaceAll(a, "goroutine 12", "goroutine 99")
	b = strings.ReplaceAll(b, "Test(0x123)", "Test(0x456)")
	if canonicalTestifyDiagnostic(a) != canonicalTestifyDiagnostic(b) {
		t.Fatal("volatile stack values retained")
	}
	for _, changed := range []string{strings.Replace(a, "application 0x123", "application 0x456", 1), strings.Replace(a, "app_test.go:42", "app_test.go:43", 1), strings.Replace(a, ".Test(", ".Other(", 1)} {
		if canonicalTestifyDiagnostic(a) == canonicalTestifyDiagnostic(changed) {
			t.Fatal("application error was erased")
		}
	}
}

func TestTestifySupportedVersionsAndNativeSemantics(t *testing.T) {
	// The fixture itself needs Testify v1.6.0+ (SuiteInformation, s.Run).
	for _, version := range []string{"v1.10.0", "v1.11.1", "v1.12.1"} {
		t.Run(version, func(t *testing.T) {
			dir, driver := prepareTestifyFixture(t, false)
			out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-replace=github.com/stretchr/testify=github.com/stretchr/testify@"+version)
			if code != 0 {
				t.Fatal(out, stderr)
			}
			bins := compileMiniPair(t, dir, driver, "-mod=mod", "-tags=testify_extra")
			native := filepath.Join(t.TempDir(), executableName("fixture.test"))
			out, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", "test", "-mod=mod", "-tags=testify_extra", "-c", "-o", native, ".")
			if code != 0 {
				t.Fatal(out, stderr)
			}
			args := []string{"-test.run=^TestParity(Suite|Alias|Dot|Helper|Tagged|ParallelA|ParallelB|Lifecycle|Internal|CustomRun|ShadowedAlias)$/^(TestPass|TestAlpha|TestBeta|custom)$"}
			want, original := runParityCase(t, dir, native, parityCase{Args: args, Env: []string{"DD_CIVISIBILITY_ENABLED=false"}})
			if original.code != 0 || len(want.events) > 0 {
				t.Fatal("native fixture failed", original.out, original.stderr)
			}
			for _, bin := range bins {
				_, got := runParityCase(t, dir, bin, parityCase{Args: args, Env: []string{"DD_CIVISIBILITY_ENABLED=false"}})
				if got.code != original.code || got.out != original.out {
					t.Fatalf("native behavior changed: %s\n%s", original.out, got.out)
				}
			}
			sdk, a := runParityCase(t, dir, bins[0], parityCase{Args: []string{"-test.run=^TestParitySuite$/^Test(Pass|Skip)$"}})
			mini, b := runParityCase(t, dir, bins[1], parityCase{Args: []string{"-test.run=^TestParitySuite$/^Test(Pass|Skip)$"}})
			if a.code != 0 || b.code != 0 {
				t.Fatal(a.stderr, b.stderr)
			}
			assertMiniCIAttributes(t, sdk.events, mini.events)
			counts, err := countCIEvents(mini.events)
			if err != nil || counts != (eventCounts{1, 1, 2, 3, 0}) {
				t.Fatal(counts, err)
			}
		})
	}
}

// An unrecognized Testify entry is skipped with a warning; the build and tests
// still run, and the rest of the package keeps its instrumentation.
func TestTestifyUnsupportedEntryWarnsAndPreservesClientNames(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	cache, stderr, code := command(t, dir, testEnv(), "go", "env", "GOMODCACHE")
	if code != 0 {
		t.Fatal(stderr)
	}
	changed := t.TempDir()
	copyTree(t, filepath.Join(strings.TrimSpace(cache), "github.com", "stretchr", "testify@v1.11.1"), changed)
	suiteFile := filepath.Join(changed, "suite", "suite.go")
	source, err := os.ReadFile(suiteFile)
	if err != nil {
		t.Fatal(err)
	}
	entry := "func Run(t *testing.T, suite TestingSuite) {"
	if !strings.Contains(string(source), entry) {
		t.Fatal("Testify entry changed upstream")
	}
	source = []byte(strings.Replace(string(source), entry, "func Run(t *testing.T, suite TestingSuite, _ ...struct{}) {", 1))
	if err := os.WriteFile(suiteFile, source, 0644); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-replace=github.com/stretchr/testify="+changed)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	out, stderr, code = command(t, dir, testEnv(), driver, "test", "--runtime=mini", "-mod=mod", "-run=^TestParitySuite$/^TestPass$", ".")
	if code != 0 || !strings.Contains(stderr, "ddtest: warning: Testify v1.11.1 is not instrumented") {
		t.Fatalf("unsupported entry: %d %s %s", code, out, stderr)
	}
	out, stderr, code = command(t, dir, testEnv(), "go", "mod", "edit", "-dropreplace=github.com/stretchr/testify")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "zz_dd_ci_testify_external_test.go"), []byte("package fixture_test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code = command(t, dir, testEnv(), driver, "test", "--runtime=mini", "-mod=mod", "-run=^TestParitySuite$/^TestPass$", ".")
	if code != 0 {
		t.Fatalf("obsolete caller wrapper name interfered with library instrumentation: %d %s %s", code, out, stderr)
	}
}

func TestTestifyUserOverlay(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	replacement := filepath.Join(t.TempDir(), "overlay.go")
	src := `package fixture_test; import "testing"; import s "github.com/stretchr/testify/suite"; func TestParityOverlay(t *testing.T){s.Run(t,new(ParitySuite))}`
	if err := os.WriteFile(replacement, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(dir, "user_overlay_test.go")
	body, _ := json.Marshal(runner.Overlay{Replace: map[string]string{logical: replacement}})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, body, 0600); err != nil {
		t.Fatal(err)
	}
	bins := compileMiniPair(t, dir, driver, "-mod=mod", "-overlay="+overlay)
	tc := parityCase{Args: []string{"-test.run=^TestParityOverlay$/^TestPass$"}}
	sdk, a := runParityCase(t, dir, bins[0], tc)
	mini, b := runParityCase(t, dir, bins[1], tc)
	if a.code != 0 || b.code != 0 {
		t.Fatal(a.stderr, b.stderr)
	}
	assertMiniCIAttributes(t, sdk.events, mini.events)
	counts, err := countCIEvents(mini.events)
	if err != nil || counts.Tests != 2 || counts.Modules != 1 || counts.Suites != 2 {
		t.Fatal(counts, err)
	}
}

// The private cover adapter must participate in native cache invalidation. A
// changed helper is covered and recompiled; unchanged inputs remain cached.
func TestTestifyCoverageCacheAndGOFLAGS(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	env := testEnv("DD_CIVISIBILITY_ENABLED=false", "GOFLAGS=-mod=mod -coverpkg=./...")
	args := []string{"test", "--runtime=mini", "-c", "-x", "-o", filepath.Join(t.TempDir(), executableName("fixture.test")), "."}
	run := func() string {
		out, stderr, code := command(t, dir, env, driver, args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
		return stderr
	}
	run()
	cached := run()
	if strings.Contains(cached, "/cover ") || strings.Contains(cached, "\\cover.exe ") {
		t.Fatalf("unchanged covered inputs were rebuilt: %s", cached)
	}
	helper := filepath.Join(dir, "helpers", "run.go")
	data, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "runner(t, s)", "t.Log(\"covered helper changed\"); runner(t, s)", 1))
	if err := os.WriteFile(helper, data, 0600); err != nil {
		t.Fatal(err)
	}
	changed := run()
	if !strings.Contains(changed, "tool-overlay testify") || !strings.Contains(changed, "helpers") {
		t.Fatalf("changed helper did not rebuild under cover: %s", changed)
	}
	profile := filepath.Join(t.TempDir(), "coverage.out")
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-run=^TestParityHelper$/^TestPass$", "-coverprofile="+profile, ".")
	if code != 0 || strings.Contains(out, "[no tests to run]") {
		t.Fatal(out, stderr)
	}
	data, err = os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "helpers/run.go") || strings.Contains(string(data), "zz_dd_ci_testify") {
		t.Fatalf("invalid client coverage: %s", data)
	}
}

// Normal callers in a requested package also need the bridge with plain -cover.
func TestTestifyRegularCallerDefaultCoverage(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	dir, driver := prepareTestifyFixture(t, reference != "")
	regular := `package fixture
import("testing";"github.com/stretchr/testify/suite")
func RunLocalSuite(t *testing.T,s suite.TestingSuite){suite.Run(t,s)}
`
	caller := `package fixture_test
import("testing";"github.com/stretchr/testify/suite";client "example.com/dd-ci-testing-fixture")
type RootCoverageSuite struct{suite.Suite}
func(s *RootCoverageSuite)TestPass(){s.True(true)}
func TestRootCoverage(t *testing.T){client.RunLocalSuite(t,new(RootCoverageSuite))}
`
	for name, source := range map[string]string{"root_suite.go": regular, "root_suite_test.go": caller} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bins, builds := miniPairBuilds(dir, driver, t.TempDir, "-mod=mod", "-cover")
	oracle := bins[0]
	if reference != "" {
		var build fixtureBuild
		oracle, build = testifyReferenceBuild(dir, reference, t.TempDir, "-mod=mod", "-cover")
		builds = append(builds, build)
	}
	buildConcurrently(t, builds...)
	tc := parityCase{Name: "regular-root-coverage", Args: []string{"-test.run=^TestRootCoverage$"}, Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 2}
	want, sdk := runParityCase(t, dir, oracle, tc)
	got, mini := runParityCase(t, dir, bins[1], tc)
	assertParityCase(t, tc, want, got, sdk, mini)
	assertTestifyCoverageFilenames(t, &want.miniWireCapture, &got.miniWireCapture)
}

// The shared comparator checks bitmaps by basename to tolerate relocated
// runtime fixtures. Testify's client sources share one root, so also require
// complete filenames: a temporary backing path must never reach the intake.
func assertTestifyCoverageFilenames(t *testing.T, want, got *miniWireCapture) {
	t.Helper()
	filenames := func(c *miniWireCapture) []string {
		var names []string
		for _, coverage := range c.coverages {
			for _, file := range coverage["files"].([]any) {
				names = append(names, filepath.ToSlash(file.(map[string]any)["filename"].(string)))
			}
		}
		sort.Strings(names)
		return names
	}
	a, b := filenames(want), filenames(got)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("coverage filenames differ: SDK %v Mini %v", a, b)
	}
}
