package instrument

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRejectUnsupportedTesting(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing\n")})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unsupported testing must fail before compilation; got %v", err)
	}
}

func TestRejectAlreadyInstrumentedTesting(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing\nvar __dd_civisibility_instrumentTestingM int\n")})
	if err == nil || !strings.Contains(err.Error(), "already instrumented") {
		t.Fatalf("double instrumentation must be rejected; got %v", err)
	}
}

func TestRejectInvalidSource(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing; func broken(")})
	if err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestFuzzHookIsSelectiveAndPreservesInputs(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(runtime.GOROOT(), "src/testing"))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	var fuzzPath string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(runtime.GOROOT(), "src/testing", entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = raw
		if bytes.Contains(raw, []byte("func (f *F) Fuzz(")) {
			fuzzPath = path
		}
	}
	if fuzzPath == "" {
		t.Fatal("native F.Fuzz entrypoint missing")
	}
	original := bytes.Clone(sources[fuzzPath])
	mini, err := TransformWithFuzz(sources)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := Transform(sources)
	if err != nil {
		t.Fatal(err)
	}
	hook := []byte("ff = __dd_civisibility_instrumentTestingFuzzFunc(ff)")
	if bytes.Count(mini.Files[fuzzPath], hook) != 1 {
		t.Fatal("Mini must instrument F.Fuzz exactly once")
	}
	if bytes.Contains(sdk.Files[fuzzPath], hook) {
		t.Fatal("base transform unexpectedly requested the optional F.Fuzz hook")
	}
	if !bytes.Equal(sources[fuzzPath], original) {
		t.Fatal("mutated caller sources")
	}
	sources[fuzzPath] = bytes.Replace(original, []byte("func (f *F) Fuzz("), []byte("func (f *F) UnsupportedFuzz("), 1)
	if _, err := TransformWithFuzz(sources); err == nil {
		t.Fatal("missing F.Fuzz must fail before compilation")
	}
	if _, err := Transform(sources); err != nil {
		t.Fatalf("F.Fuzz is optional for the base transform: %v", err)
	}
}
