package instrument

import (
	"errors"
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

type chunk struct {
	spans          []*Span
	spansToRelease []*Span
}

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

// beforeV211 turns an adoption fixture into the layout of SDKs before v2.11:
// ContextWithSpan chains context values, and submit computes each finished
// span's stats.
func beforeV211(source string) string {
	start, end := strings.Index(source, "func ContextWithSpan("), strings.Index(source, "\nvar _ = ext.PriorityAutoKeep")
	source = source[:start] + `type activeSpanKey struct{}
type activeSpanContextKey struct{}

var internal = struct{ ActiveSpanKey activeSpanKey }{}

func ContextWithSpan(ctx context.Context, s *Span) context.Context {
	newCtx := context.WithValue(ctx, internal.ActiveSpanKey, s)
	if s != nil {
		newCtx = context.WithValue(newCtx, activeSpanContextKey{}, s.Context())
	}
	return newCtx
}
` + source[end:]
	stats := strings.Index(source, "func (t *tracer) computeOversizedSpanStats(")
	return source[:stats] + `type statsCalc struct{}

func (statsCalc) newTracerStatSpan(*Span) int { return 1 }

func (t *tracer) submit(s *Span) { t.stats += statsCalc{}.newTracerStatSpan(s) }
`
}

func adoptionFixture(source string) string {
	// Windows checkouts can convert the embedded fixture to CRLF.
	source = strings.ReplaceAll(source, "\r\n", "\n")
	replaced := strings.NewReplacer(
		"type SpanContext struct{ owner *Span }",
		"type SpanContext struct {\n\towner       *Span\n\ttraceID     traceID\n\ttrace       *trace\n\torigin      string\n\tbaggageOnly bool\n}",
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
		"func (t *tracer) submitChunk(c *chunk) {\n__ddtoMirrorSubmit(c)\n",
		"span.statSpan = nil\nif __ddtoMirrorWithheld(span) {\nreturn\n}\n",
		"__ddtoMirrorAdoptSpan(span, __ddtoParent, context)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %q in:\n%s", want, source)
		}
	}
}

// SDKs before v2.11 have every edit in their own layout.
func TestSDKMirrorBeforeV211(t *testing.T) {
	got, hooks, err := TransformSDKMirror("sdk.go", []byte(beforeV211(adoptionFixture(sdkMirrorSource))))
	if err != nil || hooks != SDKMirrorCopies|SDKMirrorAdoption {
		t.Fatalf("hooks=%d err=%v", hooks, err)
	}
	for _, want := range []string{
		"__ddtoMirrorContext(ctx,__ddtoState)\nnewCtx := context.WithValue(ctx, internal.ActiveSpanKey, s)",
		"func (t *tracer) submit(s *Span) {\nif __ddtoMirrorWithheld(s) {\nreturn\n}\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for name, mutation := range map[string][2]string{
		"snapshot_missing":   {"newCtx = context.WithValue(newCtx, activeSpanContextKey{}, s.Context())", "_ = newCtx"},
		"snapshot_unguarded": {"if s != nil {", "if s == nil {"},
		"derived_twice":      {"return newCtx", "return context.WithValue(ctx, activeSpanContextKey{}, nil)"},
	} {
		source := strings.Replace(beforeV211(adoptionFixture(sdkMirrorSource)), mutation[0], mutation[1], 1)
		if _, _, err := TransformSDKMirror("sdk.go", []byte(source)); !errors.Is(err, ErrUnsupportedAPI) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	stats := strings.Replace(beforeV211(adoptionFixture(sdkMirrorSource)), "statsCalc{}.newTracerStatSpan(s)", "1", 1)
	if _, hooks, err := TransformSDKMirror("sdk.go", []byte(stats)); err != nil || hooks&SDKMirrorStats != 0 {
		t.Fatalf("submit without stats: hooks=%d err=%v", hooks, err)
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
		"origin_field":       {strings.Replace(full, "\torigin      string\n", "", 1), SDKMirrorTrace},
		"cache_hex":          {strings.Replace(full, "func (t *traceID) cacheHex()", "func (t *traceID) cacheHexadecimal()", 1), SDKMirrorTrace},
		"submit_parameter":   {strings.Replace(full, "submitChunk(c *chunk)", "submitChunk(c []*Span)", 1), SDKMirrorSubmit},
		"chunk_spans":        {strings.Replace(full, "\tspans          []*Span\n", "\titems          []*Span\n", 1), SDKMirrorSubmit},
		"chunk_release":      {strings.Replace(full, "\tspansToRelease []*Span\n", "", 1), SDKMirrorSubmit},
		"baggage_only":       {strings.Replace(full, "\tbaggageOnly bool\n", "", 1), SDKMirrorTrace},
		"oversized_reset":    {strings.Replace(full, "\tspan.statSpan = nil\n\tt.stats = stats + 1\n", "\tt.stats = stats + 1\n", 1), SDKMirrorStats},
		"oversized_resets":   {strings.Replace(full, "\tspan.statSpan = nil\n", "\tspan.statSpan = nil\n\tspan.statSpan = nil\n", 1), SDKMirrorStats},
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
		"before_v2_11":  beforeV211(adoptionFixture(sdkMirrorSource)),
		"without_trace": strings.Replace(adoptionFixture(sdkMirrorSource), "\torigin      string\n", "", 1),
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

// main's spanStart also returns whether the span is a root and its parent's
// service.
func TestSDKMirrorSpanStartResults(t *testing.T) {
	source := strings.ReplaceAll(sdkMirrorSource, "\r\n", "\n")
	source = strings.Replace(source, "opts StartSpanConfig) *Span {", "opts StartSpanConfig) (*Span, bool, string) {", 1)
	source = strings.Replace(source, "\treturn span\n", "\treturn span, context == nil, \"\"\n", 1)
	if _, hooks, err := TransformSDKMirror("sdk.go", []byte(source)); err != nil || hooks != SDKMirrorCopies {
		t.Fatalf("hooks=%d err=%v", hooks, err)
	}
	several := strings.Replace(source, "(*Span, bool, string) {", "(span, other *Span, root bool) {", 1)
	if _, _, err := TransformSDKMirror("sdk.go", []byte(several)); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("ambiguous span result accepted: %v", err)
	}
}
