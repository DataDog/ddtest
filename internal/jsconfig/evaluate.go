// Package jsconfig statically analyzes discovery configuration. It never runs
// JavaScript. Unsupported syntax or effects return an error so callers can use
// framework-native discovery instead of trusting an incomplete file set.
package jsconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/js"
)

type object = map[string]any
type builtin string
type undefined struct{}
type namespace object
type function struct {
	params js.Params
	body   js.BlockStmt
	scope  *scope
}

type evaluator struct {
	ctx     context.Context
	cwd     string
	env     map[string]string
	loading map[string]bool
	steps   int
	depth   int
}

type scope struct {
	e       *evaluator
	file    string
	vars    object
	exports object
	result  any
	module  bool
}

func newEvaluator(ctx context.Context, cwd string, env map[string]string) *evaluator {
	return &evaluator{ctx: ctx, cwd: cwd, env: env, loading: map[string]bool{}}
}

func (e *evaluator) tick() error {
	e.steps++
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if e.steps > 10000 || e.depth > 64 {
		return fmt.Errorf("static configuration analysis exceeded its complexity limit")
	}
	return nil
}

func (e *evaluator) load(filename string) (any, error) {
	v, err := e.loadModule(filename)
	if err != nil {
		return nil, err
	}
	if ns, ok := v.(namespace); ok {
		v = ns["default"]
	}
	if f, ok := v.(function); ok {
		return f.scope.callFunction(f, nil)
	}
	return v, nil
}

