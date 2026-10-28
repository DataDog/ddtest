package instrument

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// The SDK edits that TransformSDKMirror finds, one bit each. Copies need the
// first four. Adoption, with the other four, makes an SDK span under a test
// join the test's trace with the ciapp-test origin and keep priority, and
// withholds it from the SDK's delivery to APM: its copy reports it. Without
// them, copies keep their own identity and the SDK delivers its spans.
const (
	SDKMirrorContext   uint8 = 1 << iota // SpanContext gains the copy's state
	SDKMirrorStart                       // spanStart creates the copy
	SDKMirrorFinish                      // finish captures it
	SDKMirrorDetach                      // ContextWithSpan keeps its scope
	SDKMirrorSpan                        // Span's identity fields
	SDKMirrorTrace                       // trace ID, origin and priority internals
	SDKMirrorSubmit                      // submitChunk withholds adopted spans
	SDKMirrorOversized                   // computeOversizedSpanStats skips them

	SDKMirrorCopies   = SDKMirrorContext | SDKMirrorStart | SDKMirrorFinish | SDKMirrorDetach
	SDKMirrorAdoption = SDKMirrorSpan | SDKMirrorTrace | SDKMirrorSubmit | SDKMirrorOversized
)

const (
	sdkExtImport          = "github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	sdkSamplerNamesImport = "github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

// sdkSpanFields reports whether Span holds the identity that adoption sets.
func sdkSpanFields(structure *ast.StructType) bool {
	return sdkHasField(structure, "spanID", func(e ast.Expr) bool { return sdkTypeName(e, "uint64") }) &&
		sdkHasField(structure, "traceID", func(e ast.Expr) bool { return sdkTypeName(e, "uint64") }) &&
		sdkHasField(structure, "context", func(e ast.Expr) bool { return sdkPointerType(e, "SpanContext") })
}

// sdkTraceInternals reports whether file declares what adoption uses to give
// a root span its test's trace ID, the ciapp-test origin and keep priority.
func sdkTraceInternals(file *ast.File) bool {
	var traceIDFields, contextFields bool
	methods := map[string]bool{}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok != token.TYPE {
				continue
			}
			for _, spec := range decl.Specs {
				typ := spec.(*ast.TypeSpec)
				structure, ok := typ.Type.(*ast.StructType)
				if !ok || typ.Assign.IsValid() {
					continue
				}
				switch typ.Name.Name {
				case "traceID":
					traceIDFields = sdkHasField(structure, "value", func(e ast.Expr) bool {
						array, ok := e.(*ast.ArrayType)
						if !ok {
							return false
						}
						length, isLiteral := array.Len.(*ast.BasicLit)
						return isLiteral && length.Value == "16" && sdkTypeName(array.Elt, "byte")
					}) && sdkHasField(structure, "hexEncoded", func(e ast.Expr) bool { return sdkTypeName(e, "string") })
				case "SpanContext":
					contextFields = sdkHasField(structure, "trace", func(e ast.Expr) bool { return sdkPointerType(e, "trace") }) &&
						sdkHasField(structure, "origin", func(e ast.Expr) bool { return sdkTypeName(e, "string") })
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil || len(decl.Recv.List) != 1 || decl.Type.TypeParams != nil {
				continue
			}
			params, results := decl.Type.Params.List, decl.Type.Results
			switch receiver := decl.Recv.List[0].Type; {
			case sdkPointerType(receiver, "traceID") && (decl.Name.Name == "SetUpper" || decl.Name.Name == "SetLower"):
				methods[decl.Name.Name] = len(params) == 1 && len(params[0].Names) == 1 && sdkTypeName(params[0].Type, "uint64") && results == nil
			case sdkPointerType(receiver, "traceID") && decl.Name.Name == "cacheHex":
				methods[decl.Name.Name] = len(params) == 0 && results == nil
			case sdkPointerType(receiver, "trace") && decl.Name.Name == "setSamplingPriority":
				methods[decl.Name.Name] = len(params) == 2 && len(params[0].Names) == 1 && len(params[1].Names) == 1 &&
					sdkTypeName(params[0].Type, "int") && sdkQualified(params[1].Type, "samplernames", "SamplerName") &&
					results != nil && len(results.List) == 1 && sdkTypeName(results.List[0].Type, "bool")
			}
		}
	}
	return traceIDFields && contextFields && methods["SetUpper"] && methods["SetLower"] && methods["cacheHex"] &&
		methods["setSamplingPriority"] && sdkImports(file, sdkExtImport) && sdkImports(file, sdkSamplerNamesImport)
}

// rewriteSDKSubmit withholds adopted spans at the start of submitChunk, before
// the SDK computes their stats or queues them for APM.
func rewriteSDKSubmit(file *ast.File, fn *ast.FuncDecl, insert func(token.Pos, string)) bool {
	if !sdkTracerMethod(fn) || fn.Type.Results != nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 ||
		!sdkPointerType(fn.Type.Params.List[0].Type, "chunk") || !sdkChunkSpans(file) {
		return false
	}
	chunk := fn.Type.Params.List[0].Names[0].Name
	if chunk == "_" {
		return false
	}
	insert(fn.Body.Lbrace+1, fmt.Sprintf("\nif __ddtoMirrorSubmit(%s) {\nreturn\n}\n", chunk))
	return true
}

// rewriteSDKOversized skips the stats of an adopted span of an oversized
// trace, which the SDK sends without assembling a chunk.
func rewriteSDKOversized(fn *ast.FuncDecl, insert func(token.Pos, string)) bool {
	if !sdkTracerMethod(fn) || fn.Type.Results != nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 ||
		!sdkPointerType(fn.Type.Params.List[0].Type, "Span") {
		return false
	}
	span := fn.Type.Params.List[0].Names[0].Name
	if span == "_" {
		return false
	}
	// The SDK drops the span's retained stats before computing new ones: keep
	// doing so, then skip the rest.
	var reset *ast.AssignStmt
	for _, stmt := range fn.Body.List {
		if assign, ok := stmt.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 &&
			sdkSelector(assign.Lhs[0], span, "statSpan") && sdkTypeName(assign.Rhs[0], "nil") {
			if reset != nil {
				return false
			}
			reset = assign
		}
	}
	if reset == nil {
		return false
	}
	insert(reset.End(), fmt.Sprintf("\nif __ddtoMirrorWithheld(%s) {\nreturn\n}\n", span))
	return true
}

func sdkTracerMethod(fn *ast.FuncDecl) bool {
	return fn.Recv != nil && len(fn.Recv.List) == 1 && sdkPointerType(fn.Recv.List[0].Type, "tracer") &&
		fn.Type.TypeParams == nil && fn.Type.Params != nil && fn.Body != nil
}

// sdkChunkSpans reports whether file declares chunk with its spans.
func sdkChunkSpans(file *ast.File) bool {
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, spec := range group.Specs {
			typ := spec.(*ast.TypeSpec)
			if structure, ok := typ.Type.(*ast.StructType); ok && typ.Name.Name == "chunk" && !typ.Assign.IsValid() {
				return sdkHasField(structure, "spans", func(e ast.Expr) bool {
					slice, ok := e.(*ast.ArrayType)
					return ok && slice.Len == nil && sdkPointerType(slice.Elt, "Span")
				})
			}
		}
	}
	return false
}

