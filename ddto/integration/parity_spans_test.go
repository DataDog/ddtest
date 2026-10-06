package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This fixture lives under the POC module's path so it can test the hook-driven
// client without adding a public API solely for the comparison. SDK uses its
// public tracer. Both create the same CI-marked root/child spans during a real test.
func TestCIVisibilityAdditionalSpans(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	probe := filepath.Join(dir, "spanprobe")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module github.com/tonyredondo/dd-ci-testing-poc/parityprobe\n\ngo 1.26.0\nrequire (\n github.com/tonyredondo/dd-ci-testing-poc v0.0.0\n github.com/DataDog/dd-trace-go/v2 " + sdkVersion + "\n)\nreplace github.com/tonyredondo/dd-ci-testing-poc => " + filepath.ToSlash(root) + "\n"
	if err = os.WriteFile(filepath.Join(probe, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	// Source lines and test names remain identical between backends. Namespace
	// relocation is restricted to the runtime import in this synthetic bridge.
	source := `package parityprobe
import (
 "context"
 "testing"
 RUNTIME
 _ "unsafe"
)
CONTEXT_BRIDGE
func TestSpans(t *testing.T) {
 root,ctx:=tracer.StartSpanFromContext(testContext(t),"ci.step",tracer.Tag("ci.custom","retained"),tracer.Tag("_dd.origin","ciapp-test"),tracer.SpanType("custom"))
 child,_:=tracer.StartSpanFromContext(ctx,"ci.child",tracer.Tag("ci.custom","retained"),tracer.Tag("_dd.origin","ciapp-test"),tracer.SpanType("custom"))
 child.Finish();root.Finish()
}
`
	var captures []*parityReceiver
	var wallNS []int64
	for _, backend := range []string{"sdk", "mini"} {
		runtimeImport := `tracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
 _ "github.com/DataDog/dd-trace-go/v2/civisibility"`
		if backend == "mini" {
			runtimeImport = `tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"`
		}
		bridge := "//go:linkname testContext github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.getTestOptimizationContext\nfunc testContext(tb testing.TB) context.Context"
		if backend == "mini" {
			bridge = "// Native testing context, including trace identity and cancellation.\nfunc testContext(tb testing.TB) context.Context { return testopt.Context(tb) }"
			runtimeImport += "\n \"github.com/tonyredondo/dd-ci-testing-poc/testopt\""
		}
		fixtureSource := strings.Replace(strings.Replace(source, "RUNTIME", runtimeImport, 1), "CONTEXT_BRIDGE", bridge, 1)
		if err = os.WriteFile(filepath.Join(probe, "spans_test.go"), []byte(fixtureSource), 0600); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
		out, stderr, code := command(t, probe, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime="+backend, "-mod=mod", "-c", "-o", bin, ".")
		if code != 0 {
			t.Fatalf("span compile: %d %s %s", code, out, stderr)
		}
		capture, result := runParityCase(t, probe, bin, parityCase{Args: []string{"-test.run=^TestSpans$"}})
		if result.code != 0 {
			t.Fatal(result.stderr)
		}
		counts, err := countCIEvents(capture.events)
		if err != nil {
			t.Fatal(err)
		}
		if counts != (eventCounts{Sessions: 1, Modules: 1, Suites: 1, Tests: 1, Spans: 2}) {
			t.Fatalf("lost additional spans: %+v", counts)
		}
		if err = validateEventGraph(capture.events); err != nil {
			t.Fatal(err)
		}
		spans := map[uint64]map[string]any{}
		for _, event := range capture.events {
			if event["type"] == "span" {
				c := event["content"].(map[string]any)
				spans[c["span_id"].(uint64)] = c
			}
		}
		var testSpan map[string]any
		for _, event := range capture.events {
			if event["type"] == "test" {
				testSpan = event["content"].(map[string]any)
			}
		}
		for _, span := range spans {
			var parent map[string]any
			switch span["name"] {
			case "ci.step":
				parent = testSpan
			case "ci.child":
				parent = spans[span["parent_id"].(uint64)]
			default:
				t.Fatalf("unexpected span %v", span["name"])
			}
			if parent == nil || span["parent_id"] != parent["span_id"] || span["trace_id"] != parent["trace_id"] {
				t.Fatal("lost test/span distributed parentage")
			}
		}
		captures = append(captures, capture)
		wallNS = append(wallNS, result.wall.Nanoseconds())
	}
	assertMiniCIAttributes(t, captures[0].events, captures[1].events)
	writeParityEvidence(t, "spans", map[string]any{"timing": parityTiming{binaryTimingScope, wallNS[0], wallNS[1]}, "status": "passed", "sdk": eventCounts{1, 1, 1, 1, 2}, "mini": eventCounts{1, 1, 1, 1, 2}, "scope": "root/child spans attached to the active test context; external APM integration uses propagation"})
}