func (e *evaluator) loadModule(filename string) (any, error) {
	if err := e.tick(); err != nil {
		return nil, err
	}
	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if e.loading[filename] {
		return nil, fmt.Errorf("cyclic config import: %s", filename)
	}
	e.loading[filename] = true
	defer delete(e.loading, filename)
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("config exceeds 1 MiB: %s", filename)
	}
	if filepath.Ext(filename) == ".json" {
		var value any
		err := json.Unmarshal(data, &value)
		return value, err
	}
	loader := api.LoaderJS
	if ext := filepath.Ext(filename); ext == ".ts" || ext == ".cts" || ext == ".mts" {
		loader = api.LoaderTS
	}
	// Transform only syntax (including TS types and string escapes). No bundling,
	// plugins, filesystem loaders, tree shaking, or JavaScript execution.
	transformed := api.Transform(string(data), api.TransformOptions{
		Loader: loader, Sourcefile: filename, Target: api.ESNext,
		Charset: api.CharsetUTF8, TreeShaking: api.TreeShakingFalse,
		TsconfigRaw: `{"compilerOptions":{"verbatimModuleSyntax":true}}`,
	})
	if len(transformed.Errors) != 0 {
		return nil, fmt.Errorf("%s: %s", filename, transformed.Errors[0].Text)
	}
	ast, err := js.Parse(parse.NewInputBytes(transformed.Code), js.Options{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	s := &scope{e: e, file: filename, vars: object{}, exports: object{}, result: undefined{}}
	s.vars["module"] = object{"exports": s.exports}
	s.vars["exports"] = s.exports
	s.vars["__dirname"] = filepath.Dir(filename)
	s.vars["__filename"] = filename
	env := object{}
	for _, entry := range os.Environ() {
		k, v, _ := strings.Cut(entry, "=")
		env[k] = v
	}
	for k, v := range e.env {
		env[k] = v
	}
	if _, set := env["NODE_ENV"]; !set {
		env["NODE_ENV"] = "test"
	}
	s.vars["process"] = object{"env": env, "cwd": builtin("cwd")}
	s.vars["require"] = builtin("require")
	s.vars["Object"] = object{"assign": builtin("assign"), "freeze": builtin("identity")}
	_, _, err = s.statements(ast.List)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	if s.module {
		if _, missing := s.result.(undefined); !missing {
			s.exports["default"] = s.result
		}
		return namespace(s.exports), nil
	}
	return s.vars["module"].(object)["exports"], nil
}

func (s *scope) statements(statements []js.IStmt) (any, bool, error) {
	for _, stmt := range statements {
		value, returned, err := s.statement(stmt)
		if err != nil || returned {
			return value, returned, err
		}
	}
	return undefined{}, false, nil
}

func (s *scope) statement(stmt js.IStmt) (any, bool, error) {
	if err := s.e.tick(); err != nil {
		return nil, false, err
	}
	switch n := stmt.(type) {
	case *js.EmptyStmt, *js.DirectivePrologueStmt:
		return nil, false, nil
	case *js.VarDecl:
		for _, b := range n.List {
			v, err := s.expr(b.Default)
			if err != nil {
				return nil, false, err
			}
			if err := s.bind(b.Binding, v); err != nil {
				return nil, false, err
			}
		}
	case *js.FuncDecl:
		if n.Generator {
			return nil, false, fmt.Errorf("generator in config")
		}
		s.vars[string(n.Name.Data)] = function{n.Params, n.Body, s}
	case *js.ExprStmt:
		if assign, ok := n.Value.(*js.BinaryExpr); ok && assign.Op == js.EqToken {
			v, err := s.expr(assign.Y)
			if err != nil {
				return nil, false, err
			}
			// Only the module export assignment is accepted. Arbitrary mutations,
			// including aliases and array pushes, must use native discovery.
			if dot, ok := assign.X.(*js.DotExpr); ok {
				if x, ok := dot.X.(*js.Var); ok && string(x.Data) == "module" && string(dot.Y.Data) == "exports" {
					s.vars["module"].(object)["exports"] = v
					return nil, false, nil
				}
			}
		}
		return nil, false, fmt.Errorf("unsupported config statement: %T", n.Value)
	case *js.ReturnStmt:
		v, err := s.expr(n.Value)
		return v, true, err
	case *js.BlockStmt:
		// Reject shadowing rather than approximate JavaScript block scoping.
		child := *s
		child.vars = maps.Clone(s.vars)
		return child.statements(n.List)
	case *js.IfStmt:
		v, err := s.expr(n.Cond)
		if err != nil {
			return nil, false, err
		}
		if truthy(v) {
			return s.statement(n.Body)
		}
		if n.Else != nil {
			return s.statement(n.Else)
		}
	case *js.ImportStmt:
		module, err := decodeString(n.Module)
		if err != nil {
			return nil, false, err
		}
		v, err := s.importModule(module)
		if err != nil {
			return nil, false, err
		}
		if len(n.Default) != 0 {
			defaultValue := v
			if ns, ok := v.(namespace); ok {
				defaultValue = ns["default"]
			}
			s.vars[string(n.Default)] = defaultValue
		}
		for _, alias := range n.List {
			key := string(alias.Name)
			if key == "" {
				key = string(alias.Binding)
			}
			item := v
			if key != "*" {
				item, err = property(v, key)
				if err != nil {
					return nil, false, err
				}
			}
			s.vars[string(alias.Binding)] = item
		}
	case *js.ExportStmt:
		s.module = true
		if n.Default {
			v, err := s.expr(n.Decl)
			s.result = v
			return nil, false, err
		}
		if declaration, ok := n.Decl.(*js.VarDecl); ok {
			before := maps.Clone(s.vars)
			if _, _, err := s.statement(declaration); err != nil {
				return nil, false, err
			}
			for name, value := range s.vars {
				if _, existed := before[name]; !existed {
					s.exports[name] = value
				}
			}
			return nil, false, nil
		}
		if n.Module != nil || n.Decl != nil {
			return nil, false, fmt.Errorf("unsupported re-export or export declaration")
		}
		for _, alias := range n.List {
			name := string(alias.Name)
			if name == "" {
				name = string(alias.Binding)
			}
			v, ok := s.vars[name]
			if !ok {
				return nil, false, fmt.Errorf("unknown export %s", name)
			}
			s.exports[string(alias.Binding)] = v
			if string(alias.Binding) == "default" {
				s.result = v
			}
		}
	default:
		return nil, false, fmt.Errorf("unsupported config statement: %T", stmt)
	}
	return nil, false, nil
}

func (s *scope) bind(binding js.IBinding, value any) error {
	switch b := binding.(type) {
	case *js.Var:
		name := string(b.Data)
		if _, exists := s.vars[name]; exists {
			return fmt.Errorf("config variable redeclaration: %s", name)
		}
		s.vars[name] = value
	case *js.BindingObject:
		if b.Rest != nil {
			return fmt.Errorf("object rest binding is unsupported")
		}
		for _, item := range b.List {
			if item.Key == nil {
				return fmt.Errorf("unsupported binding")
			}
			key, err := s.key(item.Key)
			if err != nil {
				return err
			}
			v, err := property(value, key)
			if err != nil {
				return err
			}
			if _, missing := v.(undefined); missing && item.Value.Default != nil {
				v, err = s.expr(item.Value.Default)
				if err != nil {
					return err
				}
			}
			if err := s.bind(item.Value.Binding, v); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported config binding: %T", binding)
	}
	return nil
}

func (s *scope) key(name *js.PropertyName) (string, error) {
	if name.Computed != nil {
		v, err := s.expr(name.Computed)
		if err != nil {
			return "", err
		}
		return stringValue(v)
	}
	if name.Literal.TokenType == js.StringToken {
		return decodeString(name.Literal.Data)
	}
	return string(name.Literal.Data), nil
}

func (s *scope) expr(expr js.IExpr) (any, error) {
	if err := s.e.tick(); err != nil {
		return nil, err
	}
	if expr == nil {
		return undefined{}, nil
	}
	switch n := expr.(type) {
	case *js.Var:
		if string(n.Data) == "undefined" {
			return undefined{}, nil
		}
		v, ok := s.vars[string(n.Data)]
		if !ok {
			return nil, fmt.Errorf("unknown config variable %s", n.Data)
		}
		return v, nil
	case *js.LiteralExpr:
		switch n.TokenType {
		case js.StringToken:
			return decodeString(n.Data)
		case js.TrueToken:
			return true, nil
		case js.FalseToken:
			return false, nil
		case js.NullToken:
			return nil, nil
		case js.IntegerToken, js.DecimalToken:
			return strconv.ParseFloat(string(n.Data), 64)
		}
	case *js.GroupExpr:
		return s.expr(n.X)
	case *js.ObjectExpr:
		result := object{}
		for _, item := range n.List {
			v, err := s.expr(item.Value)
			if err != nil {
				return nil, err
			}
			if item.Spread {
				other, ok := v.(object)
				if !ok {
					return nil, fmt.Errorf("unknown object spread")
				}
				maps.Copy(result, other)
			} else {
				if item.Name == nil || item.Init != nil {
					return nil, fmt.Errorf("unsupported object property")
				}
				key, err := s.key(item.Name)
				if err != nil {
					return nil, err
				}
				if key == "__proto__" {
					return nil, fmt.Errorf("object prototype configuration needs native discovery")
				}
				result[key] = v
			}
		}
		return result, nil
	case *js.ArrayExpr:
		result := []any{}
		for _, item := range n.List {
			v, err := s.expr(item.Value)
			if err != nil {
				return nil, err
			}
			if item.Spread {
				values, ok := v.([]any)
				if !ok {
					return nil, fmt.Errorf("unknown array spread")
				}
				result = append(result, values...)
			} else {
				result = append(result, v)
			}
		}
		return result, nil
	case *js.DotExpr:
		v, err := s.expr(n.X)
		if err != nil {
			return nil, err
		}
		return property(v, string(n.Y.Data))
	case *js.IndexExpr:
		v, err := s.expr(n.X)
		if err != nil {
			return nil, err
		}
		key, err := s.expr(n.Y)
		if err != nil {
			return nil, err
		}
		k, err := stringValue(key)
		if err != nil {
			return nil, err
		}
		return property(v, k)
	case *js.CondExpr:
		v, err := s.expr(n.Cond)
		if err != nil {
			return nil, err
		}
		if truthy(v) {
			return s.expr(n.X)
		}
		return s.expr(n.Y)
	case *js.BinaryExpr:
		x, err := s.expr(n.X)
		if err != nil {
			return nil, err
		}
		if n.Op == js.OrToken && truthy(x) || n.Op == js.AndToken && !truthy(x) {
			return x, nil
		}
		if n.Op == js.NullishToken {
			if _, missing := x.(undefined); x != nil && !missing {
				return x, nil
			}
		}
		y, err := s.expr(n.Y)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case js.OrToken, js.AndToken, js.NullishToken:
			return y, nil
		case js.AddToken:
			xs, xok := x.(string)
			ys, yok := y.(string)
			if xok && yok {
				return xs + ys, nil
			}
		case js.EqEqEqToken, js.NotEqEqToken:
			equal, err := scalarEqual(x, y)
			if err != nil {
				return nil, err
			}
			if n.Op == js.NotEqEqToken {
				equal = !equal
			}
			return equal, nil
		}
	case *js.UnaryExpr:
		v, err := s.expr(n.X)
		if err != nil {
			return nil, err
		}
		if n.Op == js.NotToken {
			return !truthy(v), nil
		}
	case *js.ArrowFunc:
		return function{n.Params, n.Body, s}, nil
	case *js.FuncDecl:
		if !n.Generator {
			return function{n.Params, n.Body, s}, nil
		}
	case *js.CallExpr:
		callee, err := s.expr(n.X)
		if err != nil {
			return nil, err
		}
		args := []any{}
		for _, arg := range n.Args.List {
			v, err := s.expr(arg.Value)
			if err != nil {
				return nil, err
			}
			if arg.Rest {
				values, ok := v.([]any)
				if !ok {
					return nil, fmt.Errorf("unknown call spread")
				}
				args = append(args, values...)
			} else {
				args = append(args, v)
			}
		}
		if f, ok := callee.(function); ok {
			return s.callFunction(f, args)
		}
		if b, ok := callee.(builtin); ok {
			return s.callBuiltin(b, args)
		}
		return nil, fmt.Errorf("unknown config function")
	}
	return nil, fmt.Errorf("unsupported config expression: %T", expr)
}

func (s *scope) callFunction(f function, args []any) (any, error) {
	s.e.depth++
	defer func() { s.e.depth-- }()
	if err := s.e.tick(); err != nil {
		return nil, err
	}
	if f.params.Rest != nil {
		return nil, fmt.Errorf("rest parameters are unsupported")
	}
	child := *f.scope
	child.vars = maps.Clone(f.scope.vars)
	for i, p := range f.params.List {
		var v any = undefined{}
		if i < len(args) {
			v = args[i]
		} else if p.Default != nil {
			var err error
			v, err = child.expr(p.Default)
			if err != nil {
				return nil, err
			}
		}
		if err := child.bind(p.Binding, v); err != nil {
			return nil, err
		}
	}
	v, _, err := child.statements(f.body.List)
	return v, err
}

func property(value any, key string) (any, error) {
	if ns, ok := value.(namespace); ok {
		v, found := ns[key]
		if !found {
			return nil, fmt.Errorf("module has no export %s", key)
		}
		return v, nil
	}
	if b, ok := value.(builtin); ok && b == "require" && key == "resolve" {
		return builtin("resolveModule"), nil
	}
	if m, ok := value.(object); ok {
		if v, exists := m[key]; exists {
			return v, nil
		}
		return undefined{}, nil
	}
	return nil, fmt.Errorf("cannot statically read property %q", key)
}

func (s *scope) importModule(name string) (any, error) {
	if name == "path" || name == "node:path" {
		return object{"join": builtin("path.join"), "resolve": builtin("path.resolve"), "dirname": builtin("path.dirname"), "sep": string(filepath.Separator)}, nil
	}
	if !strings.HasPrefix(name, ".") && !filepath.IsAbs(name) {
		return nil, fmt.Errorf("package import %q needs native discovery", name)
	}
	path, err := resolveModule(filepath.Dir(s.file), name)
	if err != nil {
		return nil, err
	}
	return s.e.loadModule(path)
}

func resolveModule(dir, name string) (string, error) {
	base := name
	if !filepath.IsAbs(base) {
		base = filepath.Join(dir, base)
	}
	for _, suffix := range []string{"", ".js", ".json", ".cjs", ".mjs", ".ts", ".cts", ".mts", "/index.js", "/index.json"} {
		candidate := base + suffix
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot statically resolve config import %q from %s", name, dir)
}

func (s *scope) callBuiltin(b builtin, args []any) (any, error) {
	switch b {
	case "cwd":
		if len(args) == 0 {
			return s.e.cwd, nil
		}
	case "require", "resolveModule":
		if len(args) == 1 {
			name, err := stringValue(args[0])
			if err != nil {
				return nil, err
			}
			if b == "require" {
				return s.importModule(name)
			}
			return resolveModule(filepath.Dir(s.file), name)
		}
	case "identity":
		if len(args) == 1 {
			return args[0], nil
		}
	case "path.join", "path.resolve", "path.dirname":
		parts := []string{}
		for _, arg := range args {
			v, err := stringValue(arg)
			if err != nil {
				return nil, err
			}
			parts = append(parts, v)
		}
		if b == "path.dirname" && len(parts) == 1 {
			return filepath.Dir(parts[0]), nil
		}
		if b == "path.join" {
			if len(parts) == 0 {
				return ".", nil
			}
			joined := filepath.Join(parts...)
			if joined == "" {
				joined = "."
			}
			for i := len(parts) - 1; i >= 0; i-- {
				if parts[i] == "" {
					continue
				}
				if strings.HasSuffix(parts[i], string(filepath.Separator)) && !strings.HasSuffix(joined, string(filepath.Separator)) {
					joined += string(filepath.Separator)
				}
				break
			}
			return joined, nil
		}
		if b == "path.resolve" {
			result := s.e.cwd
			for _, part := range parts {
				if filepath.IsAbs(part) {
					result = part
				} else {
					result = filepath.Join(result, part)
				}
			}
			return result, nil
		}
	case "assign":
		// Object.assign mutates its first argument; accepting only fresh literals
		// would need alias tracking. Object spread is the supported equivalent.
		return nil, fmt.Errorf("object.assign mutation needs native discovery")
	}
	return nil, fmt.Errorf("unsupported call to %s", b)
}

func stringValue(value any) (string, error) {
	v, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected a static string, got %T", value)
	}
	return v, nil
}

func decodeString(data []byte) (string, error) {
	// esbuild emits JSON-compatible double quoted strings. Unrecognized escape
	// sequences are rejected, never approximated.
	var value string
	err := json.Unmarshal(data, &value)
	return value, err
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil, undefined:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return true
	}
}

func scalarEqual(x, y any) (bool, error) {
	switch x.(type) {
	case nil, undefined, bool, string, float64:
	default:
		return false, fmt.Errorf("non-scalar comparison")
	}
	switch y.(type) {
	case nil, undefined, bool, string, float64:
	default:
		return false, fmt.Errorf("non-scalar comparison")
	}
	return x == y, nil
}
