package bazelgen

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Every package and dependency of Mini's repository is what the go command
// lists for the same platform, with and without the race detector, so
// rules_go compiles what go test compiles. The files also include those of
// older Go releases.
func TestMiniPackagesMatchGoList(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	packages, err := miniPackages(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range platforms {
		key := platform.goos + "_" + platform.goarch
		listed := map[string][]string{}
		for _, tags := range []string{"", "race", "purego"} {
			cmd := exec.Command("go", "list", "-deps", "-tags="+tags, "-f", "{{.ImportPath}} {{join .Imports \",\"}} {{join .GoFiles \",\"}},{{join .SFiles \",\"}}", "./testopt", "./propagation")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GOOS="+platform.goos, "GOARCH="+platform.goarch, "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s %s: %v", key, tags, err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fields := strings.Split(line, " ")
				rel, ok := strings.CutPrefix(fields[0], MiniModule+"/")
				if !ok {
					continue
				}
				deps := append([]string{}, listed[rel]...)
				for _, imported := range strings.Split(fields[1], ",") {
					if dep, ok := strings.CutPrefix(imported, MiniModule+"/"); ok && !slices.Contains(deps, dep) {
						deps = append(deps, dep)
					}
				}
				sort.Strings(deps)
				listed[rel] = deps
				for _, file := range strings.Split(fields[2], ",") {
					if file != "" && (packages[rel] == nil || !slices.Contains(packages[rel].files, file)) {
						t.Errorf("%s %s: %s/%s is not generated", key, tags, rel, file)
					}
				}
			}
		}
		// The packages that the roots reach on the platform, as go list lists.
		generated := map[string][]string{}
		queue := slices.Clone(miniRoots)
		for len(queue) > 0 {
			rel := queue[0]
			queue = queue[1:]
			if _, ok := generated[rel]; ok || packages[rel] == nil {
				continue
			}
			deps := append([]string{}, packages[rel].deps[key]...)
			generated[rel] = deps
			queue = append(queue, deps...)
		}
		for rel, deps := range listed {
			if !slices.Equal(generated[rel], deps) {
				t.Errorf("%s %s: generated deps %v, go list %v", key, rel, generated[rel], deps)
			}
		}
		for rel := range generated {
			if _, ok := listed[rel]; !ok {
				t.Errorf("%s: %s is generated but not listed", key, rel)
			}
		}
	}
	// A release built with the newest Go also serves older ones.
	if !slices.Contains(packages["internal/citransport"].files, "http2_go125.go") {
		t.Error("the Go 1.25 variant of internal/citransport is not generated")
	}
}

