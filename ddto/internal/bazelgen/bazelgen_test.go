package bazelgen

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Every package and dependency of Mini's repository is what the go command
// lists for the same platform, so rules_go compiles what go test compiles.
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
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}} {{join .Imports \",\"}}", "./testopt", "./propagation")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+platform.goos, "GOARCH="+platform.goarch, "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		listed := map[string][]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			importPath, imports, _ := strings.Cut(line, " ")
			rel, ok := strings.CutPrefix(importPath, MiniModule+"/")
			if !ok {
				continue
			}
			deps := []string{}
			for _, imported := range strings.Split(imports, ",") {
				if dep, ok := strings.CutPrefix(imported, MiniModule+"/"); ok {
					deps = append(deps, dep)
				}
			}
			sort.Strings(deps)
			listed[rel] = deps
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
}

// The BUILD file format, on a module whose dependencies differ by platform.
func TestMiniBuildFileGolden(t *testing.T) {
	src := fstest.MapFS{
		"go.mod":                 {Data: []byte("module " + MiniModule + "\n\ngo 1.25.0\n")},
		"LICENSE":                {Data: []byte("license\n")},
		"testopt/testopt.go":     {Data: []byte("package testopt\n\nimport (\n\t_ \"" + MiniModule + "/internal/common\"\n\t_ \"fmt\"\n)\n")},
		"testopt/unix.go":        {Data: []byte("//go:build !windows\n\npackage testopt\n\nimport _ \"" + MiniModule + "/internal/unixonly\"\n")},
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
		src := fstest.MapFS{"testopt/t.go": {Data: []byte(source)}, "testopt/data.txt": {}, "propagation/p.go": {Data: []byte("package propagation\n")}}
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
	if info, err := os.Stat(filepath.Join(dir, "pkg", "tool", "linux_amd64", "compile")); err != nil || info.Mode().Perm()&0100 == 0 {
		t.Fatal("the compiler is not executable", err)
	}
	if err := WriteSDK(filepath.Join(t.TempDir(), "sdk"), SDKOptions{GOROOT: t.TempDir(), Compiler: compiler}); err == nil {
		t.Fatal("a directory without a compiler accepted as an SDK")
	}
}
