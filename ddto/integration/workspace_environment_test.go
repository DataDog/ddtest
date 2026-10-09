package integration

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// userCacheEnv points os.UserCacheDir at dir for ddtest's persistent vendor
// workspaces. Go's own build cache and module path stay where they were.
func userCacheEnv(t *testing.T, dir string) []string {
	t.Helper()
	out, stderr, code := command(t, ".", testEnv(), "go", "env", "GOCACHE", "GOMODCACHE", "GOPATH")
	values := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(values) != 3 {
		t.Fatalf("go env: %s%s", out, stderr)
	}
	env := []string{"GOCACHE=" + strings.TrimSpace(values[0]), "GOMODCACHE=" + strings.TrimSpace(values[1]), "GOPATH=" + strings.TrimSpace(values[2])}
	switch runtime.GOOS {
	case "windows":
		return append(env, "LocalAppData="+dir)
	case "darwin", "ios":
		return append(env, "HOME="+dir)
	case "plan9":
		return append(env, "home="+dir)
	}
	return append(env, "XDG_CACHE_HOME="+dir)
}

// vendorWorkspaceDirs lists the persistent workspace directories ddtest stored
// below dir.
func vendorWorkspaceDirs(t *testing.T, dir string) []string {
	t.Helper()
	var workspaces []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "vendor-workspaces" {
			children, err := os.ReadDir(path)
			for _, child := range children {
				if child.IsDir() { // Skip each workspace's lock file.
					workspaces = append(workspaces, filepath.Join(path, child.Name()))
				}
			}
			if err != nil {
				return err
			}
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspaces
}

func symlinksAvailable(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	return os.Symlink(dir, filepath.Join(dir, "link")) == nil
}

// A temporary workspace belongs to ddtest's build. Tests, dependencies that
// initialize before testing, and the go commands they start must see the
// caller's GOWORK and GOFLAGS, as with native go test.
func TestMiniTemporaryWorkspaceRestoresGoEnvironment(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, layout := range []string{"older-module", "vendor", "workspace"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			client := filepath.Join(root, "client")
			helper := filepath.Join(root, "helper")
			nested := filepath.Join(client, "testdata", "nested")
			for _, dir := range []string{nested, helper} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			// The helper does not import testing, so Go can initialize it first.
			writeBuildFixture(t, helper, map[string]string{"go.mod": "module example.com/environmenthelper\ngo 1.21\n", "helper.go": `package environmenthelper
import ("os/exec";"strings")
var Value, InitMain = 1, ""
func init() { out, _ := exec.Command("go", "list", "-m").Output(); InitMain = strings.TrimSpace(string(out)) }
`})
			writeBuildFixture(t, nested, map[string]string{"go.mod": "module example.com/nested\ngo 1.21\n", "main.go": "package main\nfunc main(){}\n"})
			writeBuildFixture(t, client, map[string]string{
				"go.mod": fmt.Sprintf("module example.com/environment\ngo 1.21\nrequire example.com/environmenthelper v0.0.0\nreplace example.com/environmenthelper => %q\n", filepath.ToSlash(helper)),
				"environment_test.go": `package environment
import ("fmt";"os";"os/exec";"strings";"testing";"example.com/environmenthelper")
func TestEnvironment(t *testing.T) {
 fmt.Printf("ENV INIT_MAIN=%q\n", environmenthelper.InitMain)
 for _, name := range []string{"GOWORK", "GOFLAGS", "DDTEST_ORIGINAL_GOWORK", "DDTEST_ORIGINAL_GOFLAGS"} {
  value, ok := os.LookupEnv(name)
  fmt.Printf("ENV %s=%t:%q\n", name, ok, value)
 }
 main, err := exec.Command("go", "list", "-m").CombinedOutput()
 fmt.Printf("ENV MAIN=%q %t\n", strings.TrimSpace(string(main)), err == nil)
 build := exec.Command("go", "build", "-o", os.DevNull, ".")
 build.Dir = "testdata/nested"
 fmt.Printf("ENV NESTED=%t\n", build.Run() == nil)
}
`,
			})
			env := testEnv("GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false")
			env = append(env, userCacheEnv(t, t.TempDir())...)
			switch layout {
			case "vendor":
				if out, stderr, code := command(t, client, env, "go", "mod", "vendor"); code != 0 {
					t.Fatal(out, stderr)
				}
			case "workspace":
				writeBuildFixture(t, root, map[string]string{"go.work": "go 1.21\nuse ./client\n"})
			}
			lines := func(out string) []string {
				var selected []string
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, "ENV ") {
						selected = append(selected, strings.TrimSpace(line))
					}
				}
				return selected
			}
			native, nativeErr, nativeCode := command(t, client, env, "go", "test", "-v", "-count=1", ".")
			if nativeCode != 0 {
				t.Fatalf("native: %s%s", native, nativeErr)
			}
			out, stderr, code := command(t, client, env, driver, "test", "-v", "-count=1", ".")
			if code != 0 {
				t.Fatalf("exit=%d\n%s%s", code, out, stderr)
			}
			if want, got := lines(native), lines(out); len(want) != 7 || !slices.Equal(got, want) {
				t.Fatalf("test environment changed:\nnative: %q\nddtest: %q", want, got)
			}
		})
	}
}

