package instrument

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// TransformSDKCIEnvironment keeps manual SDK testing shims inactive in a
// Mini-owned binary. It does not change the process environment used by Mini.
func TransformSDKCIEnvironment(name string, source []byte) ([]byte, bool, error) {
	return transformSDKCIGates(name, source, []sdkCIGate{{name: "FromEnv", results: []string{"EnabledMode", "bool"}, result: "EnabledModeDisabled, false"}})
}

// TransformSDKCIConfig keeps the full SDK tracer on its APM transport. The SDK
// config reads CI enablement independently of the testing environment reader.
func TransformSDKCIConfig(name string, source []byte) ([]byte, bool, error) {
	return transformSDKCIGates(name, source, []sdkCIGate{
		{name: "CIVisibilityEnabled", receiver: "Config", results: []string{"bool"}, result: "false"},
		{name: "CIVisibilityAgentlessActive", receiver: "Config", results: []string{"bool"}, result: "false"},
	})
}

type sdkCIGate struct {
	name, receiver, result string
	results                []string
}

func transformSDKCIGates(name string, source []byte, gates []sdkCIGate) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		return nil, false, err
	}
	type edit struct {
		offset int
		value  string
	}
	var edits []edit
	found := make(map[string]bool, len(gates))
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, gate := range gates {
			if fn.Name.Name != gate.name {
				continue
			}
			if found[gate.name] || !validSDKCIGate(fn, gate) {
				return nil, false, fmt.Errorf("%w: SDK CI %s signature changed or ambiguous", ErrUnsupportedAPI, gate.name)
			}
			found[gate.name] = true
			edits = append(edits, edit{fset.PositionFor(fn.Body.Lbrace, false).Offset + 1, "\nif true { return " + gate.result + " }\n"})
		}
	}
	if len(edits) == 0 {
		return source, false, nil
	}
	if len(edits) != len(gates) {
		return nil, false, fmt.Errorf("%w: incomplete SDK CI configuration API", ErrUnsupportedAPI)
	}
	// Edits follow source order. Keep the original bodies for import/type checking;
	// constant branches are folded by the compiler without runtime allocations.
	size := len(source)
	for _, edit := range edits {
		size += len(edit.value)
	}
	result := make([]byte, 0, size)
	start := 0
	for _, edit := range edits {
		result = append(result, source[start:edit.offset]...)
		result = append(result, edit.value...)
		start = edit.offset
	}
	result = append(result, source[start:]...)
	return result, true, nil
}

func validSDKCIGate(fn *ast.FuncDecl, gate sdkCIGate) bool {
	sig := fn.Type
	if fn.Body == nil || sig.TypeParams != nil || sig.Params.NumFields() != 0 || sig.Results == nil || len(sig.Results.List) != len(gate.results) || sig.Results.NumFields() != len(gate.results) {
		return false
	}
	if gate.receiver == "" {
		if fn.Recv != nil {
			return false
		}
	} else {
		if fn.Recv == nil || len(fn.Recv.List) != 1 {
			return false
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok || !sdkTypeName(star.X, gate.receiver) {
			return false
		}
	}
	for i, result := range gate.results {
		if !sdkTypeName(sig.Results.List[i].Type, result) {
			return false
		}
	}
	return true
}

func sdkTypeName(expr ast.Expr, name string) bool {
	value, ok := expr.(*ast.Ident)
	return ok && value.Name == name
}
