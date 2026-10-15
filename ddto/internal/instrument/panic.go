package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
)

// PanicHook is the runtime file that ddto's --panic-hook adds:
//   - the variable that TransformPanic's gopanic calls with an unrecovered
//     panic's value, once the panicking goroutine has run its deferred calls
//     and before the runtime prints the panic and exits. Mini's integrations
//     package declares the variable, without an initializer, and sets it
//     while it initializes; before that, or in a binary without Mini, it stays
//     nil. Like the os/signal hook, the runtime sets nothing in Mini and calls
//     Mini only through the variable;
//   - the function that copies a fatal error's message to the crash output
//     (runtime/debug.SetCrashOutput). The runtime prints that message before
//     it starts copying its output there, so the crash output otherwise starts
//     with the goroutine. TransformPanic's fatal and throw pass the message to
//     fatalthrow, which calls the function once startpanic_m has run: after
//     the secure-mode exit, and once the runtime keeps the crash output's
//     descriptor from being replaced or closed. Like print, it only writes, on
//     any stack and without a P.
const PanicHook = `package runtime

import "unsafe"

//go:linkname __dd_civisibility_unhandledPanic github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.unhandledPanic
var __dd_civisibility_unhandledPanic func(any)

const __dd_civisibility_fatalPrefix = "fatal error: "

//go:nosplit
func __dd_civisibility_copyFatal(s string) {
	if fd := crashFD.Load(); fd != ^uintptr(0) {
		write(fd, unsafe.Pointer(unsafe.StringData(__dd_civisibility_fatalPrefix)), int32(len(__dd_civisibility_fatalPrefix)))
		write(fd, unsafe.Pointer(unsafe.StringData(s)), int32(len(s)))
		write(fd, unsafe.Pointer(unsafe.StringData("\n")), 1)
	}
}
`

// panicRuntimeAPI is what PanicHook's linkname names in Mini's integrations
// package. Released runtimes before it lack the variable.
var panicRuntimeAPI = []string{"var unhandledPanic func(any)"}

// panicToolchainAPI is what PanicHook and TransformPanic's edits use in the
// runtime.
var panicToolchainAPI = []string{
	"field _panic.arg any",
	"func preprintpanics(*_panic)",
	"var crashFD atomic.Uintptr",
	"func write(uintptr, unsafe.Pointer, int32) int32",
	"func fatal(string)",
	"func throw(string)",
	"func fatalthrow(throwType)",
	"func startpanic_m() bool",
}

// PanicHookRuntimeError reports why the selected runtime's integrations
// package, given as the sources that the build compiles, cannot receive
// PanicHook's call, or nil.
func PanicHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, panicRuntimeAPI, "the runtime's integrations package lacks %s")
}

// TransformPanic checks that the runtime, given as the sources that the build
// compiles, has the shape that its edits expect, and returns the files that
// it edits:
//   - gopanic calls PanicHook's variable right after its single
//     preprintpanics call, with the panic's value as preprintpanics left it:
//     the result of its Error or String method, which the runtime calls there
//     for the last time before it stops the other goroutines. The hook thus
//     calls no method of the value again;
//   - fatal and throw pass their message to fatalthrow, which takes it as a
//     new last parameter and copies it to the crash output right after
//     startpanic_m.
//
// The edits stay on their lines, so the runtime keeps its positions.
func TransformPanic(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, panicToolchainAPI, "the runtime lacks %s"); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	output := map[string][]byte{}
	found := map[string]int{}
	for _, name := range names {
		src := files[name]
		if bytes.Contains(src, []byte("__dd_civisibility_")) {
			return nil, fmt.Errorf("%s: already instrumented", name)
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		var edits []edit
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			switch fn.Name.Name {
			case "gopanic":
				for _, stmt := range fn.Body.List {
					if panicked, ok := preprintpanicsCall(stmt); ok {
						at := fs.Position(stmt.End()).Offset
						edits = append(edits, edit{at, at, "; if __dd_civisibility_unhandledPanic != nil { __dd_civisibility_unhandledPanic(" + panicked + ".arg) }"})
						found["gopanic"]++
					}
				}
			case "fatal", "throw":
				params := fn.Type.Params.List
				if len(params) != 1 || len(params[0].Names) != 1 {
					continue
				}
				message := params[0].Names[0].Name
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						return true
					}
					if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "fatalthrow" {
						return true
					}
					at := fs.Position(call.Rparen).Offset
					edits = append(edits, edit{at, at, ", " + message})
					found[fn.Name.Name]++
					return false
				})
			case "fatalthrow":
				params := fn.Type.Params
				if len(params.List) != 1 || len(params.List[0].Names) != 1 {
					continue
				}
				at := fs.Position(params.Closing).Offset
				edits = append(edits, edit{at, at, ", __dd_civisibility_message string"})
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					stmt, ok := n.(*ast.ExprStmt)
					if !ok {
						return true
					}
					call, ok := stmt.X.(*ast.CallExpr)
					if !ok || len(call.Args) != 0 {
						return true
					}
					if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "startpanic_m" {
						return true
					}
					start, end := fs.Position(stmt.Pos()).Offset, fs.Position(stmt.End()).Offset
					edits = append(edits, edit{start, end, "if startpanic_m() { __dd_civisibility_copyFatal(__dd_civisibility_message) }"})
					found["fatalthrow"]++
					return false
				})
			}
		}
		if len(edits) == 0 {
			continue
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
		var buf bytes.Buffer
		buf.WriteString("//line " + name + ":1\n")
		cursor := 0
		for _, e := range edits {
			buf.Write(src[cursor:e.start])
			buf.WriteString(e.text)
			cursor = e.end
		}
		buf.Write(src[cursor:])
		output[name] = buf.Bytes()
	}
	for _, function := range []string{"gopanic", "fatal", "throw", "fatalthrow"} {
		if found[function] != 1 {
			return nil, fmt.Errorf("the runtime's %s has %d statements that TransformPanic edits, want 1", function, found[function])
		}
	}
	return output, nil
}

// preprintpanicsCall reports whether stmt is gopanic's preprintpanics(&p),
// and returns p.
func preprintpanicsCall(stmt ast.Stmt) (string, bool) {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "preprintpanics" {
		return "", false
	}
	arg, ok := call.Args[0].(*ast.UnaryExpr)
	if !ok || arg.Op != token.AND {
		return "", false
	}
	panicked, ok := arg.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return panicked.Name, true
}
