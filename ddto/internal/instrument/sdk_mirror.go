package instrument

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// TransformSDKMirror edits the compiler's actual SDK inputs, including covered
// sources. Opaque state belongs to SpanContext, never the recyclable Span.
func TransformSDKMirror(name string, source []byte) ([]byte, uint8, error) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, 0, err
	}
	reserved := false
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && strings.HasPrefix(id.Name, "__ddtest") {
			reserved = true
		}
		return !reserved
	})
	if reserved {
		return nil, 0, sdkMirrorError("reserved SDK mirror name")
	}
	var edits []edit
	var found uint8
	insert := func(pos token.Pos, code string) {
		offset := fs.PositionFor(pos, false).Offset
		edits = append(edits, edit{offset, offset, code})
	}
	for _, decl := range file.Decls {
		if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.TYPE {
			for _, spec := range group.Specs {
				typ := spec.(*ast.TypeSpec)
				if typ.Name.Name == "SpanContext" {
					structure, ok := typ.Type.(*ast.StructType)
					if !ok || typ.Assign.IsValid() || found&1 != 0 {
						return nil, 0, sdkMirrorError("SDK SpanContext changed")
					}
					insert(structure.Fields.Opening+1, "\n__ddtestMirror any\n")
					found |= 1
				}
			}
		}
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var bit uint8
		switch {
		case fn.Recv == nil && fn.Name.Name == "spanStart":
			bit = 2
			err = rewriteSDKSpanStart(fn, insert)
		case sdkSpanReceiver(fn) != "" && fn.Name.Name == "finish":
			bit = 4
			err = rewriteSDKFinish(fn, insert)
		case fn.Recv == nil && fn.Name.Name == "ContextWithSpan":
			bit = 8
			err = rewriteSDKContext(file, fn, insert)
		}
		if err != nil {
			return nil, 0, err
		}
		if bit&found != 0 {
			return nil, 0, sdkMirrorError("duplicate SDK mirror function")
		}
		found |= bit
	}
	if found == 0 {
		return source, 0, nil
	}
	result, err := applyTestifyEdits(name, source, edits)
	return result, found, err
}

func sdkMirrorError(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedAPI, reason)
}

func sdkPointerType(expr ast.Expr, name string) bool {
	star, ok := expr.(*ast.StarExpr)
	return ok && sdkTypeName(star.X, name)
}
func sdkSelector(expr ast.Expr, receiver, field string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == field && sdkTypeName(selector.X, receiver)
}
func sdkSpanReceiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	receiver := fn.Recv.List[0]
	if len(receiver.Names) != 1 || !sdkPointerType(receiver.Type, "Span") {
		return ""
	}
	return receiver.Names[0].Name
}

const SDKMirrorHook = `package tracer
import (
 "context"
 _ "unsafe"
)
//go:linkname __ddtestMirrorEnable github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.sdkMirrorEnable
func __ddtestMirrorEnable()
func init(){ __ddtestMirrorEnable() }
//go:linkname __ddtestMirrorStart github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.sdkMirrorStart
func __ddtestMirrorStart(context.Context,any,string) any
//go:linkname __ddtestMirrorContext github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.sdkMirrorContext
func __ddtestMirrorContext(context.Context,any) context.Context
//go:linkname __ddtestMirrorCapture github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.sdkMirrorCapture
func __ddtestMirrorCapture(any,string,string,string,string,int64,int64,int32,func(func(string,string)bool),map[string]float64,[16]byte,uint64) any
//go:linkname __ddtestMirrorDeliver github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer.sdkMirrorDeliver
func __ddtestMirrorDeliver(any)
`
