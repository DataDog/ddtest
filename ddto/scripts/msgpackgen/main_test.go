package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestRelocateDuplicateRuntimeImport(t *testing.T) {
	source := []byte(`// Code generated; license notice.
package fixture
import (
 "time"
 "github.com/tinylib/msgp/msgp"
 "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)
var _ = time.Now
var _ msgp.Raw
`)
	output, err := relocate(source)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", output, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Imports) != 2 || strings.Contains(string(output), "github.com/tinylib/msgp/msgp") || !strings.Contains(string(output), "license notice") {
		t.Fatalf("bad relocation: %s", output)
	}
	second, err := relocate(output)
	if err != nil || string(second) != string(output) {
		t.Fatal("relocation is not stable")
	}
}
