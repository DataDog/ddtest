// Command testnames lists the root module's race-enabled tests without building
// test executables or running package initializers and TestMain.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func isTest(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

// Match cmd/go's syntactic signature check, including aliased and dot imports.
func hasArgument(fn *ast.FuncDecl, kind string) bool {
	if fn.Type.Results.NumFields() != 0 || fn.Type.Params.NumFields() != 1 || fn.Type.TypeParams.NumFields() != 0 {
		return false
	}
	pointer, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch argument := pointer.X.(type) {
	case *ast.Ident:
		return argument.Name == kind
	case *ast.SelectorExpr:
		return argument.Sel.Name == kind
	}
	return false
}

func fileTests(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		if name == "TestMain" && hasArgument(fn, "M") {
			continue
		}
		kind := ""
		switch {
		case isTest(name, "Test"):
			kind = "T"
		case isTest(name, "Fuzz"):
			kind = "F"
		}
		if kind != "" {
			if !hasArgument(fn, kind) {
				return nil, fmt.Errorf("%s: invalid signature for %s", path, name)
			}
			names = append(names, name)
		}
	}
	for _, example := range doc.Examples(file) {
		if example.Output != "" || example.EmptyOutput {
			names = append(names, "Example"+example.Name)
		}
	}
	return names, nil
}

func discover(ctx context.Context, directory string) ([]string, error) {
	// Let Go select files for the current platform, tags, cgo and race mode.
	command := exec.CommandContext(ctx, "go", "list", "-race", "-json", "./...")
	command.Dir = directory
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var names []string
	for {
		var pkg struct {
			Dir                       string
			TestGoFiles, XTestGoFiles []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		for _, path := range append(pkg.TestGoFiles, pkg.XTestGoFiles...) {
			selected, err := fileTests(filepath.Join(pkg.Dir, path))
			if err != nil {
				return nil, err
			}
			names = append(names, selected...)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

func main() {
	names, err := discover(context.Background(), "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, name := range names {
		fmt.Println(name)
	}
}
