package runner

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPackagePatternSemantics(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "module")
	pkg := func(dir, path string, standard bool) *goPackage {
		return &goPackage{Dir: filepath.Join(cwd, filepath.FromSlash(dir)), ImportPath: path, Standard: standard}
	}
	root := pkg(".", "example.com/m", false)
	sub := pkg("sub", "example.com/m/sub", false)
	vendored := pkg("vendor/go.uber.org/goleak", "go.uber.org/goleak", false)
	outside := pkg("../cache/go.uber.org/goleak@v1.3.0", "go.uber.org/goleak", false)
	standard := &goPackage{Dir: filepath.Join(filepath.Dir(cwd), "goroot", "src", "testing"), ImportPath: "testing", Standard: true}
	for _, tc := range []struct {
		pattern string
		p       *goPackage
		want    bool
	}{
		{".", root, true},
		{".", sub, false},
		{"./...", root, true},
		{"./...", sub, true},
		{"./...", vendored, false},
		{"./...", outside, false},
		{"./...", standard, false},
		{"./vendor/...", vendored, true},
		{"./vendor/go.uber.org/...", vendored, true},
		{"./sub", sub, true},
		{"./sub/...", sub, true},
		{"..", outside, false},
		{"../...", outside, true},
		{"../...", standard, true},
		{"../cache/...", standard, false},
		{"all", outside, true},
		{"std", standard, true},
		{"std", root, false},
		{"cmd", standard, false},
		{"tool", root, false},
		{"testing", standard, true},
		{"example.com/m/...", root, true},
		{"example.com/m/...", sub, true},
		{"example.com/...", vendored, false},
		{"go.uber.org/...", outside, true},
		{"go.uber.org/...", vendored, true},
		{"...", standard, true},
		{"...", vendored, true},
		{filepath.ToSlash(cwd) + "/...", sub, false},
	} {
		if got := matchPackagePattern(tc.pattern, cwd, tc.p); got != tc.want {
			t.Errorf("%q matches %s (%s) = %v, want %v", tc.pattern, tc.p.ImportPath, tc.p.Dir, got, tc.want)
		}
	}
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"net/...", "net", true},
		{"net/...", "net/http", true},
		{"net/...", "netchan", false},
		{"x/vendor/...", "x/vendor/y", true},
		{"x/...", "x/vendor/y", false},
		{"x/.../y", "x/vendor/y", false},
		{"x/vendor/...", "x/vendor", true},
		{"mycode/vendor/...", "mycode/vendor/foo/vendor", true},
		{"mycode/vendor/...", "mycode/vendor/foo/vendor/bar", false},
		{"x/vendor/y/...", "x/vendor/y/z/vendor", true},
		{"x/vendor/y/...", "x/vendor/y/vendor/z", false},
		{".../vendor/...", "x/vendor/y/z", true},
		{"cmd/...", "cmd/vendor", true},
	} {
		if got := matchImportPattern(tc.pattern, tc.name); got != tc.want {
			t.Errorf("import pattern %q matches %q = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// TestPackagePatternsMatchGoBuildFlags checks the matcher against the
// toolchain's own per-package flag selection, observed with go build -n.
func TestPackagePatternsMatchGoBuildFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes go build -n -a for each pattern")
	}
	// With an explicit environment, os/exec does not set PWD, so the go
	// commands below report directories under the resolved path (macOS's
	// /var is a link to /private/var). Patterns use the same spelling.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.21\n",
		"root.go":    "package m\n\nimport _ \"example.com/m/sub\"\n",
		"sub/sub.go": "package sub\n\nimport _ \"testing\"\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	list := exec.Command("go", "list", "-deps", "-json=Dir,ImportPath,Standard,Module", "./...")
	list.Dir, list.Env = dir, env
	output, err := list.Output()
	if err != nil {
		t.Fatal(err)
	}
	var packages []goPackage
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var p goPackage
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, p)
	}
	compileLine := regexp.MustCompile(`(?m)^.*[/\\]compile(?:\.exe)?"? -o .* -p (\S+) .*$`)
	selected := func(t *testing.T, args ...string) map[string]bool {
		build := exec.Command("go", append([]string{"build", "-n", "-a"}, args...)...)
		build.Dir, build.Env = dir, env
		output, err := build.CombinedOutput()
		if err != nil {
			t.Fatalf("go build %v: %v\n%s", args, err, output)
		}
		probed := map[string]bool{}
		for _, match := range compileLine.FindAllStringSubmatch(string(output), -1) {
			probed[match[1]] = strings.Contains(match[0], "-ddtoprobe")
		}
		for _, required := range []string{"example.com/m/sub", "testing"} {
			if _, ok := probed[required]; !ok {
				t.Fatalf("go build -n %v did not show compiling %s", args, required)
			}
		}
		return probed
	}
	compare := func(t *testing.T, probed map[string]bool, match func(*goPackage) bool) {
		compared := 0
		for i := range packages {
			p := &packages[i]
			want, built := probed[p.ImportPath]
			if !built {
				continue
			}
			compared++
			if got := match(p); got != want {
				t.Errorf("%s (%s): matched=%v, go applied flag=%v", p.ImportPath, p.Dir, got, want)
			}
		}
		if compared < 2 {
			t.Fatalf("compared only %d packages", compared)
		}
	}
	patterns := []string{".", "./...", "./sub", "./sub/...", "..", "../...", "testing", "std", "all", "work", "...", "example.com/m/...", filepath.ToSlash(dir) + "/..."}
	for _, pattern := range patterns {
		t.Run("qualified "+pattern, func(t *testing.T) {
			t.Parallel()
			probed := selected(t, "-gcflags="+pattern+"=-ddtoprobe", "./...")
			compare(t, probed, func(p *goPackage) bool { return matchPackagePattern(pattern, dir, p) })
		})
	}
	// Unqualified flags apply only to command-line packages.
	for _, target := range []string{"./...", "./sub", "."} {
		t.Run("unqualified "+target, func(t *testing.T) {
			t.Parallel()
			probed := selected(t, "-gcflags=-ddtoprobe", target)
			compare(t, probed, func(p *goPackage) bool { return matchPackagePattern(target, dir, p) })
		})
	}
}
