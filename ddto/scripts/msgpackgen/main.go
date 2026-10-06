// Command msgpackgen runs the pinned generator and relocates its runtime import.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/scripts/internal/messagepack"
)

func main() {
	if err := generate(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(args []string) error {
	output := ""
	for i, arg := range args {
		if strings.HasPrefix(arg, "-o=") {
			output = strings.TrimPrefix(arg, "-o=")
		}
		if arg == "-o" && i+1 < len(args) {
			output = args[i+1]
		}
	}
	if output == "" {
		return fmt.Errorf("msgpackgen requires -o")
	}
	// An isolated tool module keeps generator dependencies out of consumers' graphs.
	// Unlike go run tool@version, it also works with a populated offline module cache.
	directory, err := os.MkdirTemp("", "dd-msgpackgen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	modfile := filepath.Join(directory, "go.mod")
	if err := os.WriteFile(modfile, []byte(`module msgpackgen

go 1.24

require github.com/tinylib/msgp `+messagepack.Version+`
`), 0600); err != nil {
		return err
	}
	command := exec.Command("go", append([]string{"run", "-mod=mod", "-modfile=" + modfile, "github.com/tinylib/msgp"}, args...)...)
	command.Env = append(os.Environ(), "GOWORK=off")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	data, err = relocate(data)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0644)
}

// The generator can collect both the source's internal import and its own public
// runtime import. Deduplicate them after relocation so regeneration stays valid.
func relocate(data []byte) ([]byte, error) {
	data = messagepack.RelocateImports(data)
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "generated.go", data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, declaration := range file.Decls {
		imports, ok := declaration.(*ast.GenDecl)
		if !ok || imports.Tok != token.IMPORT {
			continue
		}
		specs := imports.Specs[:0]
		for _, spec := range imports.Specs {
			item := spec.(*ast.ImportSpec)
			key := item.Path.Value
			if item.Name != nil {
				key = item.Name.Name + " " + key
			}
			if !seen[key] {
				seen[key] = true
				specs = append(specs, spec)
			}
		}
		imports.Specs = specs
	}
	var output bytes.Buffer
	if err := format.Node(&output, fileset, file); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
