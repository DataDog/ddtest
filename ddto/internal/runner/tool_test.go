package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestToolDispatch(t *testing.T) {
	for _, tc := range []struct {
		mode, tool, pkg string
		probe, want     bool
	}{
		{"", "compile", "github.com/stretchr/testify/suite", false, false},
		{"cover", "compile", "github.com/stretchr/testify/suite", false, false},
		{"testify", "compile", "github.com/stretchr/testify/suite", false, true},
		{"testify", "compile", "github.com/stretchr/testify/suite [example.com/client.test]", false, true},
		{"testify", "compile", "github.com/stretchr/testify/suite_test [example.com/client.test]", false, false},
		{"testify", "compile", "example.com/client", false, false},
		{"testify", "compile", "github.com/stretchr/testify/suite", true, false},
		{"testify", "link", "example.com/client.test", false, false},
		{"goleak", "compile", "go.uber.org/goleak_test [go.uber.org/goleak.test]", false, true},
		{"goleak", "compile", "go.uber.org/goleak_test", true, false},
		{"goleak", "compile", "go.uber.org/goleak.test", false, true},
		{"goleak", "compile", "go.uber.org/goleak.test", true, false},
		{"goleak", "compile", "example.com/client.test", false, false},
		{"testify", "compile", "go.uber.org/goleak_test", false, false},
		{"testify", "asm", "example.com/client", false, false},
		{"testify", "cover", "example.com/client", false, false},
		{"cover", "cover", "example.com/client", false, false},
		{"cover", "cover", "testing", false, true},
		{"testify-cover", "cover", "testing", true, true},
		{"testify-cover", "compile.exe", "github.com/stretchr/testify/suite", false, true},
		{"mini-sdk", "compile", sdkTracerPackage, false, true},
		{"mini-sdk", "compile", sdkTracerPackage, true, false},
		{"mini-sdk-orchestrion", "compile", sdkTracerPackage, false, true},
		{"mini-sdk", "link", sdkTracerPackage, false, false},
		{"mini-sdk", "compile", "example.com/client", false, false},
		{"testify", "compile", sdkTracerPackage, false, false},
	} {
		args := []string{filepath.Join("tools", tc.tool)}
		if tc.probe {
			args = append(args, "-V=full")
		}
		if got := ToolNeedsPlan(tc.mode, args, tc.pkg); got != tc.want {
			t.Fatalf("%+v: got %v", tc, got)
		}
	}
}

func TestPreparedSuiteCompilerInputs(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "suite.go")
	prepared := filepath.Join(dir, "entry.go")
	plan := &LibraryEntry{Sources: map[string]string{source: prepared}, HookFile: filepath.Join(dir, "hook.go")}
	args := []string{"compile", "-o", "output.a", source}
	got, cleanup, err := prepareLibraryCompile(plan, args)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !reflect.DeepEqual(got, []string{"compile", "-o", "output.a", prepared, plan.HookFile}) || args[3] != source {
		t.Fatal(got, args)
	}
	covered := filepath.Join(dir, "suite.cover.go")
	src := "//line /client/suite.go:1:1\npackage suite\nimport \"testing\"\ntype TestingSuite interface{}\nfunc Run(t *testing.T,s TestingSuite){ coverage[0]++; original() }\n"
	if err := os.WriteFile(covered, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	got, cleanup, err = prepareLibraryCompile(plan, []string{"compile", covered})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == src {
		t.Fatal("covered Run not transformed")
	}
	cleanup()
	if _, err := os.Stat(got[1]); !os.IsNotExist(err) {
		t.Fatal("temporary compiler source remains", err)
	}
}

func TestGoleakCompilerRemovesOnlyItsOwnCacheMarker(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leaks.go")
	plan := &LibraryEntry{Package: "go.uber.org/goleak", Fingerprint: "abc", Sources: map[string]string{source: "prepared.go"}, HookFile: "hook.go"}
	args := []string{"compile", "-importcfg", "imports", "-I=ddto-goleak-abc", "-I=client-path", "-N", "-l", source}
	got, cleanup, err := prepareLibraryCompile(plan, args)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	want := []string{"compile", "-importcfg", "imports", "-I=client-path", "-N", "-l", "prepared.go", "hook.go"}
	if !reflect.DeepEqual(got, want) || args[3] != "-I=ddto-goleak-abc" {
		t.Fatalf("compiler arguments changed: %v / %v", got, args)
	}
}

func BenchmarkToolBypassDispatch(b *testing.B) {
	args := []string{"/goroot/pkg/tool/linux_amd64/compile", "-o", "output.a", "file.go"}
	b.ReportAllocs()
	for b.Loop() {
		if ToolNeedsPlan("testify-cover", args, "example.com/client") {
			b.Fatal("unexpected transformation")
		}
	}
}
