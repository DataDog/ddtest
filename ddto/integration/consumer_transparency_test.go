package integration

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Native -mod=mod can add a missing client requirement. Mini must add exactly
// that requirement, while its injected import stays out of the client's files.
func TestMiniModModeResolvesClientRequirements(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, language := range []string{"1.21", "1.25.0"} {
		for _, fromEnvironment := range []bool{false, true} {
			t.Run(language+"/env="+fmt.Sprint(fromEnvironment), func(t *testing.T) {
				var nativeMod []byte
				for i, tool := range []string{"go", driver} {
					dir := t.TempDir()
					if err := os.Mkdir(filepath.Join(dir, "helper"), 0700); err != nil {
						t.Fatal(err)
					}
					writeBuildFixture(t, dir, map[string]string{
						"go.mod":           "module example.com/client\ngo " + language + "\nreplace example.com/helper => ./helper\n",
						"helper/go.mod":    "module example.com/helper\ngo 1.21\n",
						"helper/helper.go": "package helper\nconst Value=42\n",
						"client_test.go":   "package client\nimport(\"testing\";\"example.com/helper\")\nfunc TestClient(t *testing.T){if helper.Value!=42{t.Fatal(helper.Value)}}\n",
					})
					args := []string{"test", "-count=1", "-v", "."}
					flags := ""
					if fromEnvironment {
						flags = "-mod=mod"
					} else {
						args = append(args, "-mod=mod")
					}
					out, stderr, code := command(t, dir, testEnv("GOFLAGS="+flags, "GOWORK=off", "GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), tool, args...)
					if code != 0 || !strings.Contains(out, "--- PASS: TestClient") {
						t.Fatalf("%s exit=%d\n%s%s", tool, code, out, stderr)
					}
					mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
					if err != nil {
						t.Fatal(err)
					}
					if i == 0 {
						nativeMod = mod
					} else if !bytes.Equal(nativeMod, mod) {
						t.Fatalf("module updates differ:\nnative: %s\nMini: %s", nativeMod, mod)
					}
				}
			})
		}
	}
}

func TestMiniPreservesConsumerLanguage(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, language := range []string{"1.21", "1.22", "1.25.0"} {
		t.Run(language, func(t *testing.T) {
			dir := t.TempDir()
			want := "0"
			if language == "1.21" {
				want = "3"
			}
			mod := "module example.com/looplanguage\n\ngo " + language + "\n"
			writeBuildFixture(t, dir, map[string]string{
				"go.mod": mod,
				"loop_test.go": `package looplanguage
import("testing";"fmt";"time")
func TestLoopLanguage(t *testing.T) {
 var fs []func() int
 for i:=0;i<3;i++ { fs=append(fs,func()int{return i}) }
 if got:=fs[0]();got!=` + want + ` { t.Fatalf("loop semantics changed: %d",got) }
 timer:=time.NewTimer(time.Hour);defer timer.Stop();fmt.Printf("TIMER_CAP=%d\n",cap(timer.C))
}
`,
			})
			var nativeOutput string
			for i, prefix := range [][]string{{"go", "test"}, {driver, "test"}} {
				out, stderr, code := command(t, dir, testEnv("GOWORK=off", "GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), prefix[0], append(prefix[1:], "-v", "-mod=readonly", "-count=1", ".")...)
				if code != 0 {
					t.Fatalf("%v: exit=%d\n%s%s", prefix, code, out, stderr)
				}
				if i == 0 {
					nativeOutput = out
				} else {
					assertNativeTimerCapacity(t, nativeOutput, out)
				}
			}
			if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(data) != mod {
				t.Fatalf("module changed: %q %v", data, err)
			}
		})
	}
}

func TestMiniPreservesOlderConsumerDependencies(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, tc := range []struct{ path, version, source string }{
		{"github.com/stretchr/testify", "v1.6.1", `import "github.com/stretchr/testify/assert"
func TestDependency(t *testing.T) { assert.Equal(t,1,1);check(t,"github.com/stretchr/testify","v1.6.1",assert.Equal) }`},
		{"github.com/davecgh/go-spew", "v1.1.0", `import "github.com/davecgh/go-spew/spew"
func TestDependency(t *testing.T) { _=spew.Sdump(1);check(t,"github.com/davecgh/go-spew","v1.1.0",spew.Sdump) }`},
		{"gopkg.in/yaml.v3", "v3.0.0-20200313102051-9f266ea9e77c", `import "gopkg.in/yaml.v3"
func TestDependency(t *testing.T) { var value any;if err:=yaml.Unmarshal([]byte("a: 1"),&value);err!=nil{t.Fatal(err)};check(t,"gopkg.in/yaml.v3","v3.0.0-20200313102051-9f266ea9e77c",yaml.Unmarshal) }`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			dir := t.TempDir()
			writeBuildFixture(t, dir, map[string]string{
				"go.mod": "module example.com/dependencyversions\n\ngo 1.21\nrequire " + tc.path + " " + tc.version + "\n",
				"dependency_test.go": `package dependencyversions
import ("testing";"runtime/debug";"runtime";"reflect";"path/filepath";"strings")
` + tc.source + `
func check(t *testing.T,path,version string,symbol any) {
 info,ok:=debug.ReadBuildInfo();if !ok{t.Fatal("missing build info")}
 for _,dep:=range info.Deps {if dep.Path==path{if dep.Version!=version{t.Fatalf("%s version=%s want=%s",path,dep.Version,version)};return}}
 // Go 1.26 test executables can omit module dependencies from build info.
 // Check the source of a linked dependency function in that case. A function
 // pointer keeps the selected package observable even when its calls inline.
 if len(info.Deps)!=0 {t.Fatal("missing dependency",path)}
 pc:=reflect.ValueOf(symbol).Pointer();file,_:=runtime.FuncForPC(pc).FileLine(pc)
 if !strings.Contains(filepath.ToSlash(file),path+"@"+version+"/"){t.Fatalf("selected dependency source=%s want=%s@%s",file,path,version)}
}
`,
			})
			env := testEnv("GOWORK=off", "DD_CIVISIBILITY_ENABLED=false")
			if out, stderr, code := command(t, dir, env, "go", "mod", "tidy"); code != 0 {
				t.Fatal(out, stderr)
			}
			beforeMod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
			beforeSum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
			for _, flags := range [][]string{{"-mod=readonly"}, {"-mod=mod"}} {
				out, stderr, code := command(t, dir, env, driver, append([]string{"test", "-count=1"}, flags...)...)
				if code != 0 {
					t.Fatalf("%v exit=%d\n%s%s", flags, code, out, stderr)
				}
				mod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
				sum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
				if string(mod) != string(beforeMod) || string(sum) != string(beforeSum) {
					t.Fatal("client module files changed")
				}
			}
		})
	}
}

func TestMiniContinuesAfterPackageSetupFailures(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, target := range []string{"./...", "./good ./missing", "./good ./empty"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			for _, sub := range []string{"good", "bad", "empty"} {
				if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeBuildFixture(t, dir, map[string]string{
				"go.mod":            "module example.com/setupfailures\n\ngo 1.21\n",
				"good/good_test.go": "package good\nimport(\"fmt\";\"testing\")\nfunc TestGood(t *testing.T){fmt.Println(\"GOOD_PACKAGE_RAN\")}\n",
				"bad/one.go":        "package bad\n",
				"bad/two.go":        "package other\n",
			})
			for _, prefix := range [][]string{{"go", "test"}, {driver, "test"}} {
				args := append(append(prefix[1:], "-v", "-count=1"), strings.Fields(target)...)
				out, stderr, code := command(t, dir, testEnv("GOWORK=off", "DD_CIVISIBILITY_ENABLED=false"), prefix[0], args...)
				if code != 1 || !strings.Contains(out, "GOOD_PACKAGE_RAN") {
					t.Fatalf("%v exit=%d\n%s%s", prefix, code, out, stderr)
				}
			}
		})
	}
}

func assertNativeTimerCapacity(t *testing.T, native, instrumented string) {
	t.Helper()
	capacity := func(out string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "TIMER_CAP=") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	want, got := capacity(native), capacity(instrumented)
	if want == "" || got != want {
		t.Fatalf("timer compatibility changed: native=%q instrumented=%q", want, got)
	}
}
