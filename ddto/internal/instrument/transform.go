// The hook advice follows dd-trace-go v2.11.0-rc.1, Apache-2.0.
package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

type edit struct {
	start, end int
	text       string
}

// Transform validates every required hook before returning rewritten sources.
// Logical filenames are retained in line directives for diagnostics and stacks.
func Transform(files map[string][]byte) (map[string][]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	counts := map[string]int{}
	output := map[string][]byte{}
	for _, name := range names {
		src := files[name]
		if bytes.Contains(src, []byte("__dd_civisibility_")) {
			return nil, fmt.Errorf("%s: already instrumented", name)
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		var edits []edit
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue
			}
			receiver := ""
			if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
				if id, ok := star.X.(*ast.Ident); ok {
					receiver = id.Name
				}
			}
			key := receiver + "." + fn.Name.Name
			prefix := ""
			recv := ""
			if len(fn.Recv.List[0].Names) == 1 {
				recv = fn.Recv.List[0].Names[0].Name
			}
			arg := func(index int) (string, error) {
				var args []string
				for _, field := range fn.Type.Params.List {
					for _, id := range field.Names {
						args = append(args, id.Name)
					}
				}
				if index >= len(args) {
					return "", fmt.Errorf("%s: unsupported arguments of %s", name, key)
				}
				return args[index], nil
			}
			switch key {
			case "M.Run":
				if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
					return nil, fmt.Errorf("unsupported M.Run result")
				}
				result := fn.Type.Results.List[0]
				typ, ok := result.Type.(*ast.Ident)
				if !ok || typ.Name != "int" {
					return nil, fmt.Errorf("unsupported M.Run result type")
				}
				resultName := "__dd_ci_result"
				if len(result.Names) == 1 {
					resultName = result.Names[0].Name
				} else {
					edits = append(edits, edit{fs.Position(fn.Type.Results.Pos()).Offset, fs.Position(fn.Type.Results.End()).Offset, "(" + resultName + " int)"})
				}
				prefix = fmt.Sprintf(`__dd_ci_proceed, __dd_ci_exit := __dd_civisibility_instrumentTestingMWithControl(%s)
if !__dd_ci_proceed { return __dd_ci_exit(1) }
defer func() {
 if __dd_ci_panic := recover(); __dd_ci_panic != nil {
  _ = __dd_ci_exit(__dd_civisibility_instrumentTestingMAbnormalExitCode())
  panic(__dd_ci_panic)
 }
 %s = __dd_ci_exit(%s)
}()`, recv, resultName, resultName)
			case "T.Run":
				f, err := arg(1)
				if err != nil {
					return nil, err
				}
				prefix = f + " = __dd_civisibility_instrumentTestingTFunc(" + f + ")"
			case "B.Run":
				n, err := arg(0)
				if err != nil {
					return nil, err
				}
				f, err := arg(1)
				if err != nil {
					return nil, err
				}
				prefix = fmt.Sprintf("%s, %s = __dd_civisibility_instrumentTestingBFunc(%s, %s, %s)", n, f, recv, n, f)
			case "common.Fail", "common.FailNow":
				prefix = fmt.Sprintf("__dd_civisibility_instrumentSetErrorInfo(%s, %q, %q, 0)", recv, fn.Name.Name, "failed test")
			case "common.SkipNow":
				prefix = "__dd_civisibility_instrumentSkipNow(" + recv + ")"
			case "T.Parallel":
				prefix = "if __dd_civisibility_instrumentTestingParallel(" + recv + ") { return }"
			case "common.Error", "common.Fatal", "common.Skip", "common.Errorf", "common.Fatalf", "common.Skipf":
				callName := "Sprintln"
				if strings.HasSuffix(fn.Name.Name, "f") {
					callName = "Sprintf"
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok || id.Name != "fmt" || sel.Sel.Name != callName {
						return true
					}
					start, end := fs.Position(call.Pos()).Offset, fs.Position(call.End()).Offset
					capture := fmt.Sprintf("__dd_civisibility_instrumentCaptureFormattedError(%s, %s, ", recv, strconv.Quote(fn.Name.Name))
					suffix := ", 0)"
					if strings.HasPrefix(fn.Name.Name, "Skip") {
						capture = fmt.Sprintf("__dd_civisibility_instrumentCaptureFormattedSkip(%s, %s, ", recv, strconv.Quote(fn.Name.Name))
						suffix = ")"
					}
					edits = append(edits, edit{start, end, capture + string(src[start:end]) + suffix})
					counts[key]++
					return false
				})
				continue
			default:
				continue
			}
			if recv == "" {
				return nil, fmt.Errorf("%s: unsupported receiver of %s", name, key)
			}
			counts[key]++
			pos := fs.Position(fn.Body.Lbrace + 1)
			text := "\n" + prefix + "\n//line " + name + ":" + strconv.Itoa(pos.Line) + ":" + strconv.Itoa(pos.Column) + "\n"
			edits = append(edits, edit{pos.Offset, pos.Offset, text})
		}
		if len(edits) == 0 {
			continue
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "//line %s:1\n", name)
		cursor := 0
		for _, e := range edits {
			if e.start < cursor {
				return nil, fmt.Errorf("%s: overlapping edits", name)
			}
			buf.Write(src[cursor:e.start])
			buf.WriteString(e.text)
			cursor = e.end
		}
		buf.Write(src[cursor:])
		output[name] = buf.Bytes()
	}
	required := []string{"M.Run", "T.Run", "B.Run", "common.Fail", "common.FailNow", "common.SkipNow", "T.Parallel", "common.Error", "common.Fatal", "common.Skip", "common.Errorf", "common.Fatalf", "common.Skipf"}
	for _, key := range required {
		if counts[key] == 0 {
			return nil, fmt.Errorf("missing testing hook %s", key)
		}
		if counts[key] != 1 {
			return nil, fmt.Errorf("ambiguous testing hook %s: %d matches", key, counts[key])
		}
	}
	return output, nil
}
