package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const GoleakImport = "go.uber.org/goleak"
const GoleakHookName = "__ddtestPrepareLeakCheck"

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
		if bytes.Contains(src, []byte("__ddtest")) {
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
		prefix := "github.com/tonyredondo/dd-ci-testing-poc/internal/"
		filters := []string{
			prefix + "thirdparty/dd-trace-go/civisibility/integrations.(*ciVisibilitySignalHandler).run",
			prefix + "thirdparty/dd-trace-go/telemetry/internal.(*Ticker).run",
			prefix + "cidelivery.BeginSend",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/logs.(*logsWriter).sendPayload",
			prefix + "thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage.(*coverageWriter).sendPayload",
			prefix + "minitracer.(*Client).deliverInBackground",
		}
		options := arg.Names[0].Name
		// Find also accepts a caller-owned variadic slice with spare capacity.
		// Keep its backing storage intact while reserving space for our filters.
		code := fmt.Sprintf("__ddtestResume := %s(); defer __ddtestResume(); __ddtestOptions := make([]Option, len(%s), len(%s)+%d); copy(__ddtestOptions, %s); %s = append(__ddtestOptions", GoleakHookName, options, options, len(filters), options, options)
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

// No runtime import is inserted into goleak's module dependency graph.
func GoleakEntryHook() string {
	return "package goleak\nimport _ \"unsafe\"\n//go:linkname " + GoleakHookName + " github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery.PrepareLeakCheck\nfunc " + GoleakHookName + "() func()\n"
}
