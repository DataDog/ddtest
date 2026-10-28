package instrument

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestTestifyAPIGuard(t *testing.T) {
	good := `package suite; import tt "testing"; type TestingSuite interface{}; type Suite struct{}; func Run(t *tt.T,s TestingSuite){}`
	if err := TestifyAPI(map[string][]byte{"suite.go": []byte(good)}); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		strings.Replace(good, "func Run(", "func Run[T any](", 1), strings.Replace(good, "*tt.T", "tt.T", 1), strings.Replace(good, "s TestingSuite", "s any", 1), strings.Replace(good, "s TestingSuite", "s ...TestingSuite", 1), strings.Replace(good, "){}", ") bool{return true}", 1), strings.Replace(good, "import tt \"testing\"", "import tt \"other\"", 1), "package suite",
	} {
		if err := TestifyAPI(map[string][]byte{"suite.go": []byte(src)}); err == nil {
			t.Fatal("unsupported API accepted", src)
		}
	}
}

func TestTestifyLibraryNameCollision(t *testing.T) {
	if err := CheckTestifyNames("suite.go", []byte(`package suite;var __dd_ci_registerTestifySuite int`)); err == nil {
		t.Fatal("hook collision accepted")
	}
	if err := CheckTestifyNames("suite.go", []byte(`package suite;const description="__dd_ci_registerTestifySuite"`)); err != nil {
		t.Fatal("string mistaken for binding", err)
	}
}

func TestTestifyMinimumVersion(t *testing.T) {
	for _, v := range []string{"v1.4.0", "v1.4.1-0.20190101000000-abcdefabcdef", "v1.8.4", "v1.10.0", "v1.11.0", "v1.11.1", "v1.12.0", "v1.12.1", "v1.13.0-rc.1", "v1.11.1+metadata"} {
		if !SupportsTestifyVersion(v) {
			t.Errorf("supported version rejected: %s", v)
		}
	}
	for _, v := range []string{"", "v1.3.0", "v1.4.0-rc.1", "v2.0.0", "1.11.1", "v1.11", "v1.-1.2"} {
		if SupportsTestifyVersion(v) {
			t.Errorf("unsupported version accepted: %s", v)
		}
	}
}

func FuzzTransformTestify(f *testing.F) {
	f.Add(`package suite; import "testing"; type TestingSuite interface{};func Run(t *testing.T,s TestingSuite){}`)
	f.Add("//line /original/suite.go:1:1\npackage suite;import \"testing\";type TestingSuite interface{};func Run(t *testing.T,s TestingSuite){}")
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 64<<10 {
			t.Skip()
		}
		out, changed, err := TransformTestifyEntry("suite.go", []byte(src), false)
		if err != nil || !changed {
			return
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "result.go", out, 0); err != nil {
			t.Fatal("rewrite produced malformed Go", err)
		}
		if strings.Count(string(out), "\n") != strings.Count(src, "\n") {
			t.Fatal("line count changed")
		}
	})
}

func BenchmarkTransformTestifyEntry(b *testing.B) {
	src := []byte(`package suite;import "testing";type TestingSuite interface{};func Run(t *testing.T,s TestingSuite){}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := TransformTestifyEntry("suite.go", src, false); err != nil {
			b.Fatal(err)
		}
	}
}
