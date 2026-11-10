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
	// expression names the declaration in its own package: a function name or
	// a method expression.
	expression       string
	prevent, noRetry bool
}

// exclusionDeclarations reads the annotations in the compiler-selected files of
// one package, including the caller's overlay, and groups them by the package
// clause of their file. The registration is compiled, so runtime source is
// optional. Only the package's own files are read: annotations in imported
// packages have no effect, which keeps preparation from scanning dependencies.
// An annotation on an unsupported declaration is ignored with a warning rather
// than failing the run.
func exclusionDeclarations(p goPackage, files []string, replacements map[string]string) (map[string][]exclusionDeclaration, []string, error) {
	declarations := map[string][]exclusionDeclaration{}
	var warnings []string
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
			return nil, nil, err
		}
		if !bytes.Contains(data, []byte("//ddtest:")) {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, logical, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil || fn.Body == nil {
				continue
			}
			d := exclusionDeclaration{expression: fn.Name.Name}
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
			if reason := exclusionMethodExpression(fn, &d); reason != "" {
				warnings = append(warnings, fmt.Sprintf("%s: ddtest annotations on %s ignored: %s", fset.Position(fn.Pos()), fn.Name.Name, reason))
				continue
			}
			declarations[file.Name.Name] = append(declarations[file.Name.Name], d)
		}
	}
	return declarations, warnings, nil
}

// exclusionMethodExpression sets d's expression for a method declaration, or
// returns why the declaration cannot be registered. A receiver spelled without
// '*' is a value type or an alias of a pointer type; either way T.Method names
// the method, and the runtime registers its pointer counterpart too.
func exclusionMethodExpression(fn *ast.FuncDecl, d *exclusionDeclaration) string {
	if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
		return "they apply only to an individual Test function or Testify Test method"
	}
	if fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0 {
		return "generic declarations are not supported"
	}
	if fn.Recv == nil {
		return ""
	}
	if len(fn.Recv.List) != 1 {
		return "unsupported receiver"
	}
	receiver := ast.Unparen(fn.Recv.List[0].Type)
	pointer := false
	if star, ok := receiver.(*ast.StarExpr); ok {
		receiver, pointer = ast.Unparen(star.X), true
	}
	switch receiver.(type) {
	case *ast.IndexExpr, *ast.IndexListExpr:
		return "generic declarations are not supported"
	}
	id, ok := receiver.(*ast.Ident)
	if !ok {
		return "unsupported receiver"
	}
	if pointer {
		d.expression = "(*" + id.Name + ")." + fn.Name.Name
	} else {
		d.expression = id.Name + "." + fn.Name.Name
	}
	return ""
}

// exclusionRegistrationSources returns the registration source for each test
// package of p, keyed by package name. runtimeImported reports that the runtime
// imports p: a registration in p itself would then be an import cycle, so the
// annotations in p's own files are ignored with a warning; those of its
// external test package still register.
func exclusionRegistrationSources(p goPackage, replacements map[string]string, runtimePackage string, runtimeImported bool) (map[string]string, []string, error) {
	files := append(append(append(append([]string{}, p.GoFiles...), p.CgoFiles...), p.TestGoFiles...), p.XTestGoFiles...)
	declarations, warnings, err := exclusionDeclarations(p, files, replacements)
	if err != nil {
		return nil, nil, err
	}
	if runtimeImported && len(declarations[p.Name]) > 0 {
		warnings = append(warnings, p.ImportPath+": ddtest annotations ignored: ddtest's runtime imports this package, so only its external test package can register them")
		delete(declarations, p.Name)
	}
	sources := map[string]string{}
	for pkg, entries := range declarations {
		var source strings.Builder
		fmt.Fprintf(&source, "package %s\nimport __dd_ci_runtime %q\nfunc init() {\n", pkg, runtimePackage)
		for _, d := range entries {
			fmt.Fprintf(&source, "__dd_ci_runtime.RegisterTestExclusions(%s, __dd_ci_runtime.TestExclusions{PreventFailureSuppression: %t, NoAutoRetry: %t})\n", d.expression, d.prevent, d.noRetry)
		}
		source.WriteString("}\n")
		sources[pkg] = source.String()
	}
	return sources, warnings, nil
}
