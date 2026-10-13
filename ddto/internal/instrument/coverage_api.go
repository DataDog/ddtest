package instrument

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// coverageHookAPI is every declaration of the toolchain that CoverageHook
// uses, rendered as goDeclarations renders them. A toolchain that lacks one,
// or declares it otherwise, keeps the text profiles.
var coverageHookAPI = map[string][]string{
	"internal/coverage": {
		"type CounterMode uint8",
		"type CounterGranularity uint8",
		"func (CounterMode) String() string",
		"func (CounterGranularity) String() string",
		"field CoverableUnit.StLine uint32", "field CoverableUnit.StCol uint32",
		"field CoverableUnit.EnLine uint32", "field CoverableUnit.EnCol uint32",
		"field CoverableUnit.NxStmts uint32", "field CoverableUnit.Parent uint32",
		"field FuncDesc.Funcname string", "field FuncDesc.Srcfile string",
		"field FuncDesc.Units []CoverableUnit", "field FuncDesc.Lit bool",
		"const NumCtrsOffset", "const PkgIdOffset", "const FuncIdOffset", "const FirstCtrOffset",
		"const CtrModeInvalid", "const CtrModeRegOnly", "const CtrModeTestMain",
	},
	"internal/coverage/rtcov": {
		"field CovMetaBlob.P *byte", "field CovMetaBlob.Len uint32",
		"field CovMetaBlob.CounterMode uint8", "field CovMetaBlob.CounterGranularity uint8",
		"field CovCounterBlob.Counters *uint32", "field CovCounterBlob.Len uint64",
		"field Meta.List []CovMetaBlob",
	},
	"internal/coverage/decodemeta": {
		"func NewCoverageMetaDataDecoder([]byte, bool) (*CoverageMetaDataDecoder, error)",
		"func (*CoverageMetaDataDecoder) PackagePath() string",
		"func (*CoverageMetaDataDecoder) NumFuncs() uint32",
		"func (*CoverageMetaDataDecoder) ReadFunc(uint32, *coverage.FuncDesc) error",
	},
	"internal/coverage/cformat": {
		"func NewFormatter(coverage.CounterMode) *Formatter",
		"func (*Formatter) SetPackage(string)",
		"func (*Formatter) AddUnit(string, string, bool, coverage.CoverableUnit, uint32)",
		"func (*Formatter) EmitTextual([]string, io.Writer) error",
	},
	"runtime": {
		"linkname coverage_getCovCounterList internal/coverage/cfile.getCovCounterList",
		"func coverage_getCovCounterList() []rtcov.CovCounterBlob",
	},
}

// CoverageHookToolchainError reports why the toolchain in goroot cannot build
// CoverageHook, or nil.
func CoverageHookToolchainError(goroot string) error {
	for dir, want := range coverageHookAPI {
		files := []string(nil)
		if dir == "runtime" {
			// The runtime's other files carry build constraints; this one holds
			// the counter list.
			files = []string{"covercounter.go"}
		}
		have, err := goDeclarations(filepath.Join(goroot, "src", filepath.FromSlash(dir)), files)
		if err != nil {
			return err
		}
		for _, decl := range want {
			if !have[decl] {
				return fmt.Errorf("%s lacks %s", dir, decl)
			}
		}
	}
	return nil
}

// CoverageHookRuntimeError reports why the selected runtime's coverage
// package, given as the sources that the build compiles, cannot receive
// CoverageHook's registration, or nil.
func CoverageHookRuntimeError(sources map[string][]byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "", CoverageHook, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	want := ""
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "__dd_civisibility_registerTestingCoverage" {
			want = "func registerTestingCoverage" + signature(fn.Type)
		}
	}
	if want == "" {
		return errors.New("the coverage hook declares no registration")
	}
	have, err := sourceDeclarations(sources)
	if err != nil {
		return err
	}
	if !have[want] {
		return fmt.Errorf("the runtime's coverage package lacks %s", want)
	}
	return nil
}

// goDeclarations renders the declarations of the package in dir, or of files
// there, as sourceDeclarations does.
func goDeclarations(dir string, files []string) (map[string]bool, error) {
	if files == nil {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				files = append(files, name)
			}
		}
	}
	sources := make(map[string][]byte, len(files))
	for _, name := range files {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sources[path] = src
	}
	return sourceDeclarations(sources)
}

// sourceDeclarations renders the declarations of a package's sources:
// functions and methods with their parameter and result types, struct fields
// of named types and of package variables, named types, constants and
// linkname directives.
func sourceDeclarations(sources map[string][]byte) (map[string]bool, error) {
	decls := map[string]bool{}
	fset := token.NewFileSet()
	for name, src := range sources {
		file, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if directive, ok := strings.CutPrefix(comment.Text, "//go:linkname "); ok {
					decls["linkname "+strings.Join(strings.Fields(directive), " ")] = true
				}
			}
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				name := decl.Name.Name
				if decl.Recv != nil && len(decl.Recv.List) == 1 {
					name = "(" + render(decl.Recv.List[0].Type) + ") " + name
				}
				decls["func "+name+signature(decl.Type)] = true
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if structType, ok := spec.Type.(*ast.StructType); ok {
							addFields(decls, spec.Name.Name, structType)
						} else {
							decls["type "+spec.Name.Name+" "+render(spec.Type)] = true
						}
					case *ast.ValueSpec:
						for _, ident := range spec.Names {
							if decl.Tok == token.CONST {
								decls["const "+ident.Name] = true
							} else if structType, ok := spec.Type.(*ast.StructType); ok {
								addFields(decls, ident.Name, structType)
							}
						}
					}
				}
			}
		}
	}
	return decls, nil
}

func addFields(decls map[string]bool, owner string, structType *ast.StructType) {
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			decls["field "+owner+"."+name.Name+" "+render(field.Type)] = true
		}
	}
}

// signature renders a function type's type parameter constraints, parameter
// types and result types. A generic function provides no symbol until it is
// instantiated, so it never matches a nongeneric one.
func signature(fn *ast.FuncType) string {
	typeParams := ""
	if constraints := renderFields(fn.TypeParams); len(constraints) > 0 {
		typeParams = "[" + strings.Join(constraints, ", ") + "]"
	}
	params := renderFields(fn.Params)
	results := renderFields(fn.Results)
	switch {
	case len(results) == 0:
		return typeParams + "(" + strings.Join(params, ", ") + ")"
	case len(results) == 1 && fn.Results.List[0].Names == nil:
		return typeParams + "(" + strings.Join(params, ", ") + ") " + results[0]
	default:
		return typeParams + "(" + strings.Join(params, ", ") + ") (" + strings.Join(results, ", ") + ")"
	}
}

func renderFields(fields *ast.FieldList) []string {
	var types []string
	if fields == nil {
		return nil
	}
	for _, field := range fields.List {
		n := max(len(field.Names), 1)
		for range n {
			types = append(types, render(field.Type))
		}
	}
	return types
}

func render(expr ast.Expr) string {
	if fn, ok := expr.(*ast.FuncType); ok {
		return "func" + signature(fn)
	}
	var out bytes.Buffer
	_ = printer.Fprint(&out, token.NewFileSet(), expr)
	return out.String()
}
