package instrument

import (
	_ "embed"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/sdkmirror/sdk.go.txt
var sdkMirrorSource string

//go:embed testdata/sdkmirror/behavior_test.go.txt
var sdkMirrorBehavior string

func TestSDKMirrorCosmeticChanges(t *testing.T) {
	variants := map[string]string{
		"unchanged":            sdkMirrorSource,
		"receiver":             renameSDKLocal(sdkMirrorSource, "finish", "s", "receiver"),
		"creation_locals":      renameSDKLocal(renameSDKLocal(renameSDKLocal(sdkMirrorSource, "spanStart", "span", "created"), "spanStart", "context", "parent"), "spanStart", "pprofContext", "base"),
		"operation":            renameSDKLocal(sdkMirrorSource, "spanStart", "operationName", "operation"),
		"context_parameters":   renameSDKLocal(renameSDKLocal(sdkMirrorSource, "ContextWithSpan", "ctx", "base"), "ContextWithSpan", "s", "value"),
		"context_locals":       renameSDKLocal(renameSDKLocal(sdkMirrorSource, "ContextWithSpan", "snapshot", "saved"), "ContextWithSpan", "newCtx", "result"),
		"context_declaration":  strings.Replace(sdkMirrorSource, "newCtx :=", "var newCtx =", 1),
		"reserved_comment":     sdkMirrorSource + "\n// A __ddtest integration comment.\nvar diagnostic = \"__ddtest is text\"\n",
		"deferred_tail":        strings.Replace(strings.Replace(sdkMirrorSource, `s.resource = "final"`, `s.resource = "intermediate"`, 1), "s.context.finish(s)", "s.context.finish(s); defer func(){ s.resource=\"final\" }()", 1),
		"context_import_alias": strings.NewReplacer(`"context"`, `gocontext "context"`, "context.Context", "gocontext.Context").Replace(sdkMirrorSource),
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module mirrorfixtures\n\ngo 1.26.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, source := range variants {
		t.Run(name, func(t *testing.T) {
			got, hooks, err := TransformSDKMirror("sdk.go", []byte(source))
			if err != nil || hooks != 15 {
				t.Fatalf("cosmetic change rejected: hooks=%d err=%v", hooks, err)
			}
			dir := filepath.Join(root, name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for file, data := range map[string][]byte{"sdk.go": got, "behavior_test.go": []byte(sdkMirrorBehavior)} {
				if err := os.WriteFile(filepath.Join(dir, file), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated fixtures must compile and preserve capture/context/lock behavior: %v\n%s", err, output)
	}
}

// Rename one function's identifiers while preserving SDK selector fields.
func renameSDKLocal(source, function, old, new string) string {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "sdk.go", source, parser.SkipObjectResolution)
	if err != nil {
		panic(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		fields := map[*ast.Ident]bool{}
		ast.Inspect(fn, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				fields[selector.Sel] = true
			}
			if pair, ok := node.(*ast.KeyValueExpr); ok {
				if id, ok := pair.Key.(*ast.Ident); ok {
					fields[id] = true
				}
			}
			return true
		})
		var positions []int
		ast.Inspect(fn, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && id.Name == old && !fields[id] {
				positions = append(positions, fs.Position(id.Pos()).Offset)
			}
			return true
		})
		for index := len(positions) - 1; index >= 0; index-- {
			pos := positions[index]
			source = source[:pos] + new + source[pos+len(old):]
		}
		return source
	}
	panic("fixture function missing: " + function)
}

func TestSDKMirrorAPIDrift(t *testing.T) {
	for name, mutation := range map[string][2]string{
		"context_type":        {"type SpanContext struct{ owner *Span }", "type SpanContext interface{}"},
		"result_type":         {"*Span {", "Span {"},
		"constructor_parent":  {"newSpanContext(span, context)", "newSpanContext(span, nil)"},
		"finish_method":       {"s.context.finish(s)", "s.context.complete(s)"},
		"finish_signature":    {"finishTime int64", "finishTime string"},
		"context_signature":   {"s *Span) context.Context", "s Span) context.Context"},
		"unlock":              {"defer s.mu.Unlock()", "s.mu.Unlock()"},
		"snapshot_type":       {"var snapshot *SpanContext", "var snapshot any"},
		"duplicate_creation":  {"span.context = newSpanContext(span, context)", "span.context = newSpanContext(span, context); span.context = newSpanContext(span, context)"},
		"duplicate_finish":    {"s.context.finish(s)", "s.context.finish(s); s.context.finish(s)"},
		"unlocked_finish":     {"s.context.finish(s)", "s.mu.Unlock(); s.context.finish(s); s.mu.Lock()"},
		"receiver_alias":      {"s.context.finish(s)", "alias := s; alias.mu.Unlock(); s.context.finish(s); alias.mu.Lock()"},
		"lock_alias":          {"s.context.finish(s)", "lock := &s.mu; _ = lock; s.context.finish(s)"},
		"snapshot_origin":     {"snapshot = s.Context()", "snapshot = (&Span{}).Context()"},
		"snapshot_guard":      {"if s != nil", "if s == nil"},
		"snapshot_ignored":    {"snapshot: snapshot", "snapshot: nil"},
		"context_ignored":     {"Context: ctx", "Context: context.Background()"},
		"span_ignored":        {"span: s", "span: nil"},
		"reserved_identifier": {"type SpanContext struct{", "type SpanContext struct{ __ddtestCollision any;"},
	} {
		t.Run(name, func(t *testing.T) {
			source := strings.Replace(sdkMirrorSource, mutation[0], mutation[1], 1)
			if source == sdkMirrorSource {
				t.Fatal("mutation did not change fixture")
			}
			if _, _, err := TransformSDKMirror("sdk.go", []byte(source)); !errors.Is(err, ErrUnsupportedAPI) {
				t.Fatalf("unsafe SDK change accepted: %v", err)
			}
		})
	}
	got, _, err := TransformSDKMirror("sdk.go", []byte(sdkMirrorSource))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := TransformSDKMirror("sdk.go", got); !errors.Is(err, ErrUnsupportedAPI) {
		t.Fatal("double instrumentation accepted", err)
	}
}
