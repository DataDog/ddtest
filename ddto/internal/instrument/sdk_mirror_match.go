package instrument

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// Match roles within a small set of SDK operations instead of depending on
// local variable names. Private types and fields remain part of the contract.
func rewriteSDKSpanStart(fn *ast.FuncDecl, insert func(token.Pos, string)) error {
	if fn.Type.TypeParams != nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 || !sdkPointerType(fn.Type.Results.List[0].Type, "Span") || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return sdkMirrorError("SDK spanStart signature changed")
	}
	operation := fn.Type.Params.List[0]
	if len(operation.Names) != 1 || !sdkTypeName(operation.Type, "string") {
		return sdkMirrorError("SDK operation argument changed")
	}
	var anchor *ast.AssignStmt
	var constructor *ast.CallExpr
	count := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && sdkTypeName(call.Fun, "newSpanContext") {
			count++
			constructor = call
		}
		return true
	})
	if count != 1 || len(constructor.Args) != 2 {
		return sdkMirrorError("SDK span construction missing or ambiguous")
	}
	span, spanOK := constructor.Args[0].(*ast.Ident)
	parent, parentOK := constructor.Args[1].(*ast.Ident)
	if !spanOK || !parentOK || span.Name == "_" || parent.Name == "nil" {
		return sdkMirrorError("SDK context construction changed")
	}
	for _, stmt := range fn.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if ok && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 && assign.Rhs[0] == constructor && sdkSelector(assign.Lhs[0], span.Name, "context") {
			anchor = assign
		}
	}
	if anchor == nil || !sdkTypedLocal(fn, parent.Name, anchor.Pos(), func(expr ast.Expr) bool { return sdkPointerType(expr, "SpanContext") }) {
		return sdkMirrorError("SDK parent context changed")
	}
	// The SDK initializes the selected profiling context from StartSpanConfig,
	// then may inherit it from its parent. Use that selected context, not raw opts.
	selected := ""
	ambiguous := false
	for _, stmt := range fn.Body.List {
		if stmt.Pos() >= anchor.Pos() {
			break
		}
		for _, binding := range sdkBindings(stmt) {
			selector, ok := binding.value.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Context" {
				continue
			}
			config, ok := selector.X.(*ast.Ident)
			if !ok || !sdkTypedLocal(fn, config.Name, stmt.Pos(), func(expr ast.Expr) bool { return sdkTypeName(expr, "StartSpanConfig") }) {
				continue
			}
			if selected != "" {
				ambiguous = true
			}
			selected = binding.name
		}
	}
	if selected == "" || selected == "_" || ambiguous {
		return sdkMirrorError("SDK selected context missing or ambiguous")
	}
	insert(anchor.End(), fmt.Sprintf("\nvar __ddtestParent any; if %[1]s != nil { __ddtestParent = %[1]s.__ddtestMirror }; %[2]s.context.__ddtestMirror = __ddtestMirrorStart(%[3]s, __ddtestParent, %[4]s)\n", parent.Name, span.Name, selected, operation.Names[0].Name))
	return nil
}

