package instrument

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
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

// Without Mini, the edited Find neither pauses delivery nor adds filters: it
// sees only the caller's options.
func TestGoleakEntryWithoutMiniRunsNatively(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	root := t.TempDir()
	find := []byte("package goleak\ntype Option interface{}\ntype option string\nfunc IgnoreAnyFunction(f string) Option { return option(f) }\nfunc Find(options ...Option) error {\n\treturn fmt.Errorf(\"%d\", len(options))\n}\n")
	find = bytes.Replace(find, []byte("package goleak\n"), []byte("package goleak\nimport \"fmt\"\n"), 1)
	edited, found, err := TransformGoleakEntry(filepath.Join(root, "goleak", "leaks.go"), find)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	for name, data := range map[string]string{
		"go.mod":          "module example.com/leaks\n\ngo 1.25.0\n",
		"goleak/leaks.go": string(edited),
		"goleak/hook.go":  GoleakEntryHook(),
		"main.go":         "package main\nimport (\"fmt\";\"example.com/leaks/goleak\")\nfunc main() { fmt.Print(goleak.Find(goleak.IgnoreAnyFunction(\"caller\"))) }\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "1" {
		t.Fatalf("Find saw %q options, want the caller's 1: %v", out, err)
	}
}
