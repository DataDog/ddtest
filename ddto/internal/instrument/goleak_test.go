package instrument

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestGoleakEntryKeepsFindAndOptions(t *testing.T) {
	source := []byte("package goleak\ntype Option interface{}\nfunc Find(options ...Option) error { return original(options...) }\n")
	for _, prefix := range []string{"", "//line /original/leaks.go:1:1\n"} {
		got, found, err := TransformGoleakEntry("/fixture/leaks.go", append([]byte(prefix), source...))
		if err != nil || !found {
			t.Fatal(found, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "rewritten.go", got, parser.AllErrors); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte("return original(options...)")) || !bytes.Contains(got, []byte("defer __ddtestResume()")) {
			t.Fatal("lost original runner or resumption")
		}
		if bytes.Contains(got, []byte("IgnoreCurrent")) || bytes.Contains(got, []byte("net/http")) {
			t.Fatal("broad goroutine ignore")
		}
		if _, _, err := TransformGoleakEntry("repeated.go", got); err == nil {
			t.Fatal("double instrumentation was accepted")
		}
	}
	for _, signature := range []string{"func Find(options []Option) error {return nil}", "func Find(_ ...Option) error {return nil}", "func Find(options ...Other) error {return nil}"} {
		if _, _, err := TransformGoleakEntry("changed.go", []byte("package goleak\n"+signature)); err == nil {
			t.Fatal("unsupported API", signature)
		}
	}
	if strings.Contains(GoleakEntryHook(), `"github.com/tonyredondo`) {
		t.Fatal("runtime import added to library graph")
	}
}
