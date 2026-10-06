package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The hierarchy API is internal in the SDK. Temporary module paths satisfy
// each library's Go internal-import boundary; test/module/suite names are
// explicit and identical. This does not make either API public.
func TestCIVisibilityManualHierarchy(t *testing.T) {
	dir, _ := prepareMiniFixture(t)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "manualprobe")
	if err = os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	source := `package main

import (
	api "API_IMPORT"
	tracer "TRACER_IMPORT"
	"fmt"
)

func main() {
	session := api.CreateTestSession(api.WithTestSessionCommand("manual parity"), api.WithTestSessionWorkingDirectory("."), api.WithTestSessionFramework("manual", "1.0"))
	ORACLE_READY
	for _, moduleName := range []string{"manual-a", "manual-b"} {
		module := session.GetOrCreateModule(moduleName)
		if module.ModuleID() != session.GetOrCreateModule(moduleName).ModuleID() {
			panic("duplicate module")
		}
		for _, suiteName := range []string{"suite-a", "suite-b"} {
			suite := module.GetOrCreateSuite(suiteName)
			if suite.SuiteID() != module.GetOrCreateSuite(suiteName).SuiteID() {
				panic("duplicate suite")
			}
			for _, status := range []api.TestResultStatus{api.ResultStatusPass, api.ResultStatusFail, api.ResultStatusSkip} {
				test := suite.CreateTest(fmt.Sprintf("manual-%d", status))
				test.SetTag("ci.custom", "retained")
				test.SetTag("ci.custom.metric", 7)
				if value, ok := test.GetTag("ci.custom"); !ok || value != "retained" {
					panic("tag read")
				}
				if value, ok := test.GetTag("ci.custom.metric"); !ok || value != float64(7) {
					panic("metric read")
				}
				if status == api.ResultStatusFail {
					test.SetError(api.WithErrorInfo("Assertion", "manual failure", "application.go:12"))
				}
				if moduleName == "manual-a" && suiteName == "suite-a" && status == api.ResultStatusPass {
					span, _ := tracer.StartSpanFromContext(test.Context(), "manual.step", tracer.Tag("_dd.origin", "ciapp-test"), tracer.SpanType("custom"))
					span.Finish()
				}
				test.Log("manual log", "kind:manual")
				test.Close(status, api.WithTestSkipReason("manual skip"))
				test.Close(status)
			}
			suite.Close()
			suite.Close()
		}
		module.Close()
		module.Close()
	}
	session.Close(1)
	session.Close(1)
	api.ExitCiVisibility()
}
`
	var captures []*parityReceiver
	var wallNS []int64
	for _, backend := range []string{"sdk", "mini"} {
		modulePath := "github.com/DataDog/dd-trace-go/v2/parityprobe"
		apiImport := "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
		tracerImport := "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
		if backend == "mini" {
			modulePath = "github.com/tonyredondo/dd-ci-testing-poc/manualprobe"
			apiImport = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
			tracerImport = "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
		}
		module := "module " + modulePath + "\n\ngo 1.26.0\nrequire (\n github.com/tonyredondo/dd-ci-testing-poc v0.0.0\n github.com/DataDog/dd-trace-go/v2 " + sdkVersion + "\n)\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + strconv.Quote(filepath.ToSlash(root)) + "\n"
		if err = os.WriteFile(filepath.Join(probe, "go.mod"), []byte(module), 0600); err != nil {
			t.Fatal(err)
		}
		// The SDK may otherwise omit capabilities on every manual event. Wait
		// through its existing feature API before using it as the oracle. Mini
		// deliberately has no barrier: the delayed server proves its early tags.
		ready := "_ = api.GetKnownTests()"
		if backend == "mini" {
			ready = "// Mini emits without waiting for remote settings."
		}
		body := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(source, "API_IMPORT", apiImport), "TRACER_IMPORT", tracerImport), "ORACLE_READY", ready)
		if err = os.WriteFile(filepath.Join(probe, "main.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
		out, stderr, code := command(t, probe, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", "build", "-mod=mod", "-o", bin, ".")
		if code != 0 {
			t.Fatalf("manual compile: %s %s", out, stderr)
		}
		receiver, result := runParityCase(t, probe, bin, parityCase{Logs: true, Policy: policySettings{SettingsDelay: 100 * time.Millisecond}})
		if result.code != 0 {
			t.Fatalf("manual hierarchy: %s %s", result.out, result.stderr)
		}
		counts, err := countCIEvents(receiver.events)
		if err != nil {
			t.Fatal(err)
		}
		if counts != (eventCounts{1, 2, 4, 12, 1}) {
			t.Fatalf("manual event counts: %+v", counts)
		}
		if err = validateEventGraph(receiver.events); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, receiver)
		wallNS = append(wallNS, result.wall.Nanoseconds())
	}
	assertMiniCIAttributes(t, captures[0].events, captures[1].events)
	assertSidePayloadParity(t, captures[0], captures[1])
	writeParityEvidence(t, "manual", map[string]any{"timing": parityTiming{binaryTimingScope, wallNS[0], wallNS[1]}, "status": "passed", "sdk": eventCounts{1, 2, 4, 12, 1}, "mini": eventCounts{1, 2, 4, 12, 1}, "scope": "internal hierarchy API: repeated lookup/close, statuses, custom tags/metrics, errors, logs and child span"})
}
