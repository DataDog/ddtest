// Package bazelgen writes what Bazel needs from a ddtest release to
// instrument Go tests: an instrumented Go SDK, whose compiler is this
// executable, and Mini's packages as a Bazel repository. The Starlark that
// uses them belongs to rules_test_optimization; Contract versions the layout
// that it relies on.
package bazelgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Contract versions the layout of both outputs, the SDK's compiler
// configuration and the sdk_name value that the rule's toolchains match.
const Contract = 1

// SDKName is the value of @rules_go//go/toolchain:sdk_name that selects the
// instrumented SDK's toolchains.
const SDKName = "ddto"

// MiniModule is Mini's module path, the import path prefix of its packages.
const MiniModule = "github.com/DataDog/ddtest/ddto"

// ContractFile, at the root of each output, records the ddtest version, the
// contract and the sdk_name value, one key=value per line.
const ContractFile = "DDTEST_INSTRUMENT"

// CompilerConfig is the instrumented SDK's compiler configuration, next to
// its compiler.
const CompilerConfig = "ddtest-instrument.json"

// miniRoots are the packages that tests import: the registration's testopt,
// and propagation for the Go API.
var miniRoots = []string{"testopt", "propagation"}

// platforms are the target platforms whose build constraints decide Mini's
// files and dependencies: those of ddtest's release executables, and 386.
var platforms = []struct{ goos, goarch string }{
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"linux", "386"}, {"linux", "amd64"}, {"linux", "arm64"},
	{"windows", "386"}, {"windows", "amd64"}, {"windows", "arm64"},
}

func writeContract(dir, version string) error {
	return os.WriteFile(filepath.Join(dir, ContractFile), fmt.Appendf(nil, "version=%s\ncontract=%d\nsdk_name=%s\n", version, Contract, SDKName), 0644)
}

// MiniOptions configures WriteMini.
type MiniOptions struct {
	// RulesGo is the name under which the generated BUILD files load rules_go.
	RulesGo string
	// Version is the ddtest version that the contract file records.
	Version string
}

// WriteMini writes, into the empty or missing directory dir, the Mini
// packages that miniRoots import from src, a file system rooted at Mini's
// module, and a BUILD.bazel file for each one: a go_library named after its
// directory, with a go_default_library alias, as Gazelle names the targets of
// go_deps repositories, so that the repository can stand in for them.
func WriteMini(dir string, src fs.FS, options MiniOptions) error {
	if options.RulesGo == "" {
		return errors.New("the name of rules_go's repository is empty")
	}
	packages, err := miniPackages(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "LICENSE.Apache", "LICENSE.BSD3", "LICENSE-3rdparty.csv", "NOTICE"} {
		if err := copyFSFile(src, name, filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "BUILD.bazel"), []byte("# Mini, the runtime of ddtest instrument, written by ddtest instrument bazel.\n"), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "REPO.bazel"), nil, 0644); err != nil {
		return err
	}
	for _, rel := range sortedKeys(packages) {
		p := packages[rel]
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		for _, file := range p.files {
			if err := copyFSFile(src, path.Join(rel, file), filepath.Join(target, file)); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(target, "BUILD.bazel"), []byte(p.build(rel, options.RulesGo)), 0644); err != nil {
			return err
		}
	}
	return writeContract(dir, options.Version)
}

// miniPackage is one of Mini's packages: the files that some platform builds
// and, per platform, the Mini packages that it imports.
type miniPackage struct {
	files []string
	deps  map[string][]string // platform -> sorted package directories
}

// miniPackages returns, by directory relative to the module, every package
// that miniRoots import on some platform.
func miniPackages(src fs.FS) (map[string]*miniPackage, error) {
	minors, err := goMinors(src)
	if err != nil {
		return nil, err
	}
	packages := map[string]*miniPackage{}
	queue := slices.Clone(miniRoots)
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		if packages[rel] != nil {
			continue
		}
		p := &miniPackage{deps: map[string][]string{}}
		files := map[string]bool{}
		for _, platform := range platforms {
			deps, found := map[string]bool{}, false
			for _, context := range miniContexts(src, platform.goos, platform.goarch, minors) {
				pkg, err := context.ImportDir(rel, 0)
				var noGo *build.NoGoError
				if errors.As(err, &noGo) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("%s on %s/%s: %w", rel, platform.goos, platform.goarch, err)
				}
				if len(pkg.EmbedPatterns) != 0 || len(pkg.CgoFiles) != 0 {
					return nil, fmt.Errorf("%s: embedded files and cgo are not supported in Mini's Bazel repository", rel)
				}
				for _, group := range [][]string{pkg.GoFiles, pkg.SFiles, pkg.HFiles} {
					for _, file := range group {
						files[file] = true
					}
				}
				for _, imported := range pkg.Imports {
					if dep, ok := strings.CutPrefix(imported, MiniModule+"/"); ok {
						deps[dep] = true
						queue = append(queue, dep)
					}
				}
				found = true
			}
			if found {
				p.deps[platform.goos+"_"+platform.goarch] = sortedKeys(deps)
			}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no Go files for any platform", rel)
		}
		p.files = sortedKeys(files)
		packages[rel] = p
	}
	return packages, nil
}