func rewriteSDKFinish(fn *ast.FuncDecl, insert func(token.Pos, string)) error {
	receiver := sdkSpanReceiver(fn)
	if fn.Type.TypeParams != nil || fn.Type.Params == nil || fn.Type.Params.NumFields() != 1 || len(fn.Type.Params.List[0].Names) != 1 || !sdkTypeName(fn.Type.Params.List[0].Type, "int64") {
		return sdkMirrorError("SDK finish signature changed")
	}
	var lock *ast.CallExpr
	var unlock *ast.DeferStmt
	var anchor *ast.ExprStmt
	lockIndex, unlockIndex, finishIndex := -1, -1, -1
	for index, stmt := range fn.Body.List {
		if deferred, ok := stmt.(*ast.DeferStmt); ok && sdkLockCall(deferred.Call, receiver, "Unlock") {
			unlock = deferred
			unlockIndex = index
		}
		if expression, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := expression.X.(*ast.CallExpr); ok {
				if sdkLockCall(call, receiver, "Lock") {
					lock = call
					lockIndex = index
				}
				if sdkFinishCall(call, receiver) {
					anchor = expression
					finishIndex = index
				}
			}
		}
	}
	// Only these two direct mutex references are understood. Explicit unlocks,
	// aliases and nested locking can invalidate capture's ownership of the data.
	mutexReferences, finishes := 0, 0
	lockChanged := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			if sdkSelector(value, receiver, "mu") {
				mutexReferences++
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "finish" && sdkSelector(selector.X, receiver, "context") {
				finishes++
			}
		case *ast.AssignStmt:
			for _, lhs := range value.Lhs {
				if sdkTypeName(lhs, receiver) || sdkSelector(lhs, receiver, "context") {
					lockChanged = true
				}
			}
			for _, rhs := range value.Rhs {
				if sdkTypeName(rhs, receiver) {
					lockChanged = true
				}
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				if name.Name == receiver {
					lockChanged = true
				}
			}
			for _, rhs := range value.Values {
				if sdkTypeName(rhs, receiver) {
					lockChanged = true
				}
			}
		}
		return true
	})
	if lock == nil || unlock == nil || mutexReferences != 2 || lockIndex >= unlockIndex || unlockIndex >= finishIndex || lockChanged {
		return sdkMirrorError("SDK finish lock lifetime changed")
	}
	if anchor == nil || finishes != 1 {
		return sdkMirrorError("SDK finish bookkeeping missing or ambiguous")
	}
	// Defers run in reverse registration order: capture -> SDK unlock -> delivery.
	// Set the marker only after bookkeeping completes, so early returns or a
	// panic before completion never turn an unfinished SDK span into a CI copy.
	insert(fn.Body.Lbrace+1, "\nvar __ddtestDelivery any; var __ddtestFinished bool; defer func(){ __ddtestMirrorDeliver(__ddtestDelivery) }()\n")
	insert(unlock.End(), fmt.Sprintf("\ndefer func(){ if __ddtestFinished && %[1]s.context.__ddtestMirror != nil { __ddtestDelivery = __ddtestMirrorCapture(%[1]s.context.__ddtestMirror,%[1]s.name,%[1]s.service,%[1]s.resource,%[1]s.spanType,%[1]s.start,%[1]s.duration,%[1]s.error,%[1]s.meta.All(),%[1]s.metrics,%[1]s.context.TraceIDBytes(),%[1]s.spanID) } }()\n", receiver))
	insert(anchor.End(), "\n__ddtestFinished = true\n")
	return nil
}

func sdkLockCall(call *ast.CallExpr, receiver, method string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && len(call.Args) == 0 && selector.Sel.Name == method && sdkSelector(selector.X, receiver, "mu")
}
func sdkFinishCall(call *ast.CallExpr, receiver string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "finish" && sdkSelector(selector.X, receiver, "context") && len(call.Args) == 1 && sdkTypeName(call.Args[0], receiver)
}

func rewriteSDKContext(file *ast.File, fn *ast.FuncDecl, insert func(token.Pos, string)) error {
	sig := fn.Type
	if sig.TypeParams != nil || sig.Params == nil || len(sig.Params.List) != 2 || sig.Params.NumFields() != 2 || len(sig.Params.List[0].Names) != 1 || len(sig.Params.List[1].Names) != 1 || !sdkPointerType(sig.Params.List[1].Type, "Span") || !sdkContextType(file, sig.Params.List[0].Type) || sig.Results == nil || len(sig.Results.List) != 1 || !sdkContextType(file, sig.Results.List[0].Type) {
		return sdkMirrorError("SDK ContextWithSpan signature changed")
	}
	ctx, span := sig.Params.List[0].Names[0].Name, sig.Params.List[1].Names[0].Name
	count := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if literal, ok := node.(*ast.CompositeLit); ok && sdkTypeName(literal.Type, "spanCtx") {
			count++
		}
		return true
	})
	var anchor ast.Stmt
	snapshot := ""
	for _, stmt := range fn.Body.List {
		for _, binding := range sdkBindings(stmt) {
			unary, ok := binding.value.(*ast.UnaryExpr)
			if !ok || unary.Op != token.AND {
				continue
			}
			literal, ok := unary.X.(*ast.CompositeLit)
			if !ok || !sdkTypeName(literal.Type, "spanCtx") {
				continue
			}
			fields := make(map[string]ast.Expr, len(literal.Elts))
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					return sdkMirrorError("SDK context fields changed")
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok || fields[key.Name] != nil {
					return sdkMirrorError("SDK context fields ambiguous")
				}
				fields[key.Name] = pair.Value
			}
			value, ok := fields["snapshot"].(*ast.Ident)
			if !ok || value.Name == "nil" || !sdkTypeName(fields["Context"], ctx) || !sdkTypeName(fields["span"], span) {
				return sdkMirrorError("SDK context construction changed")
			}
			snapshot = value.Name
			anchor = stmt
		}
	}
	if count != 1 || anchor == nil {
		return sdkMirrorError("SDK context return missing or ambiguous")
	}
	if !sdkSnapshot(fn, snapshot, span, anchor.Pos()) {
		return sdkMirrorError("SDK context snapshot changed")
	}
	insert(anchor.Pos(), fmt.Sprintf("var __ddtestState any; if %[1]s != nil { __ddtestState = %[1]s.__ddtestMirror }; %[2]s = __ddtestMirrorContext(%[2]s,__ddtestState)\n", snapshot, ctx))
	return nil
}

