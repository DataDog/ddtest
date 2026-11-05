package runner

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type exclusionDeclaration struct {
	expression       string
	receiver         string
	valueReceiver    bool
	genericReceiver  bool
	name             string
	prevent, noRetry bool
}

// exclusionDeclarations reads only the compiler-selected files, including the
// caller's overlay. The registration is compiled, so runtime source is optional.
func exclusionDeclarations(p goPackage, files []string, replacements map[string]string) (map[string][]exclusionDeclaration, error) {
	declarations := map[string][]exclusionDeclaration{}
	files = append([]string(nil), files...)
	sort.Strings(files)
	for _, name := range files {
		logical := filepath.Join(p.Dir, name)
		source := logical
		if replacement, ok := replacements[logical]; ok {
			source = replacement
		}
		if source == "" {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(data, []byte("//ddtest:")) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), logical, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil || fn.Body == nil {
				continue
			}
			d := exclusionDeclaration{name: fn.Name.Name, expression: fn.Name.Name}
			for _, comment := range fn.Doc.List {
				switch strings.TrimSpace(comment.Text) {
				case "//ddtest:prevent_failure_suppression":
					d.prevent = true
				case "//ddtest:no_auto_retry":
					d.noRetry = true
				}
			}
			if !d.prevent && !d.noRetry {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				return nil, fmt.Errorf("%s: ddtest exclusions require an individual Test function or Testify Test method", logical)
			}
			if fn.Recv != nil {
				if len(fn.Recv.List) != 1 {
					return nil, fmt.Errorf("%s: unsupported annotated test receiver", logical)
				}
				receiver := ast.Unparen(fn.Recv.List[0].Type)
				if star, ok := receiver.(*ast.StarExpr); ok {
					receiver = ast.Unparen(star.X)
				} else {
					d.valueReceiver = true
				}
				switch indexed := receiver.(type) {
				case *ast.IndexExpr:
					receiver = ast.Unparen(indexed.X)
					d.genericReceiver = true
				case *ast.IndexListExpr:
					receiver = ast.Unparen(indexed.X)
					d.genericReceiver = true
				}
				id, ok := receiver.(*ast.Ident)
				if !ok {
					return nil, fmt.Errorf("%s: unsupported annotated test receiver", logical)
				}
				d.receiver = id.Name
				d.expression = "(*" + id.Name + ")." + fn.Name.Name
			}
			declarations[file.Name.Name] = append(declarations[file.Name.Name], d)
		}
	}
	return declarations, nil
}

// go list separates active import-C sources from ordinary Go source files.
func exclusionPackageFiles(p goPackage) []string {
	return append(append([]string(nil), p.GoFiles...), p.CgoFiles...)
}

func exclusionRegistrationSources(p goPackage, replacements map[string]string, runtimePackage string) (map[string]string, error) {
	return exclusionRegistrationSourcesForBoundary(p, replacements, runtimePackage, false)
}

func exclusionRegistrationSourcesForBoundary(p goPackage, replacements map[string]string, runtimePackage string, external bool) (map[string]string, error) {
	files := append(append(append([]string{}, exclusionPackageFiles(p)...), p.TestGoFiles...), p.XTestGoFiles...)
	declarations, err := exclusionDeclarations(p, files, replacements)
	if err != nil {
		return nil, err
	}
	var aliases map[string]map[string]exclusionReceiverAlias
	callsByPackage := map[string][]string{}
	packages := make([]string, 0, len(declarations))
	for pkg := range declarations {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	for _, pkg := range packages {
		entries := declarations[pkg]
		destination := pkg
		if external && pkg == p.Name {
			destination = p.Name + "_test"
		}
		for _, d := range entries {
			if d.receiver != "" {
				if aliases == nil {
					aliases, err = exclusionReceiverAliases(p, files, replacements)
					if err != nil {
						return nil, err
					}
				}
				d = resolveExclusionReceiver(d, aliases[pkg])
				d.expression = "(*" + d.receiver + ")." + d.name
			}
			if external && pkg == p.Name || d.genericReceiver {
				declarationPath := p.ImportPath
				if pkg != p.Name {
					declarationPath += "_test"
				}
				for _, name := range exclusionRuntimeNames(declarationPath, d) {
					callsByPackage[destination] = append(callsByPackage[destination], fmt.Sprintf("__dd_ci_runtime.RegisterTestExclusionsByName(%q, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: %t, NoAutoRetry: %t})", name, d.prevent, d.noRetry))
				}
			} else {
				callsByPackage[destination] = append(callsByPackage[destination], fmt.Sprintf("__dd_ci_runtime.RegisterTestExclusions(%s, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: %t, NoAutoRetry: %t})", d.expression, d.prevent, d.noRetry))
			}
		}
	}
	sources := map[string]string{}
	for pkg, calls := range callsByPackage {
		sources[pkg] = "package " + pkg + "\nimport __dd_ci_runtime " + fmt.Sprintf("%q", runtimePackage) + "\nfunc init() {\n" + strings.Join(calls, "\n") + "\n}\n"
	}
	return sources, nil
}

// The compiler escapes special bytes and dots in the final import-path segment
// before using an import path as a runtime function-name prefix.
func exclusionRuntimePrefix(path string) string {
	lastSlash := strings.LastIndexByte(path, '/')
	var prefix strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c <= ' ' || c == '.' && i > lastSlash || c == '%' || c == '"' || c >= 0x7f {
			fmt.Fprintf(&prefix, "%%%02x", c)
		} else {
			prefix.WriteByte(c)
		}
	}
	return prefix.String()
}

