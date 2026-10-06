package instrument

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// TransformTestifyEntry registers the suite at its original Run entry. Keeping
// the library runner intact also covers callers in other modules. The same edit
// works on Go's covered source without introducing a coverage counter.
func TransformTestifyEntry(name string, src []byte) ([]byte, bool, error) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, err
	}
	return transformTestifyEntry(name, src, fs, file)
}

// TransformTestifyPackage validates and rewrites the selected library using one
// AST per file. ASTs live only for this preparation; covered compiler inputs are
// parsed independently by TransformTestifyEntry.
func TransformTestifyPackage(files map[string][]byte) (map[string][]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	output := map[string][]byte{}
	for _, name := range names {
		src := files[name]
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if err := checkTestifyNames(name, src, file); err != nil {
			return nil, err
		}
		out, changed, err := transformTestifyEntry(name, src, fs, file)
		if err != nil {
			return nil, err
		}
		if changed {
			output[name] = out
		}
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("%w: expected Testify suite.Run(*testing.T, TestingSuite)", ErrUnsupportedAPI)
	}
	return output, nil
}

func transformTestifyEntry(name string, src []byte, fs *token.FileSet, file *ast.File) ([]byte, bool, error) {
	var run *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Run" {
			run = fn
			break
		}
	}
	if run == nil {
		return nil, false, nil
	}
	if err := validateTestifyAPI([]*ast.File{file}); err != nil {
		return nil, false, err
	}
	if err := checkTestifyNames(name, src, file); err != nil {
		return nil, false, err
	}
	if run.Body == nil {
		return nil, false, fmt.Errorf("%s: %w: Testify Run has no body", name, ErrUnsupportedAPI)
	}
	parameters := make([]string, 2)
	for i, field := range run.Type.Params.List {
		if len(field.Names) != 1 || field.Names[0].Name == "_" {
			return nil, false, fmt.Errorf("%s: %w: Testify Run requires named parameters", name, ErrUnsupportedAPI)
		}
		parameters[i] = field.Names[0].Name
	}
	pos := fs.PositionFor(run.Body.Lbrace, false).Offset + 1
	edits := []edit{{pos, pos, TestifyRegisterName + "(" + strings.Join(parameters, ",") + ");"}}
	// Existing //line directives in covered sources already map to the original
	// file. Normal temporary sources need an inline directive with no extra line.
	if fs.Position(file.Package).Filename == name {
		if strings.ContainsAny(name, "\r\n") || strings.Contains(name, "*/") {
			return nil, false, fmt.Errorf("%s: filename cannot be encoded in a Go line directive", name)
		}
		p := fs.PositionFor(file.Package, false)
		edits = append(edits, edit{p.Offset, p.Offset, fmt.Sprintf("/*line %s:%d:1*/", name, p.Line)})
	}
	out, err := applyTestifyEdits(name, src, edits)
	return out, true, err
}

// TestifyEntryHook uses testing, which suite already imports, and an unsafe
// linkname declaration. No CI runtime import is added to the library's graph.
func TestifyEntryHook(hook string) string {
	return "package suite\nimport (\"testing\"; _ \"unsafe\")\n//go:linkname " + TestifyRegisterName + " " + hook + "\nfunc " + TestifyRegisterName + "(*testing.T, interface{})\n"
}

// TestifyCacheMarker must be exported: an unused private constant need not
// change export data. suite's testing dependency then makes the transformation
// part of Go's native action key while the compiler's identity stays native.
func TestifyCacheMarker(fingerprint string) string {
	return "\n// DDTestTestifyContract identifies the suite transformation for Go's build cache.\nconst DDTestTestifyContract = " + strconv.Quote(fingerprint) + "\n"
}