// The BUILD file format, on a module whose dependencies differ by platform.
func TestMiniBuildFileGolden(t *testing.T) {
	src := fstest.MapFS{
		"go.mod":                 {Data: []byte("module " + MiniModule + "\n\ngo 1.25.0\n")},
		"LICENSE":                {Data: []byte("license\n")},
		"testopt/testopt.go":     {Data: []byte("package testopt\n\nimport (\n\t_ \"" + MiniModule + "/internal/common\"\n\t_ \"fmt\"\n)\n")},
		"testopt/unix.go":        {Data: []byte("//go:build !windows\n\npackage testopt\n\nimport _ \"" + MiniModule + "/internal/unixonly\"\n")},
		"testopt/old.go":         {Data: []byte("//go:build !go1.25\n\npackage testopt\n\nimport _ \"" + MiniModule + "/internal/old\"\n")},
		"testopt/race.go":        {Data: []byte("//go:build race\n\npackage testopt\n\nimport _ \"" + MiniModule + "/internal/racy\"\n")},
		"testopt/purego.go":      {Data: []byte("//go:build purego && !unsafe\n\npackage testopt\n")},
		"testopt/new.go":         {Data: []byte("//go:build go1.1 && !go1.26\n\npackage testopt\n")},
		"internal/old/o.go":      {Data: []byte("package old\n")},
		"internal/racy/r.go":     {Data: []byte("package racy\n")},
		"testopt/doc_test.go":    {Data: []byte("package testopt\n")},
		"propagation/p.go":       {Data: []byte("package propagation\n")},
		"internal/common/c.go":   {Data: []byte("package common\n")},
		"internal/unixonly/u.go": {Data: []byte("//go:build !windows\n\npackage unixonly\n")},
	}
	dir := t.TempDir()
	if err := WriteMini(dir, src, MiniOptions{RulesGo: "rules_go", Version: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	err := filepath.WalkDir(dir, func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(file)
		got.WriteString("== " + filepath.ToSlash(rel) + "\n" + string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "mini.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		t.Fatalf("generated repository differs from %s; rerun with -update after review:\n%s", golden, got.String())
	}
}

func TestMiniRejectsEmbedAndCgo(t *testing.T) {
	for name, source := range map[string]string{
		"embed": "package testopt\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar data string\n",
		"cgo":   "package testopt\n\nimport \"C\"\n",
	} {
		src := fstest.MapFS{"go.mod": {Data: []byte("module " + MiniModule + "\n\ngo 1.25.0\n")}, "testopt/t.go": {Data: []byte(source)}, "testopt/data.txt": {}, "propagation/p.go": {Data: []byte("package propagation\n")}}
		if err := WriteMini(t.TempDir(), src, MiniOptions{RulesGo: "rules_go"}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWriteSDK(t *testing.T) {
	goroot := t.TempDir()
	files := map[string]string{
		"VERSION":                      "go1.26.7\n",
		"bin/go":                       "go",
		"src/testing/testing.go":       "package testing\n",
		"pkg/tool/linux_amd64/compile": "real compiler",
		"pkg/tool/linux_amd64/link":    "linker",
		"pkg/include/funcdata.h":       "header",
	}
	for name, data := range files {
		file := filepath.Join(goroot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	compiler := filepath.Join(t.TempDir(), "ddtest")
	if err := os.WriteFile(compiler, []byte("ddtest"), 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "sdk")
	if err := WriteSDK(dir, SDKOptions{GOROOT: goroot, Compiler: compiler, PanicHook: true, Version: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if read("pkg/tool/linux_amd64/compile") != "ddtest" || read("pkg/tool/linux_amd64/compile.real") != "real compiler" || read("pkg/tool/linux_amd64/link") != "linker" || read("src/testing/testing.go") != "package testing\n" || read("VERSION") != "go1.26.7\n" {
		t.Fatal("the SDK's files")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(read("pkg/tool/linux_amd64/"+CompilerConfig)), &config); err != nil || config["panicHook"] != true || config["contract"] != float64(Contract) || config["version"] != "v1.2.3" {
		t.Fatal(config, err)
	}
	if read(ContractFile) != "version=v1.2.3\ncontract=1\nsdk_name=ddto\n" {
		t.Fatal(read(ContractFile))
	}
	// Windows has no executable bit.
	if info, err := os.Stat(filepath.Join(dir, "pkg", "tool", "linux_amd64", "compile")); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0100 == 0 {
		t.Fatal("the compiler is not executable", err)
	}
	if err := WriteSDK(filepath.Join(t.TempDir(), "sdk"), SDKOptions{GOROOT: t.TempDir(), Compiler: compiler}); err == nil {
		t.Fatal("a directory without a compiler accepted as an SDK")
	}
	// rules_go's local and wrapped SDKs link their top-level entries to
	// another GOROOT; here the compiler is a link too.
	linked := t.TempDir()
	for _, name := range []string{"VERSION", "bin", "src", "pkg"} {
		if err := os.Symlink(filepath.Join(goroot, name), filepath.Join(linked, name)); err != nil {
			t.Skip("no symbolic links:", err)
		}
	}
	tools := filepath.Join(goroot, "pkg", "tool", "linux_amd64")
	if err := os.Rename(filepath.Join(tools, "compile"), filepath.Join(tools, "compile.orig")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("compile.orig", filepath.Join(tools, "compile")); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(t.TempDir(), "sdk")
	if err := WriteSDK(dir, SDKOptions{GOROOT: linked, Compiler: compiler, Version: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	if read("pkg/tool/linux_amd64/compile") != "ddtest" || read("pkg/tool/linux_amd64/compile.real") != "real compiler" || read("src/testing/testing.go") != "package testing\n" {
		t.Fatal("the linked SDK's files")
	}
	if info, err := os.Lstat(filepath.Join(dir, "src")); err != nil || !info.IsDir() {
		t.Fatal("a linked directory is not copied", err)
	}
}

// A junction above GOROOT, which Go reports as an irregular file, as the
// Windows runners' toolcache and Bazel's linked repositories have.
func TestWriteSDKThroughJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows'")
	}
	goroot := filepath.Join(t.TempDir(), "go")
	for name, data := range map[string]string{"VERSION": "go1.26.7\n", "src/testing/testing.go": "package testing\n", "pkg/tool/windows_amd64/compile.exe": "real compiler"} {
		file := filepath.Join(goroot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	junction := filepath.Join(t.TempDir(), "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, filepath.Dir(goroot)).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	compiler := filepath.Join(t.TempDir(), "ddtest.exe")
	if err := os.WriteFile(compiler, []byte("ddtest"), 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "sdk")
	if err := WriteSDK(dir, SDKOptions{GOROOT: filepath.Join(junction, "go"), Compiler: compiler}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"pkg/tool/windows_amd64/compile.exe": "ddtest", "pkg/tool/windows_amd64/compile.real.exe": "real compiler", "src/testing/testing.go": "package testing\n"} {
		if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))); err != nil || string(data) != want {
			t.Errorf("%s: %q, %v", name, data, err)
		}
	}
}