// build returns the package's BUILD.bazel file. Dependencies that every
// platform has are plain; the others are selected by platform.
func (p *miniPackage) build(rel, rulesGo string) string {
	name := path.Base(rel)
	common := map[string]int{}
	for _, deps := range p.deps {
		for _, dep := range deps {
			common[dep]++
		}
	}
	var plain []string
	for dep, n := range common {
		if n == len(p.deps) {
			plain = append(plain, dep)
		}
	}
	sort.Strings(plain)
	var b strings.Builder
	fmt.Fprintf(&b, "load(\"@%s//go:def.bzl\", \"go_library\")\n\ngo_library(\n    name = %q,\n    srcs = [\n", rulesGo, name)
	for _, file := range p.files {
		fmt.Fprintf(&b, "        %q,\n", file)
	}
	fmt.Fprintf(&b, "    ],\n    importpath = %q,\n    visibility = [\"//visibility:public\"],\n", MiniModule+"/"+rel)
	selected := map[string][]string{}
	for platform, deps := range p.deps {
		var extra []string
		for _, dep := range deps {
			if !slices.Contains(plain, dep) {
				extra = append(extra, dep)
			}
		}
		if len(extra) != 0 {
			selected[platform] = extra
		}
	}
	if len(plain) != 0 || len(selected) != 0 {
		b.WriteString("    deps = [\n")
		for _, dep := range plain {
			fmt.Fprintf(&b, "        %q,\n", label(dep))
		}
		b.WriteString("    ]")
		if len(selected) != 0 {
			b.WriteString(" + select({\n")
			for _, platform := range sortedKeys(selected) {
				fmt.Fprintf(&b, "        \"@%s//go/platform:%s\": [\n", rulesGo, platform)
				for _, dep := range selected[platform] {
					fmt.Fprintf(&b, "            %q,\n", label(dep))
				}
				b.WriteString("        ],\n")
			}
			b.WriteString("        \"//conditions:default\": [],\n    })")
		}
		b.WriteString(",\n")
	}
	fmt.Fprintf(&b, ")\n\nalias(\n    name = \"go_default_library\",\n    actual = \":%s\",\n    visibility = [\"//visibility:public\"],\n)\n", name)
	return b.String()
}

// label is a package's target in buildifier's short form: the target has the
// directory's name.
func label(rel string) string {
	return "//" + rel
}

// goMinors returns the Go releases whose release tags decide Mini's files:
// from its go directive to the release that built this executable, the
// newest that a ddtest release supports.
func goMinors(src fs.FS) ([]int, error) {
	data, err := fs.ReadFile(src, "go.mod")
	if err != nil {
		return nil, fmt.Errorf("Mini's go.mod: %w", err)
	}
	first := 0
	for _, line := range strings.Split(string(data), "\n") {
		if version, ok := strings.CutPrefix(strings.TrimSpace(line), "go 1."); ok {
			minor, _, _ := strings.Cut(version, ".")
			first, _ = strconv.Atoi(minor)
		}
	}
	tags := build.Default.ReleaseTags
	last, _ := strconv.Atoi(strings.TrimPrefix(tags[len(tags)-1], "go1."))
	if first == 0 || first > last {
		return nil, fmt.Errorf("Mini's go.mod has no go directive from go 1.1 to go 1.%d", last)
	}
	var minors []int
	for minor := first; minor <= last; minor++ {
		minors = append(minors, minor)
	}
	return minors, nil
}

// miniContexts returns the build contexts of goos and goarch for each Go
// release in minors, with and without the race detector. rules_go applies the
// build constraints itself, so the repository carries the files and
// dependencies of every one of them.
func miniContexts(src fs.FS, goos, goarch string, minors []int) []build.Context {
	var contexts []build.Context
	for _, minor := range minors {
		for _, race := range []bool{false, true} {
			context := fsContext(src, goos, goarch)
			context.ReleaseTags = nil
			for n := 1; n <= minor; n++ {
				context.ReleaseTags = append(context.ReleaseTags, fmt.Sprintf("go1.%d", n))
			}
			if race {
				context.BuildTags = []string{"race"}
			}
			contexts = append(contexts, context)
		}
	}
	return contexts
}

