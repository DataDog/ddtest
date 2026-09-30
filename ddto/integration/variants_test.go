package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoTestFlagsAndVariants(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference != "" {
		var e error
		reference, e = filepath.Abs(reference)
		if e != nil {
			t.Fatal(e)
		}
	}
	dir, driver := prepareFixture(t, reference != "")
	t.Run("no-tests", func(t *testing.T) {
		want, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", "test", "./notests")
		if code != 0 {
			t.Fatal(stderr)
		}
		got, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "./notests")
		if code != 0 || got != want {
			t.Fatalf("no-tests contract: %d %q %q %s", code, got, want, stderr)
		}
	})
	t.Run("test-prefix", func(t *testing.T) {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "-test.v", "-test.run=^TestPass$", "-count=1", ".")
		if code != 0 || !strings.Contains(out, "=== RUN   TestPass") {
			t.Fatalf("native test flag prefix: %d %s\n%s", code, out, stderr)
		}
	})
	t.Run("json-tags-multiple-packages", func(t *testing.T) {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOFLAGS=-tags=poc_extra"), driver, "test", "-count=1", "-json", "-run=^(TestTagged|TestOther)$", "./...")
		if code != 0 || !strings.Contains(out, `"Test":"TestTagged"`) || !strings.Contains(out, `"Test":"TestOther"`) {
			t.Fatalf("json/tags/package selection: %d %s\n%s", code, out, stderr)
		}
	})
	for _, flag := range []string{"-race", "-cover"} {
		t.Run(flag, func(t *testing.T) {
			var bins []string
			compilers := []struct {
				name string
				args []string
			}{{"go", []string{"test"}}, {driver, []string{"test"}}}
			if reference != "" {
				compilers = append(compilers, struct {
					name string
					args []string
				}{"go", []string{"test", "-toolexec=" + reference + " toolexec"}})
			}
			for _, compiler := range compilers {
				bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
				args := append(append([]string{}, compiler.args...), flag, "-c", "-o", bin, ".")
				if flag == "-cover" {
					args = append(args, "-covermode=atomic", "-coverpkg=./...")
				}
				out, e, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), compiler.name, args...)
				if code != 0 {
					t.Fatalf("compile %s: %s\n%s", flag, out, e)
				}
				bins = append(bins, bin)
			}
			args := []string{"-test.run=^Test(Pass|Cleanup|Context|Parallel|Nested)$"}
			got := execute(t, dir, bins[1], args, true, false)
			if got.code != 0 || len(got.events) == 0 {
				t.Fatalf("%s: %d %s\n%s", flag, got.code, got.out, got.stderr)
			}
			if reference != "" {
				want := execute(t, dir, bins[2], args, true, false)
				if got.code != want.code || !reflect.DeepEqual(got.events, want.events) {
					t.Fatalf("%s event mismatch\n%v\n%v", flag, got.events, want.events)
				}
			}
		})
	}
	t.Run("benchmarks", func(t *testing.T) {
		binaries := []string{}
		for _, compiler := range []struct {
			name string
			args []string
		}{{driver, []string{"test"}}, {"go", []string{"test", "-toolexec=" + reference + " toolexec"}}} {
			if compiler.name == "go" && reference == "" {
				continue
			}
			bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
			args := append(compiler.args, "-c", "-o", bin, ".")
			out, e, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), compiler.name, args...)
			if code != 0 {
				t.Fatalf("benchmark compile: %s\n%s", out, e)
			}
			binaries = append(binaries, bin)
		}
		args := []string{"-test.run=^$", "-test.bench=BenchmarkWork", "-test.benchtime=1x"}
		got := execute(t, dir, binaries[0], args, true, false)
		if got.code != 0 || len(got.events) == 0 {
			t.Fatalf("benchmark: %d %s\n%s", got.code, got.out, got.stderr)
		}
		if len(binaries) > 1 {
			want := execute(t, dir, binaries[1], args, true, false)
			if !reflect.DeepEqual(got.events, want.events) {
				t.Fatalf("benchmark event mismatch\n%v\n%v", got.events, want.events)
			}
		}
	})
}