func exclusionRuntimeNames(path string, d exclusionDeclaration) []string {
	prefix := exclusionRuntimePrefix(path)
	if d.genericReceiver {
		d.receiver += "[...]"
	}
	if d.receiver == "" {
		return []string{prefix + "." + d.name}
	}
	names := []string{prefix + ".(*" + d.receiver + ")." + d.name}
	if d.valueReceiver {
		names = append(names, prefix+"."+d.receiver+"."+d.name)
	}
	return names
}

// Receiver aliases use the defined type's runtime symbol, even when a method
// spells its receiver through a package-local alias from another selected file.
// Defined types (without '=') retain their independent identities.
type exclusionReceiverAlias struct {
	name    string
	pointer bool
}

func resolveExclusionReceiver(d exclusionDeclaration, aliases map[string]exclusionReceiverAlias) exclusionDeclaration {
	seen := map[string]bool{}
	for aliases[d.receiver].name != "" && !seen[d.receiver] {
		seen[d.receiver] = true
		alias := aliases[d.receiver]
		d.receiver = alias.name
		if alias.pointer {
			d.valueReceiver = false
		}
	}
	return d
}

func exclusionReceiverAliases(p goPackage, files []string, replacements map[string]string) (map[string]map[string]exclusionReceiverAlias, error) {
	aliases := map[string]map[string]exclusionReceiverAlias{}
	for _, name := range files {
		logical := filepath.Join(p.Dir, name)
		source := logical
		if replacement, ok := replacements[logical]; ok {
			source = replacement
		}
		if source == "" {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), logical, data, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if aliases[file.Name.Name] == nil {
			aliases[file.Name.Name] = map[string]exclusionReceiverAlias{}
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, spec := range group.Specs {
				typ := spec.(*ast.TypeSpec)
				if !typ.Assign.IsValid() {
					continue
				}
				targetType := ast.Unparen(typ.Type)
				alias := exclusionReceiverAlias{}
				if star, ok := targetType.(*ast.StarExpr); ok {
					targetType = ast.Unparen(star.X)
					alias.pointer = true
				}
				if target, ok := targetType.(*ast.Ident); ok {
					alias.name = target.Name
					aliases[file.Name.Name][typ.Name.Name] = alias
				}
			}
		}
	}
	return aliases, nil
}

// Imported suites are ordinary packages: _test.go registrations in the defining
// package would never join the consumer's binary. Register their compiled names
// in the consumer instead, without editing cached modules or importing Mini into
// dependencies that Mini already imports.
func exclusionDependencyRegistrations(root goPackage, graph packageGraph, replacements map[string]string, excluded map[string]bool) (string, error) {
	pending := append(append(append([]string{}, root.Imports...), root.TestImports...), root.XTestImports...)
	reached := map[string]bool{}
	var paths []string
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reached[path] || excluded[path] {
			continue
		}
		reached[path] = true
		p := graph[path]
		if p == nil || p.Standard || p.Module == nil || p.Error != nil || p.ownRuntime || p.ImportPath == miniPackage || strings.HasPrefix(p.ImportPath, miniModule+"/") {
			continue
		}
		paths = append(paths, path)
		pending = append(pending, p.Imports...)
	}
	sort.Strings(paths)
	var calls []string
	for _, path := range paths {
		p := graph[path]
		declarations, err := exclusionDeclarations(*p, exclusionPackageFiles(*p), replacements)
		if err != nil {
			return "", err
		}
		var aliases map[string]map[string]exclusionReceiverAlias
		for _, d := range declarations[p.Name] {
			if d.receiver != "" {
				aliases, err = exclusionReceiverAliases(*p, exclusionPackageFiles(*p), replacements)
				if err != nil {
					return "", err
				}
				break
			}
		}
		for _, d := range declarations[p.Name] {
			d = resolveExclusionReceiver(d, aliases[p.Name])
			names := exclusionRuntimeNames(path, d)
			for _, name := range names {
				calls = append(calls, fmt.Sprintf("__dd_ci_runtime.RegisterTestExclusionsByName(%q, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: %t, NoAutoRetry: %t})", name, d.prevent, d.noRetry))
			}
		}
	}
	if len(calls) == 0 {
		return "", nil
	}
	return "func init() {\n" + strings.Join(calls, "\n") + "\n}\n", nil
}

// Extend the native package graph only for test imports not already listed.
// Dependency package tests are deliberately excluded; only active GoFiles and CgoFiles join
// an import, and this query preserves the caller's module/build/overlay flags.
func exclusionDependencyGraph(packages []goPackage, graph packageGraph, list func(...string) ([]goPackage, error)) (packageGraph, error) {
	missing := map[string]bool{}
	for _, p := range packages {
		if p.ImportPath == miniPackage || p.ImportPath == sdkPackage || p.Standard || p.ownRuntime {
			continue
		}
		for _, imports := range [][]string{p.TestImports, p.XTestImports} {
			for _, path := range imports {
				if graph[path] == nil {
					missing[path] = true
				}
			}
		}
	}
	if len(missing) == 0 {
		return graph, nil
	}
	paths := make([]string, 0, len(missing))
	for path := range missing {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	dependencies, err := list(append([]string{"-deps"}, paths...)...)
	if err != nil {
		return nil, err
	}
	extended := make(packageGraph, len(graph)+len(dependencies))
	for path, p := range graph {
		extended[path] = p
	}
	for i := range dependencies {
		p := &dependencies[i]
		if extended[p.ImportPath] == nil {
			extended[p.ImportPath] = p
		}
	}
	return extended, nil
}
