package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

func TestDynamicToolSelection(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	tagged := `//go:build selective_suite

package fixture_test
import("testing";"github.com/stretchr/testify/suite")
type SelectiveSuite struct{suite.Suite}
func(s *SelectiveSuite)TestPass(){s.True(true)}
func TestSelective(t *testing.T){suite.Run(t,new(SelectiveSuite))}
`
	if err := os.WriteFile(filepath.Join(dir, "selective_test.go"), []byte(tagged), 0600); err != nil {
		t.Fatal(err)
	}
	assertOnly := `package fixture_test;import("testing";"github.com/stretchr/testify/assert");func TestAssertOnly(t *testing.T){assert.True(t,true)}`
	if err := os.WriteFile(filepath.Join(dir, "assert_only_test.go"), []byte(assertOnly), 0600); err != nil {
		t.Fatal(err)
	}
	sdk := `//go:build selective_sdk

package fixture
import _ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
`
	if err := os.WriteFile(filepath.Join(dir, "sdk.go"), []byte(sdk), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", "")
	for _, tc := range []struct {
		name                         string
		flags                        []string
		wantSuite, wantTool, wantSDK bool
	}{
		{"assert only", nil, false, false, false},
		{"native coverage", []string{"-coverpkg=./..."}, false, false, false},
		{"covered testing", []string{"-coverpkg=testing"}, false, true, false},
		{"suite", []string{"-tags=selective_suite"}, true, true, false},
		{"suite with native helper coverage", []string{"-tags=selective_suite", "-coverpkg=./..."}, true, true, false},
		{"suite with covered testing", []string{"-tags=selective_suite", "-coverpkg=testing"}, true, true, false},
		{"SDK", []string{"-tags=selective_sdk"}, false, true, true},
		{"SDK and suite", []string{"-tags=selective_sdk,selective_suite"}, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := append([]string{"-mod=mod"}, tc.flags...)
			plan, err := runner.PrepareRuntime(context.Background(), dir, flags, runner.Mini)
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(plan.Dir)
			data, err := os.ReadFile(plan.File)
			if err != nil {
				t.Fatal(err)
			}
			var overlay runner.Overlay
			if err := json.Unmarshal(data, &overlay); err != nil {
				t.Fatal(err)
			}
			if (overlay.Testify != nil) != tc.wantSuite {
				t.Fatalf("Testify activation=%v", overlay.Testify != nil)
			}
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			args := append([]string{"test", "--runtime=mini", "-c", "-x", "-o", bin}, flags...)
			args = append(args, ".")
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true"), driver, args...)
			if code != 0 {
				t.Fatal(out, stderr)
			}
			logs := cliDebugLines(stderr)
			if !strings.Contains(logs, fmt.Sprintf("tool selection testify=%t goleak=false cover=%t", tc.wantSuite, tc.wantTool && strings.Contains(strings.Join(tc.flags, " "), "coverpkg=testing"))) {
				t.Fatalf("debug tool selection does not match native tool activation: %s", logs)
			}
			if !strings.Contains(logs, fmt.Sprintf("sdk_ci_gate=%t", tc.wantSDK)) {
				t.Fatalf("SDK CI gate does not follow the selected graph: %s", logs)
			}
			if strings.Contains(stderr, "tool-overlay") != tc.wantTool {
				t.Fatalf("toolexec activation=%v, want %v: %s", strings.Contains(stderr, "tool-overlay"), tc.wantTool, stderr)
			}
		})
	}
}

// Probe and unrelated tool calls must work even with a nonexistent plan. This
// exercises the real CLI's bypass before JSON I/O, not just the dispatcher.
func TestToolBypassHasNativeIdentityAndExit(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	out, stderr, code := command(t, dir, testEnv(), "go", "env", "GOTOOLDIR")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	tools := strings.TrimSpace(out)
	for _, name := range []string{"compile", "link", "asm", "cover"} {
		tool := filepath.Join(tools, executableName(name))
		want, werr, wcode := command(t, dir, testEnv(), tool, "-V=full")
		got, gerr, gcode := command(t, dir, testEnv("TOOLEXEC_IMPORTPATH=example.com/unrelated", "DD_TRACE_DEBUG=true"), driver, "tool-overlay", "testify", "missing-plan", tool, "-V=full")
		if got != want || gerr != werr || gcode != wcode {
			t.Fatalf("%s probe differs: %d/%d %s %s", name, gcode, wcode, got, gerr)
		}
	}
	tool := filepath.Join(tools, executableName("compile"))
	want, werr, wcode := command(t, dir, testEnv(), tool, "-ddto-invalid")
	got, gerr, gcode := command(t, dir, testEnv("TOOLEXEC_IMPORTPATH=example.com/unrelated", "DD_TRACE_DEBUG=true"), driver, "tool-overlay", "testify", "missing-plan", tool, "-ddto-invalid")
	if got != want || gerr != werr || gcode != wcode {
		t.Fatalf("native failure differs: %d/%d %s %s", gcode, wcode, got, gerr)
	}
}

