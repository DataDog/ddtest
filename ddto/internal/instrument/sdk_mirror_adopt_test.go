package instrument

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// adoptionInternals models the SDK internals that adoption uses, beside the
// copy edits of testdata/sdkmirror/sdk.go.txt.
const adoptionInternals = `
var _ = ext.PriorityAutoKeep

type traceID struct {
	value      [16]byte
	hexEncoded string
}

func (t *traceID) SetUpper(i uint64) {
	for n := 0; n < 8; n++ {
		t.value[n] = byte(i >> (56 - 8*n))
	}
}
func (t *traceID) SetLower(i uint64) {
	for n := 0; n < 8; n++ {
		t.value[8+n] = byte(i >> (56 - 8*n))
	}
}
func (t *traceID) cacheHex() { t.hexEncoded = "cached" }

type trace struct{ priority int }

func (t *trace) setSamplingPriority(p int, sampler samplernames.SamplerName) bool {
	t.priority = p
	return true
}

type chunk struct{ spans []*Span }

type tracer struct {
	sent  [][]*Span
	stats int
}

func (t *tracer) submitChunk(c *chunk) {
	t.stats += len(c.spans)
	t.sent = append(t.sent, c.spans)
}

func (t *tracer) computeOversizedSpanStats(span *Span) {
	stats := t.stats
	span.statSpan = nil
	t.stats = stats + 1
}
`

func adoptionFixture(source string) string {
	replaced := strings.NewReplacer(
		"type SpanContext struct{ owner *Span }",
		"type SpanContext struct {\n\towner   *Span\n\ttraceID traceID\n\ttrace   *trace\n\torigin  string\n}",
		"import (\n",
		"import (\n\t\"github.com/DataDog/dd-trace-go/v2/ddtrace/ext\"\n\t\"github.com/DataDog/dd-trace-go/v2/internal/samplernames\"\n",
		"\tspanID                            uint64\n",
		"\tspanID                            uint64\n\ttraceID                           uint64\n\tstatSpan                          *int\n",
	).Replace(source)
	return replaced + adoptionInternals
}

// Adoption's edits withhold adopted spans at the start of submitChunk and
// skip their oversized stats after the SDK resets them; spanStart adopts.
func TestSDKMirrorAdoptionEdits(t *testing.T) {
	got, hooks, err := TransformSDKMirror("sdk.go", []byte(adoptionFixture(sdkMirrorSource)))
	if err != nil || hooks != SDKMirrorCopies|SDKMirrorAdoption {
		t.Fatalf("adoption edits: hooks=%d err=%v", hooks, err)
	}
	source := string(got)
	for _, want := range []string{
		"func (t *tracer) submitChunk(c *chunk) {\nif __ddtoMirrorSubmit(c) {\nreturn\n}\n",
		"span.statSpan = nil\nif __ddtoMirrorWithheld(span) {\nreturn\n}\n",
		"__ddtoMirrorAdoptSpan(span, __ddtoParent, context == nil)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %q in:\n%s", want, source)
		}
	}
}

// An SDK that lacks one of adoption's pieces keeps copies of Mini's own
// identity: the other edits are not an error, and copies still work.
func TestSDKMirrorAdoptionDrift(t *testing.T) {
	full := adoptionFixture(sdkMirrorSource)
	variants := map[string]struct {
		source string
		lost   uint8
	}{
		"span_trace_id":      {strings.Replace(full, "\ttraceID                           uint64\n", "", 1), SDKMirrorSpan},
		"sampling_signature": {strings.Replace(full, "setSamplingPriority(p int, sampler samplernames.SamplerName) bool", "setSamplingPriority(p int) bool", 1), SDKMirrorTrace},
		"origin_field":       {strings.Replace(full, "\torigin  string\n", "", 1), SDKMirrorTrace},
		"cache_hex":          {strings.Replace(full, "func (t *traceID) cacheHex()", "func (t *traceID) cacheHexadecimal()", 1), SDKMirrorTrace},
		"submit_parameter":   {strings.Replace(full, "submitChunk(c *chunk)", "submitChunk(c []*Span)", 1), SDKMirrorSubmit},
		"chunk_spans":        {strings.Replace(full, "type chunk struct{ spans []*Span }", "type chunk struct{ items []*Span }", 1), SDKMirrorSubmit},
		"oversized_reset":    {strings.Replace(full, "\tspan.statSpan = nil\n\tt.stats = stats + 1\n", "\tt.stats = stats + 1\n", 1), SDKMirrorOversized},
		"oversized_resets":   {strings.Replace(full, "\tspan.statSpan = nil\n", "\tspan.statSpan = nil\n\tspan.statSpan = nil\n", 1), SDKMirrorOversized},
	}
	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			_, hooks, err := TransformSDKMirror("sdk.go", []byte(variant.source))
			if err != nil || hooks != SDKMirrorCopies|SDKMirrorAdoption&^variant.lost {
				t.Fatalf("hooks=%d err=%v, want %d", hooks, err, SDKMirrorCopies|SDKMirrorAdoption&^variant.lost)
			}
		})
	}
	// Without the SDK's samplernames import, the hook file could not name it.
	withoutImport := strings.Replace(full, "\t\"github.com/DataDog/dd-trace-go/v2/internal/samplernames\"\n", "", 1)
	if _, hooks, err := TransformSDKMirror("sdk.go", []byte(withoutImport)); err != nil || hooks&SDKMirrorTrace != 0 {
		t.Fatalf("hooks=%d err=%v without the samplernames import", hooks, err)
	}
}

// The hook file compiles with the edited SDK: with every adoption edit, and
// with the edits found but adoption's internals missing.
func TestSDKMirrorHookCompiles(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module github.com/DataDog/dd-trace-go/v2\n\ngo 1.25.0\n")
	write("ddtrace/ext/priority.go", "package ext\n\nconst PriorityAutoKeep = 1\n")
	write("internal/samplernames/samplernames.go", "package samplernames\n\ntype SamplerName int8\n\nconst Manual SamplerName = 4\n")
	for name, source := range map[string]string{
		"adoption":      adoptionFixture(sdkMirrorSource),
		"without_trace": strings.Replace(adoptionFixture(sdkMirrorSource), "\torigin  string\n", "", 1),
	} {
		got, hooks, err := TransformSDKMirror("sdk.go", []byte(source))
		if err != nil || hooks&SDKMirrorCopies != SDKMirrorCopies {
			t.Fatalf("%s: hooks=%d err=%v", name, hooks, err)
		}
		write("ddtrace/"+name+"/sdk.go", string(got))
		write("ddtrace/"+name+"/hook.go", SDKMirrorHook(hooks))
	}
	cmd := exec.Command("go", "build", "./ddtrace/...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the edited SDK and its hook file must compile: %v\n%s", err, output)
	}
}
