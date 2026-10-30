package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

const GoleakImport = "go.uber.org/goleak"
const GoleakHookName = "__ddtoPrepareLeakCheck"

// TransformGoleakEntry keeps Find's options, retries and error reporting. The
// exact filters apply after the caller's options, which can replace filters.
// Coverage-generated inputs use the same edit, after Go has inserted counters.
func TransformGoleakEntry(name string, src []byte) ([]byte, bool, error) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "Find" {
			continue
		}
		if bytes.Contains(src, []byte("__ddto")) {
			return nil, false, fmt.Errorf("%s: reserved goleak hook name", name)
		}
		if fn.Body == nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			return nil, false, fmt.Errorf("%s: %w: unsupported goleak.Find signature", name, ErrUnsupportedAPI)
		}
		arg := fn.Type.Params.List[0]
		variadic, ok := arg.Type.(*ast.Ellipsis)
		if !ok || arg.Names[0].Name == "_" {
			return nil, false, fmt.Errorf("%s: %w: goleak.Find requires named variadic options", name, ErrUnsupportedAPI)
		}
		typ, ok := variadic.Elt.(*ast.Ident)
		result, resultOK := fn.Type.Results.List[0].Type.(*ast.Ident)
		if !ok || typ.Name != "Option" || !resultOK || result.Name != "error" {
			return nil, false, fmt.Errorf("%s: %w: unsupported goleak.Find types", name, ErrUnsupportedAPI)
		}
		prefix := "github.com/DataDog/ddtest/ddto/internal/"
		filters := []string{
			prefix + "thirdparty/dd-trace-go/civisibility/integrations.(*ciVisibilitySignalHandler).run",
			prefix + "thirdparty/dd-trace-go/telemetry/internal.(*Ticker).run",
			prefix + "cidelivery.BeginSend",
			prefix + "cidelivery.BeginSendContext",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/logs.(*logsWriter).sendPayload",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage.(*coverageWriter).sendPayload",
			prefix + "minitracer.(*Client).deliverInBackground",
			prefix + "minitracer.(*Client).drainWorker",
			// The root waits for its seed while goleak runs inside that seed.
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).executeInternalFuzzTarget.func1",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).executeInternalFuzzTarget.func2",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).instrumentInternalFuzzTargets.(*M).executeInternalFuzzTarget.func1",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.(*M).instrumentInternalFuzzTargets.(*M).executeInternalFuzzTarget.func2",
			// With Auto Test Retries or Early Flake Detection, the test's own
			// goroutine waits while its attempt runs in another.
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.runFreshRetryAttemptInGroupWithCallbacks",
			// Native testing also has a pipe reader while an example executes.
			"testing.runExample.func1",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.runManagedExample",
			// Example output capture waits on the pipe owned by the wrapper.
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting.captureExampleOutput",
		}
		options := arg.Names[0].Name
		// Find also accepts a caller-owned variadic slice with spare capacity.
		// Keep its backing storage intact while reserving space for our filters.
		code := fmt.Sprintf("__ddtoResume := %s(); defer __ddtoResume(); __ddtoOptions := make([]Option, len(%s), len(%s)+%d); copy(__ddtoOptions, %s); %s = append(__ddtoOptions", GoleakHookName, options, options, len(filters), options, options)
		for _, filter := range filters {
			code += fmt.Sprintf(", IgnoreAnyFunction(%q)", filter)
		}
		code += ");"
		pos := fs.PositionFor(fn.Body.Lbrace, false).Offset + 1
		edits := []edit{{pos, pos, code}}
		if fs.Position(file.Package).Filename == name {
			if strings.ContainsAny(name, "\r\n") || strings.Contains(name, "*/") {
				return nil, false, fmt.Errorf("%s: filename cannot be encoded in a line directive", name)
			}
			p := fs.PositionFor(file.Package, false)
			edits = append(edits, edit{p.Offset, p.Offset, fmt.Sprintf("/*line %s:%d:1*/", name, p.Line)})
		}
		out, err := applyTestifyEdits(name, src, edits)
		return out, true, err
	}
	return nil, false, nil
}

// GoleakAPI checks the rest of the package for what the Find edit calls: a
// top-level IgnoreAnyFunction(string) Option. goleak v1.2.1 has the same Find
// but no IgnoreAnyFunction, so the edit would not compile there. The code
// decides, not the version: a Bazel compile does not know module versions.
func GoleakAPI(files map[string][]byte) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		file, err := parser.ParseFile(token.NewFileSet(), name, files[name], parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "IgnoreAnyFunction" {
				continue
			}
			params, results := fn.Type.Params, fn.Type.Results
			if fn.Type.TypeParams == nil && params != nil && len(params.List) == 1 && len(params.List[0].Names) <= 1 && isIdent(params.List[0].Type, "string") &&
				results != nil && len(results.List) == 1 && len(results.List[0].Names) == 0 && isIdent(results.List[0].Type, "Option") {
				return nil
			}
			return fmt.Errorf("%s: %w: unsupported goleak.IgnoreAnyFunction signature", name, ErrUnsupportedAPI)
		}
	}
	return fmt.Errorf("%w: goleak.IgnoreAnyFunction(string) Option is missing", ErrUnsupportedAPI)
}

// No runtime import is inserted into goleak's module dependency graph.
func GoleakEntryHook() string {
	return "package goleak\nimport _ \"unsafe\"\n//go:linkname " + GoleakHookName + " github.com/DataDog/ddtest/ddto/internal/cidelivery.PrepareLeakCheck\nfunc " + GoleakHookName + "() func()\n"
}