// A snapshot must start nil and be populated once, from this span, under its
// nil guard. Otherwise detaching or pooling could carry a foreign test identity.
func sdkSnapshot(fn *ast.FuncDecl, name, span string, before token.Pos) bool {
	declared, guarded := false, false
	for _, stmt := range fn.Body.List {
		if stmt.Pos() >= before {
			break
		}
		if declaration, ok := stmt.(*ast.DeclStmt); ok {
			group, ok := declaration.Decl.(*ast.GenDecl)
			if ok && group.Tok == token.VAR {
				for _, spec := range group.Specs {
					value := spec.(*ast.ValueSpec)
					if len(value.Names) == 1 && value.Names[0].Name == name && sdkPointerType(value.Type, "SpanContext") && len(value.Values) == 0 {
						declared = true
					}
				}
			}
		}
		branch, ok := stmt.(*ast.IfStmt)
		if !ok || !declared || branch.Init != nil || branch.Else != nil {
			continue
		}
		cond, ok := branch.Cond.(*ast.BinaryExpr)
		if !ok || cond.Op != token.NEQ || !(sdkTypeName(cond.X, span) && sdkTypeName(cond.Y, "nil") || sdkTypeName(cond.Y, span) && sdkTypeName(cond.X, "nil")) {
			continue
		}
		for _, body := range branch.Body.List {
			assign, ok := body.(*ast.AssignStmt)
			if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || !sdkTypeName(assign.Lhs[0], name) {
				continue
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if ok && len(call.Args) == 0 && sdkSelector(call.Fun, span, "Context") {
				guarded = true
			}
		}
	}
	writes := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, lhs := range assign.Lhs {
				if sdkTypeName(lhs, name) {
					writes++
				}
			}
		}
		return true
	})
	return declared && guarded && writes == 1
}

func sdkContextType(file *ast.File, expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "context" {
			continue
		}
		alias := "context"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		return sdkTypeName(selector.X, alias)
	}
	return false
}

// Consider only declarations in the function's outer scope, visible at the
// insertion site. Nested declarations may shadow names but cannot supply roles.
func sdkTypedLocal(fn *ast.FuncDecl, name string, before token.Pos, match func(ast.Expr) bool) bool {
	for _, param := range fn.Type.Params.List {
		for _, id := range param.Names {
			if id.Name == name {
				return match(param.Type)
			}
		}
	}
	for _, stmt := range fn.Body.List {
		if stmt.Pos() >= before {
			break
		}
		declaration, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		group, ok := declaration.Decl.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			for _, id := range value.Names {
				if id.Name == name {
					return match(value.Type)
				}
			}
		}
	}
	return false
}

type sdkBinding struct {
	name  string
	value ast.Expr
}

// Both short declarations/assignments and var declarations can construct the
// same SDK value. Multi-result assignments are deliberately not inferred.
func sdkBindings(stmt ast.Stmt) []sdkBinding {
	var bindings []sdkBinding
	switch value := stmt.(type) {
	case *ast.AssignStmt:
		if len(value.Lhs) != len(value.Rhs) {
			break
		}
		for index, lhs := range value.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				bindings = append(bindings, sdkBinding{id.Name, value.Rhs[index]})
			}
		}
	case *ast.DeclStmt:
		group, ok := value.Decl.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			break
		}
		for _, spec := range group.Specs {
			entry := spec.(*ast.ValueSpec)
			if len(entry.Names) != len(entry.Values) {
				continue
			}
			for index, id := range entry.Names {
				bindings = append(bindings, sdkBinding{id.Name, entry.Values[index]})
			}
		}
	}
	return bindings
}