func sdkHasField(structure *ast.StructType, name string, match func(ast.Expr) bool) bool {
	for _, field := range structure.Fields.List {
		for _, ident := range field.Names {
			if ident.Name == name {
				return match(field.Type)
			}
		}
	}
	return false
}

func sdkQualified(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == name && sdkTypeName(selector.X, pkg)
}

func sdkImports(file *ast.File, path string) bool {
	for _, spec := range file.Imports {
		if value, err := strconv.Unquote(spec.Path.Value); err == nil && value == path && (spec.Name == nil || spec.Name.Name != "_") {
			return true
		}
	}
	return false
}

// SDKMirrorHook is the SDK file that connects the edits that found names to
// Mini's mirror. Adoption's functions do nothing unless every adoption edit
// was found.
func SDKMirrorHook(found uint8) string {
	adoption := found&SDKMirrorAdoption == SDKMirrorAdoption
	var source strings.Builder
	source.WriteString("package tracer\nimport (\n \"context\"\n _ \"unsafe\"\n")
	if adoption {
		source.WriteString(" \"encoding/binary\"\n \"" + sdkExtImport + "\"\n \"" + sdkSamplerNamesImport + "\"\n")
	}
	source.WriteString(`)
//go:linkname __ddtoMirrorEnable github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorEnable
func __ddtoMirrorEnable()
func init(){ __ddtoMirrorEnable() }
//go:linkname __ddtoMirrorStart github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorStart
func __ddtoMirrorStart(context.Context,any,string) any
//go:linkname __ddtoMirrorContext github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorContext
func __ddtoMirrorContext(context.Context,any) context.Context
//go:linkname __ddtoMirrorCapture github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorCapture
func __ddtoMirrorCapture(any,string,string,string,string,int64,int64,int32,func(func(string,string)bool),map[string]float64,[16]byte,uint64) any
//go:linkname __ddtoMirrorDeliver github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorDeliver
func __ddtoMirrorDeliver(any)
`)
	if !adoption {
		source.WriteString("func __ddtoMirrorAdoptSpan(*Span, any, bool) {}\n")
		if found&SDKMirrorSubmit != 0 {
			source.WriteString("func __ddtoMirrorSubmit(*chunk) bool { return false }\n")
		}
		if found&SDKMirrorOversized != 0 {
			source.WriteString("func __ddtoMirrorWithheld(*Span) bool { return false }\n")
		}
		return source.String()
	}
	source.WriteString(`//go:linkname __ddtoMirrorAdopt github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorAdopt
func __ddtoMirrorAdopt(any,any,uint64,bool) ([16]byte, bool)
//go:linkname __ddtoMirrorAdopted github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorAdopted
func __ddtoMirrorAdopted(any) bool
//go:linkname __ddtoMirrorReport github.com/DataDog/ddtest/ddto/internal/minitracer.sdkMirrorWithheld
func __ddtoMirrorReport(int,int)
// A root SDK span under a test takes the test's trace ID, the ciapp-test
// origin, which its children and propagation inherit, and keep priority.
func __ddtoMirrorAdoptSpan(span *Span, parent any, root bool) {
	id, ok := __ddtoMirrorAdopt(span.context.__ddtoMirror, parent, span.spanID, root)
	if !ok || !root {
		return
	}
	span.traceID = binary.BigEndian.Uint64(id[8:])
	span.context.traceID.SetUpper(binary.BigEndian.Uint64(id[:8]))
	span.context.traceID.SetLower(span.traceID)
	span.context.traceID.cacheHex()
	span.context.origin = "ciapp-test"
	span.context.trace.setSamplingPriority(ext.PriorityAutoKeep, samplernames.Manual)
}
func __ddtoMirrorWithheld(span *Span) bool {
	return span != nil && span.context != nil && __ddtoMirrorAdopted(span.context.__ddtoMirror)
}
// Adopted spans are not delivered: a new slice keeps spansToRelease intact.
func __ddtoMirrorSubmit(c *chunk) bool {
	withheld := 0
	for _, span := range c.spans {
		if __ddtoMirrorWithheld(span) {
			withheld++
		}
	}
	if withheld == 0 {
		return false
	}
	kept := make([]*Span, 0, len(c.spans)-withheld)
	for _, span := range c.spans {
		if !__ddtoMirrorWithheld(span) {
			kept = append(kept, span)
		}
	}
	__ddtoMirrorReport(withheld, len(kept))
	c.spans = kept
	return len(kept) == 0
}
`)
	return source.String()
}
