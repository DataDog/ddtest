package instrument

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

const TestifySuiteImport = "github.com/stretchr/testify/suite"

// ErrUnsupportedAPI marks a library entry whose shape this instrumentation does
// not recognize. Preparation then continues without that library integration.
var ErrUnsupportedAPI = errors.New("unsupported library API")

const TestifyRegisterName = "__dd_ci_registerTestifySuite"

// CheckTestifyNames rejects bindings that would capture the inserted hook,
// including declarations in another library file.
func CheckTestifyNames(name string, src []byte) error {
	if !bytes.Contains(src, []byte(TestifyRegisterName)) {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		return err
	}
	return checkTestifyNames(name, src, file)
}

func checkTestifyNames(name string, src []byte, file *ast.File) error {
	if !bytes.Contains(src, []byte(TestifyRegisterName)) {
		return nil
	}
	var err error
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == TestifyRegisterName {
			err = fmt.Errorf("%s: reserved Testify instrumentation name %s", name, id.Name)
			return false
		}
		return true
	})
	return err
}

func applyTestifyEdits(name string, src []byte, edits []edit) ([]byte, error) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out bytes.Buffer
	size := len(src)
	for _, e := range edits {
		size += len(e.text) - (e.end - e.start)
	}
	out.Grow(size)

	cursor := 0
	for _, e := range edits {
		if e.start < cursor {
			return nil, fmt.Errorf("%s: overlapping Testify edits", name)
		}
		out.Write(src[cursor:e.start])
		out.WriteString(e.text)
		cursor = e.end
	}
	out.Write(src[cursor:])
	return out.Bytes(), nil
}

// TestifyAPI validates the library entry without loading a type checker or
// parsing client sources. The package's own compiler still checks its types.
func TestifyAPI(files map[string][]byte) error {
	var parsed []*ast.File
	for name, src := range files {
		file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		parsed = append(parsed, file)
	}
	return validateTestifyAPI(parsed)
}

func validateTestifyAPI(files []*ast.File) error {
	validRun := false
	for _, file := range files {
		testingAlias := ""
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err == nil && path == "testing" {
				testingAlias = "testing"
				if spec.Name != nil {
					testingAlias = spec.Name.Name
				}
			}
		}
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Recv != nil || d.Name.Name != "Run" || d.Type.TypeParams != nil && len(d.Type.TypeParams.List) > 0 || d.Type.Results != nil && len(d.Type.Results.List) > 0 || len(d.Type.Params.List) != 2 {
				continue
			}
			first, ok := d.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := first.X.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || testingAlias == "" || pkg.Name != testingAlias {
				continue
			}
			second, ok := d.Type.Params.List[1].Type.(*ast.Ident)
			validRun = ok && second.Name == "TestingSuite" && len(d.Type.Params.List[0].Names) <= 1 && len(d.Type.Params.List[1].Names) <= 1
		}
	}
	if !validRun {
		return fmt.Errorf("%w: expected Testify suite.Run(*testing.T, TestingSuite)", ErrUnsupportedAPI)
	}
	return nil
}

// SupportsTestifyVersion accepts Testify v1.4.0 and later v1 releases: every
// one runs each suite method as t.Run(method) from Run(*testing.T, TestingSuite).
// Local replacements use their declared version plus TestifyAPI.
func SupportsTestifyVersion(version string) bool {
	if !strings.HasPrefix(version, "v") {
		return false
	}
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "+")
	core, suffix, pre := strings.Cut(core, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	values := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return false
		}
		values[i] = n
	}
	if values[0] != 1 || values[1] < 4 {
		return false
	}
	// A pre-release of the minimum version precedes it.
	return !(values[1] == 4 && values[2] == 0 && pre && suffix != "")
}