func TestTestifyCoveredLibraryAndWorkspace(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	dir, driver := prepareTestifyFixture(t, reference != "")
	flags := []string{"-mod=mod", "-coverpkg=./...,github.com/stretchr/testify/suite", "-covermode=atomic"}
	bins := compileMiniPair(t, dir, driver, flags...)
	oracle := bins[0]
	if reference != "" {
		oracle = filepath.Join(t.TempDir(), executableName("fixture.test"))
		args := append([]string{"go", "test"}, flags...)
		args = append(args, "-c", "-o", oracle, ".")
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), reference, args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
	}
	tc := parityCase{Name: "external-covered-library", Args: []string{"-test.run=^TestParityExternalHelper$/^TestPass$"}, Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 2}
	want, sdk := runParityCase(t, dir, oracle, tc)
	got, mini := runParityCase(t, dir, bins[1], tc)
	assertParityCase(t, tc, want, got, sdk, mini)
	assertTestifyCoverageFilenames(t, &want.miniWireCapture, &got.miniWireCapture)
	kit := installExternalTestifyHelper(t, dir)
	out, stderr, code := command(t, dir, testEnv(), "go", "work", "init", ".", kit)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	out, stderr, code = command(t, dir, testEnv("GOWORK="+filepath.Join(dir, "go.work"), "DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-count=1", "-run=^TestParityExternalHelper$/^TestPass$", ".")
	if code != 0 || strings.Contains(out, "no tests to run") {
		t.Fatal(out, stderr)
	}
}

func TestTestifyContractInvalidatesOnlyTestingDependents(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	t.Setenv("GOFLAGS", "")
	plan, err := runner.PrepareRuntime(context.Background(), dir, []string{"-mod=mod"}, runner.Mini)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(plan.Dir)
	data, err := os.ReadFile(plan.File)
	if err != nil {
		t.Fatal(err)
	}
	var overlay runner.Overlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Testify == nil {
		t.Fatal("Testify was not detected through external test imports")
	}
	// Stable output prevents -o changes from forcing a link on every invocation.
	output := filepath.Join(t.TempDir(), executableName("fixture.test"))
	args := []string{"test", "-mod=mod", "-overlay=" + plan.File, "-toolexec=" + quoteToolArgument(t, driver) + " tool-overlay testify " + quoteToolArgument(t, plan.File), "-c", "-x", "-o", output, "."}
	run := func() string {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", args...)
		if code != 0 {
			t.Fatal(out, stderr)
		}
		return stderr
	}
	run()
	cached := run()
	if len(compilerTraceLines(cached)) != 0 {
		t.Fatal("unchanged plan recompiled packages", cached)
	}
	var marker string
	for logical, backing := range overlay.Replace {
		if filepath.Base(logical) == "zz_dd_ci_visibility_hooks.go" {
			marker = backing
		}
	}
	if marker == "" {
		t.Fatal("testing cache marker is missing")
	}
	data, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), overlay.Testify.Fingerprint, fmt.Sprintf("%x", sha256.Sum256([]byte(t.TempDir()))), 1)
	if edited == string(data) {
		t.Fatal("exported fingerprint absent from testing")
	}
	if err := os.WriteFile(marker, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	changed := run()
	var suiteRebuilt bool
	for _, line := range compilerTraceLines(changed) {
		if strings.Contains(line, "-p github.com/stretchr/testify/suite ") {
			suiteRebuilt = true
		}
		if strings.Contains(line, "-p strings ") || strings.Contains(line, "-p fmt ") || strings.Contains(line, "-p crypto/sha256 ") {
			t.Fatal("unrelated standard library package was rebuilt", line)
		}
	}
	if !suiteRebuilt {
		t.Fatal("changed testing contract did not invalidate Testify", changed)
	}
	if again := run(); len(compilerTraceLines(again)) != 0 {
		t.Fatal("changed contract cannot reuse cache", again)
	}
}

func quoteToolArgument(t *testing.T, s string) string {
	t.Helper()
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	t.Fatal("test tool path cannot be quoted", s)
	return ""
}

// The Unix bypass must replace its own PID rather than keeping a wrapper and
// spawning another process. Windows preserves behavior through a child instead.
func TestToolBypassProcessReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	dir, driver := prepareMiniFixture(t)
	helperDir := t.TempDir()
	source := filepath.Join(helperDir, "pid.go")
	if err := os.WriteFile(source, []byte("package main\nimport(\"fmt\";\"os\")\nfunc main(){fmt.Print(os.Getpid())}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperDir, "pid-tool")
	out, stderr, code := command(t, dir, testEnv(), "go", "build", "-o", helper, source)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	cmd := exec.Command(driver, "tool-overlay", "testify", "missing-plan", helper)
	cmd.Dir, cmd.Env = dir, testEnv()
	reader, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	buffer := make([]byte, 64)
	for {
		n, err := reader.Read(buffer)
		output.Write(buffer[:n])
		if err != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(output.String())
	if err != nil || pid != cmd.Process.Pid {
		t.Fatalf("native PID=%d wrapper PID=%d err=%v", pid, cmd.Process.Pid, err)
	}
}