// Tools that ddtest chains belong to the build: unlike test processes, a user
// -toolexec must keep the temporary workspace that makes Mini resolvable.
func TestMiniTemporaryWorkspaceKeepsBuildToolEnvironment(t *testing.T) {
	driver := sharedDriver(t, "..")
	root := t.TempDir()
	wrapper, client := filepath.Join(root, "wrapper"), filepath.Join(root, "client")
	for _, dir := range []string{wrapper, client} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBuildFixture(t, wrapper, map[string]string{
		"go.mod": "module example.com/toolwrapper\ngo 1.21\n",
		"main.go": `package main
import ("fmt";"os";"os/exec";"path/filepath";"strings")
func main() {
 tool := os.Args[1]
 if strings.TrimSuffix(filepath.Base(tool), ".exe") == "compile" && strings.HasPrefix(os.Getenv("TOOLEXEC_IMPORTPATH"), "example.com/toolclient") {
  out, err := exec.Command("go", "list", "github.com/tonyredondo/dd-ci-testing-poc/testopt").CombinedOutput()
  if err != nil { fmt.Fprintf(os.Stderr, "wrapper go list failed: %v\n%s", err, out); os.Exit(1) }
  if err := os.WriteFile(os.Getenv("TOOL_MARKER"), out, 0600); err != nil { panic(err) }
 }
 cmd := exec.Command(tool, os.Args[2:]...)
 cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
 if err := cmd.Run(); err != nil {
  if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }
  panic(err)
 }
}
`,
	})
	writeBuildFixture(t, client, map[string]string{
		"go.mod":         "module example.com/toolclient\ngo 1.21\n",
		"client_test.go": "package toolclient\nimport \"testing\"\nfunc TestTool(t *testing.T){}\n",
	})
	env := testEnv("GOWORK=off", "GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false")
	binary := filepath.Join(root, executableName("toolwrapper"))
	if out, stderr, code := command(t, wrapper, env, "go", "build", "-o", binary, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	marker := filepath.Join(root, "marker")
	// Covering testing makes ddtest's selective tool chain the user's wrapper.
	args := []string{"test", "-count=1", "-coverpkg=testing", "-toolexec=" + quoteToolArgument(t, binary), "."}
	out, stderr, code := command(t, client, append(env, "TOOL_MARKER="+marker), driver, args...)
	if code != 0 {
		t.Fatalf("exit=%d\n%s%s", code, out, stderr)
	}
	if data, err := os.ReadFile(marker); err != nil || !strings.Contains(string(data), "testopt") {
		t.Fatalf("wrapper did not resolve Mini through the build workspace: %q %v", data, err)
	}
}

// Linked vendor workspaces live at a stable path, so Go's build cache reuses
// unchanged vendored packages, and later vendor edits are used.
func TestMiniVendorWorkspaceLinksAndReusesBuildCache(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	helper := t.TempDir()
	cache := t.TempDir()
	writeBuildFixture(t, helper, map[string]string{"go.mod": "module example.com/linkedhelper\ngo 1.21\n", "helper.go": "package linkedhelper\nconst Value=1\n"})
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":         fmt.Sprintf("module example.com/linkedvendor\ngo 1.21\nrequire example.com/linkedhelper v0.0.0\nreplace example.com/linkedhelper => %q\n", filepath.ToSlash(helper)),
		"client_test.go": "package linkedvendor\nimport(\"fmt\";\"testing\";\"example.com/linkedhelper\")\nfunc TestLinked(t *testing.T){fmt.Printf(\"VALUE=%d\\n\",linkedhelper.Value)}\n",
	})
	env := append(testEnv("GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), userCacheEnv(t, cache)...)
	if out, stderr, code := command(t, dir, env, "go", "mod", "vendor"); code != 0 {
		t.Fatal(out, stderr)
	}
	patched := filepath.Join(dir, "vendor", "example.com", "linkedhelper", "helper.go")
	for i, value := range []int{7, 7, 8} {
		if i != 1 {
			if err := os.WriteFile(patched, []byte(fmt.Sprintf("package linkedhelper\nconst Value=%d\n", value)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		out, stderr, code := command(t, dir, env, driver, "test", "-x", "-v", "-count=1", ".")
		if code != 0 {
			t.Fatalf("run %d exit=%d\n%s%s", i, code, out, stderr)
		}
		if !strings.Contains(out, fmt.Sprintf("VALUE=%d", value)) {
			t.Fatalf("run %d did not use the vendored source with value %d:\n%s", i, value, out)
		}
		var compiled bool
		for _, line := range compilerTraceLines(stderr) {
			compiled = compiled || strings.Contains(line, "-p example.com/linkedhelper")
		}
		if i == 1 && compiled {
			t.Fatalf("unchanged vendored package was recompiled:\n%s", stderr)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "vendor", "modules.txt")); err != nil || strings.Contains(string(data), "## workspace") {
		t.Fatalf("client vendor manifest changed: %q %v", data, err)
	}
	// The edit before the last run changed the file's time, so it has its own
	// workspace.
	workspaces := vendorWorkspaceDirs(t, cache)
	if len(workspaces) != 2 {
		t.Fatalf("expected a workspace before and after the edit: %q", workspaces)
	}
	if !hardLinksAvailable(t, dir, cache) {
		return
	}
	original, err := os.Stat(patched)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range workspaces {
		if info, err := os.Lstat(filepath.Join(workspace, "vendor", "example.com", "linkedhelper", "helper.go")); err == nil && os.SameFile(info, original) {
			return
		}
	}
	t.Fatalf("no vendor workspace shares the edited source: %q", workspaces)
}

// hardLinksAvailable reports whether a file in from can be hard-linked into to.
func hardLinksAvailable(t *testing.T, from, to string) bool {
	t.Helper()
	file := filepath.Join(from, "hard-link-probe")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file)
	link := filepath.Join(to, "hard-link-probe")
	defer os.Remove(link)
	return os.Link(file, link) == nil
}

// ddtest must use the vendored sources that go test uses. Go selects a vendor
// directory by mode: a go mod vendor tree beside go.work, or a go work vendor
// tree under GOWORK=off, is ignored. Relative local replacements, including a
// go.work replacement of one version, must survive the move to a temporary
// workspace.
func TestMiniVendorSelectionMatchesGo(t *testing.T) {
	driver := sharedDriver(t, "..")
	for _, tc := range []struct {
		name string
		// work is written in workAt ("client" or the parent "root"); vendor is
		// produced there by go mod vendor ("module") or go work vendor.
		work, workAt, vendor      string
		clientGo                  string
		absolute, workOff, spaced bool
		// exact names a module that a versioned replacement also replaces,
		// outside the project directory: in go.work where it says EXACT,
		// otherwise in go.mod.
		exact string
		// linkedVendor moves the vendor directory and links to it.
		linkedVendor bool
	}{
		// Absolute replacements keep a misused vendor tree consistent, so the
		// wrong sources would run silently instead of failing.
		{name: "module-vendor-beside-go1.21-workspace", work: "go 1.21\nuse .\n", workAt: "client", vendor: "module", absolute: true},
		{name: "module-vendor-beside-go1.25-workspace", work: "go 1.25.0\nuse .\n", workAt: "client", vendor: "module", absolute: true},
		{name: "module-vendor-beside-workspace-relative-replacements", work: "go 1.21\nuse .\n", workAt: "client", vendor: "module"},
		{name: "module-vendor-with-relative-replacements", vendor: "module"},
		{name: "workspace-vendor-with-relative-replacements", work: "go 1.22\nuse ./client\nreplace example.com/other => ./other\n", workAt: "root", vendor: "workspace"},
		{name: "workspace-vendor-with-one-version-replaced-by-go-work", work: "go 1.22\nuse ./client\nreplace example.com/helper v0.0.1 => ./other\n", workAt: "root", vendor: "workspace"},
		{name: "workspace-vendor-ignored-with-gowork-off", work: "go 1.25.0\nuse .\n", workAt: "client", vendor: "workspace", clientGo: "1.25.0", workOff: true},
		// modules.txt cannot represent whitespace; moved relative paths must not
		// gain the spaces of the project's directory.
		{name: "module-vendor-in-directory-with-spaces", vendor: "module", spaced: true},
		{name: "workspace-vendor-in-directory-with-spaces", work: "go 1.22\nuse ./client\nreplace example.com/other => ./other\n", workAt: "root", vendor: "workspace", spaced: true},
		// The whitespace alias of every version must not hide another version's
		// replacement.
		{name: "module-vendor-in-directory-with-spaces-and-exact-replacement", vendor: "module", spaced: true, exact: "helper"},
		{name: "workspace-vendor-in-directory-with-spaces-and-exact-replacement", work: "go 1.22\nuse ./client\nreplace example.com/other => ./other\nreplace example.com/other v0.0.0 => EXACT\n", workAt: "root", vendor: "workspace", spaced: true, exact: "other"},
		// Go follows a vendor directory that is itself a link.
		{name: "module-vendor-through-link", vendor: "module", linkedVendor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.spaced {
				root = filepath.Join(root, "my project")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			client := filepath.Join(root, "client")
			for _, name := range []string{"helper", "other"} {
				dir := filepath.Join(root, name)
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				writeBuildFixture(t, dir, map[string]string{"go.mod": "module example.com/" + name + "\ngo 1.21\n", name + ".go": "package " + name + "\nconst Value=1\n"})
			}
			if err := os.Mkdir(client, 0700); err != nil {
				t.Fatal(err)
			}
			exact := ""
			if tc.exact != "" {
				dir := filepath.Join(t.TempDir(), "exact")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				writeBuildFixture(t, dir, map[string]string{"go.mod": "module example.com/" + tc.exact + "\ngo 1.21\n", tc.exact + ".go": "package " + tc.exact + "\nconst Value=1\n"})
				exact = strconv.Quote(filepath.ToSlash(dir))
			}
			if tc.linkedVendor && !symlinksAvailable(t) {
				t.Skip("symlinks unavailable")
			}
			local := func(name string) string {
				if tc.absolute {
					return strconv.Quote(filepath.ToSlash(filepath.Join(root, name)))
				}
				return "../" + name
			}
			clientGo := tc.clientGo
			if clientGo == "" {
				clientGo = "1.21"
			}
			mod := "module example.com/vendorselection\ngo " + clientGo + "\nrequire (\nexample.com/helper v0.0.0\nexample.com/other v0.0.0\n)\nreplace example.com/helper => " + local("helper") + "\n"
			if !strings.Contains(tc.work, "example.com/other =>") {
				mod += "replace example.com/other => " + local("other") + "\n"
			}
			if tc.exact != "" && !strings.Contains(tc.work, "EXACT") {
				mod += "replace example.com/" + tc.exact + " v0.0.0 => " + exact + "\n"
			}
			writeBuildFixture(t, client, map[string]string{
				"go.mod":         mod,
				"client_test.go": "package vendorselection\nimport(\"fmt\";\"testing\";\"example.com/helper\";\"example.com/other\")\nfunc TestValues(t *testing.T){fmt.Printf(\"VALUES=%d,%d\\n\",helper.Value,other.Value)}\n",
			})
			env := append(testEnv("GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), userCacheEnv(t, t.TempDir())...)
			workDir := root
			if tc.workAt == "client" {
				workDir = client
			}
			vendor := filepath.Join(client, "vendor")
			if tc.vendor == "module" {
				if out, stderr, code := command(t, client, append(env, "GOWORK=off"), "go", "mod", "vendor"); code != 0 {
					t.Fatal(out, stderr)
				}
			}
			if tc.work != "" {
				writeBuildFixture(t, workDir, map[string]string{"go.work": strings.ReplaceAll(tc.work, "EXACT", exact)})
			}
			if tc.vendor == "workspace" {
				if out, stderr, code := command(t, workDir, env, "go", "work", "vendor"); code != 0 {
					t.Fatal(out, stderr)
				}
				vendor = filepath.Join(workDir, "vendor")
			}
			// Patched vendored copies make the selected source observable.
			for _, name := range []string{"helper", "other"} {
				path := filepath.Join(vendor, "example.com", name, name+".go")
				if err := os.WriteFile(path, []byte("package "+name+"\nconst Value=7\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.linkedVendor {
				shared := filepath.Join(root, "shared-vendor")
				if err := os.Rename(vendor, shared); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(shared, vendor); err != nil {
					t.Fatal(err)
				}
			}
			if tc.work == "" || tc.workOff {
				env = append(env, "GOWORK=off")
			}
			values := func(out string) string {
				for _, line := range strings.Split(out, "\n") {
					if strings.HasPrefix(line, "VALUES=") {
						return strings.TrimSpace(line)
					}
				}
				return ""
			}
			native, nativeErr, nativeCode := command(t, client, env, "go", "test", "-v", "-count=1", ".")
			if nativeCode != 0 {
				t.Fatalf("native: %s%s", native, nativeErr)
			}
			out, stderr, code := command(t, client, env, driver, "test", "-v", "-count=1", ".")
			if code != 0 {
				t.Fatalf("exit=%d\n%s%s", code, out, stderr)
			}
			if want, got := values(native), values(out); want == "" || got != want {
				t.Fatalf("selected sources differ: native=%q ddtest=%q", want, got)
			}
		})
	}
}

// The vendor workspace must present the vendored tree as go test sees it.
// Wildcard patterns must select the same packages: cmd/go ignores linked
// directories when it expands patterns, so a failing vendored test would
// otherwise disappear from the run. //go:embed must accept the same files:
// cmd/go rejects linked files and skips them in embedded directories.
func TestMiniVendorTreeMatchesGo(t *testing.T) {
	driver := sharedDriver(t, "..")
	root := t.TempDir()
	client, helper := filepath.Join(root, "client"), filepath.Join(root, "helper")
	for _, dir := range []string{client, filepath.Join(helper, "assets")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBuildFixture(t, helper, map[string]string{
		"go.mod":         "module example.com/patternhelper\ngo 1.21\n",
		"helper.go":      "package patternhelper\nimport \"embed\"\nconst Value=1\n//go:embed data.txt\nvar Data string\n//go:embed assets\nvar Assets embed.FS\n",
		"data.txt":       "embedded file",
		"assets/one.txt": "embedded directory",
	})
	writeBuildFixture(t, client, map[string]string{
		"go.mod":         "module example.com/patternclient\ngo 1.21\nrequire example.com/patternhelper v0.0.0\nreplace example.com/patternhelper => ../helper\n",
		"client_test.go": "package patternclient\nimport(\"fmt\";\"testing\";\"example.com/patternhelper\")\nfunc TestClient(t *testing.T){one, err := patternhelper.Assets.ReadFile(\"assets/one.txt\");fmt.Printf(\"EMBED=%q,%q,%v\\n\", patternhelper.Data, one, err)}\n",
	})
	env := append(testEnv("GOWORK=off", "GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), userCacheEnv(t, t.TempDir())...)
	if out, stderr, code := command(t, client, env, "go", "mod", "vendor"); code != 0 {
		t.Fatal(out, stderr)
	}
	vendored := filepath.Join(client, "vendor", "example.com", "patternhelper", "helper_test.go")
	if err := os.WriteFile(vendored, []byte("package patternhelper\nimport \"testing\"\nfunc TestVendored(t *testing.T){t.Fatal(\"vendored test ran\")}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	results := func(out string) []string {
		var lines []string
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && (fields[0] == "ok" || fields[0] == "FAIL" || fields[0] == "?") {
				lines = append(lines, fields[0]+" "+fields[1])
			} else if strings.HasPrefix(line, "EMBED=") {
				lines = append(lines, strings.TrimSpace(line))
			}
		}
		slices.Sort(lines)
		return lines
	}
	args := []string{"test", "-v", "-count=1", ".", "example.com/patternhelper/..."}
	native, nativeErr, nativeCode := command(t, client, env, "go", args...)
	out, stderr, code := command(t, client, env, driver, args...)
	if want, got := results(native), results(out); code != nativeCode || !slices.Equal(got, want) || !slices.Contains(want, "FAIL example.com/patternhelper") ||
		!slices.Contains(want, `EMBED="embedded file","embedded directory",<nil>`) {
		t.Fatalf("native exit=%d %q\n%s%s\nddtest exit=%d %q\n%s%s", nativeCode, want, native, nativeErr, code, got, out, stderr)
	}
}

// Go keys cached results on its own environment, which holds ddtest's stable
// vendor workspace, while tests see the caller's values. A result cached under
// GOWORK=off must not pass for a caller without GOWORK; an unchanged caller
// still reuses its cached result.
func TestMiniTestCacheFollowsCallerEnvironment(t *testing.T) {
	driver := sharedDriver(t, "..")
	root := t.TempDir()
	client, helper := filepath.Join(root, "client"), filepath.Join(root, "helper")
	for _, dir := range []string{client, helper} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBuildFixture(t, helper, map[string]string{"go.mod": "module example.com/cachehelper\ngo 1.21\n", "helper.go": "package cachehelper\nconst Value=1\n"})
	writeBuildFixture(t, client, map[string]string{
		"go.mod":         "module example.com/cacheclient\ngo 1.21\nrequire example.com/cachehelper v0.0.0\nreplace example.com/cachehelper => ../helper\n",
		"client_test.go": "package cacheclient\nimport(\"os\";\"testing\";\"example.com/cachehelper\")\nfunc TestCaller(t *testing.T){_ = cachehelper.Value;if v:=os.Getenv(\"GOWORK\");v!=\"off\"{t.Fatalf(\"caller GOWORK=%q\",v)}}\n",
	})
	env := append(testEnv("GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false"), userCacheEnv(t, t.TempDir())...)
	if out, stderr, code := command(t, client, append(env, "GOWORK=off"), "go", "mod", "vendor"); code != 0 {
		t.Fatal(out, stderr)
	}
	for i, run := range []struct {
		env    []string
		fails  bool
		cached bool
	}{
		{env: []string{"GOWORK=off"}},
		{fails: true}, // GOWORK unset, as testEnv leaves it.
		{env: []string{"GOWORK=off"}, cached: true},
	} {
		out, stderr, code := command(t, client, append(append([]string(nil), env...), run.env...), driver, "test", ".")
		if (code != 0) != run.fails || run.fails && !strings.Contains(out, `caller GOWORK=""`) || run.cached && !strings.Contains(out, "(cached)") {
			t.Fatalf("run %d exit=%d\n%s%s", i, code, out, stderr)
		}
	}
}

// A go test -exec program starts before the test binary can restore anything.
// ddtest wraps it so the program sees the caller's Go settings, as natively.
func TestMiniExecWrapperSeesCallerEnvironment(t *testing.T) {
	driver := sharedDriver(t, "..")
	root := t.TempDir()
	wrapper, client := filepath.Join(root, "wrapper"), filepath.Join(root, "client")
	for _, dir := range []string{wrapper, client} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeBuildFixture(t, wrapper, map[string]string{
		"go.mod": "module example.com/execwrapper\ngo 1.21\n",
		"main.go": `package main
import ("fmt";"os";"os/exec")
func main() {
 for _, name := range []string{"GOWORK", "GOFLAGS", "DDTEST_ORIGINAL_GOWORK", "DDTEST_ORIGINAL_GOFLAGS"} {
  value, ok := os.LookupEnv(name)
  fmt.Printf("WRAPPER %s=%t:%q\n", name, ok, value)
 }
 fmt.Printf("WRAPPER ARG=%q\n", os.Args[1])
 cmd := exec.Command(os.Args[2], os.Args[3:]...)
 cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
 if err := cmd.Run(); err != nil { os.Exit(1) }
}
`,
	})
	writeBuildFixture(t, client, map[string]string{
		"go.mod":         "module example.com/execclient\ngo 1.21\n",
		"client_test.go": "package execclient\nimport \"testing\"\nfunc TestExec(t *testing.T){}\n",
	})
	env := testEnv("GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false")
	binary := filepath.Join(root, executableName("execwrapper"))
	if out, stderr, code := command(t, wrapper, append(env, "GOWORK=off"), "go", "build", "-o", binary, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	lines := func(out string) []string {
		var selected []string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "WRAPPER ") {
				selected = append(selected, strings.TrimSpace(line))
			}
		}
		return selected
	}
	// cmd/go accepts both quote characters inside an unquoted argument.
	args := []string{"test", "-v", "-count=1", "-exec=" + quoteToolArgument(t, binary) + ` PAYLOAD={"s":"don't"}`, "."}
	native, nativeErr, nativeCode := command(t, client, env, "go", args...)
	out, stderr, code := command(t, client, env, driver, args...)
	if want, got := lines(native), lines(out); nativeCode != 0 || code != 0 || len(want) != 5 || !slices.Equal(got, want) {
		t.Fatalf("native exit=%d %q\n%s%s\nddtest exit=%d %q\n%s%s", nativeCode, want, native, nativeErr, code, got, out, stderr)
	}
}
