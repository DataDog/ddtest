package instrument

import (
	"bytes"
	"errors"
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
		if !bytes.Contains(got, []byte("return original(options...)")) || !bytes.Contains(got, []byte("defer __ddtoResume()")) {
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
	if strings.Contains(GoleakEntryHook(), `"github.com/DataDog/ddtest/ddto`) {
		t.Fatal("runtime import added to library graph")
	}
}

func TestGoleakAPIRequiresIgnoreAnyFunction(t *testing.T) {
	const find = "package goleak\ntype Option interface{}\nfunc Find(options ...Option) error { return nil }\n"
	ok := map[string][]byte{"leaks.go": []byte(find), "options.go": []byte("package goleak\nfunc IgnoreAnyFunction(f string) Option { return nil }\n")}
	if err := GoleakAPI(ok); err != nil {
		t.Fatal(err)
	}
	for name, options := range map[string]string{
		"v1.2.1":       "package goleak\nfunc IgnoreTopFunction(f string) Option { return nil }\n",
		"method":       "package goleak\ntype opts struct{}\nfunc (opts) IgnoreAnyFunction(f string) Option { return nil }\n",
		"generic":      "package goleak\nfunc IgnoreAnyFunction[T any](f string) Option { return nil }\n",
		"two params":   "package goleak\nfunc IgnoreAnyFunction(f, g string) Option { return nil }\n",
		"variadic":     "package goleak\nfunc IgnoreAnyFunction(f ...string) Option { return nil }\n",
		"other result": "package goleak\nfunc IgnoreAnyFunction(f string) error { return nil }\n",
		"named result": "package goleak\nfunc IgnoreAnyFunction(f string) (o Option) { return nil }\n",
		"two results":  "package goleak\nfunc IgnoreAnyFunction(f string) (Option, error) { return nil, nil }\n",
	} {
		err := GoleakAPI(map[string][]byte{"leaks.go": []byte(find), "options.go": []byte(options)})
		if !errors.Is(err, ErrUnsupportedAPI) {
			t.Errorf("%s: accepted or wrong error: %v", name, err)
		}
	}
	if err := GoleakAPI(map[string][]byte{"leaks.go": []byte("package goleak\nfunc (")}); err == nil || errors.Is(err, ErrUnsupportedAPI) {
		t.Fatalf("a syntax error must fail the preparation, not warn: %v", err)
	}
}