// fsContext is build.Default for goos and goarch, without cgo, reading src.
func fsContext(src fs.FS, goos, goarch string) build.Context {
	context := build.Default
	context.GOOS, context.GOARCH, context.CgoEnabled = goos, goarch, false
	context.GOROOT, context.GOPATH, context.Dir = "", "", ""
	context.JoinPath = path.Join
	context.IsAbsPath = path.IsAbs
	context.SplitPathList = func(string) []string { return nil }
	context.HasSubdir = func(root, dir string) (string, bool) { return "", false }
	context.IsDir = func(name string) bool {
		info, err := fs.Stat(src, name)
		return err == nil && info.IsDir()
	}
	context.ReadDir = func(dir string) ([]fs.FileInfo, error) {
		entries, err := fs.ReadDir(src, dir)
		if err != nil {
			return nil, err
		}
		infos := make([]fs.FileInfo, 0, len(entries))
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			infos = append(infos, info)
		}
		return infos, nil
	}
	context.OpenFile = func(name string) (io.ReadCloser, error) { return src.Open(name) }
	return context
}

func copyFSFile(src fs.FS, name, dst string) error {
	data, err := fs.ReadFile(src, name)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// SDKOptions configures WriteSDK.
type SDKOptions struct {
	// GOROOT is the official SDK to instrument; Compiler is the ddtest
	// executable for the SDK's platform, which becomes its compiler.
	GOROOT, Compiler string
	// PanicHook edits the runtime as ddtest instrument's --panic-hook does.
	PanicHook bool
	// Version is the ddtest version that the contract records.
	Version string
}

// WriteSDK writes into dir a copy of an SDK whose compiler is options'
// Compiler, with the SDK's own compiler next to it as compile.real. Files are
// hard links when the file system allows, and copies otherwise.
func WriteSDK(dir string, options SDKOptions) error {
	tools, err := filepath.Glob(filepath.Join(options.GOROOT, "pkg", "tool", "*_*"))
	if err != nil {
		return err
	}
	var toolDir string
	for _, candidate := range tools {
		if _, err := os.Stat(filepath.Join(candidate, executable(candidate, "compile"))); err == nil {
			if toolDir != "" {
				return fmt.Errorf("%s has compilers for more than one platform", options.GOROOT)
			}
			toolDir = candidate
		}
	}
	if toolDir == "" {
		return fmt.Errorf("%s is not a Go SDK: it has no pkg/tool/<goos_goarch>/compile", options.GOROOT)
	}
	if _, err := os.Stat(filepath.Join(options.GOROOT, "VERSION")); err != nil {
		return fmt.Errorf("%s is not a Go SDK: %w", options.GOROOT, err)
	}
	compiler := executable(toolDir, "compile")
	relativeTools, err := filepath.Rel(options.GOROOT, toolDir)
	if err != nil {
		return err
	}
	// The copy follows symbolic links: rules_go's local and wrapped SDKs link
	// their top-level directories to another GOROOT, and distributions link
	// some of theirs.
	var copyDir func(source, rel string) error
	copyDir = func(source, rel string) error {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0755); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := filepath.Join(rel, entry.Name())
			resolved := filepath.Join(source, entry.Name())
			info, err := entry.Info()
			// Links, and on Windows junctions, which Go reports as irregular.
			if err == nil && !entry.IsDir() && !entry.Type().IsRegular() {
				if resolved, err = filepath.EvalSymlinks(resolved); err == nil {
					info, err = os.Stat(resolved)
				}
			}
			switch {
			case err != nil:
			case info.IsDir():
				err = copyDir(resolved, name)
			case name == filepath.Join(relativeTools, compiler):
				err = linkOrCopy(resolved, filepath.Join(dir, relativeTools, executable(toolDir, "compile.real")))
			default:
				err = linkOrCopy(resolved, filepath.Join(dir, name))
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	root, err := filepath.EvalSymlinks(options.GOROOT)
	if err != nil {
		return err
	}
	if err := copyDir(root, "."); err != nil {
		return err
	}
	if err := copyExecutable(options.Compiler, filepath.Join(dir, relativeTools, compiler)); err != nil {
		return err
	}
	config, err := json.Marshal(map[string]any{"version": options.Version, "contract": Contract, "panicHook": options.PanicHook})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, relativeTools, CompilerConfig), append(config, '\n'), 0644); err != nil {
		return err
	}
	return writeContract(dir, options.Version)
}

// executable returns name as the platform of tools, a pkg/tool directory,
// names an executable.
func executable(tools, name string) string {
	if strings.HasPrefix(filepath.Base(tools), "windows_") {
		return name + ".exe"
	}
	return name
}

func linkOrCopy(source, target string) error {
	if err := os.Link(source, target); err == nil {
		return nil
	}
	return copyExecutable(source, target)
}

func copyExecutable(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
